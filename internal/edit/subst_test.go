package edit

import (
	"strings"
	"testing"

	"github.com/riadafridishibly/mdgrep/internal/match"
	"github.com/riadafridishibly/mdgrep/internal/mdoc"
	"github.com/riadafridishibly/mdgrep/internal/search"
)

// substDoc holds one word in every place the document treats a replacement
// differently, so one pattern reaches a paragraph, a heading, a table cell and
// a fenced block at once.
const substDoc = "# a mark heading\n" + // 0
	"\n" + // 1
	"| Tool | Note   | N |\n" + // 2
	"|:-----|:------:|--:|\n" + // 3
	"| grep | a mark | 1 |\n" + // 4
	"\n" + // 5
	"A paragraph with a mark in it.\n" + // 6
	"\n" + // 7
	"```go\n" + // 8
	"x := \"a mark\"\n" + // 9
	"```\n" + // 10
	"\n" + // 11
	"- [ ] a mark task\n" // 12

// substitute runs a substitution the way a command line does: build the
// matcher, search with it, then hand the same matcher to the edit.
func substitute(t *testing.T, text, pattern, repl string, opt search.Options) (string, error) {
	t.Helper()
	m, err := match.New(pattern, match.Options{Mode: match.Regexp})
	if err != nil {
		t.Fatal(err)
	}
	opt.Distinct = true
	d := mdoc.Parse("t.md", []byte(text))
	res := search.File(d, m, opt)
	if len(res) == 0 {
		t.Fatalf("pattern %q matched nothing", pattern)
	}
	changes, err := Plan(d, res, Options{Op: OpReplace, Text: repl, Matcher: m})
	if err != nil {
		return "", err
	}
	return Apply(d.Src, changes), nil
}

// TestSubstFitsTheReplacementToWhereItLands is the point of asking the parse
// rather than the line: one replacement, four places, and only the table cell
// has to have its pipe escaped to survive.
func TestSubstFitsTheReplacementToWhereItLands(t *testing.T) {
	got, err := substitute(t, substDoc, "a mark", "a|b", search.Options{})
	if err != nil {
		t.Fatalf("substitute: %v", err)
	}
	want := []string{
		"# a|b heading",
		`| grep | a\|b | 1 |`,
		"A paragraph with a|b in it.",
		`x := "a|b"`,
		"- [ ] a|b task",
	}
	for _, w := range want {
		if !strings.Contains(got, w) {
			t.Errorf("missing %q in:\n%s", w, got)
		}
	}
}

// TestSubstEscapesOnlyWhatTheCellNeeds keeps the escape from spreading: a pipe
// is syntax in a row and a character everywhere else, and a pipe the text
// already escaped is left as it was rather than escaped twice.
func TestSubstEscapesOnlyWhatTheCellNeeds(t *testing.T) {
	got, err := substitute(t, substDoc, "a mark", `x\|y`, search.Options{})
	if err != nil {
		t.Fatalf("substitute: %v", err)
	}
	if !strings.Contains(got, `| grep | x\|y | 1 |`) {
		t.Errorf("cell escaped twice:\n%s", got)
	}
	if !strings.Contains(got, `A paragraph with x\|y in it.`) {
		t.Errorf("paragraph should keep the text as written:\n%s", got)
	}
}

// TestSubstRefusesALineBreakWhereNoneFits covers the case the whole oracle is
// for: GFM gives a cell and a heading no way to hold a newline, so the edit is
// refused rather than writing a row that silently loses its columns.
func TestSubstRefusesALineBreakWhereNoneFits(t *testing.T) {
	tests := []struct {
		name    string
		pattern string
		kind    string
	}{
		{"cell", "grep", "cell"},
		{"heading", "# a mark", "heading"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := substitute(t, substDoc, tt.pattern, "one\ntwo", search.Options{})
			if err == nil {
				t.Fatal("a line break was accepted")
			}
			if !strings.Contains(err.Error(), tt.kind) {
				t.Errorf("error does not name the %s: %v", tt.kind, err)
			}
		})
	}
}

// TestSubstAllowsALineBreakInAParagraph is the other half: a soft break is how
// a paragraph continues, so nothing has to be refused there.
func TestSubstAllowsALineBreakInAParagraph(t *testing.T) {
	got, err := substitute(t, substDoc, "with a mark in", "over\ntwo lines in", search.Options{})
	if err != nil {
		t.Fatalf("substitute: %v", err)
	}
	if !strings.Contains(got, "A paragraph over\ntwo lines in it.") {
		t.Errorf("paragraph did not take the break:\n%s", got)
	}
}

// TestSubstExpandsCaptureGroups is what makes a substitution worth having over
// a fixed rewrite, and what a bare span list could not have supported.
func TestSubstExpandsCaptureGroups(t *testing.T) {
	got, err := substitute(t, "See v1.2.3 today.\n", `v1\.2\.(\d+)`, "v2.0.$1", search.Options{})
	if err != nil {
		t.Fatalf("substitute: %v", err)
	}
	if want := "See v2.0.3 today.\n"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// TestSubstLeavesUnmatchedLinesAlone checks that widening changes what a
// substitution reaches and not what it overwrites: --section walks the whole
// section, and only the lines holding the pattern are rewritten.
func TestSubstLeavesUnmatchedLinesAlone(t *testing.T) {
	const src = "## Head\n\nkeep me\n\nmark here\n\nkeep me too\n"
	m, err := match.New("mark", match.Options{Mode: match.Regexp})
	if err != nil {
		t.Fatal(err)
	}
	d := mdoc.Parse("t.md", []byte(src))
	res := search.File(d, m, search.Options{Distinct: true, Section: true})
	changes, err := Plan(d, res, Options{Op: OpReplace, Text: "flag", Matcher: m})
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	// One change, covering the one line that moved, not the section walked.
	if len(changes) != 1 {
		t.Fatalf("got %d changes, want 1", len(changes))
	}
	if c := changes[0]; c.Start != 4 || c.End != 4 {
		t.Errorf("change covers lines %d-%d, want 4-4", c.Start, c.End)
	}
	if want := "## Head\n\nkeep me\n\nflag here\n\nkeep me too\n"; Apply(d.Src, changes) != want {
		t.Errorf("got %q, want %q", Apply(d.Src, changes), want)
	}
}

// TestSubstReportsANoOp keeps a run that changed nothing from claiming it did,
// which is what stops --write rewriting a file with its own contents.
func TestSubstReportsANoOp(t *testing.T) {
	m, err := match.New("mark", match.Options{Mode: match.Regexp})
	if err != nil {
		t.Fatal(err)
	}
	d := mdoc.Parse("t.md", []byte("a mark here\n"))
	res := search.File(d, m, search.Options{Distinct: true})
	changes, err := Plan(d, res, Options{Op: OpReplace, Text: "mark", Matcher: m})
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if Changed(changes) {
		t.Error("a substitution that wrote the same text reported a change")
	}
}

// TestSubstRefusesAMatcherThatNamesNoText covers the searches that select
// nodes without pointing at text in them. Plan is where it lands, because a
// pipeline's last stage is what decides which matcher the edit gets.
func TestSubstRefusesAMatcherThatNamesNoText(t *testing.T) {
	tests := []struct {
		name string
		m    match.Matcher
	}{
		{"inverted", match.Not(mustMatch(t, "mark"))},
		{"empty pattern", match.All()},
		{"fuzzy", mustFuzzy(t, "mrk")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := mdoc.Parse("t.md", []byte("a mark here\n"))
			res := search.File(d, tt.m, search.Options{Distinct: true})
			if len(res) == 0 {
				t.Skip("matcher selected nothing to edit")
			}
			_, err := Plan(d, res, Options{Op: OpReplace, Text: "x", Matcher: tt.m})
			if err == nil {
				t.Fatal("a matcher with no text to point at was accepted")
			}
			if !strings.Contains(err.Error(), "--replace-node") {
				t.Errorf("error does not point at the way out: %v", err)
			}
		})
	}
}

func mustMatch(t *testing.T, pattern string) match.Matcher {
	t.Helper()
	m, err := match.New(pattern, match.Options{Mode: match.Regexp})
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func mustFuzzy(t *testing.T, pattern string) match.Matcher {
	t.Helper()
	m, err := match.New(pattern, match.Options{Mode: match.Fuzzy, MinScore: 0.1})
	if err != nil {
		t.Fatal(err)
	}
	return m
}

// TestApplyKeepsEachLineOwnEnding pins the ending a rewritten line takes back.
// A document whose lines do not all end alike used to have the odd ones
// levelled to whichever ending the file had most of, because every line but
// the last of a change was written with the file's own. A no-op substitution
// over a two-line paragraph is the shortest way to see it: nothing should
// move, and a byte did.
func TestApplyKeepsEachLineOwnEnding(t *testing.T) {
	const src = "0\r\r\n0"
	m, err := match.New("nothing here", match.Options{Mode: match.Regexp})
	if err != nil {
		t.Fatal(err)
	}
	d := mdoc.Parse("t.md", []byte(src))
	res := search.File(d, m, search.Options{Distinct: true})
	if len(res) != 0 {
		t.Fatalf("pattern matched %d nodes, want 0", len(res))
	}
	// Plan the no-op directly: the point is what Apply does with a change
	// whose lines are unchanged, which is what --format doc prints.
	all := search.Result{Kind: mdoc.KindParagraph, Start: 0, End: 1, HitStart: 0, HitEnd: 1}
	changes, err := Plan(d, []search.Result{all}, Options{Op: OpReplace, Text: "x", Matcher: m})
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if got := Apply(d.Src, changes); got != src {
		t.Errorf("Apply moved an untouched document: %q -> %q", src, got)
	}
}

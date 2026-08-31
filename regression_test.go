package main

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// The documents these tests read. Each one is shaped around the single defect
// it exercises, so a failure names the defect rather than a fixture.
const (
	// widened puts a paragraph before every heading, so any flag that widens a
	// result gives --outline a first line that is not the heading's own.
	widened = "# Doc\n\nIntro text.\n\n## Alpha\n\n- [ ] ship the docs\n\nBeta paragraph here.\n\n## Gamma\n\nTail text.\n"

	// levels is four headings on four consecutive lines, so --lines merges
	// them into one result and the surviving Level is visible in the indent.
	levels = "# Top\n## Two\n### Three\n#### Four\n\nbody\n"

	// interrupted has an ATX heading directly after a paragraph line, with no
	// blank between: the two blocks are adjacent, so their results merge.
	interrupted = "# Doc\n\n## Alpha\n\nnnn a very long paragraph line ddd lll with plenty of filler text\n## ndl\n\ntail\n"

	// mixed holds a paragraph that is not inside any list, next to bullets
	// that are, so a kind filter that leaks is visible in one run.
	mixed = "# Doc\n\n## Tasks\n\n- [ ] alpha one\n\nSome paragraph.\n\n- apple pie\n"

	// ranked has a long task item before a short one, so a fuzzy search scores
	// the later line first and hands the two back out of file order.
	ranked = "# H\n\n- [ ] a x b x c\n- [ ] abc\n"

	// buried puts the match on the last line of a run of paragraphs, so any
	// flag that widens the region backwards leaves the hit far from its start.
	buried = "# T\n\npara one\n\npara two\n\nmore stuff\n\nneedle here\n"

	// empty is a heading with nothing under it: its body covers no line at
	// all, which is the region that has no span to print.
	empty = "# Top\n\n## Empty\n## Next\n\nbody\n"
)

// --- A. Data loss and silent wrong writes -----------------------------------

// A plan is documented three times over as applying whole or not at all
// (main.go's help, README.md, SKILL.md). A write that fails partway must
// therefore leave every file as it found it.
func TestApplyLeavesNoFileWrittenWhenAnotherCannotBe(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores the directory mode this test depends on")
	}
	dir := t.TempDir()
	good := filepath.Join(dir, "good.md")
	if err := os.WriteFile(good, []byte("- [ ] one\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sealed := filepath.Join(dir, "sealed")
	if err := os.Mkdir(sealed, 0o755); err != nil {
		t.Fatal(err)
	}
	bad := filepath.Join(sealed, "bad.md")
	if err := os.WriteFile(bad, []byte("- [ ] two\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// The temp file an edit writes through is created in the target's
	// directory, so a directory with no write bit is what refuses the write.
	if err := os.Chmod(sealed, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(sealed, 0o755) })

	p := plan(t,
		`{"path":`+quote(good)+`,"match":"one","op":"check"}`,
		`{"path":`+quote(bad)+`,"match":"two","op":"check"}`,
	)
	_, stderr, code := capture(t, "--apply", p)
	if code == 0 {
		t.Fatalf("exit = 0, want a refusal (%s)", stderr)
	}
	if got := read(t, good); got != "- [ ] one\n" {
		t.Errorf("the plan was applied in part: %s reads %q, want it untouched", good, got)
	}
	if got := read(t, bad); got != "- [ ] two\n" {
		t.Errorf("%s reads %q, want it untouched", bad, got)
	}
}

// --kind item names bullets. The text of a bullet lives in a child block, so
// those children have to be searched -- but a paragraph that is nobody's child
// is not a bullet and must not come back.
func TestKindItemSelectsOnlyItems(t *testing.T) {
	path := doc(t, mixed)
	stdout, stderr, code := capture(t, "-k", "item", "paragraph|apple", path)
	if code != 0 {
		t.Fatalf("exit = %d (%s)", code, stderr)
	}
	if !strings.Contains(stdout, "- apple pie") {
		t.Errorf("want the bullet that matched:\n%s", stdout)
	}
	if strings.Contains(stdout, "Some paragraph.") {
		t.Errorf("--kind item returned a paragraph that is in no list:\n%s", stdout)
	}
}

// The same filter reaches the write path through a plan's "kind". An edit
// scoped to bullets must not rewrite prose.
func TestApplyKindItemDoesNotEditAParagraph(t *testing.T) {
	path := doc(t, mixed)
	p := plan(t, `{"path":`+quote(path)+`,"match":"Some paragraph","kind":"item","op":"replace","text":"REWRITTEN"}`)
	stdout, _, code := capture(t, "--apply", p, "--dry-run")
	if code == 0 {
		t.Errorf("an entry scoped to \"item\" matched a paragraph:\n%s", stdout)
	}
	if strings.Contains(stdout, "REWRITTEN") {
		t.Errorf("the paragraph was planned for rewriting:\n%s", stdout)
	}
}

// edit.Apply walks a file once, forwards, so a change behind the one before it
// is a change it steps over. A ranked search orders its results by score, and
// a report that claims an edit the file never took is worse than a refusal.
func TestRankedEditsAllReachTheFile(t *testing.T) {
	path := doc(t, ranked)
	stdout, stderr, code := capture(t,
		"--fuzzy", "abc", path, "--min-score", "0.1", "--multi", "--check")
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (%s)", code, stderr)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"- [x] a x b x c", "- [x] abc"} {
		if !strings.Contains(string(got), want) {
			t.Errorf("stdout reported every edit, but the file has no %q:\n%s\n%s",
				want, got, stdout)
		}
	}
}

// --- B. Wrong output ---------------------------------------------------------

// An outline is one line per heading. A widened result no longer begins on
// the heading that line is meant to be, so the two cannot be combined -- and
// saying so beats printing a paragraph where a heading belongs.
func TestOutlineRefusesTheFlagsThatWidenAResult(t *testing.T) {
	path := doc(t, widened)
	for _, args := range [][]string{
		{"-B", "1"}, {"-A", "1"}, {"-C", "1"},
		{"--lines", "2"}, {"--expand", "1"}, {"--section"}, {"--section-body"},
	} {
		stdout, stderr, code := capture(t, append([]string{"--outline", path}, args...)...)
		if code != 2 {
			t.Errorf("%v: exit = %d, want 2:\n%s", args, code, stdout)
		}
		if !strings.Contains(stderr, "--outline") {
			t.Errorf("%v: want an error naming --outline:\n%s", args, stderr)
		}
	}
}

// Whatever else it says, an outline says only headings.
func TestOutlinePrintsOnlyHeadings(t *testing.T) {
	path := doc(t, widened)
	stdout, stderr, code := capture(t, "--outline", path)
	if code != 0 {
		t.Fatalf("exit = %d (%s)", code, stderr)
	}
	for _, want := range []string{"# Doc", "## Alpha", "## Gamma"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("want the heading %q in the outline:\n%s", want, stdout)
		}
	}
	for _, unwanted := range []string{"Intro text.", "Beta paragraph here.", "ship the docs"} {
		if strings.Contains(stdout, unwanted) {
			t.Errorf("outline printed %q, which is not a heading:\n%s", unwanted, stdout)
		}
	}
}

// The indent is the heading's own level, so headings on consecutive lines --
// which are adjacent enough to be merged into one region -- must still each
// be printed at their own depth.
func TestOutlineIndentsByTheLevelOfTheHeadingItPrints(t *testing.T) {
	path := doc(t, levels)
	stdout, stderr, code := capture(t, "--outline", path)
	if code != 0 {
		t.Fatalf("exit = %d (%s)", code, stderr)
	}
	seen := 0
	for line := range strings.SplitSeq(stdout, "\n") {
		bar := strings.Index(line, "│")
		if bar < 0 {
			continue // the path line
		}
		// The gutter is "  NN │ ", then two spaces per level below the first.
		body := strings.TrimPrefix(line[bar+len("│"):], " ")
		text := strings.TrimLeft(body, " ")
		if !strings.HasPrefix(text, "#") {
			continue
		}
		seen++
		level := len(text) - len(strings.TrimLeft(text, "#"))
		got, want := len(body)-len(text), 2*(level-1)
		if got != want {
			t.Errorf("%q is indented %d spaces, want %d for a level-%d heading:\n%s",
				text, got, want, level, stdout)
		}
	}
	if seen != 4 {
		t.Errorf("outlined %d of the 4 headings:\n%s", seen, stdout)
	}
}

// crumb drops the last element of a heading result's trail because the heading
// is printed on the next line. A merged result can carry a heading Kind while
// its trail and first printed line belong to a block inside another section --
// there the last element is a real ancestor and has to stay.
func TestMergedResultKeepsTheAncestorTrail(t *testing.T) {
	path := doc(t, interrupted)
	stdout, stderr, code := capture(t, "--fuzzy", "--min-score", "0.1", "ndl", path)
	if code != 0 {
		t.Fatalf("exit = %d (%s)", code, stderr)
	}
	if !strings.Contains(stdout, "nnn a very long paragraph") {
		t.Fatalf("the paragraph did not match, so there is nothing to merge:\n%s", stdout)
	}
	if !strings.Contains(stdout, "Doc › Alpha") {
		t.Errorf("the merged region starts inside ## Alpha, but the trail does not say so:\n%s", stdout)
	}
}

// Appending --help to a command already being typed is how the manual is
// normally reached. The optional topic must not swallow the pattern.
func TestHelpPrintsTheManualDespiteAPattern(t *testing.T) {
	path := doc(t, widened)
	for _, args := range [][]string{{"Beta", path, "--help"}, {"Beta", "--help"}} {
		stdout, stderr, code := capture(t, args...)
		if code != 0 {
			t.Fatalf("%v: exit = %d, want 0 (%s)", args, code, stderr)
		}
		if !strings.Contains(stdout, "usage: mdgrep") {
			t.Errorf("%v: want the manual:\n%s\n%s", args, stdout, stderr)
		}
	}
}

// --truncate is a cap on printed lines, not a licence to print the wrong ones.
// A flag that widens the region backwards must not spend the whole budget on
// context and leave out what the caller searched for.
func TestTruncateKeepsTheMatchedNode(t *testing.T) {
	path := doc(t, buried)
	for _, format := range [][]string{nil, {"--format", "compact"}, {"--json"}} {
		args := append([]string{"needle", path, "-B", "3", "--truncate", "2"}, format...)
		stdout, stderr, code := capture(t, args...)
		if code != 0 {
			t.Fatalf("%v: exit = %d, want 0 (%s)", format, code, stderr)
		}
		if !strings.Contains(stdout, "needle here") {
			t.Errorf("%v: the match was truncated away:\n%s", format, stdout)
		}
	}
}

// --- C. Contracts for machine callers ----------------------------------------

// --json says every refusal is a record a reader can branch on. A refusal that
// arrives as prose is one the caller cannot see at all.
func TestApplyRefusalIsAlwaysJSONWhenAskedFor(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "nope.md")
	p := plan(t, `{"path":`+quote(missing)+`,"match":"x","op":"check"}`)
	_, stderr, code := capture(t, "--apply", p, "--json")
	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	named := false
	for line := range strings.SplitSeq(strings.TrimSpace(stderr), "\n") {
		if line == "" {
			continue
		}
		var rec map[string]any
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Errorf("stderr line is not JSON: %q", line)
			continue
		}
		if _, ok := rec["entry"]; ok {
			named = true
		}
	}
	// The run closes with a record about the run rather than about one entry,
	// so not every line names one -- but the entry that could not be carried
	// out has to be somewhere a reader can find it.
	if !named {
		t.Errorf("no refusal record names the entry that failed:\n%s", stderr)
	}
}

// A compact record is "start[-end]". An insertion has no span, and printing
// one whose end precedes its start is unreadable by any parser of that format.
func TestCompactInsertionSpanIsReadable(t *testing.T) {
	path := doc(t, widened)
	p := plan(t, `{"path":`+quote(path)+`,"match":"Beta paragraph","op":"append","text":"appended para"}`)
	stdout, stderr, code := capture(t, "--apply", p, "--format", "compact", "--dry-run")
	if code != 0 {
		t.Fatalf("exit = %d (%s)", code, stderr)
	}
	for line := range strings.SplitSeq(strings.TrimSpace(stdout), "\n") {
		span, _, isRecord := strings.Cut(line, "\t")
		if !isRecord {
			continue // the path line
		}
		first, last, ok := strings.Cut(span, "-")
		if !ok {
			continue
		}
		start, err := strconv.Atoi(first)
		if err != nil {
			t.Errorf("span %q does not start with a line number:\n%s", span, stdout)
			continue
		}
		end, err := strconv.Atoi(last)
		if err != nil {
			t.Errorf("span %q does not end with a line number:\n%s", span, stdout)
			continue
		}
		if end < start {
			t.Errorf("span %q ends before it starts:\n%s", span, stdout)
		}
	}
}

// The compact format's one structural rule is that the path is the line with
// no tab in it. A path is not the printer's to trust.
func TestCompactEscapesThePath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "we\tird.md")
	if err := os.WriteFile(path, []byte("# Hi\n\nBeta line\n"), 0o644); err != nil {
		t.Skipf("this filesystem will not hold a tab in a name: %v", err)
	}
	stdout, stderr, code := capture(t, "--format", "compact", "Beta", path)
	if code != 0 {
		t.Fatalf("exit = %d (%s)", code, stderr)
	}
	first, _, _ := strings.Cut(strings.TrimSpace(stdout), "\n")
	if strings.Contains(first, "\t") {
		t.Errorf("the path line carries a raw tab, so a reader takes it for a record: %q", first)
	}
}

// --format with an empty value is what an unset shell variable produces. It is
// a format nobody has, not the absence of the flag.
func TestEmptyFormatIsRejected(t *testing.T) {
	path := doc(t, widened)
	_, stderr, code := capture(t, "--format=", "Beta", path)
	if code != 2 {
		t.Errorf("exit = %d, want 2 for an empty --format", code)
	}
	if !strings.Contains(stderr, "format") {
		t.Errorf("want an error naming the format:\n%s", stderr)
	}
}

// compact and --json both publish a region as start[-end]. A region that
// covers no line is spelled End < Start inside, and printing that unchanged
// gives a range running backwards that no reader of the grammar can take.
func TestEmptySectionBodyHasNoBackwardsSpan(t *testing.T) {
	path := doc(t, empty)
	stdout, stderr, code := capture(t, "--section-body", "--format", "compact", "^## Empty", path)
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (%s)", code, stderr)
	}
	for line := range strings.SplitSeq(strings.TrimSpace(stdout), "\n") {
		span, _, ok := strings.Cut(line, "\t")
		if !ok {
			continue
		}
		lo, hi, ranged := strings.Cut(span, "-")
		if !ranged {
			continue
		}
		if start, end := atoi(t, lo), atoi(t, hi); end < start {
			t.Errorf("span %q runs backwards:\n%s", span, stdout)
		}
	}

	stdout, stderr, code = capture(t, "--section-body", "--json", "^## Empty", path)
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (%s)", code, stderr)
	}
	for line := range strings.SplitSeq(strings.TrimSpace(stdout), "\n") {
		var rec struct{ Start, End int }
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("stdout line is not JSON: %q", line)
		}
		if rec.End < rec.Start {
			t.Errorf("end %d precedes start %d:\n%s", rec.End, rec.Start, stdout)
		}
	}
}

// --- D. Latent and cosmetic ---------------------------------------------------

// A section title is judged by its first character. Reading one byte of a
// multi-byte character judges a byte that is not a character at all: 0xC3, the
// lead byte of every C-with-cedilla and every U-with-diaeresis, is 'Ã' on its
// own, which is uppercase.
func TestIsHelpTitleReadsAWholeRune(t *testing.T) {
	tests := []struct {
		line string
		want bool
	}{
		{"Filters", true},
		{"filters", false},
		{"", false},
		{"Output formats", false}, // a space is not a letter
		{"über", false},           // lower case, whatever its encoding
	}
	for _, tt := range tests {
		if got := isHelpTitle(tt.line); got != tt.want {
			t.Errorf("isHelpTitle(%q) = %v, want %v", tt.line, got, tt.want)
		}
	}
}

// A doc comment belongs to the function below it. This one drifted onto its
// neighbour, so godoc describes separator as the thing parseFormat does.
func TestDocCommentsNameTheirOwnFunction(t *testing.T) {
	fset := token.NewFileSet()
	paths, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	var files []*ast.File
	for _, path := range paths {
		f, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, f)
	}

	// Only a name this package actually declares counts, so a comment opening
	// with some other capitalised word is left alone.
	declared := map[string]bool{}
	for _, f := range files {
		for _, decl := range f.Decls {
			if fn, ok := decl.(*ast.FuncDecl); ok && fn.Recv == nil {
				declared[fn.Name.Name] = true
			}
		}
	}

	for _, f := range files {
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Doc == nil {
				continue
			}
			first, _, _ := strings.Cut(strings.TrimSpace(fn.Doc.Text()), " ")
			if first != fn.Name.Name && declared[first] {
				t.Errorf("%s: %s's doc comment opens by describing %q",
					fset.Position(fn.Pos()), fn.Name.Name, first)
			}
		}
	}
}

// quote renders a path as a JSON string, so a temp directory with a character
// JSON would otherwise have to escape still produces a readable plan.
func quote(path string) string {
	b, err := json.Marshal(path)
	if err != nil {
		panic(err)
	}
	return string(b)
}

// atoi reads a line number a format published, and fails the test rather than
// the parse: a span that is not a number is a defect of its own.
func atoi(t *testing.T, s string) int {
	t.Helper()
	n, err := strconv.Atoi(s)
	if err != nil {
		t.Fatalf("span holds %q, which is not a line number", s)
	}
	return n
}

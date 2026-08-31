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

// --- B. Wrong output ---------------------------------------------------------

// An outline is a list of headings. Widening a result moves its Start off the
// heading and on to whatever the widening pulled in; the line to print is the
// heading's own, which is HitStart.
func TestOutlinePrintsTheHeadingNotTheWidenedStart(t *testing.T) {
	path := doc(t, widened)
	stdout, stderr, code := capture(t, "--outline", "-B", "1", path)
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

// mergeOverlapping takes Kind from the higher-scoring result; Level has to
// travel with it, or the outline indents a heading by a level that belongs to
// a different node.
func TestOutlineIndentsByTheLevelOfTheHeadingItPrints(t *testing.T) {
	path := doc(t, levels)
	stdout, stderr, code := capture(t, "--outline", "--lines", "1", path)
	if code != 0 {
		t.Fatalf("exit = %d (%s)", code, stderr)
	}
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
		level := len(text) - len(strings.TrimLeft(text, "#"))
		got, want := len(body)-len(text), 2*(level-1)
		if got != want {
			t.Errorf("%q is indented %d spaces, want %d for a level-%d heading:\n%s",
				text, got, want, level, stdout)
		}
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
	stdout, stderr, code := capture(t, "Beta", path, "--help")
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (%s)", code, stderr)
	}
	if !strings.Contains(stdout, "usage: mdgrep") {
		t.Errorf("want the manual:\n%s\n%s", stdout, stderr)
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

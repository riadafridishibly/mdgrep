package edit

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/riadafridishibly/mdgrep/internal/match"
	"github.com/riadafridishibly/mdgrep/internal/mdoc"
	"github.com/riadafridishibly/mdgrep/internal/search"
)

const doc = "# Guide\n" + // 0
	"\n" + // 1
	"## Tasks\n" + // 2
	"\n" + // 3
	"- [ ] ship the docs\n" + // 4
	"  - [x] write the notes\n" + // 5
	"- plain bullet\n" + // 6
	"\n" + // 7
	"## Setup\n" + // 8
	"\n" + // 9
	"Run the thing.\n" + // 10
	"\n" + // 11
	"```bash\n" + // 12
	"echo old\n" + // 13
	"```\n" + // 14
	"\n" + // 15
	"## Empty\n" + // 16
	"\n" + // 17
	"Trailer\n" + // 18
	"-------\n" + // 19
	"\n" + // 20
	"last word\n" // 21

// apply runs the whole path a command line takes: search, plan, apply.
func apply(t *testing.T, text, pattern string, opt search.Options, e Options) string {
	t.Helper()
	m, err := match.New(pattern, match.Options{Mode: match.Substring, IgnoreCase: true})
	if err != nil {
		t.Fatal(err)
	}
	opt.Distinct = true
	d := mdoc.Parse("t.md", []byte(text))
	res := search.File(d, m, opt)
	if len(res) == 0 {
		t.Fatalf("pattern %q matched nothing", pattern)
	}
	changes, err := Plan(d.Src, res, e)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	return Apply(d.Src, changes)
}

func wantLines(t *testing.T, got string, want ...string) {
	t.Helper()
	lines := strings.Split(got, "\n")
	for _, w := range want {
		if !slicesContain(lines, w) {
			t.Fatalf("missing line %q in:\n%s", w, got)
		}
	}
}

func slicesContain(lines []string, want string) bool {
	for _, l := range lines {
		if l == want {
			return true
		}
	}
	return false
}

func TestCheckTicksTheBoxAndKeepsIndentation(t *testing.T) {
	got := apply(t, doc, "ship the docs", search.Options{Task: search.TaskAny}, Options{Op: OpCheck})
	wantLines(t, got, "- [x] ship the docs", "  - [x] write the notes")
}

func TestUncheckReachesTheNestedItem(t *testing.T) {
	got := apply(t, doc, "write the notes", search.Options{Task: search.TaskAny}, Options{Op: OpUncheck})
	wantLines(t, got, "- [ ] ship the docs", "  - [ ] write the notes")
}

func TestToggleFlipsWhicheverWayTheBoxSits(t *testing.T) {
	got := apply(t, doc, "ship the docs", search.Options{Task: search.TaskAny}, Options{Op: OpToggle})
	wantLines(t, got, "- [x] ship the docs")
	got = apply(t, got, "ship the docs", search.Options{Task: search.TaskAny}, Options{Op: OpToggle})
	wantLines(t, got, "- [ ] ship the docs")
}

func TestCheckingATickedBoxChangesNothing(t *testing.T) {
	d := mdoc.Parse("t.md", []byte(doc))
	m, _ := match.New("write the notes", match.Options{Mode: match.Substring, IgnoreCase: true})
	res := search.File(d, m, search.Options{Task: search.TaskAny, Distinct: true})
	changes, err := Plan(d.Src, res, Options{Op: OpCheck})
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 1 || !changes[0].NoOp {
		t.Fatalf("want one no-op change, got %+v", changes)
	}
	if got := Apply(d.Src, changes); got != doc {
		t.Fatalf("file changed:\n%s", got)
	}
}

func TestCheckRefusesANodeWithNoCheckbox(t *testing.T) {
	d := mdoc.Parse("t.md", []byte(doc))
	m, _ := match.New("plain bullet", match.Options{Mode: match.Substring, IgnoreCase: true})
	res := search.File(d, m, search.Options{Distinct: true})
	if _, err := Plan(d.Src, res, Options{Op: OpCheck}); err == nil {
		t.Fatal("want an error for an item with no checkbox")
	}
}

func TestSetTextKeepsHeadingLevel(t *testing.T) {
	got := apply(t, doc, "## Setup", search.Options{}, Options{Op: OpSetText, Text: "Installation"})
	wantLines(t, got, "## Installation")
}

func TestSetTextResizesASetextUnderline(t *testing.T) {
	got := apply(t, doc, "Trailer", search.Options{}, Options{Op: OpSetText, Text: "Coda"})
	wantLines(t, got, "Coda", "----")
}

func TestSetTextKeepsMarkerAndCheckbox(t *testing.T) {
	got := apply(t, doc, "ship the docs", search.Options{}, Options{Op: OpSetText, Text: "ship the guide"})
	wantLines(t, got, "- [ ] ship the guide", "  - [x] write the notes")
}

func TestSetTextKeepsCodeFences(t *testing.T) {
	got := apply(t, doc, "echo old", search.Options{Kinds: map[mdoc.Kind]bool{mdoc.KindCode: true}},
		Options{Op: OpSetText, Text: "echo new"})
	wantLines(t, got, "```bash", "echo new", "```")
}

func TestSetTextRefusesAHeadingWithSeveralLines(t *testing.T) {
	d := mdoc.Parse("t.md", []byte(doc))
	m, _ := match.New("## Setup", match.Options{Mode: match.Substring, IgnoreCase: true})
	res := search.File(d, m, search.Options{Distinct: true})
	if _, err := Plan(d.Src, res, Options{Op: OpSetText, Text: "one\ntwo"}); err == nil {
		t.Fatal("want an error for a two-line heading")
	}
}

func TestReplaceSectionRewritesTheWholeRegion(t *testing.T) {
	got := apply(t, doc, "## Tasks", search.Options{Section: true},
		Options{Op: OpReplace, Text: "## Tasks\n\n- [ ] start over\n"})
	wantLines(t, got, "## Tasks", "- [ ] start over", "## Setup")
	if strings.Contains(got, "ship the docs") {
		t.Fatalf("old section survived:\n%s", got)
	}
}

func TestReplaceSectionBodyLeavesTheHeading(t *testing.T) {
	got := apply(t, doc, "## Setup", search.Options{Body: true},
		Options{Op: OpReplace, Text: "Nothing to do."})
	wantLines(t, got, "## Setup", "Nothing to do.", "## Empty")
	if strings.Contains(got, "echo old") {
		t.Fatalf("old body survived:\n%s", got)
	}
}

func TestReplaceAnEmptySectionBodyInsertsBetweenTheHeadings(t *testing.T) {
	got := apply(t, doc, "## Empty", search.Options{Body: true},
		Options{Op: OpReplace, Text: "filled in."})
	want := "## Empty\n\nfilled in.\n\nTrailer\n"
	if !strings.Contains(got, want) {
		t.Fatalf("want %q in:\n%s", want, got)
	}
}

func TestDeleteTakesTheBlankLineItWouldLeaveBehind(t *testing.T) {
	got := apply(t, doc, "Run the thing.", search.Options{}, Options{Op: OpDelete})
	if strings.Contains(got, "Run the thing.") {
		t.Fatal("the paragraph survived")
	}
	if !strings.Contains(got, "## Setup\n\n```bash") {
		t.Fatalf("blank lines stacked up:\n%s", got)
	}
}

func TestAppendPartsItselfFromTheBlockAbove(t *testing.T) {
	got := apply(t, doc, "Run the thing.", search.Options{}, Options{Op: OpAppend, Text: "Then the other."})
	if !strings.Contains(got, "Run the thing.\n\nThen the other.\n\n```bash") {
		t.Fatalf("bad spacing:\n%s", got)
	}
}

func TestAppendToAnItemLandsAsItsSibling(t *testing.T) {
	got := apply(t, doc, "write the notes", search.Options{}, Options{Op: OpAppend, Text: "- [ ] file the notes"})
	if !strings.Contains(got, "  - [x] write the notes\n  - [ ] file the notes\n- plain bullet") {
		t.Fatalf("sibling not indented into the list:\n%s", got)
	}
}

func TestPrependPartsItselfFromTheBlockBelow(t *testing.T) {
	got := apply(t, doc, "Run the thing.", search.Options{}, Options{Op: OpPrepend, Text: "First:"})
	if !strings.Contains(got, "## Setup\n\nFirst:\n\nRun the thing.") {
		t.Fatalf("bad spacing:\n%s", got)
	}
}

func TestMultipleChangesInOneFileAllLand(t *testing.T) {
	got := apply(t, doc, "the", search.Options{Task: search.TaskAny}, Options{Op: OpCheck})
	wantLines(t, got, "- [x] ship the docs", "  - [x] write the notes")
}

func TestCarriageReturnsSurvive(t *testing.T) {
	crlf := strings.ReplaceAll(doc, "\n", "\r\n")
	got := apply(t, crlf, "ship the docs", search.Options{Task: search.TaskAny}, Options{Op: OpCheck})
	if !strings.Contains(got, "- [x] ship the docs\r\n") {
		t.Fatalf("line endings lost:\n%q", got)
	}
	if strings.Contains(strings.ReplaceAll(got, "\r\n", ""), "\n") {
		t.Fatalf("stray bare newline:\n%q", got)
	}
}

func TestAFileWithoutATrailingNewlineKeepsEndingThatWay(t *testing.T) {
	text := strings.TrimSuffix(doc, "\n")
	got := apply(t, text, "last word", search.Options{}, Options{Op: OpSetText, Text: "final word"})
	if strings.HasSuffix(got, "\n") {
		t.Fatalf("a newline was added:\n%q", got)
	}
	if !strings.HasSuffix(got, "final word") {
		t.Fatalf("last line not rewritten:\n%q", got)
	}
}

func TestWriteReplacesTheFileAndKeepsItsMode(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "t.md")
	if err := os.WriteFile(path, []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Write(path, "rewritten\n"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "rewritten\n" {
		t.Fatalf("contents = %q", data)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %v, want 0600", info.Mode().Perm())
	}
	// The temporary file must not be left beside the original.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("stray files: %v", entries)
	}
}

func TestWriteFollowsASymlinkToTheFileItPointsAt(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target.md")
	link := filepath.Join(dir, "link.md")
	if err := os.WriteFile(target, []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := Write(link, "rewritten\n"); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(link)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Error("the link was replaced by a regular file")
	}
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "rewritten\n" {
		t.Errorf("the file the link points at = %q", data)
	}
}

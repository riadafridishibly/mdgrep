package search

import (
	"strings"
	"testing"

	"mdgrep/internal/match"
	"mdgrep/internal/mdoc"
)

const doc = "# Guide\n" + // 0
	"\n" + // 1
	"## Install\n" + // 2
	"\n" + // 3
	"- Install the CLI:\n" + // 4
	"  - On macOS run brew install foo\n" + // 5
	"  - On Linux download the tarball\n" + // 6
	"- Configure credentials\n" + // 7
	"\n" + // 8
	"## Later\n" + // 9
	"\n" + // 10
	"Nothing relevant here.\n" // 11

func find(t *testing.T, pattern string, opt Options) []Result {
	t.Helper()
	m, err := match.New(match.Fuzzy, pattern, true, 0.55)
	if err != nil {
		t.Fatal(err)
	}
	return File(mdoc.Parse("t.md", []byte(doc)), m, opt)
}

func text(t *testing.T, r Result) string {
	t.Helper()
	src := mdoc.NewSource("t.md", []byte(doc))
	return strings.Join(src.Lines(r.Start, r.End), "\n")
}

func TestHitTightensToInnermostItem(t *testing.T) {
	res := find(t, "brew install", Options{})
	if len(res) != 1 {
		t.Fatalf("got %d results, want 1", len(res))
	}
	if res[0].Start != 5 || res[0].End != 5 {
		t.Fatalf("range = %d..%d, want 5..5", res[0].Start, res[0].End)
	}
	if res[0].Kind != mdoc.KindItem {
		t.Fatalf("kind = %q, want item", res[0].Kind)
	}
}

func TestParentItemCarriesItsChildren(t *testing.T) {
	res := find(t, "install cli", Options{})
	if len(res) != 1 {
		t.Fatalf("got %d results, want 1", len(res))
	}
	if got := text(t, res[0]); !strings.Contains(got, "tarball") {
		t.Fatalf("parent item should include nested lines, got:\n%s", got)
	}
	if res[0].End != 6 {
		t.Fatalf("end = %d, want 6", res[0].End)
	}
}

func TestExpandClimbsAncestors(t *testing.T) {
	res := find(t, "brew install", Options{Expand: 2})
	if len(res) != 1 {
		t.Fatalf("got %d results, want 1", len(res))
	}
	if res[0].Start != 4 || res[0].End != 6 {
		t.Fatalf("range = %d..%d, want 4..6", res[0].Start, res[0].End)
	}
}

func TestSectionExpansion(t *testing.T) {
	res := find(t, "brew install", Options{Section: true})
	if res[0].Start != 2 || res[0].End != 7 {
		t.Fatalf("range = %d..%d, want 2..7", res[0].Start, res[0].End)
	}
}

func TestSiblingContext(t *testing.T) {
	res := find(t, "brew install", Options{After: 1})
	if res[0].End != 6 {
		t.Fatalf("end = %d, want 6 (next sibling bullet)", res[0].End)
	}
}

func TestLinePadding(t *testing.T) {
	res := find(t, "nothing relevant", Options{Lines: 2})
	if res[0].Start != 9 {
		t.Fatalf("start = %d, want 9 after blank trimming", res[0].Start)
	}
}

func TestKindFilter(t *testing.T) {
	m, err := match.New(match.Fuzzy, "install", true, 0.55)
	if err != nil {
		t.Fatal(err)
	}
	res := File(mdoc.Parse("t.md", []byte(doc)), m, Options{
		Kinds: map[mdoc.Kind]bool{mdoc.KindHeading: true},
	})
	if len(res) != 1 || res[0].Start != 2 {
		t.Fatalf("res = %+v, want the Install heading at line 2", res)
	}
}

func TestBreadcrumbOnResult(t *testing.T) {
	res := find(t, "brew install", Options{})
	if got := res[0].Breadcrumb; len(got) != 2 || got[1] != "Install" {
		t.Fatalf("breadcrumb = %v, want [Guide Install]", got)
	}
}

func TestMaxCaps(t *testing.T) {
	res := find(t, "install", Options{Max: 1})
	if len(res) != 1 {
		t.Fatalf("got %d results, want 1", len(res))
	}
}

func TestNoMatch(t *testing.T) {
	if res := find(t, "quantum entanglement", Options{}); res != nil {
		t.Fatalf("got %+v, want nil", res)
	}
}

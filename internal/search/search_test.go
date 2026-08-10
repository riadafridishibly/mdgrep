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

// Blank lines keep the hits far enough apart that merging leaves one result
// per item, so the filters can be asserted on start lines.
const tasks = "# Sprint\n" + // 0
	"\n" + // 1
	"- [ ] deploy the canary\n" + // 2
	"\n" + // 3
	"Deploy prose, not a task.\n" + // 4
	"\n" + // 5
	"- [x] deploy the docs\n" + // 6
	"  - link the deploy runbook\n" + // 7
	"\n" + // 8
	"- deploy by hand\n" // 9

func findTasks(t *testing.T, pattern string, opt Options) []Result {
	t.Helper()
	m, err := match.New(match.Fuzzy, pattern, true, 0.55)
	if err != nil {
		t.Fatal(err)
	}
	return File(mdoc.Parse("t.md", []byte(tasks)), m, opt)
}

func TestTaskFilters(t *testing.T) {
	cases := []struct {
		filter TaskFilter
		starts []int
	}{
		{TaskIgnore, []int{2, 4, 6, 9}},
		{TaskAny, []int{2, 6}},
		{TaskChecked, []int{6}},
		{TaskUnchecked, []int{2}},
	}
	for _, c := range cases {
		res := findTasks(t, "deploy", Options{Task: c.filter})
		var got []int
		for _, r := range res {
			got = append(got, r.Start)
		}
		if len(got) != len(c.starts) {
			t.Fatalf("filter %d: starts = %v, want %v", c.filter, got, c.starts)
		}
		for i, want := range c.starts {
			if got[i] != want {
				t.Fatalf("filter %d: starts = %v, want %v", c.filter, got, c.starts)
			}
		}
	}
}

func TestTaskFilterClimbsToOwningTask(t *testing.T) {
	res := findTasks(t, "runbook", Options{Task: TaskAny})
	if len(res) != 1 {
		t.Fatalf("got %d results, want 1", len(res))
	}
	if res[0].Start != 6 || res[0].End != 7 {
		t.Fatalf("range = %d..%d, want 6..7 (the checked parent task)", res[0].Start, res[0].End)
	}
	if !res[0].Task || !res[0].Checked {
		t.Fatalf("task=%v checked=%v, want true/true", res[0].Task, res[0].Checked)
	}
}

func TestTaskFilterDropsNonTaskHits(t *testing.T) {
	if res := findTasks(t, "sprint", Options{Task: TaskAny}); res != nil {
		t.Fatalf("got %+v, want nil for a heading hit under a task filter", res)
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

package search

import (
	"strings"
	"testing"

	"github.com/riadafridishibly/mdgrep/internal/match"
	"github.com/riadafridishibly/mdgrep/internal/mdoc"
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
	m, err := match.New(pattern, match.Options{Mode: match.Fuzzy, IgnoreCase: true, MinScore: 0.55})
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
	m, err := match.New("install", match.Options{Mode: match.Fuzzy, IgnoreCase: true, MinScore: 0.55})
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
	m, err := match.New(pattern, match.Options{Mode: match.Fuzzy, IgnoreCase: true, MinScore: 0.55})
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

func TestRankOrdersByScore(t *testing.T) {
	res := find(t, "install", Options{Rank: true})
	if len(res) < 2 {
		t.Fatalf("got %d results, want at least 2 to order", len(res))
	}
	for i := 1; i < len(res); i++ {
		if res[i].Score > res[i-1].Score {
			t.Fatalf("result %d scores %v, above its predecessor %v", i, res[i].Score, res[i-1].Score)
		}
	}
	plain := find(t, "install", Options{})
	if len(plain) != len(res) {
		t.Fatalf("ranking changed the result count: %d, want %d", len(res), len(plain))
	}
	best := plain[0]
	for _, r := range plain {
		if r.Score > best.Score {
			best = r
		}
	}
	if res[0].Start != best.Start {
		t.Fatalf("first result starts at line %d, want the best-scoring one at %d", res[0].Start, best.Start)
	}
}

// Ranking happens before the cap, so -m keeps a file's best results rather
// than the ones it happens to hold first.
func TestRankedMaxKeepsTheBest(t *testing.T) {
	all := find(t, "install", Options{Rank: true})
	capped := find(t, "install", Options{Rank: true, Max: 1})
	if len(capped) != 1 {
		t.Fatalf("got %d results, want 1", len(capped))
	}
	if capped[0].Score != all[0].Score {
		t.Fatalf("cap kept score %v, want the best %v", capped[0].Score, all[0].Score)
	}
}

// Code spans are the one place where the source syntax is part of what people
// search for, so a pattern typed with backticks has to reach them.
const spans = "# API\n" + // 0
	"\n" + // 1
	"- On macOS run `brew install foo`\n" + // 2
	"- Configure credentials in `~/.foo/config`\n" // 3

func TestBacktickPattern(t *testing.T) {
	cases := []struct {
		mode    match.Mode
		pattern string
		start   int
	}{
		{match.Substring, "`brew install foo`", 2},
		{match.Substring, "in `~/.foo/config`", 3},
		{match.Fuzzy, "`brew install foo`", 2},
		{match.Fuzzy, "`config`", 3},
		{match.Regexp, "run `brew.*foo`$", 2},
	}
	for _, c := range cases {
		m, err := match.New(c.pattern, match.Options{Mode: c.mode, IgnoreCase: true, MinScore: 0.55})
		if err != nil {
			t.Fatal(err)
		}
		res := File(mdoc.Parse("t.md", []byte(spans)), m, Options{})
		if len(res) != 1 || res[0].Start != c.start {
			t.Fatalf("mode %d pattern %q: res = %+v, want one hit at line %d",
				c.mode, c.pattern, res, c.start)
		}
	}
}

func TestBacktickPatternMissesPlainText(t *testing.T) {
	m, err := match.New("`credentials`", match.Options{Mode: match.Substring, IgnoreCase: true, MinScore: 0.55})
	if err != nil {
		t.Fatal(err)
	}
	if res := File(mdoc.Parse("t.md", []byte(spans)), m, Options{}); res != nil {
		t.Fatalf("got %+v, want nil: the word is not in a code span", res)
	}
}

func TestNoMatch(t *testing.T) {
	if res := find(t, "quantum entanglement", Options{}); res != nil {
		t.Fatalf("got %+v, want nil", res)
	}
}

// Nodes are matched against the markdown as written, so structure a reader can
// see — heading markers, list markers, table pipes, emphasis — is searchable,
// and "^" anchors to a line inside the block rather than to the block.
func TestRawSourceIsSearchable(t *testing.T) {
	src := "# Title\n" + // 0
		"\n" + // 1
		"- [ ] rotate the **deploy** key\n" + // 2
		"\n" + // 3
		"| canary | 10% | ops |\n" // 4
	cases := []struct {
		mode    match.Mode
		pattern string
		start   int
	}{
		{match.Regexp, `^# `, 0},
		{match.Regexp, `^- \[ \] `, 2},
		{match.Substring, "**deploy**", 2},
		{match.Regexp, `^\|.*\bops\b`, 4},
	}
	for _, c := range cases {
		m, err := match.New(c.pattern, match.Options{Mode: c.mode})
		if err != nil {
			t.Fatal(err)
		}
		res := File(mdoc.Parse("t.md", []byte(src)), m, Options{})
		if len(res) != 1 || res[0].HitStart != c.start {
			t.Fatalf("pattern %q: res = %+v, want one hit at line %d", c.pattern, res, c.start)
		}
	}
}

func TestSectionBodyLeavesTheHeadingOut(t *testing.T) {
	res := find(t, "install", Options{Body: true})
	if len(res) == 0 {
		t.Fatal("no results")
	}
	if got := text(t, res[0]); strings.Contains(got, "## Install") {
		t.Fatalf("body should not carry its heading:\n%s", got)
	}
	if res[0].Start != 4 || res[0].End != 7 {
		t.Fatalf("range = %d..%d, want 4..7", res[0].Start, res[0].End)
	}
}

func TestSectionBodyOfAHeadingWithNothingUnderItIsEmpty(t *testing.T) {
	const bare = "# Guide\n\n## Empty\n\n## Later\n\ntail\n"
	m, err := match.New("empty", match.Options{Mode: match.Substring, IgnoreCase: true})
	if err != nil {
		t.Fatal(err)
	}
	res := File(mdoc.Parse("t.md", []byte(bare)), m, Options{Body: true})
	if len(res) != 1 {
		t.Fatalf("got %d results, want 1", len(res))
	}
	if res[0].End >= res[0].Start {
		t.Fatalf("range = %d..%d, want an empty one", res[0].Start, res[0].End)
	}
}

func TestDistinctKeepsTouchingResultsApart(t *testing.T) {
	const list = "- [ ] alpha one\n- [ ] alpha two\n"
	m, err := match.New("alpha", match.Options{Mode: match.Substring, IgnoreCase: true})
	if err != nil {
		t.Fatal(err)
	}
	doc := mdoc.Parse("t.md", []byte(list))
	if res := File(doc, m, Options{}); len(res) != 1 {
		t.Fatalf("printing got %d results, want the two runs together as 1", len(res))
	}
	if res := File(doc, m, Options{Distinct: true}); len(res) != 2 {
		t.Fatalf("got %d results, want 2", len(res))
	}
}

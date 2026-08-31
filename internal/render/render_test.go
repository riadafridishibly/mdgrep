package render

import (
	"testing"

	"github.com/riadafridishibly/mdgrep/internal/match"
	"github.com/riadafridishibly/mdgrep/internal/mdoc"
	"github.com/riadafridishibly/mdgrep/internal/search"
)

// fence is a block whose match sits well past the first few lines, which is
// the shape --truncate exists for and the shape that used to lose the hit.
const fence = "```\na1\na2\na3\na4\na5\nneedle here\na7\na8\n```\n"

func regexp(t *testing.T, pattern string) match.Matcher {
	t.Helper()
	m, err := match.New(pattern, match.Options{Mode: match.Regexp})
	if err != nil {
		t.Fatal(err)
	}
	return m
}

// A block is scored whole, so HitStart is where the fence opens and says
// nothing about where in it the match is. Truncating from there printed the
// opening lines of a long block and cut the one line the caller searched for.
func TestWindowKeepsTheMatchedLine(t *testing.T) {
	src := mdoc.NewSource("t.md", []byte(fence))
	// The whole fence is one block: lines 0-9, with the match on line 6.
	r := search.Result{Start: 0, End: 9, HitStart: 0, HitEnd: 9}
	p := &Printer{Truncate: 3}

	first, last, before, after := p.window(src, r, regexp(t, "needle"))
	if first > 6 || last < 6 {
		t.Errorf("window = [%d,%d], want it to hold the match on line 6", first, last)
	}
	if last-first+1 > 3 {
		t.Errorf("window = [%d,%d], want at most 3 lines", first, last)
	}
	if before != first || after != 9-last {
		t.Errorf("before, after = %d, %d, want %d, %d", before, after, first, 9-last)
	}
	// start plus before is the line the text begins on, which is the whole
	// reason the two counts are reported separately.
	if got := r.Start + before; got != first {
		t.Errorf("start+before = %d, want %d", got, first)
	}
}

// A match near the top must not slide the window down: the region's own first
// lines are the context, and there is nothing to skip to reach the hit.
func TestWindowStartsAtTheRegionWhenTheHitIsInReach(t *testing.T) {
	src := mdoc.NewSource("t.md", []byte(fence))
	r := search.Result{Start: 0, End: 9, HitStart: 0, HitEnd: 9}
	p := &Printer{Truncate: 3}

	first, _, before, _ := p.window(src, r, regexp(t, "a1"))
	if first != 0 || before != 0 {
		t.Errorf("first, before = %d, %d, want 0, 0", first, before)
	}
}

// A matcher that cannot point at any one line -- an anchor search selects a
// heading by name, and a fuzzy score can be spread over several lines --
// leaves the block's own first line, which is what the window used before.
func TestWindowFallsBackToTheBlockStart(t *testing.T) {
	src := mdoc.NewSource("t.md", []byte(fence))
	r := search.Result{Start: 0, End: 9, HitStart: 0, HitEnd: 9}
	p := &Printer{Truncate: 3}

	for _, m := range []match.Matcher{nil, regexp(t, "nothing here")} {
		first, last, before, after := p.window(src, r, m)
		if first != 0 || last != 2 || before != 0 || after != 7 {
			t.Errorf("window = [%d,%d] %d/%d, want [0,2] 0/7", first, last, before, after)
		}
	}
}

// A region inside the budget is printed whole, and reports nothing held back.
func TestWindowLeavesAShortRegionAlone(t *testing.T) {
	src := mdoc.NewSource("t.md", []byte(fence))
	r := search.Result{Start: 0, End: 2, HitStart: 0, HitEnd: 2}
	p := &Printer{Truncate: 5}

	first, last, before, after := p.window(src, r, regexp(t, "a1"))
	if first != 0 || last != 2 || before != 0 || after != 0 {
		t.Errorf("window = [%d,%d] %d/%d, want [0,2] 0/0", first, last, before, after)
	}
}

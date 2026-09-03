package render

import (
	"testing"

	"github.com/riadafridishibly/mdgrep/internal/search"
)

// A block is scored whole, so HitStart is where the fence opens and says
// nothing about where in it the match is. Truncating from there printed the
// opening lines of a long block and cut the one line the caller searched for.
func TestWindowKeepsTheMatchedLine(t *testing.T) {
	// The whole fence is one block: lines 0-9, with the match on line 6.
	r := search.Result{Start: 0, End: 9, HitStart: 0, HitEnd: 9, Hits: []int{6}}
	p := &Printer{Truncate: 3}

	first, last := p.window(r)
	if first > 6 || last < 6 {
		t.Errorf("window = [%d,%d], want it to hold the match on line 6", first, last)
	}
	if last-first+1 > 3 {
		t.Errorf("window = [%d,%d], want at most 3 lines", first, last)
	}
}

// A match near the top must not slide the window down: the region's own first
// lines are the context, and there is nothing to skip to reach the hit.
func TestWindowStartsAtTheRegionWhenTheHitIsInReach(t *testing.T) {
	r := search.Result{Start: 0, End: 9, HitStart: 0, HitEnd: 9, Hits: []int{1}}
	p := &Printer{Truncate: 3}

	first, _ := p.window(r)
	if first != 0 {
		t.Errorf("first = %d, want 0", first)
	}
}

// A matcher that cannot point at any one line -- an anchor search selects a
// heading by name, and a filter searches with the empty pattern -- leaves no
// match lines at all, and the window falls back to the block's own first line.
func TestWindowFallsBackToTheBlockStart(t *testing.T) {
	r := search.Result{Start: 0, End: 9, HitStart: 0, HitEnd: 9}
	p := &Printer{Truncate: 3}

	first, last := p.window(r)
	if first != 0 || last != 2 {
		t.Errorf("window = [%d,%d], want [0,2]", first, last)
	}
}

// A region inside the budget is printed whole.
func TestWindowLeavesAShortRegionAlone(t *testing.T) {
	r := search.Result{Start: 0, End: 2, HitStart: 0, HitEnd: 2, Hits: []int{1}}
	p := &Printer{Truncate: 5}

	first, last := p.window(r)
	if first != 0 || last != 2 {
		t.Errorf("window = [%d,%d], want [0,2]", first, last)
	}
}

// A rung the page already covers has nothing left to give, so it is left out
// and the note names only what widening would still buy.
func TestSpanNoteDropsTheRungsThePageCovers(t *testing.T) {
	r := search.Result{
		HitStart: 12, HitEnd: 13,
		Rungs: []search.Rung{
			{Kind: "item", Start: 12, End: 13},
			{Kind: "list", Start: 12, End: 14},
			{Kind: "section", Start: 10, End: 14},
		},
	}
	p := &Printer{Span: true}

	// The page holds the item whole, so the item rung goes and the two rungs
	// with lines left to show stay.
	shown := []outLine{{12, true}, {13, true}}
	want := "(list 13-15, section 11-15)"
	if got := p.spanNote(r, shown); got != want {
		t.Errorf("note = %q, want %q", got, want)
	}
	// A page short of the item keeps every rung.
	part := []outLine{{12, true}}
	wantAll := "(item 13-14, list 13-15, section 11-15)"
	if got := p.spanNote(r, part); got != wantAll {
		t.Errorf("note = %q, want %q", got, wantAll)
	}
	// Every rung covered by what printed drops the note entirely.
	whole := []outLine{{10, true}, {11, true}, {12, true}, {13, true}, {14, true}}
	if got := p.spanNote(r, whole); got != "" {
		t.Errorf("note = %q, want none once the page covers every rung", got)
	}
}

// A hit before the first heading has no section to widen to, and a ladder that
// cannot end on one says nothing at all.
func TestSpanNoteNeedsASection(t *testing.T) {
	r := search.Result{
		HitStart: 0, HitEnd: 0,
		Rungs: []search.Rung{{Kind: "paragraph", Start: 0, End: 0}},
	}
	p := &Printer{Span: true}
	if got := p.spanNote(r, []outLine{{0, true}}); got != "" {
		t.Errorf("note = %q, want none before the first heading", got)
	}
}

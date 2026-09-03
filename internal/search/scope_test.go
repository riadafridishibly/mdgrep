package search

import "testing"

// A scope is what an earlier stage of a pipeline selected, so a search inside
// one answers about those lines and no others.
func TestScopeKeepsTheSearchInsideItsRegions(t *testing.T) {
	// Lines 4..7 are the Install list; the Later section starts at 9.
	res := find(t, "install", Options{Scope: []Region{{Start: 9, End: 11}}})
	if len(res) != 0 {
		t.Fatalf("got %d results inside Later, want none: %+v", len(res), res)
	}
	if res := find(t, "install", Options{Scope: []Region{{Start: 4, End: 7}}}); len(res) == 0 {
		t.Fatal("got nothing inside the list the scope names")
	}
}

// Narrowing goes by containment. A node the region holds whole is a
// candidate; one it would have cut in half is not, so a stage hands on the
// nodes the last one selected rather than the pieces it clipped.
func TestScopeAdmitsOnlyNodesItHoldsWhole(t *testing.T) {
	// The nested list runs 5..6; line 5 alone holds one of its items.
	item := find(t, "brew install", Options{Scope: []Region{{Start: 5, End: 5}}})
	if len(item) != 1 || item[0].Start != 5 {
		t.Fatalf("the item the region holds should match: %+v", item)
	}
	// "Install the CLI" is line 4, the text of an item running 4..6. The item
	// straddles a region of 5..6 and stays out of it, so nothing the search
	// hands back reaches above the region -- whatever else inside it the
	// pattern happens to score.
	for _, r := range find(t, "Install the CLI", Options{Scope: []Region{{Start: 5, End: 6}}}) {
		if r.HitStart < 5 {
			t.Errorf("a node straddling the region matched: %+v", r)
		}
	}
}

// Several regions of one file are one scope: a stage that selected three
// nodes hands on three, and the next searches all of them.
func TestScopeTakesMoreThanOneRegion(t *testing.T) {
	res := find(t, "install", Options{Scope: []Region{{Start: 2, End: 2}, {Start: 5, End: 5}}})
	if len(res) != 2 {
		t.Fatalf("got %d results, want 2: %+v", len(res), res)
	}
}

// A nil scope is every line, which is what every run without a stream behind
// it has always meant.
func TestNoScopeSearchesTheWholeFile(t *testing.T) {
	all := find(t, "install", Options{})
	if len(all) == 0 {
		t.Fatal("an unscoped search should still find the whole file")
	}
}

// Widening is not narrowing: the scope decides what may match, and --section
// then says how much of the file to print around it. A stage that asks for a
// section is asking to see past the region it was handed.
func TestScopeGatesTheHitAndNotTheWidening(t *testing.T) {
	res := find(t, "brew install", Options{Scope: []Region{{Start: 5, End: 5}}, Section: true})
	if len(res) != 1 {
		t.Fatalf("got %d results, want 1", len(res))
	}
	if res[0].Start != 2 || res[0].End != 7 {
		t.Fatalf("range = %d..%d, want the Install section 2..7", res[0].Start, res[0].End)
	}
}

// A filter that climbs -- --todo reporting the task a sub-bullet hangs under --
// climbs to a node the region may not hold. Containment is about the node a
// stage selects, not about the block that happened to match inside it, so the
// climb is checked too: otherwise a later stage prints, and an edit rewrites,
// lines no stage ever selected.
func TestScopeAdmitsOnlyTheNodeAStageSelects(t *testing.T) {
	// Line 7 is a plain sub-bullet of the checked item on line 6.
	res := findTasks(t, "runbook", Options{Scope: []Region{{Start: 7, End: 7}}, Task: TaskAny})
	if len(res) != 0 {
		t.Fatalf("the task filter climbed out of the region: %+v", res)
	}
	// The region holding the whole item still selects it.
	if res := findTasks(t, "runbook", Options{Scope: []Region{{Start: 6, End: 7}}, Task: TaskAny}); len(res) != 1 {
		t.Fatalf("got %d results inside the whole item, want 1: %+v", len(res), res)
	}
}

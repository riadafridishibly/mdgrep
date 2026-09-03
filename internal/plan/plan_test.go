package plan

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/riadafridishibly/mdgrep/internal/edit"
)

func change(entry, start, end int) planChange {
	return planChange{entry: entry, Change: edit.Change{Op: edit.OpReplaceNode, Start: start, End: end}}
}

// Two entries over one node refuse the plan, and the refusal names the entry
// that actually holds the lines -- which is not always the entry sorted just
// before, since one long change can cover several that start after it.
func TestOrderChangesNamesTheEntryThatHoldsTheLines(t *testing.T) {
	tests := []struct {
		name    string
		changes []planChange
		want    string
	}{
		{"disjoint", []planChange{change(1, 0, 2), change(2, 4, 6)}, ""},
		{"first line of the file", []planChange{change(1, 0, 0), change(2, 0, 0)}, "entry 1"},
		{"reached by an earlier long change", []planChange{
			change(1, 0, 20), change(2, 5, 5), change(3, 8, 8),
		}, "entry 1"},
		{"empty", nil, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := orderChanges(tc.changes)
			switch {
			case tc.want == "" && err != nil:
				t.Fatalf("orderChanges = %v, want no error", err)
			case tc.want != "" && err == nil:
				t.Fatalf("orderChanges = nil, want a refusal naming %s", tc.want)
			case tc.want != "" && !strings.Contains(err.Error(), tc.want):
				t.Errorf("orderChanges = %v, want it to name %s", err, tc.want)
			}
		})
	}
}

// Two spellings of one file are one file. Every entry that reaches it has to
// be gathered under the name the plan used first, or the file is planned twice
// against the original and written twice, the second write undoing the first.
func TestDocCacheHoldsOneFileUnderOneName(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "x.md")
	if err := os.WriteFile(path, []byte("# One\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link.md")
	if err := os.Symlink(path, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	d := newDocCache()
	if _, held, err := d.get(path); err != nil || held != path {
		t.Fatalf("get(%q) = %q, %v", path, held, err)
	}
	for _, spelling := range []string{link, filepath.Join(dir, ".", "x.md")} {
		_, held, err := d.get(spelling)
		if err != nil {
			t.Fatalf("get(%q): %v", spelling, err)
		}
		if held != path {
			t.Errorf("get(%q) = %q, want it held under %q", spelling, held, path)
		}
	}
	if len(d.order) != 1 {
		t.Errorf("order = %v, want one file", d.order)
	}
}

// The bucket a new spelling is looked up in is keyed on the size and time the
// file was last written, and the two spellings are stat'd at different
// moments. Anything writing the file in between puts them in different
// buckets, and a plan that took the miss for an unread file would write the
// file twice, the second write undoing the first.
func TestDocCacheFindsAFileThatChangedBetweenTwoSpellings(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "x.md")
	if err := os.WriteFile(path, []byte("# One\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link.md")
	if err := os.Symlink(path, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	d := newDocCache()
	if _, held, err := d.get(path); err != nil || held != path {
		t.Fatalf("get(%q) = %q, %v", path, held, err)
	}
	// Somebody else writes the file: its size and mtime both move, so the
	// stat behind the second spelling lands in a bucket the first is not in.
	if err := os.WriteFile(path, []byte("# One\n\nand more\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, held, err := d.get(link)
	if err != nil {
		t.Fatalf("get(%q): %v", link, err)
	}
	if held != path {
		t.Errorf("get(%q) = %q, want it held under %q", link, held, path)
	}
	if len(d.order) != 1 {
		t.Errorf("order = %v, want one file: the plan read it twice", d.order)
	}
}

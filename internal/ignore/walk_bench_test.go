package ignore

import (
	"os"
	"path/filepath"
	"testing"
)

// A walk asks Excluded about every entry of every directory it passes, so what
// one miss costs is what the ignore files cost a search that matches nothing.
func BenchmarkExcludedMiss(b *testing.B) {
	root := b.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte("node_modules\ndist\n*.log\n"), 0o644); err != nil {
		b.Fatal(err)
	}
	dir := filepath.Join(root, "packages", "app", "src", "components")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		b.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "widget.md"), nil, 0o644); err != nil {
		b.Fatal(err)
	}
	f := New(root).Root()
	for _, d := range descend(root, dir) {
		entries, err := os.ReadDir(d)
		if err != nil {
			b.Fatal(err)
		}
		f = f.Enter(d, entries)
	}
	b.ResetTimer()
	for b.Loop() {
		f.Excluded("widget.md", false)
	}
}

// descend lists root and every directory between it and leaf, outermost first.
func descend(root, leaf string) []string {
	var out []string
	for d := leaf; d != root; d = filepath.Dir(d) {
		out = append([]string{d}, out...)
	}
	return append([]string{root}, out...)
}

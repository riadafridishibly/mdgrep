package walk

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

var markdown = map[string]bool{".md": true}

// plant writes each named file under a fresh directory and makes it the
// working directory, so the paths a walk reports are the names given here.
func plant(t *testing.T, files ...string) {
	t.Helper()
	dir := t.TempDir()
	for _, name := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("# heading\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Chdir(dir)
}

func collected(t *testing.T, paths []string, hidden, noIgnore bool) []string {
	t.Helper()
	files, stdin, err := Files(paths, markdown, hidden, noIgnore)
	if err != nil {
		t.Fatal(err)
	}
	if stdin {
		t.Fatal("the walk asked for stdin")
	}
	return files
}

func same(t *testing.T, got, want []string) {
	t.Helper()
	if !slices.Equal(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

// The walk splits across goroutines, and it still has to hand back the files
// in the order one goroutine reading the tree in listing order would have
// found them in.
func TestWalkReportsFilesInListingOrder(t *testing.T) {
	var files []string
	for _, top := range []string{"a", "b", "c", "d", "e", "f"} {
		for i := range 4 {
			files = append(files,
				fmt.Sprintf("%s/%d.md", top, i),
				fmt.Sprintf("%s/sub%d/deep.md", top, i),
				fmt.Sprintf("%s/sub%d/nested/deeper.md", top, i))
		}
	}
	files = append(files, "top.md")
	plant(t, files...)

	var want []string
	err := filepath.WalkDir(".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && filepath.Ext(path) == ".md" {
			want = append(want, path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	same(t, collected(t, []string{"."}, false, false), want)
}

// A file two of the named paths both lead to is searched once, and from the
// path that reached it first.
func TestWalkReportsAFileOnce(t *testing.T) {
	plant(t, "docs/a.md", "docs/b.md", "top.md")
	same(t, collected(t, []string{"docs", ".", "top.md", "top.md"}, false, false),
		[]string{"docs/a.md", "docs/b.md", "top.md"})
}

// Build output and caches are most of the files under a repository and none of
// the ones worth reading, and a name that is not markdown is not worth opening.
func TestWalkSkipsWhatItIsNotThereFor(t *testing.T) {
	plant(t,
		"keep.md",
		"notes.txt",
		".hidden.md",
		".config/a.md",
		"node_modules/pkg/readme.md",
		"vendor/lib/doc.md",
		"target/out.md",
	)
	same(t, collected(t, []string{"."}, false, false), []string{"keep.md"})
}

// --hidden reaches the dotted names, and --no-ignore reaches everything else.
func TestWalkOpensUpWhenAsked(t *testing.T) {
	plant(t, "keep.md", ".config/a.md", "node_modules/pkg/readme.md")
	same(t, collected(t, []string{"."}, true, false), []string{".config/a.md", "keep.md"})
	same(t, collected(t, []string{"."}, false, true),
		[]string{"keep.md", "node_modules/pkg/readme.md"})
}

// The rules the tree carries reach the walk itself, not only the matcher's own
// tests: a directory left out is never descended into.
func TestWalkObeysTheIgnoreFiles(t *testing.T) {
	plant(t, "keep.md", "drafts/a.md", "drafts/deep/b.md", "notes.md")
	if err := os.WriteFile(".gitignore", []byte("drafts/\nnotes.md\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	same(t, collected(t, []string{"."}, false, false), []string{"keep.md"})
	got := collected(t, []string{"."}, false, true)
	want := []string{"drafts/a.md", "drafts/deep/b.md", "keep.md", "notes.md"}
	if !slices.Equal(got, want) {
		t.Fatalf("with --no-ignore: got %v, want %v", got, want)
	}
}

// A root spelled with a "./" in front of it is the same root, rules and all.
func TestWalkTakesAnUncleanRoot(t *testing.T) {
	plant(t, "docs/keep.md", "docs/drop.md")
	if err := os.WriteFile(filepath.Join("docs", ".gitignore"), []byte("drop.md\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, root := range []string{"docs", "./docs", "docs/"} {
		got := collected(t, []string{root}, false, false)
		if len(got) != 1 || !strings.HasSuffix(got[0], "keep.md") {
			t.Fatalf("root %q: got %v, want one keep.md", root, got)
		}
	}
}

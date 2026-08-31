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
	files, stdin, unread, err := Files(paths, markdown, hidden, noIgnore)
	if err != nil {
		t.Fatal(err)
	}
	if len(unread) > 0 {
		t.Fatalf("the walk could not read %v", unread)
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

// A walk keys what it has found on a cleaned path, so a file named in any
// other spelling of itself has to be cleaned before it is asked about --
// otherwise it is searched a second time, which shows up as two hits on one
// node, a count gate that refuses a single match, and an edit planned twice
// against the same original.
func TestWalkReportsAFileOnceHoweverItIsSpelled(t *testing.T) {
	plant(t, "docs/a.md", "top.md")
	spellings := []string{"./top.md", "docs//a.md", "docs/../docs/a.md", "./docs/./a.md"}
	same(t, collected(t, append([]string{"."}, spellings...), false, false),
		[]string{"docs/a.md", "top.md"})
}

// The spelling the caller used is the one reported back, so a file named
// before the walk that would also find it keeps the name it was asked for.
func TestWalkKeepsTheSpellingItWasGiven(t *testing.T) {
	plant(t, "top.md")
	same(t, collected(t, []string{"./top.md", "."}, false, false), []string{"./top.md"})
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

// A directory the walk cannot read is a hole in the search rather than an
// empty one, so it comes back named. The rest of the tree is still collected:
// the files that could be read are worth reporting whatever the caller decides
// to do about the ones that could not.
func TestUnreadableDirectoryIsReportedNotDropped(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads a directory whatever its mode says")
	}
	plant(t, "top.md", "closed/hidden.md")
	if err := os.Chmod("closed", 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod("closed", 0o755) })
	if _, err := os.ReadDir("closed"); err == nil {
		t.Skip("the filesystem does not enforce directory modes")
	}

	files, _, unread, err := Files([]string{"."}, markdown, false, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(unread) != 1 {
		t.Fatalf("unread = %v, want the one directory that could not be read", unread)
	}
	if !strings.Contains(unread[0].Error(), "closed") {
		t.Errorf("unread = %v, want it to name closed", unread[0])
	}
	same(t, files, []string{"top.md"})
}

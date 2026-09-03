package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// unwritable makes dir unwritable for the rest of the test, or skips, on the
// same terms as unreadable: the test is about what mdgrep does with a write it
// cannot make, not about which systems refuse one.
func unwritable(t *testing.T, dir string) {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Skip("root writes a directory whatever its mode says")
	}
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(dir, 0o755) })
	if f, err := os.CreateTemp(dir, "probe-*"); err == nil {
		f.Close()
		os.Remove(f.Name())
		t.Skip("the filesystem does not enforce directory modes")
	}
}

// twoTrees writes one task item under each of two directories and returns
// their paths, so a test can make one of them unwritable and watch what the
// other one does.
func twoTrees(t *testing.T) (root, first, second string) {
	t.Helper()
	root = t.TempDir()
	for _, d := range []string{"a", "b"} {
		if err := os.Mkdir(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, d, "t.md"), []byte("- [ ] task\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root, filepath.Join(root, "a", "t.md"), filepath.Join(root, "b", "t.md")
}

// TestEditWritesNothingWhenOneFileCannotBeWritten is the whole of the change:
// the flag path used to write each file as it reached it, so a directory it
// could not write left the run half applied and the files before it already
// changed. It stages every file before renaming any, the way --apply does.
func TestEditWritesNothingWhenOneFileCannotBeWritten(t *testing.T) {
	root, first, second := twoTrees(t)
	unwritable(t, filepath.Dir(second))

	_, stderr, code := capture(t, "--check", "--multi", "task", root, "-W")
	if code != 2 {
		t.Fatalf("code %d, want 2\n%s", code, stderr)
	}
	if !strings.Contains(stderr, "nothing was written") {
		t.Fatalf("stderr does not say the run was held back: %q", stderr)
	}
	if got := read(t, first); got != "- [ ] task\n" {
		t.Fatalf("the writable file was changed anyway: %q", got)
	}
}

// TestEditStillWritesEveryFileItCan keeps the other half honest: staging is
// not a reason for a run that can be carried out to carry out less.
func TestEditStillWritesEveryFileItCan(t *testing.T) {
	root, first, second := twoTrees(t)
	stdout, stderr, code := capture(t, "--check", "--multi", "task", root, "-W")
	if code != 0 {
		t.Fatalf("code %d, want 0\n%s", code, stderr)
	}
	for _, path := range []string{first, second} {
		if got := read(t, path); got != "- [x] task\n" {
			t.Fatalf("%s: %q, want the ticked item", path, got)
		}
	}
	// The report is printed after the renames now, so it should still name
	// both files rather than only the one written before a failure.
	for _, want := range []string{"a", "b"} {
		if !strings.Contains(stdout, filepath.Join(want, "t.md")) {
			t.Fatalf("stdout does not report %s:\n%s", want, stdout)
		}
	}
}

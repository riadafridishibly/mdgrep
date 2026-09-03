package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// unreadable makes dir unreadable for the rest of the test, or skips: a run as
// root reads it anyway, and so does a filesystem that does not carry the mode.
func unreadable(t *testing.T, dir string) {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Skip("root reads a directory whatever its mode says")
	}
	if err := os.Chmod(dir, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(dir, 0o755) })
	if _, err := os.ReadDir(dir); err == nil {
		t.Skip("the filesystem does not enforce directory modes")
	}
}

// TestUnreadableDirectoryIsAnError covers a search that could not look
// everywhere it was asked to. Returning "no matches" there reads as an answer
// about the tree rather than an admission that most of it went unread.
func TestUnreadableDirectoryIsAnError(t *testing.T) {
	dir := t.TempDir()
	unreadable(t, dir)
	_, stderr, code := capture(t, "anything", dir)
	if code != 2 {
		t.Fatalf("code %d, want 2\n%s", code, stderr)
	}
	if !strings.Contains(stderr, "permission denied") {
		t.Fatalf("stderr does not say why: %q", stderr)
	}
}

// TestUnreadableSubdirectoryStillSearchesTheRest keeps the hole from swallowing
// the tree around it: the files that could be read are still reported, and the
// run still says the answer is short of the whole.
func TestUnreadableSubdirectoryStillSearchesTheRest(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "top.md"), []byte("# heading\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	closed := filepath.Join(root, "closed")
	if err := os.Mkdir(closed, 0o755); err != nil {
		t.Fatal(err)
	}
	unreadable(t, closed)

	stdout, stderr, code := capture(t, "heading", root)
	if code != 2 {
		t.Fatalf("code %d, want 2\n%s", code, stderr)
	}
	if !strings.Contains(stdout, "top.md") {
		t.Fatalf("the readable file went unreported:\n%s", stdout)
	}
	if !strings.Contains(stderr, "permission denied") {
		t.Fatalf("stderr does not say why: %q", stderr)
	}
}

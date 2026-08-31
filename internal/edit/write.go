package edit

import (
	"os"
	"path/filepath"
)

// Staged is a file's new contents, written beside the file and not yet in
// place. Splitting the write from the rename is what lets a caller editing
// several files find out that one of them cannot be written before any of
// them has been.
type Staged struct {
	path string // where Commit renames to, after symlinks are resolved
	tmp  string
	mode os.FileMode
}

// Stage writes content to a temporary file in the target's directory, so the
// rename that follows is within one filesystem and cannot fail for want of
// space. The original is untouched until Commit.
func Stage(path, content string) (*Staged, error) {
	// A file reached through a symlink is still that file. Renaming over the
	// link would leave the document it points at untouched and put a regular
	// file where the link was, so the edit is written where it was read from.
	if target, err := filepath.EvalSymlinks(path); err == nil {
		path = target
	}
	mode := os.FileMode(0o644)
	if info, err := os.Stat(path); err == nil {
		mode = info.Mode().Perm()
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".mdgrep-*")
	if err != nil {
		return nil, err
	}
	s := &Staged{path: path, tmp: f.Name(), mode: mode}

	if _, err := f.WriteString(content); err != nil {
		f.Close()
		s.Discard()
		return nil, err
	}
	if err := f.Chmod(mode); err != nil {
		f.Close()
		s.Discard()
		return nil, err
	}
	if err := f.Close(); err != nil {
		s.Discard()
		return nil, err
	}
	return s, nil
}

// Commit renames the staged file over the original. A failed Commit leaves
// nothing behind: the staged file is removed either way.
func (s *Staged) Commit() error {
	if err := os.Rename(s.tmp, s.path); err != nil {
		s.Discard()
		return err
	}
	return nil
}

// Discard removes the staged file, leaving the original as it was. Calling it
// after Commit is harmless.
func (s *Staged) Discard() {
	os.Remove(s.tmp)
}

// Write replaces a file's contents through a temporary file in the same
// directory, so an interrupted run leaves the original in place rather than a
// half-written document.
func Write(path, content string) error {
	s, err := Stage(path, content)
	if err != nil {
		return err
	}
	return s.Commit()
}

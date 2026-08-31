package edit

import (
	"os"
	"path/filepath"
)

// Write replaces a file's contents through a temporary file in the same
// directory, so an interrupted run leaves the original in place rather than a
// half-written document.
func Write(path, content string) error {
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
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)

	if _, err := f.WriteString(content); err != nil {
		f.Close()
		return err
	}
	if err := f.Chmod(mode); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

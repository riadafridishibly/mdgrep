// Package ignore applies the ignore files a search walks past: .gitignore,
// the .ignore that search tools read but git does not, and the repository's
// own .git/info/exclude. A repository's build output, caches and data
// directories are usually most of the files under it and none of the files
// worth reading, and the walk itself is most of what a small search costs, so
// skipping them whole is the difference between a search that feels instant
// and one that does not.
package ignore

import (
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// sources are the ignore files a single directory can hold, lowest precedence
// first. Git reads .gitignore; .ignore is the convention search tools added
// for rules that should keep a file out of results without keeping it out of
// the repository. Both start with a dot, which is what lets Enter find them in
// a sorted listing without reading to the end of it.
var sources = [...]string{".gitignore", ".ignore"}

// Matcher answers whether the ignore files above a path leave it out. Patterns
// are read relative to the file holding them and the nearest file has the last
// word, so a Matcher keeps one layer per directory and asks them from the
// inside out.
//
// It is built for one depth-first walk: Enter each directory on the way in and
// ask Excluded about what is inside it. Layers the walk has left behind are
// dropped as it moves on, so the stack is only ever as deep as the walk. A nil
// *Matcher excludes nothing, which is what --no-ignore asks for.
//
// Excluded answers about the path it is given and not about the path's
// parents, because the walk is expected to stop at a directory it is told to
// skip. Nothing under an excluded directory is reachable in git either.
type Matcher struct {
	root    string // the directory the walk was started from, as it was given
	absRoot string // the same directory, absolute, so layers above it are reachable
	layers  []layer
}

type layer struct {
	dir   string // absolute directory the patterns are relative to
	rules ruleSet
}

// New returns a Matcher for a walk rooted at root, seeded with the rules that
// already cover it: the repository's exclude list, and the ignore files
// between the work tree root and root. Git applies them only inside a work
// tree, so a root with no repository over it starts empty.
func New(root string) *Matcher {
	m := &Matcher{root: filepath.Clean(root)}
	abs, err := filepath.Abs(m.root)
	if err != nil {
		return m
	}
	m.absRoot = abs
	m.layers = seed(abs)
	return m
}

// seed collects the layers that cover root without being root, outermost
// first. The exclude list comes first because git ranks it under every
// .gitignore, and the layers are asked in reverse.
func seed(root string) []layer {
	worktree := workTree(root)
	if worktree == "" {
		return nil
	}
	var out []layer
	if l, ok := compose(worktree, excludeFile(worktree)); ok {
		out = append(out, l)
	}
	var dirs []string
	for dir := filepath.Dir(root); dir == worktree || relTo(worktree, dir) != ""; dir = filepath.Dir(dir) {
		dirs = append(dirs, dir)
		if dir == worktree {
			break
		}
	}
	for i := len(dirs) - 1; i >= 0; i-- {
		if l, ok := read(dirs[i]); ok {
			out = append(out, l)
		}
	}
	return out
}

// workTree climbs to the top of the git work tree holding dir, or returns "".
// A repository checked out inside another one stops the climb at its own root:
// it does not inherit the outer one's rules.
func workTree(dir string) string {
	for {
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

// excludeFile locates the repository's own list of patterns, the one kept out
// of version control. The .git entry is a directory in a clone and a file
// pointing elsewhere in a worktree or a submodule.
func excludeFile(worktree string) string {
	dot := filepath.Join(worktree, ".git")
	info, err := os.Stat(dot)
	if err != nil {
		return ""
	}
	if info.IsDir() {
		return filepath.Join(dot, "info", "exclude")
	}
	data, err := os.ReadFile(dot)
	if err != nil {
		return ""
	}
	dir := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(string(data)), "gitdir:"))
	if dir == "" {
		return ""
	}
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(worktree, dir)
	}
	return filepath.Join(dir, "info", "exclude")
}

// Enter notes the ignore files of a directory the walk is about to descend
// into. Those patterns govern what is inside the directory rather than the
// directory itself, so Enter belongs after Excluded has judged it.
//
// entries is the directory listing the walk already holds. Nearly no directory
// has an ignore file, and looking for one in a listing already in hand beats
// asking the filesystem for a file that is not there.
func (m *Matcher) Enter(dir string, entries []fs.DirEntry) {
	if m == nil {
		return
	}
	m.trim(dir)

	// os.ReadDir sorts, and "." sorts ahead of every character a name can
	// otherwise start with, so the ignore files are at the front or nowhere.
	var present [len(sources)]bool
	found := false
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), ".") {
			break
		}
		if i := slices.Index(sources[:], e.Name()); i >= 0 {
			present[i], found = true, true
		}
	}
	if !found {
		return
	}

	abs := m.abs(dir)
	var rules ruleSet
	for i, ok := range present {
		if ok {
			rules.addFile(filepath.Join(abs, sources[i]))
		}
	}
	if len(rules.rules) > 0 {
		m.layers = append(m.layers, layer{dir: abs, rules: rules})
	}
}

// Excluded reports whether the rules leave path out.
//
// The nearest file wins, so layers answer from the inside out and the first
// one to speak settles it. Speaking is not the same as excluding: a file whose
// last matching line is a "!" says "keep this", and it says it loudly enough
// to stop the file above from excluding it.
func (m *Matcher) Excluded(path string, isDir bool) bool {
	if m == nil || len(m.layers) == 0 {
		return false
	}
	m.trim(path)
	abs := m.abs(path)
	for i := len(m.layers) - 1; i >= 0; i-- {
		rel := relTo(m.layers[i].dir, abs)
		if rel == "" {
			continue
		}
		p := newProbe(rel, isDir)
		if excluded, spoke := m.layers[i].rules.verdict(&p); spoke {
			return excluded
		}
	}
	return false
}

// trim drops the layers the walk has moved out of. A depth-first walk leaves a
// directory for good the moment it reports a path outside it. A layer sitting
// on the path itself stays: entering a directory must not throw away the rules
// that were already seeded for it.
func (m *Matcher) trim(path string) {
	abs := m.abs(path)
	for len(m.layers) > 0 && !covers(m.layers[len(m.layers)-1].dir, abs) {
		m.layers = m.layers[:len(m.layers)-1]
	}
}

// covers reports whether rules kept for dir still have a say over path.
func covers(dir, path string) bool {
	return dir == path || relTo(dir, path) != ""
}

// abs turns a path the walk reported back into an absolute one. The walk
// builds every path from the root it was handed, so the root is a prefix of
// all of them — except under a root of ".", which children are reported
// without.
func (m *Matcher) abs(path string) string {
	switch {
	case path == m.root:
		return m.absRoot
	case m.root == ".":
		return filepath.Join(m.absRoot, path)
	default:
		return filepath.Join(m.absRoot, strings.TrimPrefix(path, m.root+string(filepath.Separator)))
	}
}

// relTo expresses path the way an ignore file in dir reads it: relative and
// slash separated, or empty when path is not inside dir at all.
func relTo(dir, path string) string {
	prefix := dir
	if !strings.HasSuffix(prefix, string(filepath.Separator)) {
		prefix += string(filepath.Separator)
	}
	if !strings.HasPrefix(path, prefix) {
		return ""
	}
	return filepath.ToSlash(path[len(prefix):])
}

// read builds the layer for one directory out of the ignore files it holds. It
// is for the directories above the walk, where there is no listing in hand.
func read(dir string) (layer, bool) {
	var rules ruleSet
	for _, name := range sources {
		rules.addFile(filepath.Join(dir, name))
	}
	if len(rules.rules) == 0 {
		return layer{}, false
	}
	return layer{dir: dir, rules: rules}, true
}

// compose builds a layer from one named file whose patterns are read relative
// to dir, which is how the repository exclude list works.
func compose(dir, file string) (layer, bool) {
	var rules ruleSet
	if file == "" {
		return layer{}, false
	}
	rules.addFile(file)
	if len(rules.rules) == 0 {
		return layer{}, false
	}
	return layer{dir: dir, rules: rules}, true
}

// ruleSet is a directory's rules in the order they were written, since the
// last one to match is the one that decides.
//
// Most lines in a real ignore file spell something out: a bare name, or a path
// under the file that holds them. Both are indexed by what they spell and
// found with one map lookup. Only the lines with a wildcard in them are
// scanned, from the back, and only as far as an index that could still outrank
// what the two lookups already found.
type ruleSet struct {
	rules    []rule
	literals map[string][]int // name -> indices of the rules spelling it, ascending
	paths    map[string][]int // relative path -> the same, for anchored rules
	globs    []int            // indices of the wild rules, ascending
}

func (s *ruleSet) addFile(path string) {
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	s.add(strings.Split(string(data), "\n"))
}

// add appends the rules these lines carry, keeping the order they came in and
// filing each one where verdict will look for it.
func (s *ruleSet) add(lines []string) {
	for _, line := range lines {
		r, ok := parse(line)
		if !ok {
			continue
		}
		s.rules = append(s.rules, r)
		i := len(s.rules) - 1
		switch r.kind {
		case literal:
			if s.literals == nil {
				s.literals = make(map[string][]int)
			}
			s.literals[r.text] = append(s.literals[r.text], i)
		case pathLiteral:
			if s.paths == nil {
				s.paths = make(map[string][]int)
			}
			s.paths[r.text] = append(s.paths[r.text], i)
		default:
			s.globs = append(s.globs, i)
		}
	}
}

// verdict reports what these rules say about a path, and whether they said
// anything at all.
func (s *ruleSet) verdict(p *probe) (excluded, spoke bool) {
	best := -1
	for _, indices := range [...][]int{s.literals[p.base], s.paths[p.rel]} {
		for _, i := range indices {
			if r := s.rules[i]; (!r.dirOnly || p.isDir) && i > best {
				best = i
			}
		}
	}
	for j := len(s.globs) - 1; j >= 0; j-- {
		i := s.globs[j]
		if i < best {
			break
		}
		if s.rules[i].matches(p) {
			best = i
			break
		}
	}
	if best < 0 {
		return false, false
	}
	return !s.rules[best].negate, true
}

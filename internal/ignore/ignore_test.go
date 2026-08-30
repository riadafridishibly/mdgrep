package ignore

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// tree writes a set of files, each named by its path relative to a fresh
// directory, and returns that directory.
func tree(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for name, body := range files {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// survivors walks from start the way the collector in main does and returns
// the files the Matcher let through, named relative to root and sorted.
func survivors(t *testing.T, root, start string) []string {
	t.Helper()
	m := New(start)
	var out []string
	var walk func(dir string, f Frame)
	walk = func(dir string, f Frame) {
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		f = f.Enter(dir, entries)
		for _, e := range entries {
			name := e.Name()
			path := filepath.Join(dir, name)
			if e.IsDir() {
				if !strings.HasPrefix(name, ".") && !f.Excluded(path, true) {
					walk(path, f)
				}
				continue
			}
			if strings.HasPrefix(name, ".") || f.Excluded(path, false) {
				continue
			}
			rel, err := filepath.Rel(root, path)
			if err != nil {
				t.Fatal(err)
			}
			out = append(out, filepath.ToSlash(rel))
		}
	}
	walk(start, m.Root())
	slices.Sort(out)
	return out
}

func check(t *testing.T, got, want []string) {
	t.Helper()
	if !slices.Equal(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestDirectoryPatternSkipsWholeTree(t *testing.T) {
	root := tree(t, map[string]string{
		".gitignore":       "/data/\nbuild/\n",
		"a.md":             "",
		"data/b.md":        "",
		"data/deep/c.md":   "",
		"docs/build/d.md":  "",
		"docs/e.md":        "",
		"docs/building.md": "",
	})
	check(t, survivors(t, root, root), []string{"a.md", "docs/building.md", "docs/e.md"})
}

// A pattern ending in "/" is about directories, so a plain file of that name
// stays.
func TestDirectoryPatternLeavesFileOfSameName(t *testing.T) {
	root := tree(t, map[string]string{
		".gitignore": "build/\n",
		"build":      "not a directory",
		"build.md":   "",
	})
	check(t, survivors(t, root, root), []string{"build", "build.md"})
}

// "corpus/*" excludes what is inside corpus, not corpus itself, so the walk
// has to go in and find the one file taken back out of it.
func TestContentsPatternKeepsTheDirectoryWalkable(t *testing.T) {
	root := tree(t, map[string]string{
		".gitignore":              "/data/corpus/*\n!/data/corpus/README.md\n",
		"data/corpus/README.md":   "",
		"data/corpus/archive.md":  "",
		"data/corpus/sub/deep.md": "",
	})
	check(t, survivors(t, root, root), []string{"data/corpus/README.md"})
}

func TestNegationTakesBackAnExclusion(t *testing.T) {
	root := tree(t, map[string]string{
		".gitignore": "*.md\n!keep.md\n",
		"keep.md":    "",
		"drop.md":    "",
	})
	check(t, survivors(t, root, root), []string{"keep.md"})
}

// The .gitignore nearest a file has the last word, even when a file above it
// said the opposite.
func TestNearestFileWins(t *testing.T) {
	root := tree(t, map[string]string{
		".gitignore":       "*.md\n",
		"docs/.gitignore":  "!*.md\ndraft.md\n",
		"top.md":           "",
		"docs/keep.md":     "",
		"docs/draft.md":    "",
		"docs/sub/deep.md": "",
	})
	check(t, survivors(t, root, root), []string{"docs/keep.md", "docs/sub/deep.md"})
}

// A directory's own .gitignore governs what is inside it, not the directory
// itself, so "*" does not make the directory disappear.
func TestDirectoryIsNotJudgedByItsOwnFile(t *testing.T) {
	root := tree(t, map[string]string{
		"docs/.gitignore": "*\n!keep.md\n",
		"docs/keep.md":    "",
		"docs/drop.md":    "",
	})
	check(t, survivors(t, root, root), []string{"docs/keep.md"})
}

// Searching one directory of a repository still obeys the rules its root sets.
func TestRulesAboveTheSearchRootApply(t *testing.T) {
	root := tree(t, map[string]string{
		".git/HEAD":        "ref: refs/heads/main\n",
		".gitignore":       "vendored.md\n",
		"docs/keep.md":     "",
		"docs/vendored.md": "",
	})
	check(t, survivors(t, root, filepath.Join(root, "docs")), []string{"docs/keep.md"})
}

// Without a repository above it there is nothing to climb to, so a .gitignore
// beside the search root is not reached from inside it.
func TestRulesAboveAreOnlyReadInsideARepository(t *testing.T) {
	root := tree(t, map[string]string{
		".gitignore":       "vendored.md\n",
		"docs/keep.md":     "",
		"docs/vendored.md": "",
	})
	check(t, survivors(t, root, filepath.Join(root, "docs")), []string{"docs/keep.md", "docs/vendored.md"})
}

// The walk moves between siblings, and rules from one must not leak into the
// next.
// A repository checked out inside another one keeps its own rules. Climbing
// past its root would hand it the outer repository's.
func TestNestedRepositoryDoesNotInheritTheOuterOne(t *testing.T) {
	root := tree(t, map[string]string{
		".git/HEAD":           "ref: refs/heads/main\n",
		".gitignore":          "notes.md\n",
		"inner/.git/HEAD":     "ref: refs/heads/main\n",
		"inner/notes.md":      "",
		"inner/deep/notes.md": "",
	})
	check(t, survivors(t, root, filepath.Join(root, "inner")),
		[]string{"inner/deep/notes.md", "inner/notes.md"})
}

func TestSiblingRulesDoNotLeak(t *testing.T) {
	root := tree(t, map[string]string{
		"a/.gitignore": "note.md\n",
		"a/note.md":    "",
		"a/keep.md":    "",
		"b/note.md":    "",
	})
	check(t, survivors(t, root, root), []string{"a/keep.md", "b/note.md"})
}

// .ignore says what should stay out of search results without going into the
// repository, and it outranks the .gitignore beside it.
func TestDotIgnoreIsReadAndOutranksGitignore(t *testing.T) {
	root := tree(t, map[string]string{
		".gitignore":  "drafts/\n",
		".ignore":     "!drafts/\nnotes.md\n",
		"keep.md":     "",
		"notes.md":    "",
		"drafts/a.md": "",
	})
	check(t, survivors(t, root, root), []string{"drafts/a.md", "keep.md"})
}

// The repository keeps its own list of patterns out of version control, and
// git ranks it under every .gitignore.
func TestExcludeFileIsReadAndRanksUnderGitignore(t *testing.T) {
	root := tree(t, map[string]string{
		".git/HEAD":         "ref: refs/heads/main\n",
		".git/info/exclude": "*.md\n",
		".gitignore":        "!keep.md\n",
		"keep.md":           "",
		"drop.md":           "",
	})
	check(t, survivors(t, root, root), []string{"keep.md"})
}

// A worktree or a submodule keeps its .git as a file naming the real one, and
// the exclude list is over there with it.
func TestExcludeFileIsFoundThroughAGitdirFile(t *testing.T) {
	root := tree(t, map[string]string{
		".git":                   "gitdir: ./elsewhere\n",
		"elsewhere/info/exclude": "drop.md\n",
		"keep.md":                "",
		"drop.md":                "",
	})
	check(t, survivors(t, root, root), []string{"elsewhere/info/exclude", "keep.md"})
}

// A root of "." gets its children reported without it in front, so taking the
// root off a path must not eat the dot that starts a hidden name.
func TestPathsResolveUnderADotRoot(t *testing.T) {
	t.Chdir(t.TempDir())
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	m := New(".")
	for path, want := range map[string]string{
		".":        wd,
		"a.md":     filepath.Join(wd, "a.md"),
		".hidden":  filepath.Join(wd, ".hidden"),
		"sub/a.md": filepath.Join(wd, "sub", "a.md"),
	} {
		if got := m.abs(path); got != want {
			t.Errorf("abs(%q) = %q, want %q", path, got, want)
		}
	}
}

func TestNilMatcherExcludesNothing(t *testing.T) {
	var m *Matcher
	f := m.Root().Enter("anywhere", nil)
	if f.Excluded("anything.md", false) {
		t.Fatal("a nil Matcher excluded a path")
	}
}

// A relative search root reports relative paths, and they have to land on the
// same layers an absolute one would.
func TestRelativeRootMatchesAbsolute(t *testing.T) {
	root := tree(t, map[string]string{
		".gitignore": "/data/\n",
		"a.md":       "",
		"data/b.md":  "",
	})
	t.Chdir(root)
	check(t, survivors(t, ".", "."), []string{"a.md"})
}

// os.ReadDir sorts, and thirteen punctuation characters sort ahead of ".", so
// a sibling spelled with one of them must not stop the scan before it reaches
// the ignore files.
func TestIgnoreFilesAreFoundBehindPunctuation(t *testing.T) {
	root := tree(t, map[string]string{
		".gitignore":  "sub/\n",
		"+page.md":    "",
		"keep.md":     "",
		"sub/drop.md": "",
	})
	check(t, survivors(t, root, root), []string{"+page.md", "keep.md"})
}

// A root written with a "./" in front of it names the same directory as one
// without, and the walk reports the paths under it the same way either way.
func TestUncleanSearchRootReadsItsOwnRules(t *testing.T) {
	root := tree(t, map[string]string{
		"docs/.gitignore": "drop.md\n",
		"docs/keep.md":    "",
		"docs/drop.md":    "",
	})
	t.Chdir(root)
	check(t, survivors(t, ".", "./docs"), []string{"docs/keep.md"})
}

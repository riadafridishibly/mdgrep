package ignore

import (
	"strings"
	"testing"
)

// The wants in this table are what git itself answered: each case was put to
// "git check-ignore --no-index" in a scratch repository holding exactly that
// ignore file and that path.
var ruleCases = []struct {
	patterns string // the ignore file, one pattern per "|"
	path     string
	isDir    bool
	want     bool
}{
	{"build/", "build", true, true},
	{"build/", "build", false, false},
	{"build/", "a/build", true, true},
	{"/build", "build", true, true},
	{"/build", "a/build", true, false},
	{"*.log", "a.log", false, true},
	{"*.log", ".log", false, true},
	{"*.log", "a/b.log", false, true},
	{"*.log", "alog", false, false},
	{"*.log|!keep.log", "keep.log", false, false},
	{"*.log|!keep.log", "drop.log", false, true},
	{"doc/*.md", "doc/a.md", false, true},
	{"doc/*.md", "doc/sub/a.md", false, false},
	{"**/foo", "foo", false, true},
	{"**/foo", "a/foo", false, true},
	{"**/foo", "a/b/foo", false, true},
	{"a/**/b", "a/b", false, true},
	{"a/**/b", "a/x/b", false, true},
	{"a/**/b", "a/x/y/b", false, true},
	{"a/**/b", "b", false, false},
	{"abc/**", "abc/x", false, true},
	{"abc/**", "abc", true, false},
	{"?ar", "bar", false, true},
	{"?ar", "ar", false, false},
	{"[abc]at", "bat", false, true},
	{"[abc]at", "dat", false, false},
	{"\\#lit", "#lit", false, true},
	{"\\!lit", "!lit", false, true},
	{"sp   ", "sp", false, true},
	{"node_modules", "a/b/node_modules", true, true},
	{"/*.md|!/README.md", "PLAN.md", false, true},
	{"/*.md|!/README.md", "README.md", false, false},
	{"/*.md|!/README.md", "docs/PLAN.md", false, false},
	{"data/corpus/*|!data/corpus/README.md", "data/corpus", true, false},
	{"data/corpus/*|!data/corpus/README.md", "data/corpus/README.md", false, false},
	{"data/corpus/*|!data/corpus/README.md", "data/corpus/other.md", false, true},
	{"*|!keep.md", "keep.md", false, false},
	{"*|!keep.md", "drop.md", false, true},
	{"logs/**/*.txt", "logs/a.txt", false, true},
	{"logs/**/*.txt", "logs/a/b.txt", false, true},
}

func TestRulesAgreeWithGit(t *testing.T) {
	for _, c := range ruleCases {
		var set ruleSet
		set.add(strings.Split(c.patterns, "|"))
		p := newProbe(c.path, c.isDir)
		excluded, _ := set.verdict(&p)
		if excluded != c.want {
			t.Errorf("%q against %q (isDir=%v): got %v, want %v",
				c.path, c.patterns, c.isDir, excluded, c.want)
		}
	}
}

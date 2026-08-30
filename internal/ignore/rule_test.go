package ignore

import (
	"strings"
	"testing"
	"time"
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
	{"**/**/foo", "a/b/foo", false, true},
	{"a/**/**/b", "a/b", false, true},
	{"a/**/**/b", "a/x/y/b", false, true},
	{"**/x/**", "a/x/b", false, true},
	{"**/x/**", "a/x", false, false},
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

// A "**" stands for any number of directories, so matching one means trying
// the lengths in turn. A pattern holding several must not cost every
// combination of those lengths.
func TestRepeatedDoubleStarsMatchInTime(t *testing.T) {
	r, ok := parse(strings.Repeat("**/", 12) + "zzz")
	if !ok {
		t.Fatal("the pattern did not parse")
	}
	p := newProbe(strings.Trim(strings.Repeat("a/", 24), "/"), false)
	done := make(chan bool, 1)
	go func() { done <- r.matches(&p) }()
	select {
	case matched := <-done:
		if matched {
			t.Fatal("the pattern matched a path it does not name")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("matching a pattern with repeated ** did not finish")
	}
}

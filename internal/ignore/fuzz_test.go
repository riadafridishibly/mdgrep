package ignore

import (
	"strings"
	"testing"
)

var patternSeeds = []string{
	"build/",
	"/build",
	"*.log",
	"!keep.log",
	"doc/*.md",
	"**/foo",
	"a/**/b",
	"abc/**",
	"?ar",
	"[abc]at",
	`\#lit`,
	`\!lit`,
	"trailing   ",
	`escaped\ `,
	"#comment",
	"",
	"/",
	"!",
	"**",
	"a//b",
	"[",
	`\`,
	strings.Repeat("*/", 40) + "x",
}

var pathSeeds = []string{
	"a.md",
	"docs/a.md",
	"a/b/c/d/e.md",
	".hidden",
	"build",
	"abc",
	"abc/x/y",
	"",
	"/",
	strings.Repeat("x/", 40) + "y",
}

// FuzzRuleNeverPanics keeps the parser and the matcher honest about input they
// did not expect. An ignore file is written by hand and read by everyone, so a
// line nobody meant to write must cost a search nothing worse than not
// matching.
func FuzzRuleNeverPanics(f *testing.F) {
	for _, line := range patternSeeds {
		for _, path := range pathSeeds {
			f.Add(line, path, false)
		}
	}
	f.Fuzz(func(t *testing.T, line, path string, isDir bool) {
		if len(line) > 1<<12 || len(path) > 1<<12 {
			return
		}
		r, ok := parse(line)
		if !ok {
			return
		}
		p := newProbe(path, isDir)
		r.matches(&p)
	})
}

// FuzzPrefilterAgreesWithFullMatch guards the shortcut an anchored wild
// pattern takes. Ruling a pattern out by its first segment is only sound if it
// never rules out a path the full segment walk would have matched.
func FuzzPrefilterAgreesWithFullMatch(f *testing.F) {
	for _, line := range patternSeeds {
		for _, path := range pathSeeds {
			f.Add(line, path, false)
		}
	}
	f.Fuzz(func(t *testing.T, line, path string, isDir bool) {
		if len(line) > 1<<12 || len(path) > 1<<12 {
			return
		}
		r, ok := parse(line)
		if !ok || r.kind != pathGlob {
			return
		}
		p := newProbe(path, isDir)
		got := r.matches(&p)

		full := r
		full.first = "" // the same rule with the shortcut switched off
		q := newProbe(path, isDir)
		if want := full.matches(&q); got != want {
			t.Fatalf("%q against %q: prefilter says %v, full match says %v", line, path, got, want)
		}
	})
}

// FuzzNegationIsSymmetric ties the two halves of a rule together: writing "!"
// in front of a pattern must change what the rule decides without changing
// what it recognises. If the two ever disagree about which paths they are
// about, a "!" line stops taking back the line above it.
func FuzzNegationIsSymmetric(f *testing.F) {
	for _, line := range patternSeeds {
		for _, path := range pathSeeds {
			f.Add(line, path, false)
		}
	}
	f.Fuzz(func(t *testing.T, line, path string, isDir bool) {
		if len(line) > 1<<12 || len(path) > 1<<12 {
			return
		}
		if strings.HasPrefix(line, "!") || strings.HasPrefix(line, "#") {
			return // already negated, or a comment that "!" would not rescue
		}
		plain, ok := parse(line)
		if !ok {
			return
		}
		negated, ok := parse("!" + line)
		if !ok {
			t.Fatalf("%q parsed but %q did not", line, "!"+line)
		}
		if negated.negate == plain.negate {
			t.Fatalf("%q: negate is %v either way", line, plain.negate)
		}
		p := newProbe(path, isDir)
		q := newProbe(path, isDir)
		if got, want := negated.matches(&q), plain.matches(&p); got != want {
			t.Fatalf("%q matches %q = %v, but negated = %v", line, path, want, got)
		}
	})
}

// FuzzVerdictFollowsTheLastMatch is the rule the whole file format rests on:
// of the lines that match, the last one decides. Whatever order they arrive
// in, a set has to agree with a plain scan from the back.
func FuzzVerdictFollowsTheLastMatch(f *testing.F) {
	for _, path := range pathSeeds {
		f.Add(strings.Join(patternSeeds, "\n"), path, false)
	}
	f.Fuzz(func(t *testing.T, file, path string, isDir bool) {
		if len(file) > 1<<12 || len(path) > 1<<12 {
			return
		}
		var set ruleSet
		set.add(strings.Split(file, "\n"))

		p := newProbe(path, isDir)
		excluded, spoke := set.verdict(&p)

		wantSpoke, wantExcluded := false, false
		for i := len(set.rules) - 1; i >= 0; i-- {
			q := newProbe(path, isDir)
			if set.rules[i].matches(&q) {
				wantSpoke, wantExcluded = true, !set.rules[i].negate
				break
			}
		}
		if spoke != wantSpoke || excluded != wantExcluded {
			t.Fatalf("%q against %d rules: got (%v, %v), want (%v, %v)",
				path, len(set.rules), excluded, spoke, wantExcluded, wantSpoke)
		}
	})
}

package ignore

import (
	"fmt"
	"testing"
)

// A walk asks about every directory it passes and every file it might read, so
// the paths that match nothing are the ones that decide how fast a search is.
// They must not cost a pass over the whole file: the names and the anchored
// paths are indexed, and only the lines with a wildcard in them are scanned.
func benchSet() ruleSet {
	lines := []string{"# build output", "node_modules", "dist", "target", ".DS_Store"}
	for i := range 200 {
		lines = append(lines, fmt.Sprintf("generated-%d", i))     // literal
		lines = append(lines, fmt.Sprintf("packages/p%d/lib", i)) // pathLiteral
	}
	for i := range 40 {
		lines = append(lines, fmt.Sprintf("*.gen%d", i))           // suffix
		lines = append(lines, fmt.Sprintf("tmp%d?", i))            // nameGlob
		lines = append(lines, fmt.Sprintf("apps/*/build%d/**", i)) // pathGlob
	}
	var set ruleSet
	set.add(lines)
	return set
}

func benchPaths() []string {
	var out []string
	for i := range 500 {
		out = append(out, fmt.Sprintf("packages/p%d/src/components/widget.tsx", i))
	}
	return out
}

func BenchmarkVerdictMiss(b *testing.B) {
	set, paths := benchSet(), benchPaths()
	b.ResetTimer()
	for b.Loop() {
		for _, path := range paths {
			p := newProbe(path, false)
			set.verdict(&p)
		}
	}
}

func BenchmarkVerdictHit(b *testing.B) {
	set := benchSet()
	var paths []string
	for i := range 500 {
		paths = append(paths, fmt.Sprintf("packages/p%d/lib", i))
	}
	b.ResetTimer()
	for b.Loop() {
		for _, path := range paths {
			p := newProbe(path, true)
			set.verdict(&p)
		}
	}
}

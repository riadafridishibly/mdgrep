package match

import (
	"strings"
	"testing"
)

// Most blocks in a search never match, so the miss path is the one that decides
// how fast mdgrep is. It must stay well ahead of the hit path: a miss should be
// settled by the byte-wise prefilter, without building any rune tables.
func benchLines() []string {
	base := []string{
		"The deployment pipeline runs on every push to main and takes nine minutes.",
		"- [ ] rotate the staging credentials before the next release window opens",
		"## Prerequisites for running the integration suite locally on a laptop",
		"See `docs/architecture.md` for the full request lifecycle and its notes.",
		"| column one | column two | column three | a fourth column of values |",
	}
	var out []string
	for i := range 2000 {
		out = append(out, base[i%len(base)]+strings.Repeat(" tail", i%3))
	}
	return out
}

func BenchmarkFuzzyMiss(b *testing.B) {
	lines := benchLines()
	m, _ := New("zqxjv wprmb", Options{Mode: Fuzzy, IgnoreCase: true, MinScore: 0.55})
	b.ResetTimer()
	for b.Loop() {
		for _, l := range lines {
			m.Score(l)
		}
	}
}

func BenchmarkFuzzyHit(b *testing.B) {
	lines := benchLines()
	m, _ := New("deploy pipeline", Options{Mode: Fuzzy, IgnoreCase: true, MinScore: 0.55})
	b.ResetTimer()
	for b.Loop() {
		for _, l := range lines {
			m.Score(l)
		}
	}
}

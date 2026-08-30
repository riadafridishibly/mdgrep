package match

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func modeOf(n int) Mode { return Mode(((n % 3) + 3) % 3) }

// checkSpans holds Spans to what render.highlight needs of it: the ranges have
// to be in order, disjoint and inside the line, or a highlight is dropped or
// painted over the wrong bytes.
func checkSpans(t *testing.T, spans []Span, line, pat string) {
	t.Helper()
	prev := 0
	for _, s := range spans {
		if s.Start < prev || s.End <= s.Start || s.End > len(line) {
			t.Fatalf("span %+v after %d, len(line) = %d\npat  %q\nline %q", s, prev, len(line), pat, line)
		}
		// Rune boundaries only mean anything when both sides have them. A
		// pattern that is itself raw bytes is matched as raw bytes, and lands
		// mid-rune quite legitimately.
		if utf8.ValidString(line) && utf8.ValidString(pat) && !utf8.RuneStart(line[s.Start]) {
			t.Fatalf("span %+v splits a rune\npat  %q\nline %q", s, pat, line)
		}
		prev = s.End
	}
}

// FuzzMatch drives every matcher over arbitrary text. Score and Spans read the
// same string through two different indexes — bytes for regexp and substring,
// runes for fuzzy — and this is where the two have to agree with the line they
// were given.
func FuzzMatch(f *testing.F) {
	f.Add("parseMarkDown deploy_key", "pmd dk", 2, true, false, 0.4)
	f.Add("İstanbul KELVIN ﬁle", "istanbul kelvin", 2, true, false, 0.3)
	f.Add("a-b_c/d.e:f", "abc", 0, false, true, 0.0)
	f.Add("- [ ] rotate the deploy key", "deploy", 1, true, true, 0.7)
	f.Add("| col | col |", "co", 0, false, false, 0.5)
	f.Add("\xff\xfe invalid bytes \xc9", "invalid", 2, true, false, 0.3)
	f.Add("aaaaaaaaab", "ab", 2, false, false, 0.1)

	f.Fuzz(func(t *testing.T, text, pat string, mode int, fold, word bool, min float64) {
		if len(text) > 1<<14 || len(pat) > 512 {
			return
		}
		opt := Options{Mode: modeOf(mode), IgnoreCase: fold, Word: word, MinScore: min}
		m, err := New(pat, opt)
		if err != nil {
			return
		}

		score, ok := m.Score(text)
		if ok && (score < 0 || score > 1) {
			t.Fatalf("score %v outside [0,1] for pat %q", score, pat)
		}
		if again, ok2 := m.Score(text); again != score || ok2 != ok {
			t.Fatalf("Score is not deterministic: (%v,%v) then (%v,%v)", score, ok, again, ok2)
		}

		checkSpans(t, m.Spans(text), text, pat)

		// A literal search has one obviously-correct answer to compare
		// against, and folding is where it is easiest to get wrong.
		if opt.Mode == Substring && !word && len(text) <= 512 && utf8.ValidString(text) && utf8.ValidString(pat) {
			want := strings.Contains(text, pat)
			if fold {
				want = foldSearchRef(text, pat)
			}
			if ok != want {
				t.Fatalf("Substring(fold=%v).Score(%q) with pat %q = %v, want %v", fold, text, pat, ok, want)
			}
		}

		// A matcher is used a line at a time when printing, so every line of a
		// block has to be safe to hand it on its own.
		for line := range strings.SplitSeq(text, "\n") {
			checkSpans(t, m.Spans(line), line, pat)
		}

		// Not inverts the decision and never highlights, which is what lets -v
		// select the blocks a pattern rejects.
		if _, inv := Not(m).Score(text); inv == ok {
			t.Fatalf("Not(m) agreed with m: both %v", ok)
		}
		if s := Not(m).Spans(text); s != nil {
			t.Fatalf("Not(m) offered spans %v", s)
		}

		// Any is grep's repeated -e: a block matches when any alternative does,
		// and the best score wins.
		all := Any([]Matcher{m, All()})
		if s, ok := all.Score(text); !ok || s < score {
			t.Fatalf("Any(m, All) scored (%v,%v), below m's %v", s, ok, score)
		}
		checkSpans(t, all.Spans(text), text, pat)
	})
}

// FuzzMerge checks the coalescing Any and the fuzzy matcher both lean on: the
// result has to come back sorted and disjoint whatever order it went in.
func FuzzMerge(f *testing.F) {
	f.Add([]byte{0, 3, 2, 5, 7, 9})
	f.Add([]byte{5, 6, 0, 1, 5, 6})
	f.Fuzz(func(t *testing.T, raw []byte) {
		if len(raw) > 512 {
			return
		}
		var in []Span
		for i := 0; i+1 < len(raw); i += 2 {
			lo, hi := int(raw[i]), int(raw[i+1])
			if lo > hi {
				lo, hi = hi, lo
			}
			in = append(in, Span{lo, hi})
		}
		out := Merge(in)

		covered := map[int]bool{}
		for _, s := range in {
			for i := s.Start; i < s.End; i++ {
				covered[i] = true
			}
		}
		prev := -1
		for _, s := range out {
			if s.Start > s.End {
				t.Fatalf("Merge produced an inverted span %+v", s)
			}
			if prev >= 0 && s.Start <= prev {
				t.Fatalf("Merge produced %+v touching or overlapping the span ending at %d", s, prev)
			}
			prev = s.End
			for i := s.Start; i < s.End; i++ {
				if !covered[i] {
					t.Fatalf("Merge covers byte %d that no input span did", i)
				}
				delete(covered, i)
			}
		}
		if len(covered) > 0 {
			t.Fatalf("Merge dropped %d covered bytes", len(covered))
		}
	})
}

// TestSpansPastInvalidUTF8 pins the width a byte that is not valid UTF-8 is
// given. Ranging a string yields the replacement rune for one, which measures
// three bytes where the input held one, and a span built from that width runs
// off the end of the line it was meant to highlight.
func TestSpansPastInvalidUTF8(t *testing.T) {
	for _, tc := range []struct{ text, pat string }{
		{"\xc9", "\xeb"},
		{"a\xffb", "ab"},
		{"\xf0\x28 deploy", "deploy"},
	} {
		m, err := New(tc.pat, Options{Mode: Fuzzy, MinScore: 0})
		if err != nil {
			t.Fatal(err)
		}
		for _, s := range m.Spans(tc.text) {
			if s.Start < 0 || s.End > len(tc.text) {
				t.Errorf("Spans(%q) with pat %q gave %+v, past len %d", tc.text, tc.pat, s, len(tc.text))
			}
		}
	}
}

// TestSubstringFoldWidth pins the case-insensitive literal search to what
// strings.EqualFold would say. A rune does not keep its width when it folds —
// "K" is three bytes and folds onto a one-byte "k" — so a search that assumes
// the match is as wide as the pattern both misses it and, having found it,
// highlights the wrong bytes.
func TestSubstringFoldWidth(t *testing.T) {
	cases := []struct {
		text, pat string
		want      bool
	}{
		{"\u212Aelvin road", "k", true},      // KELVIN SIGN folds onto "k"
		{"\u212Aelvin road", "kelvin", true}, // and keeps folding into the word
		{"plain kelvin", "\u212A", true},     // the same the other way round
		{"\u212Belsius", "\u00e5", true},     // ANGSTROM SIGN folds onto "\u00e5"
		{"nothing here", "zz", false},
	}
	for _, c := range cases {
		m, err := New(c.pat, Options{Mode: Substring, IgnoreCase: true})
		if err != nil {
			t.Fatal(err)
		}
		if _, got := m.Score(c.text); got != c.want {
			t.Errorf("Score(%q) with pat %q = %v, want %v", c.text, c.pat, got, c.want)
		}
		checkSpans(t, m.Spans(c.text), c.text, c.pat)

		// The fast path and the folding scan have to agree with the answer
		// strings.EqualFold gives over every rune-aligned window.
		if _, got := m.Score(c.text); got != foldSearchRef(c.text, c.pat) {
			t.Errorf("Score(%q) with pat %q disagrees with EqualFold", c.text, c.pat)
		}
	}
}

// foldSearchRef is the slow, obviously-correct answer: does any rune-aligned
// window of text fold equal to pat?
func foldSearchRef(text, pat string) bool {
	for i := 0; i <= len(text); i++ {
		if i < len(text) && !utf8.RuneStart(text[i]) {
			continue
		}
		for j := i; j <= len(text); j++ {
			if j < len(text) && !utf8.RuneStart(text[j]) {
				continue
			}
			if strings.EqualFold(text[i:j], pat) {
				return true
			}
		}
	}
	return false
}

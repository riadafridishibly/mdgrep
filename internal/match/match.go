// Package match provides the loose matchers mdgrep searches with: a
// token-wise fuzzy matcher, plain substring, and regexp.
package match

import (
	"fmt"
	"regexp"
	"strings"
	"unicode"
)

// Span is a byte range within a searched string.
type Span struct{ Start, End int }

// Matcher scores whole blocks and locates spans to highlight inside a
// single rendered line.
type Matcher interface {
	// Score reports how well text matches, in [0,1], and whether it counts.
	Score(text string) (float64, bool)
	// Spans returns byte ranges in line worth highlighting.
	Spans(line string) []Span
}

type Mode int

const (
	Fuzzy Mode = iota
	Substring
	Regexp
)

// New builds a matcher. When ignoreCase is false the match is case sensitive.
// minScore applies to Fuzzy only.
func New(mode Mode, pattern string, ignoreCase bool, minScore float64) (Matcher, error) {
	switch mode {
	case Regexp:
		expr := pattern
		if ignoreCase {
			expr = "(?i)" + expr
		}
		re, err := regexp.Compile(expr)
		if err != nil {
			return nil, err
		}
		return &reMatcher{re: re}, nil
	case Substring:
		if pattern == "" {
			return nil, fmt.Errorf("empty pattern")
		}
		return &substrMatcher{pat: pattern, fold: ignoreCase}, nil
	default:
		toks := strings.Fields(pattern)
		if len(toks) == 0 {
			return nil, fmt.Errorf("empty pattern")
		}
		m := &fuzzyMatcher{min: minScore, fold: ignoreCase}
		for _, t := range toks {
			m.tokens = append(m.tokens, []rune(t))
		}
		return m, nil
	}
}

// SmartCase reports whether a search should ignore case: yes unless the
// pattern itself contains an upper-case letter.
func SmartCase(pattern string) bool {
	for _, r := range pattern {
		if unicode.IsUpper(r) {
			return false
		}
	}
	return true
}

type reMatcher struct{ re *regexp.Regexp }

func (m *reMatcher) Score(text string) (float64, bool) {
	if m.re.MatchString(text) {
		return 1, true
	}
	return 0, false
}

func (m *reMatcher) Spans(line string) []Span {
	var out []Span
	for _, loc := range m.re.FindAllStringIndex(line, -1) {
		if loc[1] > loc[0] {
			out = append(out, Span{loc[0], loc[1]})
		}
	}
	return out
}

type substrMatcher struct {
	pat  string
	fold bool
}

func (m *substrMatcher) Score(text string) (float64, bool) {
	if m.index(text, 0) >= 0 {
		return 1, true
	}
	return 0, false
}

func (m *substrMatcher) index(s string, from int) int {
	if from >= len(s) {
		return -1
	}
	var i int
	if m.fold {
		i = indexFold(s[from:], m.pat)
	} else {
		i = strings.Index(s[from:], m.pat)
	}
	if i < 0 {
		return -1
	}
	return from + i
}

func (m *substrMatcher) Spans(line string) []Span {
	var out []Span
	for i := m.index(line, 0); i >= 0; i = m.index(line, i+len(m.pat)) {
		out = append(out, Span{i, i + len(m.pat)})
	}
	return out
}

func indexFold(s, sub string) int {
	// Fold only where it is safe to do so byte-wise; fall back to a scan.
	ls, lsub := strings.ToLower(s), strings.ToLower(sub)
	if len(ls) == len(s) && len(lsub) == len(sub) {
		return strings.Index(ls, lsub)
	}
	for i := range s {
		if len(s)-i < len(sub) {
			break
		}
		if strings.EqualFold(s[i:min(i+len(sub), len(s))], sub) {
			return i
		}
	}
	return -1
}

type fuzzyMatcher struct {
	tokens [][]rune
	min    float64
	fold   bool
}

func (m *fuzzyMatcher) Score(text string) (float64, bool) {
	t := newTarget(text, m.fold)
	if len(t.runes) == 0 {
		return 0, false
	}
	total := 0.0
	for _, tok := range m.tokens {
		s, _, ok := t.best(tok, m.fold)
		if !ok {
			return 0, false
		}
		total += s
	}
	avg := total / float64(len(m.tokens))
	return avg, avg >= m.min
}

func (m *fuzzyMatcher) Spans(line string) []Span {
	t := newTarget(line, m.fold)
	var out []Span
	for _, tok := range m.tokens {
		s, pos, ok := t.best(tok, m.fold)
		// A token can appear weakly on one line of a block that matched on
		// another; require it to stand on its own before painting it.
		if !ok || s < m.min*0.8 {
			continue
		}
		for _, p := range pos {
			out = append(out, Span{t.offs[p], t.offs[p] + t.widths[p]})
		}
	}
	return Merge(out)
}

// Merge sorts spans and coalesces overlapping or touching ones.
func Merge(spans []Span) []Span {
	if len(spans) < 2 {
		return spans
	}
	sorted := make([]Span, len(spans))
	copy(sorted, spans)
	for i := 1; i < len(sorted); i++ {
		for j := i; j > 0 && sorted[j].Start < sorted[j-1].Start; j-- {
			sorted[j], sorted[j-1] = sorted[j-1], sorted[j]
		}
	}
	out := sorted[:1]
	for _, s := range sorted[1:] {
		last := &out[len(out)-1]
		if s.Start <= last.End {
			if s.End > last.End {
				last.End = s.End
			}
			continue
		}
		out = append(out, s)
	}
	return out
}

type target struct {
	runes  []rune
	cmp    []rune // case-folded when folding
	offs   []int  // byte offset of each rune
	widths []int
}

func newTarget(s string, fold bool) *target {
	t := &target{}
	for i, r := range s {
		t.runes = append(t.runes, r)
		t.offs = append(t.offs, i)
		t.widths = append(t.widths, len(string(r)))
		if fold {
			r = unicode.ToLower(r)
		}
		t.cmp = append(t.cmp, r)
	}
	return t
}

// best finds the tightest subsequence match of tok and scores it. The score
// blends how densely the token's characters are packed, how many of them run
// consecutively, and whether the match starts on a word boundary.
func (t *target) best(tok []rune, fold bool) (float64, []int, bool) {
	pat := tok
	if fold {
		pat = make([]rune, len(tok))
		for i, r := range tok {
			pat[i] = unicode.ToLower(r)
		}
	}
	if len(pat) == 0 {
		return 0, nil, false
	}

	bestSpan := -1
	var bestPos []int
	for start := 0; start+len(pat) <= len(t.cmp); start++ {
		if t.cmp[start] != pat[0] {
			continue
		}
		end, ok := t.forward(pat, start)
		if !ok {
			break // no completion exists from here or any later start
		}
		// Re-derive positions right-to-left from the minimal end so runs of
		// characters pack together instead of spreading over the window.
		pos := t.backward(pat, end)
		if span := pos[len(pos)-1] - pos[0] + 1; bestSpan < 0 || span < bestSpan {
			bestSpan, bestPos = span, pos
		}
		if bestSpan == len(pat) {
			break // contiguous; nothing can beat it
		}
	}
	if bestPos == nil {
		return 0, nil, false
	}

	span := bestPos[len(bestPos)-1] - bestPos[0] + 1
	density := float64(len(pat)) / float64(span)
	consec := 1.0
	if len(pat) > 1 {
		runs := 0
		for i := 1; i < len(bestPos); i++ {
			if bestPos[i] == bestPos[i-1]+1 {
				runs++
			}
		}
		consec = float64(runs) / float64(len(pat)-1)
	}
	boundary := 0.0
	if t.isBoundary(bestPos[0]) {
		boundary = 1.0
	}
	return 0.5*density + 0.3*consec + 0.2*boundary, bestPos, true
}

func (t *target) forward(pat []rune, start int) (int, bool) {
	pi := 0
	for i := start; i < len(t.cmp); i++ {
		if t.cmp[i] == pat[pi] {
			pi++
			if pi == len(pat) {
				return i, true
			}
		}
	}
	return 0, false
}

func (t *target) backward(pat []rune, end int) []int {
	pos := make([]int, len(pat))
	pi := len(pat) - 1
	for i := end; i >= 0 && pi >= 0; i-- {
		if t.cmp[i] == pat[pi] {
			pos[pi] = i
			pi--
		}
	}
	return pos
}

func (t *target) isBoundary(i int) bool {
	if i == 0 {
		return true
	}
	prev, cur := t.runes[i-1], t.runes[i]
	if !unicode.IsLetter(prev) && !unicode.IsDigit(prev) {
		return true
	}
	return unicode.IsLower(prev) && unicode.IsUpper(cur)
}

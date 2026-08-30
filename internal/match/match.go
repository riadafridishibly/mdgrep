// Package match provides the matchers mdgrep searches with: regexp, plain
// substring, and a loose token-wise fuzzy matcher.
package match

import (
	"errors"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
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
	Regexp Mode = iota // the default, as it is in grep
	Substring
	Fuzzy
)

// Options configures a matcher.
type Options struct {
	Mode       Mode
	IgnoreCase bool
	MinScore   float64 // Fuzzy only
	Word       bool    // match only on word boundaries
}

// New builds a matcher. An empty pattern matches everything, as it does in
// grep.
func New(pattern string, opt Options) (Matcher, error) {
	switch opt.Mode {
	case Regexp:
		return newRegexp(pattern, opt)
	case Substring:
		if opt.Word {
			return newRegexp(regexp.QuoteMeta(pattern), opt)
		}
		if pattern == "" {
			return All(), nil
		}
		return &substrMatcher{pat: pattern, fold: opt.IgnoreCase}, nil
	default:
		if opt.Word {
			return nil, errors.New("word matching has no meaning for a fuzzy search")
		}
		toks := strings.Fields(pattern)
		if len(toks) == 0 {
			return All(), nil
		}
		m := &fuzzyMatcher{min: opt.MinScore, fold: opt.IgnoreCase}
		for _, t := range toks {
			m.tokens = append(m.tokens, []rune(t))
		}
		return m, nil
	}
}

func newRegexp(pattern string, opt Options) (Matcher, error) {
	// Compile what the user typed first, so a syntax error names their own
	// expression rather than the rewrite below.
	if _, err := regexp.Compile(pattern); err != nil {
		return nil, err
	}
	expr := pattern
	if opt.Word {
		expr = `\b(?:` + expr + `)\b`
	}
	// Blocks are matched as whole multi-line strings, so "^" and "$" are asked
	// to anchor to lines instead, which is what they mean in grep.
	flags := "(?m)"
	if opt.IgnoreCase {
		flags = "(?im)"
	}
	re, err := regexp.Compile(flags + expr)
	if err != nil {
		return nil, err
	}
	return &reMatcher{re: re}, nil
}

// Any matches whatever any of its matchers matches, the way repeated -e
// patterns are alternatives to each other in grep.
func Any(ms []Matcher) Matcher {
	if len(ms) == 1 {
		return ms[0]
	}
	return anyMatcher(ms)
}

type anyMatcher []Matcher

func (a anyMatcher) Score(text string) (float64, bool) {
	best, any := 0.0, false
	for _, m := range a {
		if s, ok := m.Score(text); ok && (!any || s > best) {
			best, any = s, true
		}
	}
	return best, any
}

func (a anyMatcher) Spans(line string) []Span {
	var out []Span
	for _, m := range a {
		out = append(out, m.Spans(line)...)
	}
	return Merge(out)
}

// Not selects what m rejects. There is nothing to highlight in a block that
// matched by not containing something.
func Not(m Matcher) Matcher { return notMatcher{m} }

type notMatcher struct{ inner Matcher }

func (n notMatcher) Score(text string) (float64, bool) {
	if _, ok := n.inner.Score(text); ok {
		return 0, false
	}
	return 1, true
}

func (notMatcher) Spans(string) []Span { return nil }

// All matches every block and highlights nothing. It backs the empty pattern,
// leaving the filters to say what a search selects.
func All() Matcher { return allMatcher{} }

type allMatcher struct{}

func (allMatcher) Score(string) (float64, bool) { return 1, true }
func (allMatcher) Spans(string) []Span          { return nil }

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
	if i, _ := m.index(text, 0); i >= 0 {
		return 1, true
	}
	return 0, false
}

// index finds the pattern at or after from and returns where it starts and how
// many bytes it took. The two are not the same thing under folding: "K" is
// three bytes and folds onto a one-byte "k", so the width has to be measured
// rather than assumed from the pattern.
func (m *substrMatcher) index(s string, from int) (at, width int) {
	if from > len(s) {
		return -1, 0
	}
	if !m.fold {
		i := strings.Index(s[from:], m.pat)
		if i < 0 {
			return -1, 0
		}
		return from + i, len(m.pat)
	}
	i, w := indexFold(s[from:], m.pat)
	if i < 0 {
		return -1, 0
	}
	return from + i, w
}

func (m *substrMatcher) Spans(line string) []Span {
	var out []Span
	for i, w := m.index(line, 0); i >= 0; i, w = m.index(line, i+w) {
		out = append(out, Span{i, i + w})
	}
	return out
}

// indexFold finds sub in s ignoring case, returning where it starts and how
// wide it is there.
func indexFold(s, sub string) (at, width int) {
	if sub == "" {
		return 0, 0
	}
	// Where lowering leaves both sides the same length, every rune folds
	// within its own width and the fast byte search is exact.
	ls, lsub := strings.ToLower(s), strings.ToLower(sub)
	if len(ls) == len(s) && len(lsub) == len(sub) {
		if i := strings.Index(ls, lsub); i >= 0 {
			return i, len(sub)
		}
		return -1, 0
	}
	for i := range s {
		if w, ok := foldPrefix(s[i:], sub); ok {
			return i, w
		}
	}
	return -1, 0
}

// foldPrefix reports whether s opens with prefix under case folding, and how
// many bytes of s that took.
func foldPrefix(s, prefix string) (int, bool) {
	n := 0
	for _, want := range prefix {
		if n >= len(s) {
			return 0, false
		}
		got, w := utf8.DecodeRuneInString(s[n:])
		if !foldEqual(got, want) {
			return 0, false
		}
		n += w
	}
	return n, true
}

// foldEqual is the rune-wise half of strings.EqualFold: two runes are the same
// letter when one is reachable from the other around the fold cycle.
func foldEqual(a, b rune) bool {
	if a == b {
		return true
	}
	for f := unicode.SimpleFold(a); f != a; f = unicode.SimpleFold(f) {
		if f == b {
			return true
		}
	}
	return false
}

type fuzzyMatcher struct {
	tokens [][]rune
	min    float64
	fold   bool
}

// couldContain reports whether text might hold the token, without building the
// rune tables scoring needs. A match requires the token's characters in order,
// so one IndexByte walk settles it, and in a search most blocks get no further
// than this.
func (m *fuzzyMatcher) couldContain(text string, tok []rune) bool {
	at := 0
	for _, r := range tok {
		// Non-ASCII needs the full unicode fold, and so do "i" and "k": they are
		// the only ASCII letters a non-ASCII rune ("İ", "K") folds onto. Leave
		// those to Score rather than rule the block out here.
		if r > unicode.MaxASCII || (m.fold && (r|0x20 == 'i' || r|0x20 == 'k')) {
			return true
		}
		i := strings.IndexByte(text[at:], byte(r))
		if m.fold {
			if alt, ok := swapCase(byte(r)); ok {
				if j := strings.IndexByte(text[at:], alt); j >= 0 && (i < 0 || j < i) {
					i = j
				}
			}
		}
		if i < 0 {
			return false
		}
		at += i + 1
	}
	return true
}

func swapCase(b byte) (byte, bool) {
	switch {
	case b >= 'a' && b <= 'z':
		return b - 'a' + 'A', true
	case b >= 'A' && b <= 'Z':
		return b - 'A' + 'a', true
	}
	return 0, false
}

func (m *fuzzyMatcher) Score(text string) (float64, bool) {
	for _, tok := range m.tokens {
		if !m.couldContain(text, tok) {
			return 0, false
		}
	}
	t := newTarget(text, m.fold, false)
	if len(t.cmp) == 0 {
		return 0, false
	}
	// Tokens are weighted by length, because a short one says little about
	// whether the block is what was asked for: "an" sits on a word boundary in
	// almost any prose and scores perfectly there, and averaging that in flat
	// would let it carry blocks that only spell "implementer" by accident.
	total, weight := 0.0, 0.0
	for _, tok := range m.tokens {
		s, _, ok := t.best(tok, m.fold)
		if !ok {
			return 0, false
		}
		w := float64(len(tok))
		total += s * w
		weight += w
	}
	avg := total / weight
	return avg, avg >= m.min
}

func (m *fuzzyMatcher) Spans(line string) []Span {
	t := newTarget(line, m.fold, true)
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

// charClass groups runes the way a reader groups them, so a match can be told
// apart by whether it starts a word, follows punctuation or lands mid-word.
type charClass uint8

const (
	classWhite charClass = iota
	classDelim
	classNonWord
	classLower
	classUpper
	classDigit
)

func classOf(r rune) charClass {
	switch {
	case r == ' ' || r == '\t' || r == '\n' || r == '\r':
		return classWhite
	case strings.ContainsRune("/,:;|_-.", r):
		return classDelim
	case unicode.IsDigit(r):
		return classDigit
	case unicode.IsUpper(r):
		return classUpper
	case unicode.IsLetter(r):
		return classLower
	default:
		return classNonWord
	}
}

// boundary scores how strongly a character reads as the start of something: a
// fresh word, then a path or snake_case delimiter, then other punctuation, then
// a camelCase or letter-to-digit hump. Mid-word scores nothing.
func boundary(prev, cur charClass) float64 {
	switch {
	case prev == classWhite:
		return 1
	case prev == classDelim:
		return 0.9
	case prev == classNonWord:
		return 0.8
	case prev == classLower && cur != classLower:
		return 0.9
	case prev == classDigit && cur != classDigit:
		return 0.9
	}
	return 0
}

type target struct {
	cmp    []rune // case-folded when folding
	class  []charClass
	offs   []int // byte offset of each rune; nil unless spans were asked for
	widths []int // byte width of each rune, likewise
}

// newTarget indexes s a rune at a time. Byte offsets are only needed to paint
// a highlight, so they are built on request: a search scores every block it
// reads and highlights almost none of them.
func newTarget(s string, fold, offsets bool) *target {
	n := utf8.RuneCountInString(s)
	t := &target{cmp: make([]rune, 0, n), class: make([]charClass, 0, n)}
	if offsets {
		t.offs = make([]int, 0, n)
		t.widths = make([]int, 0, n)
	}
	for i := 0; i < len(s); {
		// Decoding explicitly keeps a byte that is not valid UTF-8 one byte
		// wide, where the replacement rune "range" hands back would measure
		// three and put a span past the end of the line.
		r, w := utf8.DecodeRuneInString(s[i:])
		t.class = append(t.class, classOf(r))
		if offsets {
			t.offs = append(t.offs, i)
			t.widths = append(t.widths, w)
		}
		if fold {
			r = unicode.ToLower(r)
		}
		t.cmp = append(t.cmp, r)
		i += w
	}
	return t
}

// startBonus scores position i as the place a match opens. The head of the
// text is the strongest boundary there is.
func (t *target) startBonus(i int) float64 {
	if i == 0 {
		return 1
	}
	return boundary(t.class[i-1], t.class[i])
}

// jumpBonus scores position i as the place a match resumes after a gap.
// Structure inside a word counts in full, so "pmd" finds "parseMarkDown" and
// "dk" finds "deploy_key". Crossing whitespace counts for little: a pattern is
// already split on whitespace into tokens, so a token that has to jump a word
// is not what the reader asked for.
func (t *target) jumpBonus(i int) float64 {
	if t.class[i-1] == classWhite {
		return 0.3
	}
	return boundary(t.class[i-1], t.class[i])
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
		// Every start through pos[0] completes at this same end, and so
		// derives this same match. Resume past it instead of re-deriving it
		// once per character, which is what made a long line quadratic.
		start = pos[0]
	}
	if bestPos == nil {
		return 0, nil, false
	}

	span := bestPos[len(bestPos)-1] - bestPos[0] + 1
	density := float64(len(pat)) / float64(span)
	// Every character after the first earns its keep either by continuing a run
	// or by starting a new word, so an initialism like "pmd" over
	// "parseMarkDown" scores as well as a contiguous match does.
	quality := 1.0
	if len(bestPos) > 1 {
		sum := 0.0
		for i := 1; i < len(bestPos); i++ {
			if bestPos[i] == bestPos[i-1]+1 {
				sum++
				continue
			}
			sum += t.jumpBonus(bestPos[i])
		}
		quality = sum / float64(len(bestPos)-1)
	}
	return 0.45*density + 0.35*quality + 0.2*t.startBonus(bestPos[0]), bestPos, true
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

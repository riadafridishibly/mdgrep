package mdoc

import (
	"strconv"
	"strings"
	"unicode"
)

// AnchorStyle names one convention for turning a heading into the fragment a
// link points at. Generators disagree about punctuation, accents, case and
// what to do with a repeat, so mdgrep knows several and can try them all.
type AnchorStyle string

const (
	AnchorGitHub   AnchorStyle = "github"   // github.com, github-slugger
	AnchorGitLab   AnchorStyle = "gitlab"   // gitlab.com
	AnchorPython   AnchorStyle = "python"   // Python-Markdown, MkDocs
	AnchorKramdown AnchorStyle = "kramdown" // kramdown, Jekyll
	AnchorPandoc   AnchorStyle = "pandoc"   // pandoc
	AnchorLoose    AnchorStyle = "loose"    // letters and digits only: a catch-all
)

// AllAnchorStyles is every style, in the order a link is most likely to have
// come from one.
var AllAnchorStyles = []AnchorStyle{
	AnchorGitHub, AnchorGitLab, AnchorPython, AnchorKramdown, AnchorPandoc, AnchorLoose,
}

// Slug returns the anchor a style gives text, before any duplicate suffix.
// The same function normalises the pattern a search is given, so a link may be
// typed as its anchor ("the-foo-bar"), as the heading it points at ("The Foo
// Bar") or as anything in between: both sides land on the same slug.
func Slug(style AnchorStyle, text string) string {
	switch style {
	case AnchorGitLab:
		return slugGitLab(text)
	case AnchorPython:
		return slugPython(text)
	case AnchorKramdown:
		return slugKramdown(text)
	case AnchorPandoc:
		return slugPandoc(text)
	case AnchorLoose:
		return slugLoose(text)
	default:
		return slugGitHub(text)
	}
}

// HeadingAnchors returns the anchors of every heading in document order, one
// per style, duplicate suffixes included. Anchors are derived from the
// document as a whole because a repeated heading is only distinguishable by
// how many like it came before.
func (d *Doc) HeadingAnchors(styles []AnchorStyle) [][]string {
	seen := make([]map[string]int, len(styles))
	for i := range seen {
		seen[i] = map[string]int{}
	}
	out := make([][]string, len(d.Headings))
	for i, h := range d.Headings {
		text := anchorText(h.Node, d.data)
		row := make([]string, len(styles))
		for j, style := range styles {
			row[j] = unique(style, Slug(style, text), seen[j])
		}
		out[i] = row
	}
	return out
}

// unique mirrors what a generator does when two headings slug the same: a
// counter is appended, starting at one for the second heading.
func unique(style AnchorStyle, base string, seen map[string]int) string {
	n := seen[base]
	seen[base] = n + 1
	if n == 0 || base == "" {
		return base
	}
	sep := "-"
	if style == AnchorPython {
		sep = "_"
	}
	return base + sep + strconv.Itoa(n)
}

// GitHub keeps letters, digits, marks, hyphens and underscores, drops the rest
// of the punctuation and turns every space into a hyphen without collapsing
// runs, so "A  b -- c!" becomes "a--b----c".
func slugGitHub(s string) string {
	var b strings.Builder
	for _, r := range strings.TrimSpace(s) {
		switch {
		case r == '-' || r == '_':
			b.WriteRune(r)
		case unicode.IsSpace(r):
			b.WriteByte('-')
		case isWordRune(r):
			b.WriteRune(unicode.ToLower(r))
		}
	}
	return b.String()
}

// GitLab slugs as GitHub does but collapses runs of hyphens, and prefixes an
// all-digit anchor so it stays a usable identifier.
func slugGitLab(s string) string {
	out := collapse(slugGitHub(s), func(r rune) bool { return r == '-' })
	if out != "" && strings.IndexFunc(out, func(r rune) bool { return !unicode.IsDigit(r) }) < 0 {
		return "anchor-" + out
	}
	return out
}

// Python-Markdown strips the accents off Latin letters and discards whatever
// is left outside ASCII, keeps word characters, then collapses each run of
// spaces and hyphens into one hyphen.
func slugPython(s string) string {
	var kept strings.Builder
	for _, r := range foldAccents(s) {
		switch {
		case unicode.IsSpace(r) || r == '-':
			kept.WriteRune(r)
		case r == '_' || isASCIIWordRune(r):
			kept.WriteRune(unicode.ToLower(r))
		}
	}
	sep := func(r rune) bool { return unicode.IsSpace(r) || r == '-' }
	return collapse(strings.TrimSpace(kept.String()), sep)
}

// kramdown keeps only ASCII letters, digits, spaces and hyphens, and drops
// everything before the first letter so the anchor is a legal HTML id.
func slugKramdown(s string) string {
	var b strings.Builder
	for _, r := range strings.TrimLeftFunc(s, func(r rune) bool { return !isASCIILetter(r) }) {
		switch {
		case unicode.IsSpace(r):
			b.WriteByte('-')
		case r == '-' || isASCIIWordRune(r):
			b.WriteRune(unicode.ToLower(r))
		}
	}
	if b.Len() == 0 {
		return "section"
	}
	return b.String()
}

// pandoc keeps letters, digits, underscores, hyphens and full stops, joins the
// words with hyphens, and drops everything before the first letter.
func slugPandoc(s string) string {
	var kept strings.Builder
	for _, r := range s {
		switch {
		case unicode.IsSpace(r):
			kept.WriteByte(' ')
		case r == '_' || r == '-' || r == '.' || isWordRune(r):
			kept.WriteRune(unicode.ToLower(r))
		}
	}
	out := collapse(strings.TrimSpace(kept.String()), unicode.IsSpace)
	out = strings.TrimLeftFunc(out, func(r rune) bool { return !unicode.IsLetter(r) })
	if out == "" {
		return "section"
	}
	return out
}

// Loose reduces a heading to its letters and digits joined by single hyphens.
// It is what makes an anchor from a generator mdgrep has never heard of still
// find its heading, at the price of telling two headings apart only by the
// words in them.
func slugLoose(s string) string {
	sep := func(r rune) bool { return !isWordRune(r) }
	return strings.Trim(collapse(strings.ToLower(foldAccents(s)), sep), "-")
}

// collapse replaces every run of separator characters with a single hyphen.
func collapse(s string, sep func(rune) bool) string {
	var b strings.Builder
	prev := false
	for _, r := range s {
		if sep(r) {
			if !prev {
				b.WriteByte('-')
			}
			prev = true
			continue
		}
		b.WriteRune(r)
		prev = false
	}
	return b.String()
}

func isWordRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || unicode.IsMark(r)
}

func isASCIILetter(r rune) bool {
	return r|0x20 >= 'a' && r|0x20 <= 'z'
}

func isASCIIWordRune(r rune) bool {
	return isASCIILetter(r) || (r >= '0' && r <= '9')
}

// foldAccents spells an accented Latin letter without its accent, which is the
// part of the NFKD normalisation a Python generator performs that changes
// which anchor a heading gets. A letter that does not decompose, such as "ø"
// or "ß", is left alone: the caller decides whether to keep or drop it.
func foldAccents(s string) string {
	if !hasAccent(s) {
		return s
	}
	var b strings.Builder
	for _, r := range s {
		if fold, ok := accents[unicode.ToLower(r)]; ok {
			if unicode.IsUpper(r) {
				fold = strings.ToUpper(fold)
			}
			b.WriteString(fold)
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

func hasAccent(s string) bool {
	for _, r := range s {
		if r > unicode.MaxASCII {
			return true
		}
	}
	return false
}

var accents = func() map[rune]string {
	groups := []struct{ to, from string }{
		{"a", "àáâãäåāăą"},
		{"c", "çćĉċč"},
		{"d", "ď"},
		{"e", "èéêëēĕėęě"},
		{"g", "ĝğġģ"},
		{"h", "ĥ"},
		{"i", "ìíîïĩīĭįǐ"},
		{"ij", "ĳ"},
		{"j", "ĵ"},
		{"k", "ķ"},
		{"l", "ĺļľ"},
		{"n", "ñńņň"},
		{"o", "òóôõöōŏő"},
		{"r", "ŕŗř"},
		{"s", "śŝşš"},
		{"t", "ţť"},
		{"u", "ùúûüũūŭůűų"},
		{"w", "ŵ"},
		{"y", "ýÿŷ"},
		{"z", "źżž"},
	}
	m := map[rune]string{}
	for _, g := range groups {
		for _, r := range g.from {
			m[r] = g.to
		}
	}
	return m
}()

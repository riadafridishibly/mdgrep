package search

import (
	"fmt"
	"net/url"
	"path/filepath"
	"strings"

	"github.com/riadafridishibly/mdgrep/internal/mdoc"
)

// Anchor selects headings by the link that points at them: "#the-foo-bar"
// finds "## The Foo Bar". Generators slug a heading differently, so a query is
// slugged once per style and a heading matches if any style agrees.
type Anchor struct {
	styles  []mdoc.AnchorStyle
	queries []anchorQuery
}

type anchorQuery struct {
	path  string   // the file the link named, if it named one
	frag  string   // the fragment exactly as it was written
	slugs []string // the fragment slugged once per style, in styles order
}

// NewAnchor builds the selector from link fragments. A pattern may be written
// as "#install", as "install", as the heading itself ("## Install"), or with
// the file the link points at in front of it: "docs/setup.md#install", in
// which case only that file is searched.
func NewAnchor(patterns []string, styles []mdoc.AnchorStyle) (*Anchor, error) {
	a := &Anchor{styles: styles}
	for _, p := range patterns {
		path, frag := splitLink(p)
		if frag == "" {
			return nil, fmt.Errorf("no heading anchor in %q", p)
		}
		q := anchorQuery{path: path, frag: frag, slugs: make([]string, len(styles))}
		for i, style := range styles {
			q.slugs[i] = mdoc.Slug(style, frag)
		}
		a.queries = append(a.queries, q)
	}
	return a, nil
}

// wantsFile reports whether any query could match this file at all, so a link
// that named a file costs nothing in the files it did not name.
func (a *Anchor) wantsFile(path string) bool {
	for _, q := range a.queries {
		if q.path == "" || pathMatches(path, q.path) {
			return true
		}
	}
	return false
}

// matches reports whether a heading with these anchors, one per style, is the
// one a query points at.
func (a *Anchor) matches(path string, anchors []string) bool {
	for _, q := range a.queries {
		if q.path != "" && !pathMatches(path, q.path) {
			continue
		}
		for i, want := range q.slugs {
			if want != "" && want == anchors[i] {
				return true
			}
			// The pattern may already be the anchor a generator produced. The
			// suffix that tells two alike headings apart is appended after
			// slugging, so on a style that collapses punctuation it does not
			// always survive being slugged a second time.
			if q.frag != "" && q.frag == anchors[i] {
				return true
			}
		}
	}
	return false
}

// splitLink separates a link into the file it points at and the fragment after
// "#". Either half may be missing. A pattern that opens with "#" is the
// fragment itself, or the heading line copied verbatim, so its markers are
// dropped rather than read as a path. Percent escapes are decoded, which lets
// a URL be pasted whole.
func splitLink(s string) (path, frag string) {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "#") {
		return "", unescape(strings.TrimSpace(strings.TrimLeft(s, "#")))
	}
	if i := strings.LastIndex(s, "#"); i >= 0 {
		return unescape(s[:i]), unescape(s[i+1:])
	}
	return "", unescape(s)
}

func unescape(s string) string {
	if out, err := url.PathUnescape(s); err == nil {
		return out
	}
	return s
}

// pathMatches reports whether the file half of a link points at path. The two
// are compared component by component from the end, and only as far as the
// shorter one reaches, so "setup.md", "docs/setup.md" and a full
// "https://github.com/o/r/blob/main/docs/setup.md" all find docs/setup.md.
//
// Stopping at the shorter one is what lets a pasted URL work: nothing in it
// says where the repository root sits, so its "blob/main" can never be lined
// up with a local path. The price is that a link deeper than the file it finds
// still matches, which only ever widens the search — the anchor itself still
// has to agree.
func pathMatches(path, want string) bool {
	p, w := pathParts(path), pathParts(want)
	if len(p) == 0 || len(w) == 0 {
		return false
	}
	for i := 1; i <= len(p) && i <= len(w); i++ {
		if !strings.EqualFold(p[len(p)-i], w[len(w)-i]) {
			return false
		}
	}
	return true
}

func pathParts(s string) []string {
	var out []string
	for part := range strings.SplitSeq(filepath.ToSlash(s), "/") {
		if part != "" && part != "." && part != ".." {
			out = append(out, part)
		}
	}
	return out
}

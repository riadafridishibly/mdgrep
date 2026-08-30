package ignore

import (
	"path"
	"strings"
)

// kind is how a rule has to be compared, cheapest first. Most lines in a real
// ignore file are a plain name or a "*.ext" suffix, and neither needs a glob
// engine; sorting them out once at parse time is most of what makes matching
// fast.
type kind uint8

const (
	literal     kind = iota // a plain name, compared to the base name
	pathLiteral             // an anchored plain path, compared to the whole relative path
	suffix                  // "*.ext", a suffix of the base name
	nameGlob                // any other single segment, globbed against the base name
	pathGlob                // anchored and wild: globbed segment by segment
)

type rule struct {
	kind    kind
	negate  bool // written with a leading "!": takes an exclusion back
	dirOnly bool // written with a trailing "/": names directories only
	text    string
	segs    []string // pathGlob only
	// first is the leading segment of an anchored pattern when that segment
	// is a plain name. Comparing it against the front of a path rules the
	// pattern out without splitting anything, which is what a walk mostly
	// needs a wild pattern to do.
	first string
}

// parse reads one line of an ignore file, reporting whether it left a rule.
// Blank lines and comments do not.
func parse(line string) (rule, bool) {
	body := strings.TrimRight(line, "\r")
	if body == "" || strings.HasPrefix(body, "#") {
		return rule{}, false
	}
	if body = trimTrailingSpace(body); body == "" {
		return rule{}, false
	}

	var r rule
	switch {
	case strings.HasPrefix(body, "!"):
		r.negate, body = true, body[1:]
	case strings.HasPrefix(body, `\#`), strings.HasPrefix(body, `\!`):
		// The backslash is there to stop the "#" or "!" from being read as
		// punctuation, and has done its job.
		body = body[1:]
	}
	if strings.HasSuffix(body, "/") {
		r.dirOnly, body = true, strings.TrimSuffix(body, "/")
	}

	// A separator anywhere but the end ties the pattern to the directory its
	// file sits in. Without one it is a name, and names match at any depth.
	anchored := strings.HasPrefix(body, "/")
	if body = strings.TrimPrefix(body, "/"); body == "" {
		return rule{}, false
	}
	if anchored || strings.Contains(body, "/") {
		if !hasMeta(body) {
			r.kind, r.text = pathLiteral, body
			return r, true
		}
		r.kind, r.segs = pathGlob, strings.Split(body, "/")
		if head := r.segs[0]; head != "**" && !hasMeta(head) {
			r.first = head
		}
		return r, true
	}

	switch {
	case !hasMeta(body):
		r.kind, r.text = literal, body
	case strings.HasPrefix(body, "*") && !hasMeta(body[1:]):
		r.kind, r.text = suffix, body[1:]
	default:
		r.kind, r.text = nameGlob, body
	}
	return r, true
}

// matches reports whether the rule names this path. Whether it excludes it is
// a separate question, since a negated rule names a path in order to keep it.
func (r rule) matches(p *probe) bool {
	if r.dirOnly && !p.isDir {
		return false
	}
	switch r.kind {
	case literal:
		return p.base == r.text
	case pathLiteral:
		return p.rel == r.text
	case suffix:
		return strings.HasSuffix(p.base, r.text)
	case nameGlob:
		ok, err := path.Match(r.text, p.base)
		return ok && err == nil
	default:
		if r.first != "" && !p.startsWith(r.first) {
			return false
		}
		return matchSegments(r.segs, p.segments())
	}
}

// probe is one path being asked about. The segments are split once, and only
// if some rule is anchored enough to need them; most rules only ever look at
// the base name.
type probe struct {
	rel   string
	base  string
	isDir bool
	segs  []string
}

func newProbe(rel string, isDir bool) probe {
	base := rel
	if i := strings.LastIndexByte(rel, '/'); i >= 0 {
		base = rel[i+1:]
	}
	return probe{rel: rel, base: base, isDir: isDir}
}

// startsWith reports whether the path opens with exactly this segment.
func (p *probe) startsWith(name string) bool {
	return strings.HasPrefix(p.rel, name) &&
		(len(p.rel) == len(name) || p.rel[len(name)] == '/')
}

func (p *probe) segments() []string {
	if p.segs == nil {
		p.segs = strings.Split(p.rel, "/")
	}
	return p.segs
}

// matchSegments walks a split pattern against a split path. "**" is the only
// segment that spans more than one: in the middle it stands for zero or more
// directories, so "a/**/b" matches "a/b"; at the end it names what is inside a
// directory rather than the directory, so "a/**" does not match "a" alone.
func matchSegments(pattern, name []string) bool {
	for len(pattern) > 0 {
		if pattern[0] == "**" {
			if len(pattern) == 1 {
				return len(name) > 0
			}
			for i := 0; i <= len(name); i++ {
				if matchSegments(pattern[1:], name[i:]) {
					return true
				}
			}
			return false
		}
		if len(name) == 0 {
			return false
		}
		if ok, err := path.Match(pattern[0], name[0]); err != nil || !ok {
			return false
		}
		pattern, name = pattern[1:], name[1:]
	}
	return len(name) == 0
}

func hasMeta(s string) bool {
	return strings.ContainsAny(s, `*?[\`)
}

// trimTrailingSpace drops the trailing spaces git disregards, keeping any that
// a backslash quotes.
func trimTrailingSpace(s string) string {
	end := len(s)
	for end > 0 && s[end-1] == ' ' {
		slashes := 0
		for i := end - 2; i >= 0 && s[i] == '\\'; i-- {
			slashes++
		}
		if slashes%2 == 1 {
			break
		}
		end--
	}
	return s[:end]
}

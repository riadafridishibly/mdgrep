// Package edit rewrites the markdown a search selected: a checkbox flipped, a
// heading renamed, a section replaced. Every change is planned against the
// file as it was read and applied in one write, so a run lands whole or not at
// all.
package edit

import (
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/riadafridishibly/mdgrep/internal/mdoc"
	"github.com/riadafridishibly/mdgrep/internal/search"
)

// Op is the rewrite to perform on each selected node.
type Op string

const (
	OpNone    Op = ""
	OpCheck   Op = "check"
	OpUncheck Op = "uncheck"
	OpToggle  Op = "toggle"
	OpReplace Op = "replace"
	OpSetText Op = "set-text"
	OpDelete  Op = "delete"
	OpAppend  Op = "append"
	OpPrepend Op = "prepend"
)

// Options is one edit and the text it carries.
type Options struct {
	Op   Op
	Text string
}

// Node reports whether the op rewrites the matched node itself rather than the
// region a search printed, which is what makes it incompatible with the flags
// that widen that region.
func (o Op) Node() bool {
	switch o {
	case OpCheck, OpUncheck, OpToggle, OpSetText:
		return true
	}
	return false
}

// Change is one contiguous line range rewritten. End < Start means the change
// inserts at Start without replacing anything.
type Change struct {
	Path       string
	Op         Op
	Start, End int // inclusive, zero-based lines of the original file
	Old, New   []string
	Breadcrumb []string
	NoOp       bool // the file already reads the way the edit asks
}

// Plan turns search results into changes without touching the file, so a run
// can be rejected whole before anything is written.
func Plan(src *mdoc.Source, results []search.Result, opt Options) ([]Change, error) {
	out := make([]Change, 0, len(results))
	for _, r := range results {
		c, err := plan(src, r, opt)
		if err != nil {
			return nil, err
		}
		if c.Old == nil {
			c.Old = src.Lines(c.Start, c.End)
		}
		out = append(out, c)
	}
	return out, nil
}

func plan(src *mdoc.Source, r search.Result, opt Options) (Change, error) {
	c := Change{Path: src.Path, Op: opt.Op, Start: r.Start, End: r.End, Breadcrumb: r.Breadcrumb}
	switch opt.Op {
	case OpCheck, OpUncheck, OpToggle:
		return checkbox(src, r, opt.Op, c)
	case OpSetText:
		return setText(src, r, opt.Text, c)
	case OpReplace:
		c.New = lines(opt.Text)
		if c.End < c.Start {
			// The region is an insertion point — an empty section body — so
			// the text has to be parted from what surrounds it.
			c.New = pad(src, c.Start, c.New, r.Kind)
			c.Old = []string{}
		}
	case OpDelete:
		c.End = swallowBlank(src, c.Start, c.End)
	case OpAppend, OpPrepend:
		return insert(src, r, opt, c)
	default:
		return c, fmt.Errorf("unknown edit %q", opt.Op)
	}
	return c, nil
}

// taskMark finds the GFM checkbox at the head of a list item, whatever marker
// and indentation the item was written with.
var taskMark = regexp.MustCompile(`^(\s*(?:[-*+]|\d+[.)])[ \t]+)\[([ xX])\]([ \t]|$)`)

func checkbox(src *mdoc.Source, r search.Result, op Op, c Change) (Change, error) {
	line := src.Line(r.HitStart)
	m := taskMark.FindStringSubmatchIndex(line)
	if m == nil {
		return c, fmt.Errorf("%s:%d: not a task list item", src.Path, r.HitStart+1)
	}
	c.Start, c.End = r.HitStart, r.HitStart
	c.Old = []string{line}

	checked := line[m[4]:m[5]] != " "
	want := op == OpCheck
	if op == OpToggle {
		want = !checked
	}
	if want == checked {
		c.New, c.NoOp = c.Old, true
		return c, nil
	}
	mark := " "
	if want {
		mark = "x"
	}
	c.New = []string{line[:m[4]] + mark + line[m[5]:]}
	return c, nil
}

var (
	// The trailing group holds what a heading may carry after its text: a
	// kramdown attribute block, closing hashes, or both.
	atxHeading = regexp.MustCompile(`^(\s*#{1,6}[ \t]+)(.*?)((?:[ \t]+\{[^}]*\})?(?:[ \t]+#+)?[ \t]*)$`)
	itemHead   = regexp.MustCompile(`^\s*(?:[-*+]|\d+[.)])[ \t]+(?:\[[ xX]\][ \t]+)?`)
	quoteHead  = regexp.MustCompile(`^\s*(?:>[ \t]?)+`)
)

// setText rewrites what a node says while leaving the markdown that makes it
// that kind of node in place: a heading keeps its level, a task item keeps its
// marker and its checkbox, a fenced block keeps its fences.
func setText(src *mdoc.Source, r search.Result, text string, c Change) (Change, error) {
	body := lines(text)
	c.Start, c.End = r.HitStart, r.HitEnd

	single := func(kind string) error {
		if len(body) != 1 {
			return fmt.Errorf("a %s holds a single line of text", kind)
		}
		return nil
	}

	switch r.Kind {
	case mdoc.KindHeading:
		if err := single("heading"); err != nil {
			return c, err
		}
		first := src.Line(r.HitStart)
		if m := atxHeading.FindStringSubmatch(first); m != nil {
			c.End = r.HitStart
			c.New = []string{m[1] + body[0] + m[3]}
			return c, nil
		}
		// Setext: the underline stays, resized to the text above it.
		c.New = []string{body[0]}
		if rule := strings.TrimSpace(src.Line(r.HitEnd)); r.HitEnd > r.HitStart && rule != "" {
			c.New = append(c.New, strings.Repeat(rule[:1], max(1, utf8.RuneCountInString(body[0]))))
		}
	case mdoc.KindItem:
		if err := single("list item"); err != nil {
			return c, err
		}
		first := src.Line(r.HitStart)
		head := itemHead.FindString(first)
		if head == "" {
			return c, fmt.Errorf("%s:%d: no list marker to keep", src.Path, r.HitStart+1)
		}
		// Only the item's own first line is text; anything nested under it
		// stays where it is.
		c.End = r.HitStart
		c.New = []string{head + body[0]}
	case mdoc.KindQuote:
		c.New = prefixAll(quoteHead.FindString(src.Line(r.HitStart)), body)
	case mdoc.KindCode:
		if r.HitEnd > r.HitStart && isFence(src.Line(r.HitStart)) && isFence(src.Line(r.HitEnd)) {
			c.Start, c.End = r.HitStart+1, r.HitEnd-1
			c.New = body
			return c, nil
		}
		c.New = prefixAll(indentOf(src.Line(r.HitStart)), body)
	case mdoc.KindParagraph, mdoc.KindTextBlock:
		c.New = body
	default:
		return c, fmt.Errorf("--set-text does not apply to a %s; use --replace", r.Kind)
	}
	return c, nil
}

// insert places text beside a node. It is indented to match, so a bullet
// appended to a nested item lands as its sibling, and separated by a blank
// line everywhere a blank line is what keeps two blocks apart.
func insert(src *mdoc.Source, r search.Result, opt Options, c Change) (Change, error) {
	if opt.Op == OpAppend {
		c.Start, c.End = r.End+1, r.End
	} else {
		c.Start, c.End = r.Start, r.Start-1
	}
	body := prefixAll(indentOf(src.Line(r.Start)), lines(opt.Text))
	c.New, c.Old = pad(src, c.Start, body, r.Kind), []string{}
	return c, nil
}

// pad parts inserted text from the lines it lands between, on whichever side
// is not already blank. Inside a list or a table nothing is added: a blank
// line there would loosen the list or break the table.
func pad(src *mdoc.Source, at int, body []string, kind mdoc.Kind) []string {
	if len(body) == 0 || !needsBlankLine(kind) {
		return body
	}
	if at > 0 && !blankLine(src.Line(at-1)) {
		body = append([]string{""}, body...)
	}
	if at < src.NumLines() && !blankLine(src.Line(at)) {
		body = append(body, "")
	}
	return body
}

// blankLine reports whether a line is the blank one markdown parts blocks with:
// empty, or nothing but spaces and tabs. Vertical tabs and form feeds are
// whitespace to strings.TrimSpace but content to the parser, and a line of them
// belongs to the block around it.
func blankLine(line string) bool {
	return strings.Trim(line, " \t") == ""
}

func needsBlankLine(k mdoc.Kind) bool {
	switch k {
	case mdoc.KindItem, mdoc.KindRow, mdoc.KindCell:
		return false
	}
	return true
}

// swallowBlank extends a deletion over the blank line it would otherwise leave
// stacked on the one already above the block.
func swallowBlank(src *mdoc.Source, start, end int) int {
	above := start == 0 || blankLine(src.Line(start-1))
	if above && end+1 < src.NumLines() && blankLine(src.Line(end+1)) {
		return end + 1
	}
	return end
}

// Apply rewrites the source with the planned changes, which arrive in
// ascending order and never overlap.
func Apply(src *mdoc.Source, changes []Change) string {
	text := src.Text()
	eol := "\n"
	if strings.Contains(text, "\r\n") {
		eol = "\r\n"
	}
	trailing := strings.HasSuffix(text, "\n")

	var sb strings.Builder
	sb.Grow(len(text))
	prev := 0
	for _, c := range changes {
		lo, hi := src.ByteRange(c.Start, c.End)
		if lo < prev {
			continue
		}
		sb.WriteString(text[prev:lo])
		// Every line but the last takes the file's own ending. The last takes
		// back exactly what Source.Line trimmed off it, which is how a file
		// that ended without a newline keeps ending without one, and how a
		// last line ending in carriage returns keeps them: Line strips those
		// as an ending, and nothing else here would ever write one back.
		last := trimmedEnding(src, text, lo, hi, c.End)
		if lo == hi {
			// An insertion replaces no line, so it has no ending of its own to
			// take back. It parts from whatever follows instead, and adds
			// nothing at the end of a file that ends without one.
			last = ""
			if hi < len(text) || trailing {
				last = eol
			}
		}
		for i, line := range c.New {
			sb.WriteString(line)
			if i < len(c.New)-1 {
				sb.WriteString(eol)
			} else {
				sb.WriteString(last)
			}
		}
		prev = hi
	}
	sb.WriteString(text[prev:])
	return sb.String()
}

// trimmedEnding is what Source.Line trimmed from the last line of a replaced
// range: the run of carriage returns and newlines it ends on. Reading it from
// that line alone rather than from the whole range is what keeps a range of
// blank lines from collecting the endings of the lines above it, each of which
// its own entry in New already accounts for.
func trimmedEnding(src *mdoc.Source, text string, lo, hi, end int) string {
	last, _ := src.ByteRange(end, end)
	if last < lo || last > hi {
		last = lo
	}
	seg := text[last:hi]
	return seg[len(strings.TrimRight(seg, "\r\n")):]
}

func lines(text string) []string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	if text == "" {
		return nil
	}
	return strings.Split(strings.TrimSuffix(text, "\n"), "\n")
}

func prefixAll(prefix string, ls []string) []string {
	if prefix == "" {
		return ls
	}
	out := make([]string, len(ls))
	for i, l := range ls {
		if strings.TrimSpace(l) == "" {
			out[i] = l
			continue
		}
		out[i] = prefix + l
	}
	return out
}

func indentOf(line string) string {
	return line[:len(line)-len(strings.TrimLeft(line, " \t"))]
}

func isFence(line string) bool {
	t := strings.TrimSpace(line)
	return strings.HasPrefix(t, "```") || strings.HasPrefix(t, "~~~")
}

// Changed reports whether a plan would alter the file at all, so a run that
// amounts to nothing can skip the write rather than rewrite a file with its
// own contents.
func Changed(changes []Change) bool {
	for _, c := range changes {
		if !c.NoOp {
			return true
		}
	}
	return false
}

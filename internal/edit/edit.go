// Package edit rewrites the markdown a search selected: a checkbox flipped, a
// heading renamed, a section replaced. Every change is planned against the
// file as it was read and applied in one write, so a run lands whole or not at
// all.
package edit

import (
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/riadafridishibly/mdgrep/internal/match"
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
	// OpReplace rewrites the text a matcher pointed at and leaves the rest of
	// the node where it is, the way a substitution does in sed. OpReplaceNode
	// is the other half of what used to be one "replace": it rewrites the
	// whole selected node, text and syntax together.
	OpReplace     Op = "replace"
	OpReplaceNode Op = "replace-node"
	OpSetText     Op = "set-text"
	OpDelete      Op = "delete"
	OpAppend      Op = "append"
	OpPrepend     Op = "prepend"
)

// Options is one edit and the text it carries.
type Options struct {
	Op   Op
	Text string
	// Matcher is what OpReplace substitutes for, and nothing else reads. It is
	// the last stage's matcher, which is the one that chose the nodes being
	// edited.
	Matcher match.Matcher
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
	// cells are the cell rewrites this change carries, as byte offsets into
	// the original line. Several cells of one row are one change to that
	// line, so they are collected rather than each rewriting the line from
	// what it said before the run -- which would keep only the last of them.
	cells []cellEdit
}

// cellEdit is one cell's new text, placed by its byte range in the line.
type cellEdit struct {
	lo, hi int
	text   string
}

// Plan turns search results into changes without touching the file, so a run
// can be rejected whole before anything is written.
func Plan(doc *mdoc.Doc, results []search.Result, opt Options) ([]Change, error) {
	src := doc.Src
	out := make([]Change, 0, len(results))
	for _, r := range results {
		cs, err := plan(doc, r, opt)
		if err != nil {
			return nil, err
		}
		for _, c := range cs {
			if c.Old == nil {
				c.Old = src.Lines(c.Start, c.End)
			}
			if at, ok := sameLineCell(out, c); ok {
				out[at].cells = append(out[at].cells, c.cells...)
				out[at].New = []string{applyCells(src.Line(c.Start), out[at].cells)}
				continue
			}
			out = append(out, c)
		}
	}
	return out, nil
}

// sameLineCell finds a change already rewriting cells of the same line, which
// is the one a further cell of that row belongs to.
func sameLineCell(out []Change, c Change) (int, bool) {
	if len(c.cells) == 0 {
		return 0, false
	}
	for i := range out {
		if len(out[i].cells) > 0 && out[i].Path == c.Path && out[i].Start == c.Start {
			return i, true
		}
	}
	return 0, false
}

// applyCells writes every cell edit into the line it came from. They are
// disjoint by construction -- a cell is a cell's worth of bytes -- so writing
// them from the right leaves the offsets of the ones still to come alone.
func applyCells(line string, cells []cellEdit) string {
	sorted := append([]cellEdit(nil), cells...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].lo > sorted[j].lo })
	for _, e := range sorted {
		if e.lo < 0 || e.hi > len(line) || e.lo > e.hi {
			continue
		}
		line = line[:e.lo] + e.text + line[e.hi:]
	}
	return line
}

// one adapts the edits that rewrite a single range. Only a substitution
// produces more than one change from one result, because only it leaves the
// lines it did not match alone.
func one(c Change, err error) ([]Change, error) {
	if err != nil {
		return nil, err
	}
	return []Change{c}, nil
}

func plan(doc *mdoc.Doc, r search.Result, opt Options) ([]Change, error) {
	src := doc.Src
	c := Change{Path: src.Path, Op: opt.Op, Start: r.Start, End: r.End, Breadcrumb: r.Breadcrumb}
	switch opt.Op {
	case OpCheck, OpUncheck, OpToggle:
		return one(checkbox(src, r, opt.Op, c))
	case OpSetText:
		return one(setText(doc, r, opt.Text, c))
	case OpReplace:
		return subst(doc, opt, c)
	case OpReplaceNode:
		c.New = lines(opt.Text)
		if err := fitsInside(doc, r.Start, r.End, c.New); err != nil {
			return nil, err
		}
		if c.End < c.Start {
			// The region is an insertion point — an empty section body — so
			// the text has to be parted from what surrounds it.
			c.New = pad(src, c.Start, c.New, r.Kind)
			c.Old = []string{}
		}
	case OpDelete:
		if err := keepsTable(doc, c.Start, c.End); err != nil {
			return nil, err
		}
		c.End = swallowBlank(src, c.Start, c.End)
	case OpAppend, OpPrepend:
		if err := fitsInside(doc, r.Start, r.End, lines(opt.Text)); err != nil {
			return nil, err
		}
		return one(insert(src, r, opt, c))
	default:
		return nil, fmt.Errorf("unknown edit %q", opt.Op)
	}
	return one(c, nil)
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
func setText(doc *mdoc.Doc, r search.Result, text string, c Change) (Change, error) {
	src := doc.Src
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
	case mdoc.KindCell:
		// A cell owns bytes rather than lines, so what is rewritten is the
		// span inside the row it holds; the pipes and the cells beside it
		// stay where they are. Fit escapes a pipe in the text and refuses a
		// line break, either of which would rewrite the row's columns.
		fitted, err := doc.BlockAt(r.HitStart, r.Lo).Constraint().Fit(text)
		if err != nil {
			return c, fmt.Errorf("%s:%d: %v", src.Path, r.HitStart+1, err)
		}
		line := src.Line(r.HitStart)
		base := src.LineStart(r.HitStart)
		lo, hi := r.Lo-base, r.Hi-base+1
		if lo < 0 || hi > len(line) || lo > hi {
			return c, fmt.Errorf("%s:%d: the cell is not on the line it names", src.Path, r.HitStart+1)
		}
		c.End = r.HitStart
		c.cells = []cellEdit{{lo: lo, hi: hi, text: fitted}}
		c.New = []string{applyCells(line, c.cells)}
	case mdoc.KindParagraph, mdoc.KindTextBlock:
		c.New = body
	default:
		// A cell and a row are made of the text inside them, and --replace is
		// the op that rewrites text knowing it is text. Sending them to
		// --replace-node instead offers a region op a line of table syntax to
		// write blind.
		if r.Kind == mdoc.KindRow {
			return c, fmt.Errorf(
				"--set-text does not apply to a row; use --replace-node to write " +
					"a row, or -k cell to name one cell of it")
		}
		return c, fmt.Errorf("--set-text does not apply to a %s; use --replace-node", r.Kind)
	}
	return c, nil
}

// tableRow is what a line has to look like to stand as a row: the pipes at
// both ends. GFM would take a bare "a|b" as a row too, which is the reason to
// insist -- text that was never meant as markup reads as markup there, and
// silently.
var tableRow = regexp.MustCompile(`^\s*\|.*\|\s*$`)

// fitsInside refuses text that would be read as markup where it lands rather
// than as the content it was meant to be. A pipe there opens a column and a line break ends the row, so a region
// op writing "a|b" over a cell gives back two columns where one was asked for,
// and two lines give back two rows. --replace escapes what it writes because
// its matcher told it the text is text; a region op is handed lines and has to
// be told. Replacing a table whole is left alone: what follows it is a
// document, not a row.
func fitsInside(doc *mdoc.Doc, start, end int, body []string) error {
	if table := within(doc, start, end, mdoc.KindTable); table != nil {
		for _, line := range body {
			if tableRow.MatchString(line) {
				continue
			}
			return fmt.Errorf(
				"%s:%d: only a row belongs inside a table, and %q is not one; "+
					"write the pipes, or use --replace to rewrite the text in a cell",
				doc.Src.Path, start+1, line)
		}
	}
	if code := within(doc, start, end, mdoc.KindCode); code != nil {
		for _, line := range body {
			if !isFence(line) {
				continue
			}
			// A fence written inside a fenced block closes it, and everything
			// under it -- the rest of the document -- becomes code.
			return fmt.Errorf(
				"%s:%d: %q would close the fenced block it is written into; "+
					"replace the block itself, or use --replace to rewrite the text in it",
				doc.Src.Path, start+1, line)
		}
	}
	return nil
}

// keepsTable refuses a deletion that would take a table's header or the
// delimiter under it. GFM reads the columns off those two lines, and without
// them there is no table: the rows left behind are read as a paragraph full of
// pipes. Deleting the table whole is another matter, and allowed.
func keepsTable(doc *mdoc.Doc, start, end int) error {
	table := within(doc, start, end, mdoc.KindTable)
	if table == nil || start > table.Start+1 {
		return nil
	}
	return fmt.Errorf(
		"%s:%d: a table is built on its header and the line under it, and "+
			"deleting either leaves the rows behind as text; delete the table "+
			"itself, or empty the cells with --replace",
		doc.Src.Path, start+1)
}

// within is the block of the given kind a region sits strictly inside, or nil
// where the region sits in none or covers the whole of one. Replacing a node
// whole is always allowed: what goes in its place answers to whatever holds
// the node, not to the node being replaced.
func within(doc *mdoc.Doc, start, end int, kind mdoc.Kind) *mdoc.Block {
	for b := doc.Enclosing(start, end); b != nil; b = b.Parent {
		if b.Kind != kind {
			continue
		}
		if start <= b.Start && end >= b.End {
			return nil
		}
		return b
	}
	return nil
}

// subst rewrites the text a matcher pointed at and leaves everything else on
// the line where it was. Unlike every other edit it does not care what kind of
// node it is standing in, only where in the document each match falls -- which
// is what lets it run over a whole section as readily as over one bullet.
//
// The region it walks is the region the search reported, so what --expand and
// --section widen a page to is what a substitution reaches. Its own node is
// the default, and that is the only difference from a page.
func subst(doc *mdoc.Doc, opt Options, c Change) ([]Change, error) {
	rp, ok := opt.Matcher.(match.Replacer)
	if !ok {
		return nil, errNoSubstText
	}
	old := doc.Src.Lines(c.Start, c.End)
	out := make([]string, len(old))
	for i, line := range old {
		rewritten, err := substLine(doc, c.Start+i, line, rp, opt.Text)
		if err != nil {
			return nil, err
		}
		out[i] = rewritten
	}
	// One change per run of lines that moved, so a substitution across a whole
	// section reports the lines it rewrote rather than the section it walked.
	// The lines between two runs matched nothing and are not part of the edit.
	var changes []Change
	for i := 0; i < len(old); {
		if old[i] == out[i] {
			i++
			continue
		}
		j := i
		for j+1 < len(old) && old[j+1] != out[j+1] {
			j++
		}
		run := c
		run.Start, run.End = c.Start+i, c.Start+j
		run.Old, run.New = old[i:j+1], out[i:j+1]
		changes = append(changes, run)
		i = j + 1
	}
	if len(changes) == 0 {
		// Nothing moved. The result is still reported, as a no-op, so a run
		// that selected a node and left it alone says so rather than going
		// quiet.
		c.Old, c.New, c.NoOp = old, out, true
		return []Change{c}, nil
	}
	return changes, nil
}

// errNoSubstText is the refusal for a search that selected its nodes without
// pointing at any text inside them. It is exported through Plan rather than
// checked at the flag, because a pipeline's last stage is what decides it.
var errNoSubstText = errors.New(
	"--replace rewrites the text a pattern matched, and this search matched " +
		"nodes without pointing at text in them; use --replace-node to rewrite the node")

// substLine rewrites one line, asking the document at every match what may be
// written there. That question is the reason a substitution needs the parse and
// not just the matcher: a pipe is a character in a paragraph and a column
// boundary in a table row, and only the tree knows which line is which.
func substLine(doc *mdoc.Doc, n int, line string, rp match.Replacer, repl string) (string, error) {
	reps := rp.Replacements(line, repl)
	if len(reps) == 0 {
		return line, nil
	}
	base := doc.Src.LineStart(n)
	var sb strings.Builder
	sb.Grow(len(line))
	prev := 0
	for _, m := range reps {
		if m.Start < prev || m.End > len(line) {
			continue
		}
		text, err := doc.BlockAt(n, base+m.Start).Constraint().Fit(m.Text)
		if err != nil {
			return "", fmt.Errorf("%s:%d: %v", doc.Src.Path, n+1, err)
		}
		sb.WriteString(line[prev:m.Start])
		sb.WriteString(text)
		prev = m.End
	}
	sb.WriteString(line[prev:])
	return sb.String(), nil
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
			switch {
			case i == len(c.New)-1:
				sb.WriteString(last)
			case c.Start+i <= c.End:
				// A line that stands where an original one stood takes back
				// that line's own ending rather than the file's commonest, so
				// a document whose endings are not all alike keeps them.
				sb.WriteString(ending(src, text, c.Start+i, hi))
			default:
				// A line the edit added stood where nothing did, so it takes
				// the ending the rest of the file is written with.
				sb.WriteString(eol)
			}
		}
		prev = hi
	}
	sb.WriteString(text[prev:])
	return sb.String()
}

// ending is what Source.Line trimmed from one line: the bytes between the end
// of its text and the start of the next, clamped to the range being rewritten.
// Reading it per line is what keeps a file whose endings are not uniform -- a
// stray carriage return inside an otherwise LF document -- from having them
// levelled to whichever one the file has most of.
func ending(src *mdoc.Source, text string, n, hi int) string {
	from := src.LineStart(n) + len(src.Line(n))
	to := src.LineStart(n + 1)
	if to > hi {
		to = hi
	}
	if from > to || to > len(text) {
		return ""
	}
	return text[from:to]
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

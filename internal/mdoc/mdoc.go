// Package mdoc parses markdown into a line-addressable block tree.
//
// goldmark reports byte offsets that sometimes exclude the syntax that
// introduced a block (the "#" of a heading, the fences around a code block).
// mdoc converts every block to an inclusive line range and widens it back over
// that syntax, so a block can be reproduced verbatim from the original source.
package mdoc

import (
	"fmt"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	east "github.com/yuin/goldmark/extension/ast"
	"github.com/yuin/goldmark/text"
)

type Kind string

const (
	KindDocument    Kind = "document"
	KindHeading     Kind = "heading"
	KindParagraph   Kind = "paragraph"
	KindTextBlock   Kind = "textblock"
	KindList        Kind = "list"
	KindItem        Kind = "item"
	KindCode        Kind = "code"
	KindQuote       Kind = "quote"
	KindHTML        Kind = "html"
	KindTable       Kind = "table"
	KindRow         Kind = "row"
	KindCell        Kind = "cell"
	KindBreak       Kind = "break"
	KindFrontmatter Kind = "frontmatter"
	// KindSection is a heading and everything under it. No parse produces
	// one -- headings are flat siblings of the document -- but it is the rung
	// the expand ladder ends on and the region --section selects.
	KindSection Kind = "section"
	// KindRegion is a run of lines the document does not itself draw. Only
	// --at can select one, and only where the lines it names are no node.
	KindRegion Kind = "region"
)

// Block is one addressable markdown node.
type Block struct {
	Kind     Kind
	Node     ast.Node // nil for the synthetic root and for frontmatter
	Start    int      // inclusive, zero-based line
	End      int      // inclusive, zero-based line
	Level    int      // heading level, else 0
	Depth    int      // nesting depth below the document root
	Text     string   // headings only: plain text, used for breadcrumbs
	Lo, Hi   int      // byte range of the node's own text, -1 when not Located
	Parent   *Block
	Children []*Block
	Located  bool // false when goldmark exposed no offsets for this node
	Task     bool // item opens with a GFM task checkbox
	Checked  bool // that checkbox is ticked
}

// Contains reports whether b is an ancestor of other.
func (b *Block) Contains(other *Block) bool {
	for p := other.Parent; p != nil; p = p.Parent {
		if p == b {
			return true
		}
	}
	return false
}

type Doc struct {
	Src      *Source
	Root     *Block
	Blocks   []*Block // document order, root excluded
	Headings []*Block // document order

	data []byte // the original file, for text rendered on demand
}

var parser = goldmark.New(goldmark.WithExtensions(extension.GFM)).Parser()

func Parse(path string, data []byte) *Doc {
	src := NewSource(path, data)
	d := &Doc{Src: src, data: data}
	d.Root = &Block{
		Kind:    KindDocument,
		Start:   0,
		End:     src.NumLines() - 1,
		Lo:      0,
		Hi:      max(0, len(data)-1),
		Located: true,
	}

	scan := data
	if end, ok := frontmatterEnd(src); ok {
		fm := &Block{
			Kind:    KindFrontmatter,
			Start:   0,
			End:     end,
			Lo:      0,
			Hi:      src.LineEnd(end),
			Depth:   1,
			Parent:  d.Root,
			Located: true,
		}
		d.Root.Children = append(d.Root.Children, fm)
		d.Blocks = append(d.Blocks, fm)
		// Blank the region so goldmark does not read "---" as a thematic break
		// or turn the first key into a setext heading. Length is preserved so
		// every later byte offset still lines up with the original source.
		scan = maskLines(data, src, 0, end)
	}

	root := parser.Parse(text.NewReader(scan))
	d.build(root, d.Root, data)
	for _, b := range d.Blocks {
		// An empty heading ("#" on its own) reaches goldmark with no content
		// and so with no offsets. It sits on no line a breadcrumb or a section
		// could be measured from, so it is not one of the document's headings.
		if b.Kind == KindHeading && b.Located {
			d.Headings = append(d.Headings, b)
		}
	}
	return d
}

func (d *Doc) build(n ast.Node, parent *Block, data []byte) {
	for c := n.FirstChild(); c != nil; c = c.NextSibling() {
		if c.Type() != ast.TypeBlock {
			continue
		}
		b := &Block{
			Kind:   kindOf(c),
			Node:   c,
			Depth:  parent.Depth + 1,
			Parent: parent,
		}
		if h, ok := c.(*ast.Heading); ok {
			b.Level = h.Level
			// Searching reads the raw source, so rendered text is only needed
			// where a breadcrumb has to be printed without its "#" markers.
			b.Text = nodeText(c, data)
		}
		if b.Kind == KindItem {
			b.Task, b.Checked = taskState(c)
		}
		if lo, hi, ok := nodeRange(c); ok {
			b.Lo, b.Hi = lo, hi
			b.Start, b.End = d.Src.LineIndex(lo), d.Src.LineIndex(hi)
			b.Located = true
			widen(b, d.Src)
		} else {
			b.Start, b.End = -1, -1
			b.Lo, b.Hi = -1, -1
		}
		parent.Children = append(parent.Children, b)
		d.Blocks = append(d.Blocks, b)
		d.build(c, b, data)
	}
}

// taskState reads the GFM task checkbox goldmark puts at the head of a list
// item's first inline run: "- [ ] a" and "- [x] a" are items, "- a" is not.
func taskState(item ast.Node) (task, checked bool) {
	first := item.FirstChild()
	if first == nil {
		return false, false
	}
	if cb, ok := first.FirstChild().(*east.TaskCheckBox); ok {
		return true, cb.IsChecked
	}
	return false, false
}

func kindOf(n ast.Node) Kind {
	switch n.Kind() {
	case ast.KindHeading:
		return KindHeading
	case ast.KindParagraph:
		return KindParagraph
	case ast.KindTextBlock:
		return KindTextBlock
	case ast.KindList:
		return KindList
	case ast.KindListItem:
		return KindItem
	case ast.KindCodeBlock, ast.KindFencedCodeBlock:
		return KindCode
	case ast.KindBlockquote:
		return KindQuote
	case ast.KindHTMLBlock:
		return KindHTML
	case ast.KindThematicBreak:
		return KindBreak
	}
	switch strings.ToLower(n.Kind().String()) {
	case "table":
		return KindTable
	case "tableheader", "tablerow":
		return KindRow
	case "tablecell":
		return KindCell
	}
	return Kind(strings.ToLower(n.Kind().String()))
}

// nodeRange unions every byte offset reachable from n, including inline
// descendants, which is how container blocks such as list items get a range.
func nodeRange(n ast.Node) (lo, hi int, ok bool) {
	add := func(a, b int) {
		if a < 0 || b < a {
			return
		}
		if !ok || a < lo {
			lo = a
		}
		if !ok || b > hi {
			hi = b
		}
		ok = true
	}
	if n.Type() == ast.TypeBlock {
		if ls := n.Lines(); ls != nil && ls.Len() > 0 {
			add(ls.At(0).Start, ls.At(ls.Len()-1).Stop-1)
		}
	}
	switch t := n.(type) {
	case *ast.Text:
		add(t.Segment.Start, t.Segment.Stop-1)
	case *ast.RawHTML:
		if t.Segments != nil && t.Segments.Len() > 0 {
			add(t.Segments.At(0).Start, t.Segments.At(t.Segments.Len()-1).Stop-1)
		}
	case *ast.HTMLBlock:
		add(t.ClosureLine.Start, t.ClosureLine.Stop-1)
	case *ast.FencedCodeBlock:
		if t.Info != nil {
			add(t.Info.Segment.Start, t.Info.Segment.Stop-1)
		}
	}
	for c := n.FirstChild(); c != nil; c = c.NextSibling() {
		if clo, chi, cok := nodeRange(c); cok {
			add(clo, chi)
		}
	}
	return lo, hi, ok
}

// widen pulls a range back over syntax goldmark left outside the node's
// content: code fences and setext underlines.
func widen(b *Block, src *Source) {
	switch b.Kind {
	case KindCode:
		if _, fenced := b.Node.(*ast.FencedCodeBlock); !fenced {
			return
		}
		if b.Start > 0 && isFence(src.Line(b.Start-1)) {
			b.Start--
		}
		if b.End+1 < src.NumLines() && isFence(src.Line(b.End+1)) {
			b.End++
		}
	case KindHeading:
		if strings.HasPrefix(strings.TrimSpace(src.Line(b.Start)), "#") {
			return
		}
		if b.End+1 < src.NumLines() && isSetextRule(src.Line(b.End+1)) {
			b.End++
		}
	}
}

func isFence(line string) bool {
	t := strings.TrimSpace(line)
	return strings.HasPrefix(t, "```") || strings.HasPrefix(t, "~~~")
}

func isSetextRule(line string) bool {
	t := strings.TrimSpace(line)
	if t == "" {
		return false
	}
	c := t[0]
	if c != '=' && c != '-' {
		return false
	}
	return strings.Trim(t, string(c)) == ""
}

// nodeText renders a node's plain text for matching. Link and image
// destinations are included so URLs are searchable.
func nodeText(n ast.Node, src []byte) string { return renderText(n, src, true) }

// anchorText renders a heading the way an anchor generator sees it: what a
// reader would read, with the destination of a link left out because only the
// visible text is slugged.
func anchorText(n ast.Node, src []byte) string { return renderText(n, src, false) }

func renderText(n ast.Node, src []byte, dests bool) string {
	var sb strings.Builder
	var walk func(ast.Node)
	walk = func(n ast.Node) {
		switch t := n.(type) {
		case *ast.Text:
			sb.Write(t.Segment.Value(src))
			if t.SoftLineBreak() || t.HardLineBreak() {
				sb.WriteByte('\n')
			}
			return
		case *ast.String:
			sb.Write(t.Value)
			return
		case *ast.AutoLink:
			sb.Write(t.URL(src))
			return
		case *ast.CodeSpan:
			sb.WriteString(codeSpan(t, src))
			return
		}
		if n.Type() == ast.TypeBlock && n.IsRaw() {
			if ls := n.Lines(); ls != nil {
				for i := range ls.Len() {
					seg := ls.At(i)
					sb.Write(seg.Value(src))
				}
			}
			return
		}
		for c := n.FirstChild(); c != nil; c = c.NextSibling() {
			walk(c)
			if c.Type() == ast.TypeBlock && c.NextSibling() != nil {
				sb.WriteByte('\n')
			}
		}
		if !dests {
			return
		}
		switch t := n.(type) {
		case *ast.Link:
			sb.WriteByte(' ')
			sb.Write(t.Destination)
		case *ast.Image:
			sb.WriteByte(' ')
			sb.Write(t.Destination)
		}
	}
	walk(n)
	return sb.String()
}

// codeSpan reproduces an inline code span the way it is written, backticks
// included, so a pattern typed with them still matches. The AST holds only the
// content, and CommonMark strips one space of padding from it, so the
// delimiters are recovered by scanning the source on either side.
func codeSpan(n *ast.CodeSpan, src []byte) string {
	lo, hi, ok := nodeRange(n)
	if !ok || lo > hi {
		return ""
	}
	return string(src[openTicks(src, lo):closeTicks(src, hi+1)])
}

func openTicks(src []byte, content int) int {
	i := content
	for i > 0 && src[i-1] == ' ' {
		i--
	}
	if i == 0 || src[i-1] != '`' {
		return content
	}
	for i > 0 && src[i-1] == '`' {
		i--
	}
	return i
}

func closeTicks(src []byte, content int) int {
	i := content
	for i < len(src) && src[i] == ' ' {
		i++
	}
	if i == len(src) || src[i] != '`' {
		return content
	}
	for i < len(src) && src[i] == '`' {
		i++
	}
	return i
}

// frontmatterEnd returns the line holding the closing delimiter of a YAML/TOML
// front matter block, if the file opens with one.
func frontmatterEnd(src *Source) (int, bool) {
	if src.NumLines() < 2 {
		return 0, false
	}
	open := strings.TrimRight(src.Line(0), " \t")
	if open != "---" && open != "+++" {
		return 0, false
	}
	closer := open
	for i := 1; i < src.NumLines(); i++ {
		t := strings.TrimRight(src.Line(i), " \t")
		if t == closer || (closer == "---" && t == "...") {
			return i, true
		}
	}
	return 0, false
}

func maskLines(data []byte, src *Source, from, to int) []byte {
	out := make([]byte, len(data))
	copy(out, data)
	for i := from; i <= to && i < src.NumLines(); i++ {
		start := src.lineStart[i]
		end := len(data)
		if i+1 < src.NumLines() {
			end = src.lineStart[i+1]
		}
		for j := start; j < end; j++ {
			if out[j] != '\n' && out[j] != '\r' {
				out[j] = ' '
			}
		}
	}
	return out
}

// Breadcrumb returns the heading trail enclosing the given line.
func (d *Doc) Breadcrumb(line int) []string {
	stack := d.HeadingStack(line)
	out := make([]string, 0, len(stack))
	for _, h := range stack {
		out = append(out, strings.TrimSpace(strings.ReplaceAll(h.Text, "\n", " ")))
	}
	return out
}

// Section returns the line range of the heading section enclosing line: the
// nearest preceding heading through the line before the next heading of the
// same or higher rank.
func (d *Doc) Section(line int) (int, int, bool) {
	idx, end, ok := sectionEnd(d, line)
	if !ok {
		return 0, 0, false
	}
	return d.Headings[idx].Start, end, true
}

// SectionBody returns the same range with the heading itself left out. end is
// start-1 when the heading has no body, which is an insertion point rather
// than a region.
func (d *Doc) SectionBody(line int) (int, int, bool) {
	idx, end, ok := sectionEnd(d, line)
	if !ok {
		return 0, 0, false
	}
	start := d.Headings[idx].End + 1
	if end < start {
		end = start - 1
	}
	return start, end, true
}

// sectionEnd finds the heading enclosing a line and the last line under it:
// everything up to the next heading of the same or higher rank.
func sectionEnd(d *Doc, line int) (idx, end int, ok bool) {
	idx = -1
	for i, h := range d.Headings {
		if h.Start <= line {
			idx = i
		} else {
			break
		}
	}
	if idx < 0 {
		return 0, 0, false
	}
	end = d.Src.NumLines() - 1
	for _, h := range d.Headings[idx+1:] {
		if h.Level <= d.Headings[idx].Level {
			end = h.Start - 1
			break
		}
	}
	return idx, end, true
}

// HeadingStack is the trail of headings enclosing a line, outermost first: the
// nearest preceding heading and every heading of higher rank still open above
// it. Breadcrumb is its text, and the expand ladder is its line ranges.
func (d *Doc) HeadingStack(line int) []*Block {
	var stack []*Block
	for _, h := range d.Headings {
		if h.Start > line {
			break
		}
		for len(stack) > 0 && stack[len(stack)-1].Level >= h.Level {
			stack = stack[:len(stack)-1]
		}
		stack = append(stack, h)
	}
	return stack
}

// Enclosing is the smallest located block holding every line of start..end, or
// the document root where no smaller one does. It is what names a region the
// document did not itself draw: the lines several merged results cover
// together, or the ones an address asked for.
func (d *Doc) Enclosing(start, end int) *Block {
	best := d.Root
	for _, b := range d.Blocks {
		if !b.Located || b.Start > start || b.End < end {
			continue
		}
		if b.Depth > best.Depth {
			best = b
		}
	}
	return best
}

// BlockAt is the innermost block covering a point in the file, named by the
// line it sits on and its byte offset. Lines settle it everywhere the document
// is a stack of blocks; bytes settle it inside a table row, where the cells all
// share one line and only their byte ranges tell them apart.
//
// The two are needed together because neither is right on its own. A block's
// line range is widened back over the syntax that introduced it -- the "#" of a
// heading, the fences around a code block -- and its byte range is not, so a
// point in that syntax is inside the block by line and outside it by byte.
func (d *Doc) BlockAt(line, off int) *Block {
	cur := d.Root
	for {
		var next *Block
		for _, c := range cur.Children {
			if !c.Located {
				continue
			}
			if c.Kind == KindCell {
				// A cell is narrower than the line it sits on. A point in the
				// padding or on a delimiting pipe belongs to no cell and stops
				// at the row, which asks for the same care anyway.
				if c.Lo <= off && off <= c.Hi {
					next = c
				}
				continue
			}
			if c.Start <= line && line <= c.End {
				next = c
				break
			}
		}
		if next == nil {
			return cur
		}
		cur = next
	}
}

// Constraint is what the markdown at a point requires of text written there.
// It is read off the block that owns the point rather than guessed from the
// text around it, which is the whole reason the parse is kept after a search:
// only the tree knows that a line is a table row rather than a paragraph that
// happens to hold pipes.
type Constraint struct {
	Kind Kind
	// SingleLine is whether a newline would end the node rather than continue
	// it. A heading stops at one and a table row stops at one; a paragraph
	// takes it as a soft break and a fenced block takes it as content.
	SingleLine bool
	// Escape is the characters that carry syntax here and have to leave
	// backslashed to be read as text.
	Escape string
}

// Constraint reports what a rewrite inside this block has to respect.
func (b *Block) Constraint() Constraint {
	switch b.Kind {
	case KindCell, KindRow, KindTable:
		// GFM gives a cell no way to hold a newline, and an unescaped pipe in
		// one opens a column the header never declared -- which the parser
		// takes silently, leaving a table that is wrong rather than refused.
		return Constraint{Kind: b.Kind, SingleLine: true, Escape: "|"}
	case KindHeading:
		return Constraint{Kind: b.Kind, SingleLine: true}
	}
	return Constraint{Kind: b.Kind}
}

// Fit rewrites text so it reads as text at this point, and refuses what it
// cannot rewrite. Escaping is a repair; a newline where none can go is not,
// because the alternatives -- dropping it, or turning it into a <br> -- are
// both decisions about the document that belong to whoever asked for the edit.
func (c Constraint) Fit(text string) (string, error) {
	if c.SingleLine && strings.ContainsAny(text, "\r\n") {
		return "", fmt.Errorf("a %s holds one line, and the replacement carries a line break", c.Kind)
	}
	if c.Escape == "" {
		return text, nil
	}
	return escape(text, c.Escape), nil
}

// escape backslashes the characters that carry syntax, leaving alone what the
// text already escaped: a "\|" written by hand means a pipe and would come out
// meaning a backslash if it were escaped again.
func escape(s, chars string) string {
	if !strings.ContainsAny(s, chars) {
		return s
	}
	var sb strings.Builder
	sb.Grow(len(s) + 4)
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '\\' && i+1 < len(s) {
			sb.WriteByte(c)
			i++
			sb.WriteByte(s[i])
			continue
		}
		if strings.IndexByte(chars, c) >= 0 {
			sb.WriteByte('\\')
		}
		sb.WriteByte(c)
	}
	return sb.String()
}

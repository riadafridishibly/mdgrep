// Command mdfmt parses markdown with goldmark and writes markdown back out,
// reconstructing the document from the AST alone.
//
// goldmark ships no markdown renderer -- renderer/html is the only one in the
// module -- so this is a renderer written directly against what the AST
// retains. Every place it has to pick a spelling rather than recover one is
// marked GUESS: those are the points where the parse dropped information and
// no renderer built on this AST could put it back.
//
// Usage:
//
//	mdfmt [-c] [file...]     # no file, or "-", reads stdin
//
// With -c the output is compared against the input instead of printed and the
// file is reported as one of three things: "round-trips" when the render is
// byte-identical, "reformats" when the bytes differ but both sides render to
// the same HTML, and "lost" when the HTML differs -- a render that changed
// what the document means.
package main

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	east "github.com/yuin/goldmark/extension/ast"
	"github.com/yuin/goldmark/text"
)

// The same parser mdgrep itself uses, so what round-trips here is what would
// round-trip there. The HTML renderer beside it is the one goldmark does ship,
// and it is what settles whether a render that changed the bytes also changed
// what the document means.
var (
	md     = goldmark.New(goldmark.WithExtensions(extension.GFM))
	parser = md.Parser()
)

// html is the document as goldmark renders it, which is the meaning a render
// has to preserve to count as a reformat rather than a loss.
func html(src []byte) string {
	var sb strings.Builder
	if err := md.Convert(src, &sb); err != nil {
		return "!" + err.Error()
	}
	return sb.String()
}

func main() {
	check := flag.Bool("c", false, "compare against the input instead of printing")
	flag.Parse()

	out := bufio.NewWriter(os.Stdout)
	defer out.Flush()

	paths := flag.Args()
	if len(paths) == 0 {
		paths = []string{"-"}
	}
	code := 0
	for _, path := range paths {
		src, err := read(path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "mdfmt: %v\n", err)
			code = 2
			continue
		}
		got := Format(src)
		if !*check {
			out.WriteString(got)
			continue
		}
		// Three outcomes, and only the third is a document the AST could not
		// hold. Byte-identical is a document it kept whole. Different bytes
		// but the same HTML is a reformat: a spelling the AST never recorded
		// was replaced by this renderer's own and the meaning survived.
		// Different HTML is a loss, and it is the only verdict that needs the
		// renderer goldmark ships to reach -- rendering twice and comparing
		// would call a mangled code span stable, because it is stably wrong.
		if got == string(src) {
			fmt.Fprintf(out, "%s: round-trips\n", path)
			continue
		}
		n, _ := firstDiff(string(src), got)
		if html([]byte(got)) == html(src) {
			fmt.Fprintf(out, "%s:%d: reformats\n  in:  %s\n  out: %s\n",
				path, n+1, quote(nth(string(src), n)), quote(nth(got, n)))
			continue
		}
		out.Flush()
		m, _ := firstDiff(html(src), html([]byte(got)))
		fmt.Fprintf(os.Stderr, "%s:%d: lost\n  in:   %s\n  out:  %s\n  html: %s\n     -> %s\n",
			path, n+1, quote(nth(string(src), n)), quote(nth(got, n)),
			quote(nth(html(src), m)), quote(nth(html([]byte(got)), m)))
		code = 1
	}
	out.Flush()
	os.Exit(code)
}

func read(path string) ([]byte, error) {
	if path == "-" {
		return io.ReadAll(os.Stdin)
	}
	return os.ReadFile(path)
}

// Format parses src and renders the AST back to markdown.
func Format(src []byte) string {
	r := &rend{src: src}
	doc := parser.Parse(text.NewReader(src))
	s := r.children(doc, "\n\n")
	if s != "" {
		s += "\n"
	}
	return s
}

type rend struct{ src []byte }

// children renders every block child of n and joins them.
func (r *rend) children(n ast.Node, sep string) string {
	var parts []string
	for c := n.FirstChild(); c != nil; c = c.NextSibling() {
		parts = append(parts, r.block(c))
	}
	return strings.Join(parts, sep)
}

func (r *rend) block(n ast.Node) string {
	switch n := n.(type) {
	case *ast.Heading:
		// GUESS: always ATX. The AST records Level and nothing about how the
		// heading was written, so a setext heading comes back as "#", and a
		// heading closed with trailing hashes comes back without them.
		return strings.Repeat("#", n.Level) + " " + r.inline(n)

	case *ast.Paragraph:
		return r.inline(n)

	case *ast.TextBlock:
		return r.inline(n)

	case *ast.ThematicBreak:
		// GUESS: "---". "***", "___" and spaced forms are the same node.
		return "---"

	case *ast.FencedCodeBlock:
		// GUESS: three backticks. The AST keeps Info and nothing about the
		// fence: "~~~" and a fence longer than three come back as "```".
		info := ""
		if n.Info != nil {
			info = string(n.Info.Segment.Value(r.src))
		}
		return "```" + info + "\n" + r.raw(n) + "```"

	case *ast.CodeBlock:
		// GUESS: four spaces. A tab-indented block comes back spaced.
		return indentAll("    ", strings.TrimSuffix(r.raw(n), "\n"))

	case *ast.HTMLBlock:
		body := strings.TrimSuffix(r.raw(n), "\n")
		if n.HasClosure() {
			closure := n.ClosureLine
			body += "\n" + string(closure.Value(r.src))
			body = strings.TrimSuffix(body, "\n")
		}
		return body

	case *ast.Blockquote:
		return indentAll("> ", r.children(n, "\n\n"))

	case *ast.List:
		return r.list(n)

	case *ast.LinkReferenceDefinition:
		// Reference definitions are collected by the parser and every link
		// that used one is resolved to an inline Link, so a definition left
		// in the tree has no users to render for.
		return ""

	case *east.Table:
		return r.table(n)
	}
	return r.children(n, "\n\n")
}

// raw is the source text of a leaf block, taken from the segments goldmark
// recorded rather than rebuilt from children.
func (r *rend) raw(n ast.Node) string {
	var sb strings.Builder
	ls := n.Lines()
	for i := 0; i < ls.Len(); i++ {
		seg := ls.At(i)
		sb.Write(seg.Value(r.src))
	}
	return sb.String()
}

func (r *rend) list(n *ast.List) string {
	// GUESS: one space after the marker, and the continuation indent that
	// follows from it. The AST keeps the marker byte but not the width of the
	// gap after it, so "-   a" comes back as "- a".
	sep := "\n"
	if !n.IsTight {
		sep = "\n\n"
	}
	num := n.Start
	var parts []string
	for c := n.FirstChild(); c != nil; c = c.NextSibling() {
		mark := string(n.Marker) + " "
		if n.IsOrdered() {
			mark = fmt.Sprintf("%d%c ", num, n.Marker)
			num++
		}
		body := r.children(c, sep)
		parts = append(parts, mark+indentRest(strings.Repeat(" ", len(mark)), body))
	}
	return strings.Join(parts, sep)
}

func (r *rend) table(n *east.Table) string {
	// GUESS: single-space padding and a three-dash rule. The AST keeps the
	// per-column Alignment and nothing about how wide the source drew the
	// columns, so an aligned table comes back minimal.
	var rows []string
	for row := n.FirstChild(); row != nil; row = row.NextSibling() {
		var cells []string
		for c := row.FirstChild(); c != nil; c = c.NextSibling() {
			// A pipe inside a cell has to leave as "\|" or it would open a
			// column that was never there -- and one the source already
			// escaped arrives with its backslash still on it, so escaping it
			// again would write the backslash rather than the pipe.
			cells = append(cells, escapePipes(r.inline(c)))
		}
		rows = append(rows, "|"+strings.Join(pad(cells), "|")+"|")
		if _, ok := row.(*east.TableHeader); ok {
			rows = append(rows, rule(n.Alignments))
		}
	}
	return strings.Join(rows, "\n")
}

func escapePipes(s string) string {
	if !strings.Contains(s, "|") {
		return s
	}
	var sb strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+1 < len(s) {
			sb.WriteByte(s[i])
			i++
			sb.WriteByte(s[i])
			continue
		}
		if s[i] == '|' {
			sb.WriteByte('\\')
		}
		sb.WriteByte(s[i])
	}
	return sb.String()
}

func rule(aligns []east.Alignment) string {
	cells := make([]string, len(aligns))
	for i, a := range aligns {
		switch a {
		case east.AlignLeft:
			cells[i] = ":---"
		case east.AlignRight:
			cells[i] = "---:"
		case east.AlignCenter:
			cells[i] = ":---:"
		default:
			cells[i] = "---"
		}
	}
	return "|" + strings.Join(pad(cells), "|") + "|"
}

// pad puts a space either side of a cell, and a single space in an empty one.
func pad(cells []string) []string {
	out := make([]string, len(cells))
	for i, c := range cells {
		if c == "" {
			out[i] = " "
			continue
		}
		out[i] = " " + c + " "
	}
	return out
}

func (r *rend) inline(n ast.Node) string {
	var sb strings.Builder
	for c := n.FirstChild(); c != nil; c = c.NextSibling() {
		r.in(&sb, c)
	}
	return sb.String()
}

func (r *rend) in(sb *strings.Builder, n ast.Node) {
	switch n := n.(type) {
	case *ast.Text:
		sb.Write(n.Segment.Value(r.src))
		switch {
		case n.HardLineBreak():
			// GUESS: backslash. Two trailing spaces make the same node.
			sb.WriteString("\\\n")
		case n.SoftLineBreak():
			sb.WriteString("\n")
		}

	case *ast.String:
		sb.Write(n.Value)

	case *ast.CodeSpan:
		// GUESS: one backtick, no inner padding. A span written with two
		// backticks to hold a backtick comes back with one, which changes
		// what it means.
		sb.WriteString("`")
		for c := n.FirstChild(); c != nil; c = c.NextSibling() {
			if t, ok := c.(*ast.Text); ok {
				sb.Write(t.Segment.Value(r.src))
			}
		}
		sb.WriteString("`")

	case *ast.Emphasis:
		// GUESS: asterisks. "_a_" and "*a*" are the same node.
		d := strings.Repeat("*", n.Level)
		sb.WriteString(d)
		sb.WriteString(r.inline(n))
		sb.WriteString(d)

	case *east.Strikethrough:
		sb.WriteString("~~")
		sb.WriteString(r.inline(n))
		sb.WriteString("~~")

	case *ast.Link:
		// GUESS: the inline form. A reference-style link resolves to this
		// same node, so "[a][ref]" comes back as "[a](url)" and the
		// definition it pointed at is left behind unused.
		sb.WriteString("[")
		sb.WriteString(r.inline(n))
		sb.WriteString("](")
		sb.Write(n.Destination)
		if len(n.Title) > 0 {
			sb.WriteString(` "`)
			sb.Write(n.Title)
			sb.WriteString(`"`)
		}
		sb.WriteString(")")

	case *ast.Image:
		sb.WriteString("![")
		sb.WriteString(r.inline(n))
		sb.WriteString("](")
		sb.Write(n.Destination)
		if len(n.Title) > 0 {
			sb.WriteString(` "`)
			sb.Write(n.Title)
			sb.WriteString(`"`)
		}
		sb.WriteString(")")

	case *ast.AutoLink:
		// GUESS: the angle-bracket form. GFM linkify turns a bare URL in
		// running text into this node too, and it comes back bracketed.
		sb.WriteString("<")
		sb.Write(n.URL(r.src))
		sb.WriteString(">")

	case *ast.RawHTML:
		for i := 0; i < n.Segments.Len(); i++ {
			seg := n.Segments.At(i)
			sb.Write(seg.Value(r.src))
		}

	case *east.TaskCheckBox:
		// The parser eats the space between the checkbox and the item's text,
		// so it is written back here rather than recovered.
		if n.IsChecked {
			sb.WriteString("[x] ")
		} else {
			sb.WriteString("[ ] ")
		}

	default:
		sb.WriteString(r.inline(n))
	}
}

// indentAll puts a prefix on every line, trimming it back on the blank ones so
// a blockquote does not carry trailing spaces.
func indentAll(prefix, s string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		if l == "" {
			lines[i] = strings.TrimRight(prefix, " ")
			continue
		}
		lines[i] = prefix + l
	}
	return strings.Join(lines, "\n")
}

// indentRest indents every line but the first, which already carries the list
// marker that sets the indent.
func indentRest(prefix, s string) string {
	lines := strings.Split(s, "\n")
	for i := 1; i < len(lines); i++ {
		if lines[i] == "" {
			continue
		}
		lines[i] = prefix + lines[i]
	}
	return strings.Join(lines, "\n")
}

// firstDiff reports the zero-based line where two documents part.
func firstDiff(a, b string) (int, bool) {
	as, bs := strings.Split(a, "\n"), strings.Split(b, "\n")
	for i := 0; i < len(as) || i < len(bs); i++ {
		if i >= len(as) || i >= len(bs) || as[i] != bs[i] {
			return i, false
		}
	}
	return 0, true
}

func nth(s string, n int) string {
	lines := strings.Split(s, "\n")
	if n >= len(lines) {
		return "<end of file>"
	}
	return lines[n]
}

func quote(s string) string { return fmt.Sprintf("%q", s) }

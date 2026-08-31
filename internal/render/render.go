// Package render writes search results to a terminal or a pipe.
package render

import (
	"bufio"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/riadafridishibly/mdgrep/internal/match"
	"github.com/riadafridishibly/mdgrep/internal/mdoc"
	"github.com/riadafridishibly/mdgrep/internal/search"
)

const (
	reset     = "\x1b[0m"
	boldRed   = "\x1b[1;31m"
	red       = "\x1b[31m"
	green     = "\x1b[32m"
	magenta   = "\x1b[35m"
	dim       = "\x1b[2m"
	cyanFaint = "\x1b[36m"
)

// Format is how results are written. Plain is for a person reading a
// terminal; Compact and JSON are for a program, and neither is coloured.
type Format int

const (
	Plain Format = iota
	// Compact prints the path once per file and then one tab-separated
	// record per result, which costs a fraction of the same results as JSON
	// while staying line-oriented.
	Compact
	JSON
	// Outline answers "what is in these files" rather than "where does this
	// appear": one indented line per heading, which is the cheapest useful
	// view of a tree.
	Outline
)

type Printer struct {
	W           *bufio.Writer
	Color       bool
	LineNumbers bool
	Breadcrumb  bool
	Format      Format
	// Separator goes between two results of the same file. It is there for a
	// person scanning the output; an empty one leaves the results flush
	// against each other.
	Separator string
	// Truncate caps how many lines of any one result are printed, so that a
	// hit inside a 400-line fenced block does not print 400 lines. Zero means
	// print all of them.
	Truncate int

	wroteAny bool
}

// paint colours a string for a person. Compact and JSON are read by programs,
// so they are never coloured; Plain and Outline are read by people.
func (p *Printer) paint(code, s string) string {
	if !p.Color || s == "" || p.Format == Compact || p.Format == JSON {
		return s
	}
	return code + s + reset
}

// Print writes one file's results. src supplies the lines, m highlights them.
func (p *Printer) Print(src *mdoc.Source, results []search.Result, m match.Matcher) {
	if len(results) == 0 {
		return
	}
	switch p.Format {
	case JSON:
		p.printJSON(src, results)
		return
	case Compact:
		p.printCompact(src, results)
		return
	case Outline:
		p.printOutline(src, results)
		return
	}
	if p.wroteAny {
		fmt.Fprintln(p.W)
	}
	p.wroteAny = true

	fmt.Fprintln(p.W, p.paint(magenta, src.Path))
	last := 0
	for _, r := range results {
		last = max(last, r.End+1)
	}
	width := len(strconv.Itoa(last))
	for i, r := range results {
		if i > 0 && p.Separator != "" {
			fmt.Fprintf(p.W, "  %s\n", p.paint(dim, p.Separator))
		}
		if crumb := p.crumb(r); p.Breadcrumb && len(crumb) > 0 {
			fmt.Fprintf(p.W, "  %s\n", p.paint(cyanFaint, joinCrumb(crumb)))
		}
		end, cut := p.cap(r.Start, r.End)
		for n := r.Start; n <= end; n++ {
			line := src.Line(n)
			body := line
			if n >= r.HitStart && n <= r.HitEnd {
				body = p.highlight(line, m)
			}
			if p.LineNumbers {
				num := fmt.Sprintf("%*d", width, n+1)
				fmt.Fprintf(p.W, "  %s %s %s\n", p.paint(green, num), p.paint(dim, "│"), body)
			} else {
				fmt.Fprintf(p.W, "  %s\n", body)
			}
		}
		if cut > 0 {
			fmt.Fprintf(p.W, "  %s\n", p.paint(dim, elision(cut)))
		}
	}
}

// crumb is the trail to print above a result. A heading result prints that
// heading on the very next line and the trail ends with it, so the last
// element would say the same thing twice; the trail stops at the parent
// instead. A --section-body result keeps the whole trail, because there the
// heading line itself is never printed.
func (p *Printer) crumb(r search.Result) []string {
	if r.Kind == mdoc.KindHeading && len(r.Breadcrumb) > 0 &&
		r.Start <= r.HitStart && r.HitStart <= r.End {
		return r.Breadcrumb[:len(r.Breadcrumb)-1]
	}
	return r.Breadcrumb
}

// cap applies Truncate to one result, returning the last line to print and how
// many lines were left out.
func (p *Printer) cap(start, end int) (last, cut int) {
	if p.Truncate <= 0 || end-start+1 <= p.Truncate {
		return end, 0
	}
	last = start + p.Truncate - 1
	return last, end - last
}

func elision(cut int) string {
	if cut == 1 {
		return "… +1 line"
	}
	return fmt.Sprintf("… +%d lines", cut)
}

func (p *Printer) highlight(line string, m match.Matcher) string {
	if !p.Color {
		return line
	}
	spans := m.Spans(line)
	if len(spans) == 0 {
		return line
	}
	var sb strings.Builder
	prev := 0
	for _, s := range spans {
		if s.Start < prev || s.End > len(line) {
			continue
		}
		sb.WriteString(line[prev:s.Start])
		sb.WriteString(boldRed)
		sb.WriteString(line[s.Start:s.End])
		sb.WriteString(reset)
		prev = s.End
	}
	sb.WriteString(line[prev:])
	return sb.String()
}

// printCompact writes the path once and then one record per result:
//
//	start[-end] <TAB> kind <TAB> text
//
// The text is escaped so a record is always one line, which is the whole point
// of the format — a reader splits on newline and then on tab, and a path is
// the line that has no tab in it.
func (p *Printer) printCompact(src *mdoc.Source, results []search.Result) {
	p.wroteAny = true
	fmt.Fprintln(p.W, src.Path)
	for _, r := range results {
		end, cut := p.cap(r.Start, r.End)
		text := strings.Join(src.Lines(r.Start, end), "\n")
		if cut > 0 {
			text += "\n" + elision(cut)
		}
		fmt.Fprintf(p.W, "%s\t%s\t%s\n", lineSpan(r.Start, r.End), r.Kind, escape(text))
	}
}

// printOutline writes one line per result, indented by heading level, so the
// shape of a document is readable at a glance. A result that is not a heading
// sits at the outermost level rather than being dropped, since the caller
// asked to see what matched.
func (p *Printer) printOutline(src *mdoc.Source, results []search.Result) {
	if p.wroteAny {
		fmt.Fprintln(p.W)
	}
	p.wroteAny = true
	fmt.Fprintln(p.W, p.paint(magenta, src.Path))

	last := 0
	for _, r := range results {
		last = max(last, r.HitStart+1)
	}
	width := len(strconv.Itoa(last))
	for _, r := range results {
		// HitStart, not Start: the line to print is the heading's own, and
		// Start is wherever the region around it happens to begin.
		indent := strings.Repeat("  ", max(r.Level-1, 0))
		text := strings.TrimSpace(src.Line(r.HitStart))
		if p.LineNumbers {
			fmt.Fprintf(p.W, "  %s %s %s%s\n",
				p.paint(green, fmt.Sprintf("%*d", width, r.HitStart+1)),
				p.paint(dim, "│"), indent, text)
		} else {
			fmt.Fprintf(p.W, "  %s%s\n", indent, text)
		}
	}
}

// lineSpan numbers a result the way the rest of the output does, 1-based and
// inclusive, and says a single line once rather than as a span of itself.
func lineSpan(start, end int) string {
	if start == end {
		return strconv.Itoa(start + 1)
	}
	return strconv.Itoa(start+1) + "-" + strconv.Itoa(end+1)
}

var escaper = strings.NewReplacer("\\", "\\\\", "\n", "\\n", "\r", "\\r", "\t", "\\t")

func escape(s string) string { return escaper.Replace(s) }

type jsonResult struct {
	Path       string   `json:"path"`
	Kind       string   `json:"kind"`
	Score      float64  `json:"score"`
	Start      int      `json:"start"`
	End        int      `json:"end"`
	Checked    *bool    `json:"checked,omitempty"`
	Breadcrumb []string `json:"breadcrumb,omitempty"`
	Text       string   `json:"text"`
	// Truncated counts the lines --truncate held back, so a reader can tell a
	// short node from a capped one without measuring the text against start
	// and end.
	Truncated int `json:"truncated,omitempty"`
}

func (p *Printer) printJSON(src *mdoc.Source, results []search.Result) {
	enc := json.NewEncoder(p.W)
	for _, r := range results {
		p.wroteAny = true
		// Present only on task items, where false is meaningful.
		var checked *bool
		if r.Task {
			checked = &r.Checked
		}
		end, cut := p.cap(r.Start, r.End)
		enc.Encode(jsonResult{
			Path:       r.Path,
			Kind:       string(r.Kind),
			Score:      r.Score,
			Start:      r.Start + 1,
			End:        r.End + 1,
			Checked:    checked,
			Breadcrumb: r.Breadcrumb,
			Text:       strings.Join(src.Lines(r.Start, end), "\n"),
			Truncated:  cut,
		})
	}
}

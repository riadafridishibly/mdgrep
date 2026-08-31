// Package render writes search results to a terminal or a pipe.
package render

import (
	"bufio"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/riadafridishibly/mdgrep/internal/match"
	"github.com/riadafridishibly/mdgrep/internal/mdoc"
	"github.com/riadafridishibly/mdgrep/internal/search"
	"github.com/riadafridishibly/mdgrep/internal/stream"
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
	// Stream is what one mdgrep hands the next in a pipe: the file and the
	// span of each result, and none of the text, because the stage reading it
	// opens the file itself.
	Stream
)

// machine reports whether a format is read by a program rather than by a
// person: never coloured, and never run into prose.
func (f Format) machine() bool { return f == Compact || f == JSON || f == Stream }

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
	if !p.Color || s == "" || p.Format.machine() {
		return s
	}
	return code + s + reset
}

// Begin writes whatever a format puts before its first result. A stream says
// it is one up front, and says so even when nothing matches: an empty stream
// is a search that ran and selected nothing, where an empty pipe is no search
// at all.
func (p *Printer) Begin() {
	if p.Format == Stream {
		stream.WriteHeader(p.W)
	}
}

// Print writes one file's results. src supplies the lines, m highlights them.
func (p *Printer) Print(src *mdoc.Source, results []search.Result, m match.Matcher) {
	if len(results) == 0 {
		return
	}
	switch p.Format {
	case Stream:
		p.printStream(results)
		return
	case JSON:
		p.printJSON(src, results, m)
		return
	case Compact:
		p.printCompact(src, results, m)
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
	var shown []string
	for i, r := range results {
		if i > 0 && p.Separator != "" {
			fmt.Fprintf(p.W, "  %s\n", p.paint(dim, p.Separator))
		}
		if p.Breadcrumb && len(r.Breadcrumb) > 0 && !slices.Equal(r.Breadcrumb, shown) {
			fmt.Fprintf(p.W, "  %s\n", p.paint(cyanFaint, joinCrumb(r.Breadcrumb)))
		}
		shown = r.Breadcrumb
		first, last, before, after := p.window(src, r, m)
		if before > 0 {
			fmt.Fprintf(p.W, "  %s\n", p.paint(dim, elision(before)))
		}
		for n := first; n <= last; n++ {
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
		if after > 0 {
			fmt.Fprintf(p.W, "  %s\n", p.paint(dim, elision(after)))
		}
	}
}

// window applies Truncate to one result. The cap is a budget of lines and the
// matched line is the one thing the caller asked for, so the window starts at
// the top of the region and slides down only as far as it must to hold that
// line, spending what is left of the budget below it. before is therefore
// what was skipped to reach the hit, not context kept above it.
func (p *Printer) window(src *mdoc.Source, r search.Result, m match.Matcher) (first, last, before, after int) {
	if p.Truncate <= 0 || r.End-r.Start+1 <= p.Truncate {
		return r.Start, r.End, 0, 0
	}
	hit := hitLine(src, r, m)
	first = r.Start
	if hit > first+p.Truncate-1 {
		first = min(hit, r.End-p.Truncate+1)
	}
	last = min(first+p.Truncate-1, r.End)
	return first, last, first - r.Start, r.End - last
}

// hitLine is the line the window has to keep. A block is scored whole -- the
// matcher reads the raw text of the fence or the table, not its lines -- so
// HitStart is where the block begins and says nothing about where in it the
// match is. Truncating from HitStart cuts a long block down to its opening
// lines and drops the very line that was searched for. Spans finds that line
// the same way the highlight does; a matcher with nothing to point at leaves
// the block's first line, which is what an anchor search selects a heading by
// and what a fuzzy score spread over several lines comes to anyway.
func hitLine(src *mdoc.Source, r search.Result, m match.Matcher) int {
	if m == nil {
		return r.HitStart
	}
	for n := r.HitStart; n <= r.HitEnd && n < src.NumLines(); n++ {
		if len(m.Spans(src.Line(n))) > 0 {
			return n
		}
	}
	return r.HitStart
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
//	start[-end] <TAB> kind <TAB> text <TAB> before <TAB> after
//
// The text is escaped so a record is always one line, which is the whole point
// of the format — a reader splits on newline and then on tab, and a path is
// the line that has no tab in it. The path is escaped for the same reason: a
// filename may hold a tab or a newline, and the format has to survive one.
//
// before and after are how many lines --truncate held back on each side, so
// the count is read as a number rather than out of an English notice inside
// the text, which a document that says the same thing would be
// indistinguishable from. They are two fields and not their sum because the
// span is the node's and the text is the window: a reader adds before to
// start to find the line the text begins on, which one total cannot say.
func (p *Printer) printCompact(src *mdoc.Source, results []search.Result, m match.Matcher) {
	p.wroteAny = true
	fmt.Fprintln(p.W, Escape(src.Path))
	for _, r := range results {
		first, last, before, after := p.window(src, r, m)
		text := strings.Join(src.Lines(first, last), "\n")
		fmt.Fprintf(p.W, "%s\t%s\t%s\t%d\t%d\n",
			lineSpan(r.Start, r.End), r.Kind, Escape(text), before, after)
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
// inclusive. A region that covers no line at all is spelled End < Start, and a
// span running backwards is one no reader of "start[-end]" can take, so a
// single line — real or empty — is said once as itself.
func lineSpan(start, end int) string {
	if end <= start {
		return strconv.Itoa(start + 1)
	}
	return strconv.Itoa(start+1) + "-" + strconv.Itoa(end+1)
}

var escaper = strings.NewReplacer("\\", "\\\\", "\n", "\\n", "\r", "\\r", "\t", "\\t")

// Escape puts a string into one field of a compact record, so a value holding
// a tab or a newline cannot be read as the end of the field or the record.
func Escape(s string) string { return escaper.Replace(s) }

type jsonResult struct {
	Path       string   `json:"path"`
	Kind       string   `json:"kind"`
	Score      float64  `json:"score"`
	Start      int      `json:"start"`
	End        int      `json:"end"`
	Checked    *bool    `json:"checked,omitempty"`
	Breadcrumb []string `json:"breadcrumb,omitempty"`
	Text       string   `json:"text"`
	// TruncatedBefore and TruncatedAfter count the lines --truncate held back
	// on each side, so a reader can tell a short node from a capped one
	// without measuring the text against start and end -- and, since start is
	// the node's and text is the window, can place the window by adding
	// TruncatedBefore to start.
	TruncatedBefore int `json:"truncated_before,omitempty"`
	TruncatedAfter  int `json:"truncated_after,omitempty"`
}

// printStream writes one region per result: where it is, and nothing about
// what it says. The next stage reads the file for the rest.
func (p *Printer) printStream(results []search.Result) {
	for _, r := range results {
		p.wroteAny = true
		stream.WriteRegion(p.W, r.Path, r.Start, r.End)
	}
}

func (p *Printer) printJSON(src *mdoc.Source, results []search.Result, m match.Matcher) {
	enc := json.NewEncoder(p.W)
	for _, r := range results {
		p.wroteAny = true
		// Present only on task items, where false is meaningful.
		var checked *bool
		if r.Task {
			checked = &r.Checked
		}
		first, last, before, after := p.window(src, r, m)
		enc.Encode(jsonResult{
			Path:            r.Path,
			Kind:            string(r.Kind),
			Score:           r.Score,
			Start:           r.Start + 1,
			End:             max(r.End, r.Start) + 1,
			Checked:         checked,
			Breadcrumb:      r.Breadcrumb,
			Text:            strings.Join(src.Lines(first, last), "\n"),
			TruncatedBefore: before,
			TruncatedAfter:  after,
		})
	}
}

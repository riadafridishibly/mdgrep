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
	// Filename prints which file a result came from, and Heading puts that
	// name on a line above the file's results rather than in front of every
	// line of them. Both are grep's and ripgrep's, and so are the two facts
	// the caller settles them from when nobody said: a name is worth printing
	// when more than one file could answer, and a heading when a person is
	// reading the output rather than a program.
	Filename bool
	Heading  bool
	// Breadcrumb writes the heading trail above a result. It is the one piece
	// of the page with no counterpart in grep, and it goes wherever a
	// Heading goes unless asked otherwise: a heading is what says a person
	// is reading, and the trail is what tells a person where in the document
	// they are.
	Breadcrumb bool
	Format     Format
	// Separator goes between two groups of file lines that are not next to
	// each other in the file: two results, two runs of match lines inside one
	// result, and two files where no heading parts them. It is grep's and
	// ripgrep's "--"; an empty one leaves the groups flush against each other.
	Separator string
	// Before and After are how many lines of context to print each side of a
	// match line, counted in the file and clipped to it, exactly as in
	// ripgrep. They pad the page and nothing else: an edit still rewrites the
	// node, and a stream still hands on the node.
	Before, After int
	// Span writes the expand ladder after a result -- what the result could be
	// widened to, and what each rung would cost.
	Span bool
	// Whole prints the region entire rather than the lines that matched
	// inside it. Asking for a widener asks to see the region whole, which is
	// the only switch between line output and node output.
	Whole bool
	// Truncate caps how many lines of any one result are printed, so that a
	// hit inside a 400-line fenced block does not print 400 lines. Zero means
	// print all of them.
	Truncate int

	wroteAny bool
}

// inline reports whether the file name rides every line, as against standing
// above a file's results.
func (p *Printer) inline() bool { return p.Filename && !p.Heading }

// writePrefix writes what stands in front of one printed line: the file name,
// the line number, or neither, each closed by a marker, so that a line reads
// "path:line:text" the way grep and rg write one. mark is ":" on a line that
// matched and "-" on one a context flag pulled in, which is grep's convention
// and ripgrep's. It writes straight to the buffer because it runs once per
// line of output, which is the hottest path in the program.
func (p *Printer) writePrefix(path string, n int, mark string) {
	if p.inline() {
		p.writePath(path, mark)
	}
	if p.LineNumbers {
		p.W.WriteString(p.paint(green, strconv.Itoa(n+1)))
		p.W.WriteString(p.paint(dim, mark))
	}
}

// writePath writes a file name and the marker that closes it.
func (p *Printer) writePath(path, mark string) {
	p.W.WriteString(p.paint(magenta, path))
	p.W.WriteString(p.paint(dim, mark))
}

// writeLine writes one line of a file behind its prefix.
func (p *Printer) writeLine(path string, n int, text, mark string) {
	p.writePrefix(path, n, mark)
	p.W.WriteString(text)
	p.W.WriteByte('\n')
}

// writeNote writes a line that is about the output rather than of the file:
// what --truncate held back. It names its file the way every other line does
// and takes no line number, having none, so a reader splitting on the colon
// finds the path where it always is and prose where a number would be.
func (p *Printer) writeNote(path, note string) {
	if p.inline() {
		p.writePath(path, ":")
	}
	p.W.WriteString(p.paint(dim, note))
	p.W.WriteByte('\n')
}

// beginFile writes what stands between one file's results and the next. A
// heading stands above a file's results with a blank line between two files,
// the way rg writes a terminal. Without one there is nothing to stand above
// and nothing to separate: the file name rides every line instead, and the
// separator goes between two files as it goes between two results, the way
// grep and rg print "--" between two groups whether or not a file boundary
// lies between them.
func (p *Printer) beginFile(path string) {
	if p.Heading {
		if p.wroteAny {
			fmt.Fprintln(p.W)
		}
		if p.Filename {
			fmt.Fprintln(p.W, p.paint(magenta, path))
		}
	} else if p.wroteAny && p.Separator != "" {
		fmt.Fprintln(p.W, p.paint(dim, p.Separator))
	}
	p.wroteAny = true
}

// PrintFile answers -l: the file's name is the whole of the line.
func (p *Printer) PrintFile(path string) {
	fmt.Fprintln(p.W, p.paint(magenta, path))
}

// PrintCount answers -c. grep and rg name the file beside a tally on the same
// terms they name it beside a result, and never above one: a tally is one
// line, and a heading over one line is a line spent on nothing.
func (p *Printer) PrintCount(path string, n int) {
	if p.Filename {
		p.writePath(path, ":")
	}
	p.W.WriteString(strconv.Itoa(n))
	p.W.WriteByte('\n')
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
		p.printJSON(src, results)
		return
	case Compact:
		p.printCompact(src, results)
		return
	case Outline:
		p.printOutline(src, results)
		return
	}
	p.beginFile(src.Path)

	// A page is the file's lines, not each result's own copy of them, so the
	// two questions grep answers per line are answered over the file: whether
	// the matcher pointed at it, wherever it was pointed at, and whether it
	// has already been printed.
	hit := make(map[int]bool)
	for _, r := range results {
		for _, n := range r.MatchLines() {
			hit[n] = true
		}
	}
	// last is the file line the page is standing on, and -1 until one has been
	// printed: nothing stands between a group and the top of the page.
	last := -1
	var shown []string
	for _, r := range results {
		full := p.page(src, r)
		window, before, after := p.cap(full, r)
		// The note writes out the spans a result could be widened to, so where
		// the page it capped is one of them the note's own numbers are the
		// count: "item 495-505" beside a printed 495 says the ten lines held
		// back as plainly as "… +10 lines" does. It takes the numbers to say
		// it, though -- they are what places the window inside the span -- and
		// a page of match lines is no span at all, so there the counts are the
		// only thing that says lines were cut.
		if p.LineNumbers && named(full, r.Rungs) && p.spanNote(r, window) != "" {
			before, after = 0, 0
		}
		// Context is counted in the file and clipped to it, so two windows
		// reaching the same line are one group of file lines rather than two
		// copies of it. The lines a widener asked for are the region itself
		// and are the result's own answer, so those are never dropped.
		lines := window
		if !p.Whole {
			lines = past(window, last)
		}
		// grep prints "--" between two groups of file lines that are not next
		// to each other, and nothing between two that are. Two results whose
		// lines run on are one such group, the same as two runs of match lines
		// inside one result are two.
		if last >= 0 && len(lines) > 0 && lines[0].n != last+1 {
			p.separate()
		}
		if p.Breadcrumb && len(r.Breadcrumb) > 0 && !slices.Equal(r.Breadcrumb, shown) {
			fmt.Fprintln(p.W, p.paint(cyanFaint, joinCrumb(r.Breadcrumb)))
		}
		shown = r.Breadcrumb
		// What --truncate held back is said wherever it was held back: a
		// window with nothing to say it is short is a short node to whoever
		// reads it. --format compact and --format json carry the same two
		// counts as numbers.
		if before > 0 {
			p.writeNote(src.Path, elision(before))
		}
		for j, l := range lines {
			if j > 0 && lines[j-1].n != l.n-1 {
				p.separate()
			}
			mark, body := "-", src.Line(l.n)
			if l.match || hit[l.n] {
				mark, body = ":", p.highlight(body, m)
			}
			p.writeLine(src.Path, l.n, body, mark)
		}
		if len(lines) > 0 {
			last = lines[len(lines)-1].n
		}
		if after > 0 {
			p.writeNote(src.Path, elision(after))
		}
		// The note is a terminator rather than a group of file lines, so no
		// separator stands in front of it. It is measured against the whole
		// window rather than what was left of it: the lines dropped are on the
		// page above, so a rung they cover is a rung the reader can see.
		if note := p.spanNote(r, window); note != "" {
			p.writeNote(src.Path, note)
		}
	}
}

// named reports whether a page is exactly one of the spans the note writes
// out. The page is sorted and holds each line once, so covering the span's
// count from its first line to its last is the whole of it.
func named(page []outLine, rungs []search.Rung) bool {
	if len(page) == 0 {
		return false
	}
	for _, g := range rungs {
		if len(page) == g.End-g.Start+1 && page[0].n == g.Start && page[len(page)-1].n == g.End {
			return true
		}
	}
	return false
}

// past drops the lines a group already printed, so a window reaching back into
// one is the rest of that group rather than a second copy of it.
func past(lines []outLine, last int) []outLine {
	for i, l := range lines {
		if l.n > last {
			return lines[i:]
		}
	}
	return nil
}

func (p *Printer) separate() {
	if p.Separator != "" {
		fmt.Fprintln(p.W, p.paint(dim, p.Separator))
	}
}

// outLine is one line of the page: which line of the file it is, and whether
// the matcher pointed at it or a context flag pulled it in.
type outLine struct {
	n     int
	match bool
}

// page is the lines one result prints. A result prints the lines that matched,
// not the node they sit in -- unless a widener asked for the region whole, or
// the matcher could name no line at all and the node claimed them all.
func (p *Printer) page(src *mdoc.Source, r search.Result) []outLine {
	if p.Whole {
		out := make([]outLine, 0, r.End-r.Start+1)
		for n := r.Start; n <= r.End; n++ {
			out = append(out, outLine{n, true})
		}
		return out
	}
	hits := r.MatchLines()
	is := make(map[int]bool, len(hits))
	for _, n := range hits {
		is[n] = true
	}
	// Context is counted in the file and clipped to the file, not to the node
	// or the region, exactly as in ripgrep.
	lo, hi := 0, src.NumLines()-1
	seen := make(map[int]bool, len(hits))
	var out []outLine
	for _, h := range hits {
		for n := max(h-p.Before, lo); n <= min(h+p.After, hi); n++ {
			if !seen[n] {
				seen[n] = true
				out = append(out, outLine{n, is[n]})
			}
		}
	}
	slices.SortFunc(out, func(a, b outLine) int { return a.n - b.n })
	return out
}

// cap applies --truncate to a page. The cap is a budget of lines and the
// matched line is the one thing the caller asked for, so the window starts at
// the top of what would have printed and slides down only as far as it must to
// hold that line, spending what is left of the budget below it. before is
// therefore what was skipped to reach the hit, not context kept above it.
func (p *Printer) cap(lines []outLine, r search.Result) (kept []outLine, before, after int) {
	if p.Truncate <= 0 || len(lines) <= p.Truncate {
		return lines, 0, 0
	}
	hit := 0
	for i, l := range lines {
		if l.n >= hitLine(r) {
			hit = i
			break
		}
	}
	first := 0
	if hit > p.Truncate-1 {
		first = min(hit, len(lines)-p.Truncate)
	}
	last := min(first+p.Truncate-1, len(lines)-1)
	return lines[first : last+1], first, len(lines) - 1 - last
}

// spanNote is the expand ladder written out: one rung per entry, in ladder
// order, so position is the --expand count. It is printed whole or not at all
// -- drop one rung and every rung after it sits at a position that is no
// longer its count -- and it drops when the page already covers every rung, or
// when the hit lies before the first heading and there is no section to widen
// to.
func (p *Printer) spanNote(r search.Result, shown []outLine) string {
	if !p.Span || len(r.Rungs) == 0 {
		return ""
	}
	if r.Rungs[len(r.Rungs)-1].Kind != mdoc.KindSection {
		return ""
	}
	on := make(map[int]bool, len(shown))
	for _, l := range shown {
		on[l.n] = true
	}
	covered := true
	parts := make([]string, len(r.Rungs))
	for i, g := range r.Rungs {
		parts[i] = fmt.Sprintf("%s %d-%d", g.Kind, g.Start+1, g.End+1)
		for n := g.Start; n <= g.End && covered; n++ {
			covered = on[n]
		}
	}
	if covered {
		return ""
	}
	return "(" + strings.Join(parts, ", ") + ")"
}

// window applies Truncate to the whole region a machine format reports. A
// record carries the node, so the window is measured over Start..End rather
// than over the lines a page would have printed.
func (p *Printer) window(r search.Result) (first, last, before, after int) {
	if p.Truncate <= 0 || r.End-r.Start+1 <= p.Truncate {
		return r.Start, r.End, 0, 0
	}
	hit := hitLine(r)
	first = r.Start
	if hit > first+p.Truncate-1 {
		first = min(hit, r.End-p.Truncate+1)
	}
	last = min(first+p.Truncate-1, r.End)
	return first, last, first - r.Start, r.End - last
}

// hitLine is the line a window has to keep. A block is scored whole -- the
// matcher reads the raw text of the fence or the table, not its lines -- so
// HitStart is where the block begins and says nothing about where in it the
// match is. Truncating from HitStart cuts a long block down to its opening
// lines and drops the very line that was searched for. The first of the lines
// the matcher pointed at is that line; a matcher with nothing to point at
// leaves the block's first line, which is what an anchor search selects a
// heading by and what a fuzzy score spread over several lines comes to anyway.
func hitLine(r search.Result) int {
	if len(r.Hits) > 0 {
		return r.Hits[0]
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
//	start[-end] <TAB> kind <TAB> text <TAB> before <TAB> after <TAB> hits <TAB> spans
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
func (p *Printer) printCompact(src *mdoc.Source, results []search.Result) {
	p.wroteAny = true
	fmt.Fprintln(p.W, Escape(src.Path))
	for _, r := range results {
		first, last, before, after := p.window(r)
		text := strings.Join(src.Lines(first, last), "\n")
		fmt.Fprintf(p.W, "%s\t%s\t%s\t%d\t%d\t%s\t%s\n",
			lineSpan(r.Start, r.End), r.Kind, Escape(text), before, after,
			hitList(r.Hits), rungList(r.Rungs))
	}
}

// printOutline writes one line per result, indented by heading level, so the
// shape of a document is readable at a glance. A result that is not a heading
// sits at the outermost level rather than being dropped, since the caller
// asked to see what matched.
func (p *Printer) printOutline(src *mdoc.Source, results []search.Result) {
	p.beginFile(src.Path)
	for _, r := range results {
		// HitStart, not Start: the line to print is the heading's own, and
		// Start is wherever the region around it happens to begin.
		indent := strings.Repeat("  ", max(r.Level-1, 0))
		text := strings.TrimSpace(src.Line(r.HitStart))
		p.writeLine(src.Path, r.HitStart, indent+text, ":")
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
	// Hits are the lines that matched, 1-based. Empty for a node matcher,
	// which is how a reader tells "every line" from "these lines".
	Hits []int `json:"hits"`
	// Spans is the expand ladder, so the array index is the --expand count
	// and the last entry is what --section selects. Always present in full,
	// including where the plain note would have dropped the lot for being
	// wholly covered.
	Spans []jsonRung `json:"spans"`
}

// jsonRung is one rung of the ladder. An entry of it, written "start-end", is
// what --at takes back.
type jsonRung struct {
	Kind  string `json:"kind"`
	Start int    `json:"start"`
	End   int    `json:"end"`
}

// hitList and rungList are the two new compact fields: the match lines as
// numbers, and the ladder as "kind:start-end", each comma-separated so a
// record stays one line of tab-separated fields.
func hitList(hits []int) string {
	out := make([]string, len(hits))
	for i, n := range hits {
		out[i] = strconv.Itoa(n + 1)
	}
	return strings.Join(out, ",")
}

func rungList(rungs []search.Rung) string {
	out := make([]string, len(rungs))
	for i, g := range rungs {
		out[i] = fmt.Sprintf("%s:%d-%d", g.Kind, g.Start+1, g.End+1)
	}
	return strings.Join(out, ",")
}

// oneBased renumbers the match lines for a reader that counts from one.
func oneBased(hits []int) []int {
	out := make([]int, len(hits))
	for i, n := range hits {
		out[i] = n + 1
	}
	return out
}

// ladderOf is the expand ladder in the shape json reports it.
func ladderOf(rungs []search.Rung) []jsonRung {
	out := make([]jsonRung, len(rungs))
	for i, g := range rungs {
		out[i] = jsonRung{Kind: string(g.Kind), Start: g.Start + 1, End: g.End + 1}
	}
	return out
}

// printStream writes one region per result: where it is, and nothing about
// what it says. The next stage reads the file for the rest.
func (p *Printer) printStream(results []search.Result) {
	for _, r := range results {
		stream.WriteRegion(p.W, r.Path, r.Start, r.End)
	}
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
		first, last, before, after := p.window(r)
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
			Hits:            oneBased(r.Hits),
			Spans:           ladderOf(r.Rungs),
		})
	}
}

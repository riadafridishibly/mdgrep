// Package render writes search results to a terminal or a pipe.
package render

import (
	"bufio"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"mdgrep/internal/match"
	"mdgrep/internal/mdoc"
	"mdgrep/internal/search"
)

const (
	reset     = "\x1b[0m"
	boldRed   = "\x1b[1;31m"
	green     = "\x1b[32m"
	magenta   = "\x1b[35m"
	dim       = "\x1b[2m"
	cyanFaint = "\x1b[36m"
)

type Printer struct {
	W           *bufio.Writer
	Color       bool
	LineNumbers bool
	Breadcrumb  bool
	JSON        bool

	wroteAny bool
}

func (p *Printer) paint(code, s string) string {
	if !p.Color || s == "" {
		return s
	}
	return code + s + reset
}

// Print writes one file's results. src supplies the lines, m highlights them.
func (p *Printer) Print(src *mdoc.Source, results []search.Result, m match.Matcher) {
	if len(results) == 0 {
		return
	}
	if p.JSON {
		p.printJSON(src, results)
		return
	}
	if p.wroteAny {
		fmt.Fprintln(p.W)
	}
	p.wroteAny = true

	fmt.Fprintln(p.W, p.paint(magenta, src.Path))
	width := len(strconv.Itoa(results[len(results)-1].End + 1))
	for i, r := range results {
		if i > 0 {
			fmt.Fprintln(p.W, p.paint(dim, "  --"))
		}
		if p.Breadcrumb && len(r.Breadcrumb) > 0 {
			trail := strings.Join(r.Breadcrumb, " › ")
			fmt.Fprintf(p.W, "  %s\n", p.paint(cyanFaint, trail))
		}
		for n := r.Start; n <= r.End; n++ {
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
	}
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

type jsonResult struct {
	Path       string   `json:"path"`
	Kind       string   `json:"kind"`
	Score      float64  `json:"score"`
	Start      int      `json:"start"`
	End        int      `json:"end"`
	Breadcrumb []string `json:"breadcrumb,omitempty"`
	Text       string   `json:"text"`
}

func (p *Printer) printJSON(src *mdoc.Source, results []search.Result) {
	enc := json.NewEncoder(p.W)
	for _, r := range results {
		p.wroteAny = true
		enc.Encode(jsonResult{
			Path:       r.Path,
			Kind:       string(r.Kind),
			Score:      r.Score,
			Start:      r.Start + 1,
			End:        r.End + 1,
			Breadcrumb: r.Breadcrumb,
			Text:       strings.Join(src.Lines(r.Start, r.End), "\n"),
		})
	}
}

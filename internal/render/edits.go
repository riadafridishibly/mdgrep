package render

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/riadafridishibly/mdgrep/internal/edit"
	"github.com/riadafridishibly/mdgrep/internal/mdoc"
)

// PrintEdits writes one file's changes as the lines that went and the lines
// that came, each numbered where it sits in its own version of the file.
func (p *Printer) PrintEdits(src *mdoc.Source, changes []edit.Change, dry bool) {
	if len(changes) == 0 {
		return
	}
	switch p.Format {
	case JSON:
		p.printEditJSON(changes, dry)
		return
	case Compact:
		p.printEditCompact(src, changes, dry)
		return
	}
	if p.wroteAny {
		fmt.Fprintln(p.W)
	}
	p.wroteAny = true

	head := p.paint(magenta, src.Path)
	if dry {
		head += " " + p.paint(dim, "(dry run)")
	}
	fmt.Fprintln(p.W, head)

	width := 1
	for _, c := range changes {
		width = max(width, len(strconv.Itoa(c.End+len(c.New)+1)))
	}
	// Lines the edit adds or removes shift everything after them, so the new
	// side is numbered against a running offset rather than the old file.
	offset := 0
	for i, c := range changes {
		if i > 0 && p.Separator != "" {
			fmt.Fprintf(p.W, "  %s\n", p.paint(dim, p.Separator))
		}
		if p.Breadcrumb && len(c.Breadcrumb) > 0 {
			fmt.Fprintf(p.W, "  %s\n", p.paint(cyanFaint, joinCrumb(c.Breadcrumb)))
		}
		if c.NoOp {
			for n, line := range c.Old {
				p.editLine(dim, "=", c.Start+n, width, line)
			}
			fmt.Fprintf(p.W, "  %s\n", p.paint(dim, "unchanged"))
			continue
		}
		for n, line := range c.Old {
			p.editLine(red, "-", c.Start+n, width, line)
		}
		for n, line := range c.New {
			p.editLine(green, "+", c.Start+offset+n, width, line)
		}
		offset += len(c.New) - len(c.Old)
	}
}

func (p *Printer) editLine(color, mark string, num, width int, line string) {
	if !p.LineNumbers {
		fmt.Fprintf(p.W, "%s %s\n", p.paint(color, mark), line)
		return
	}
	fmt.Fprintf(p.W, "%s %s %s %s\n",
		p.paint(color, mark),
		p.paint(color, fmt.Sprintf("%*d", width, num+1)),
		p.paint(dim, "│"),
		line)
}

// printEditCompact reports a change the way printCompact reports a result:
// the path once, then one record per change.
//
//	start[-end] <TAB> op <TAB> applied|dry|unchanged <TAB> new text
//
// The old text is left out — the caller either has the file or asked for a dry
// run against it — and "new" is empty for a deletion.
func (p *Printer) printEditCompact(src *mdoc.Source, changes []edit.Change, dry bool) {
	p.wroteAny = true
	fmt.Fprintln(p.W, escape(src.Path))
	for _, c := range changes {
		fmt.Fprintf(p.W, "%s\t%s\t%s\t%s\n",
			editSpan(c), c.Op,
			editStatus(c.NoOp, dry), escape(strings.Join(c.New, "\n")))
	}
}

// editSpan numbers the lines a change covers. An insertion covers none: it is
// a point, spelled End == Start-1, and printing that as a span would be a
// range running backwards that no reader of "start[-end]" can take. A point is
// the line it lands on, said once.
func editSpan(c edit.Change) string {
	if c.End < c.Start {
		return lineSpan(c.Start, c.Start)
	}
	return lineSpan(c.Start, c.End)
}

func editStatus(noop, dry bool) string {
	switch {
	case noop:
		return "unchanged"
	case dry:
		return "dry"
	}
	return "applied"
}

type jsonEdit struct {
	Path       string   `json:"path"`
	Op         string   `json:"op"`
	Start      int      `json:"start"`
	End        int      `json:"end"`
	Old        []string `json:"old"`
	New        []string `json:"new"`
	Applied    bool     `json:"applied"`
	Breadcrumb []string `json:"breadcrumb,omitempty"`
}

func (p *Printer) printEditJSON(changes []edit.Change, dry bool) {
	enc := json.NewEncoder(p.W)
	for _, c := range changes {
		p.wroteAny = true
		enc.Encode(jsonEdit{
			Path:       c.Path,
			Op:         string(c.Op),
			Start:      c.Start + 1,
			End:        c.End + 1,
			Old:        c.Old,
			New:        c.New,
			Applied:    !dry && !c.NoOp,
			Breadcrumb: c.Breadcrumb,
		})
	}
}

func joinCrumb(crumb []string) string { return strings.Join(crumb, " › ") }

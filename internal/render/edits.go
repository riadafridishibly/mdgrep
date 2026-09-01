package render

import (
	"encoding/json"
	"fmt"
	"slices"
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
	if p.Heading {
		if p.wroteAny {
			fmt.Fprintln(p.W)
		}
		if p.Filename {
			head := p.paint(magenta, src.Path)
			if dry {
				head += " " + p.paint(dim, "(dry run)")
			}
			fmt.Fprintln(p.W, head)
		}
	} else if p.wroteAny && p.Separator != "" {
		fmt.Fprintln(p.W, p.paint(dim, p.Separator))
	}
	p.wroteAny = true

	// Lines the edit adds or removes shift everything after them, so the new
	// side is numbered against a running offset rather than the old file.
	offset := 0
	var shown []string
	for i, c := range changes {
		if i > 0 && p.Separator != "" {
			fmt.Fprintln(p.W, p.paint(dim, p.Separator))
		}
		if p.Breadcrumb && len(c.Breadcrumb) > 0 && !slices.Equal(c.Breadcrumb, shown) {
			fmt.Fprintln(p.W, p.paint(cyanFaint, joinCrumb(c.Breadcrumb)))
		}
		shown = c.Breadcrumb
		if c.NoOp {
			for n, line := range c.Old {
				p.editLine(src.Path, dim, "=", c.Start+n, line)
			}
			fmt.Fprintln(p.W, p.paint(dim, "unchanged"))
			continue
		}
		for n, line := range c.Old {
			p.editLine(src.Path, red, "-", c.Start+n, line)
		}
		for n, line := range c.New {
			p.editLine(src.Path, green, "+", c.Start+offset+n, line)
		}
		offset += len(c.New) - len(c.Old)
	}
}

// editLine writes one side of a change: a patch's own -, + or = for what
// happened to the line, and then the same "path:line:" prefix a search
// prints, so that the two halves of the tool number a line the one way.
func (p *Printer) editLine(path, color, mark string, num int, line string) {
	fmt.Fprintf(p.W, "%s %s%s\n", p.paint(color, mark), p.prefix(path, num), line)
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
	fmt.Fprintln(p.W, Escape(src.Path))
	for _, c := range changes {
		fmt.Fprintf(p.W, "%s\t%s\t%s\t%s\n",
			lineSpan(c.Start, c.End), c.Op,
			editStatus(c.NoOp, dry), Escape(strings.Join(c.New, "\n")))
	}
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
			End:        max(c.End, c.Start) + 1,
			Old:        c.Old,
			New:        c.New,
			Applied:    !dry && !c.NoOp,
			Breadcrumb: c.Breadcrumb,
		})
	}
}

func joinCrumb(crumb []string) string { return strings.Join(crumb, " › ") }

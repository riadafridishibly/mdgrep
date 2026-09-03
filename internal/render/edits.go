package render

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/riadafridishibly/mdgrep/internal/edit"
	"github.com/riadafridishibly/mdgrep/internal/mdoc"
)

// PrintEdits writes one file's changes as the lines that went and the lines
// that came, each numbered where it sits in its own version of the file: "-"
// before a line the edit removed, "+" before one it added, and "=" before one
// it left as it found it, which is how a checkbox already in the asked-for
// state is reported. wrote says whether the file on disk was changed, which
// only --write does; the lines are the same either way.
func (p *Printer) PrintEdits(src *mdoc.Source, changes []edit.Change, wrote bool) {
	if len(changes) == 0 {
		return
	}
	switch p.Format {
	case JSON:
		p.printEditJSON(changes, wrote)
		return
	case Compact:
		p.printEditCompact(src, changes, wrote)
		return
	}
	p.beginFile(src.Path)

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
	p.W.WriteString(p.paint(color, mark))
	p.W.WriteByte(' ')
	p.writeLine(path, num, line, ":")
}

// printEditCompact reports a change the way printCompact reports a result:
// the path once, then one record per change.
//
//	start[-end] <TAB> op <TAB> applied|preview|unchanged <TAB> new text
//
// The old text is left out — the caller either has the file or is being shown
// the edit against it — and "new" is empty for a deletion.
func (p *Printer) printEditCompact(src *mdoc.Source, changes []edit.Change, wrote bool) {
	p.wroteAny = true
	fmt.Fprintln(p.W, Escape(src.Path))
	for _, c := range changes {
		fmt.Fprintf(p.W, "%s\t%s\t%s\t%s\n",
			lineSpan(c.Start, c.End), c.Op,
			editStatus(c.NoOp, wrote), Escape(strings.Join(c.New, "\n")))
	}
}

func editStatus(noop, wrote bool) string {
	switch {
	case noop:
		return "unchanged"
	case !wrote:
		return "preview"
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

func (p *Printer) printEditJSON(changes []edit.Change, wrote bool) {
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
			Applied:    wrote && !c.NoOp,
			Breadcrumb: c.Breadcrumb,
		})
	}
}

func joinCrumb(crumb []string) string { return strings.Join(crumb, " › ") }

// diffContext is how many unchanged lines a hunk carries either side of a
// change: three, which is what diff -u has printed since it was written and
// what patch and git apply expect when nothing says otherwise.
const diffContext = 3

// PrintDiff writes one file's changes as a unified diff, the format patch and
// git apply read. No lines are matched up here: edit.Plan already knows the
// exact lines that go and the exact lines that come, so the patch says what
// the edit planned rather than what a line-matching algorithm would guess
// about it. A replacement that happens to share words with what it replaces
// is one block out and one block in, not the two spliced together.
func (p *Printer) PrintDiff(src *mdoc.Source, changes []edit.Change) {
	// A node already as asked contributes no line to a patch, and dropping
	// those first is what lets a file whose changes are all no-ops print
	// nothing rather than a header with no hunk under it.
	real := make([]edit.Change, 0, len(changes))
	for _, c := range changes {
		if !c.NoOp {
			real = append(real, c)
		}
	}
	if len(real) == 0 {
		return
	}
	old, new := patchNames(src.Path)
	fmt.Fprintf(p.W, "--- %s\n+++ %s\n", old, new)

	// Two changes closer than twice the context share a hunk: the lines
	// between them are context for both, and printed once rather than twice.
	offset := 0
	for i := 0; i < len(real); {
		j := i + 1
		for j < len(real) && real[j].Start-oldEnd(real[j-1]) <= 2*diffContext {
			j++
		}
		offset = p.hunk(src, real[i:j], offset)
		i = j
	}
}

// patchNames writes the two sides of a patch header. A relative path takes
// git's "a/" and "b/", so "git apply" reads the patch as it stands; an
// absolute path takes neither, since prefixing one would make a name no
// strip level can turn back into the file it came from. Both spellings are
// read by patch, which strips nothing unless told to.
func patchNames(path string) (string, string) {
	if filepath.IsAbs(path) {
		return path, path
	}
	return "a/" + path, "b/" + path
}

// oldEnd is the line after the last one a change removes. A change that
// removes nothing is an insertion and covers no line of the old file at all,
// which is why the span comes from len(Old) rather than from Change.End.
func oldEnd(c edit.Change) int { return c.Start + len(c.Old) }

// hunk writes one run of changes with the context around it, numbered on both
// sides, and returns the offset the next hunk's new side is shifted by.
func (p *Printer) hunk(src *mdoc.Source, changes []edit.Change, offset int) int {
	lo := max(changes[0].Start-diffContext, 0)
	hi := min(oldEnd(changes[len(changes)-1])+diffContext, src.NumLines())

	oldCount := hi - lo
	newCount := oldCount
	for _, c := range changes {
		newCount += len(c.New) - len(c.Old)
	}
	fmt.Fprintf(p.W, "@@ -%s +%s @@\n", hunkSpan(lo+1, oldCount), hunkSpan(lo+1+offset, newCount))

	// A file that does not end in a newline says so after the last line of
	// whichever side owns it, which is how diff -u marks it and how patch
	// puts it back.
	noEOL := !strings.HasSuffix(src.Text(), "\n")
	last := hi == src.NumLines()

	cur := lo
	for n, c := range changes {
		for ; cur < c.Start; cur++ {
			p.diffLine(" ", src.Line(cur), noEOL && last && cur == hi-1)
		}
		ends := last && n == len(changes)-1 && oldEnd(c) == hi
		for i, line := range c.Old {
			p.diffLine("-", line, noEOL && ends && i == len(c.Old)-1)
		}
		for i, line := range c.New {
			p.diffLine("+", line, noEOL && ends && i == len(c.New)-1)
		}
		cur = oldEnd(c)
	}
	for ; cur < hi; cur++ {
		p.diffLine(" ", src.Line(cur), noEOL && last && cur == hi-1)
	}
	return offset + newCount - oldCount
}

// diffLine writes one line of a hunk behind its marker, and the note that the
// file stops there without a newline.
func (p *Printer) diffLine(mark, line string, noEOL bool) {
	p.W.WriteString(mark)
	p.W.WriteString(line)
	p.W.WriteByte('\n')
	if noEOL {
		p.W.WriteString("\\ No newline at end of file\n")
	}
}

// hunkSpan writes one side of a hunk header. A side with no lines at all sits
// after the line before it, which is what "-0,0" on a new file means.
func hunkSpan(start, count int) string {
	if count == 0 {
		return fmt.Sprintf("%d,0", start-1)
	}
	return fmt.Sprintf("%d,%d", start, count)
}

// PrintDoc writes a document the way an edit left it. There is nothing to lay
// out: the answer is the file, so it goes out as it would have been written.
func (p *Printer) PrintDoc(text string) { p.W.WriteString(text) }

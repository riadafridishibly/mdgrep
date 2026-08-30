// Package search turns a matcher plus expansion options into line ranges.
package search

import (
	"sort"
	"strings"

	"mdgrep/internal/match"
	"mdgrep/internal/mdoc"
)

// TaskFilter restricts results to GFM task-list items by checkbox state.
type TaskFilter int

const (
	TaskIgnore    TaskFilter = iota // no restriction
	TaskAny                         // any checkbox item
	TaskChecked                     // "- [x]" only
	TaskUnchecked                   // "- [ ]" only
)

func (f TaskFilter) accepts(b *mdoc.Block) bool {
	switch f {
	case TaskChecked:
		return b.Checked
	case TaskUnchecked:
		return !b.Checked
	}
	return true
}

// Options controls which blocks qualify and how far a hit is widened.
type Options struct {
	Kinds   map[mdoc.Kind]bool // nil means every kind
	Task    TaskFilter         // checkbox state a hit must sit in
	Before  int                // sibling blocks before the hit
	After   int                // sibling blocks after the hit
	Lines   int                // raw lines padded on both sides
	Expand  int                // ancestor levels to climb from the hit
	Section bool               // widen to the enclosing heading section
	Body    bool               // that section without its heading line
	Anchor  *Anchor            // when set, headings are selected by link anchor
	Rank    bool               // order by score rather than by position
	Max     int                // cap on results per file, 0 for unlimited
	// Distinct keeps results that merely touch apart. Printing runs two
	// neighbouring hits together so the page reads as one passage; an edit
	// wants them separate, because each is a node it could be asked to
	// rewrite on its own.
	Distinct bool
}

// Result is one region of a file to print.
type Result struct {
	Path       string
	Kind       mdoc.Kind
	Score      float64
	Start, End int // inclusive, zero-based lines, after expansion
	HitStart   int // first line of the matched block itself
	HitEnd     int // last line of the matched block itself
	Task       bool
	Checked    bool
	Breadcrumb []string
}

// File searches one parsed document.
func File(doc *mdoc.Doc, m match.Matcher, opt Options) []Result {
	hits := candidates(doc, m, opt)
	if len(hits) == 0 {
		return nil
	}

	var out []Result
	for _, h := range hits {
		sel, ok := promote(h.block, opt)
		if !ok {
			continue
		}
		start, end := sel.Start, sel.End
		start, end = withSiblings(sel, start, end, opt.Before, opt.After)
		switch {
		case opt.Body:
			// A body stands on its own rather than widening the hit: asking
			// for a section without its heading cannot pull the heading back
			// in through the block that matched it.
			if s, e, ok := doc.SectionBody(sel.Start); ok {
				start, end = trimBlankEnds(doc.Src, s, e)
			}
		case opt.Section:
			if s, e, ok := doc.Section(sel.Start); ok {
				start, end = min(start, s), max(end, e)
			}
		}
		if end >= start {
			start, end = clamp(start-opt.Lines, end+opt.Lines, doc.Src.NumLines())
			start, end = trimBlank(doc.Src, start, end, sel.Start, sel.End)
		}
		out = append(out, Result{
			Path:       doc.Src.Path,
			Kind:       sel.Kind,
			Score:      h.score,
			Start:      start,
			End:        end,
			HitStart:   sel.Start,
			HitEnd:     sel.End,
			Task:       sel.Task,
			Checked:    sel.Checked,
			Breadcrumb: doc.Breadcrumb(sel.Start),
		})
	}
	if len(out) == 0 {
		return nil
	}

	out = mergeOverlapping(out, opt.Distinct)
	if opt.Rank {
		// Rank before the cap, so -m keeps the best results rather than the
		// first ones the file happens to hold.
		sort.SliceStable(out, func(i, j int) bool { return out[i].Score > out[j].Score })
	}
	if opt.Max > 0 && len(out) > opt.Max {
		out = out[:opt.Max]
	}
	return out
}

type hit struct {
	block *mdoc.Block
	score float64
}

// candidates picks the blocks a search selects. An anchor names its heading
// outright, so it stands in for the matcher rather than narrowing it.
func candidates(doc *mdoc.Doc, m match.Matcher, opt Options) []hit {
	if opt.Anchor != nil {
		return anchorHits(doc, opt.Anchor, opt)
	}
	return matchHits(doc, m, opt)
}

// anchorHits selects the headings a link points at. Only a heading is given an
// anchor, so nothing else is considered, and every hit is equally exact.
func anchorHits(doc *mdoc.Doc, a *Anchor, opt Options) []hit {
	if (opt.Kinds != nil && !opt.Kinds[mdoc.KindHeading]) || !a.wantsFile(doc.Src.Path) {
		return nil
	}
	anchors := doc.HeadingAnchors(a.styles)
	var out []hit
	for i, h := range doc.Headings {
		if h.Located && a.matches(doc.Src.Path, anchors[i]) {
			out = append(out, hit{h, 1})
		}
	}
	return out
}

// matchHits scores every eligible block, then keeps only the tightest ones:
// a block whose own descendant also matched is dropped, so a hit inside a
// nested bullet reports that bullet and not the whole list.
func matchHits(doc *mdoc.Doc, m match.Matcher, opt Options) []hit {
	matched := map[*mdoc.Block]float64{}
	for _, b := range doc.Blocks {
		if !b.Located {
			continue
		}
		if opt.Kinds != nil && !opt.Kinds[b.Kind] {
			continue
		}
		// Blocks are matched against the markdown as written, so anchors,
		// list markers, table pipes and emphasis are all searchable, and a
		// highlight found in a printed line is the same hit that was scored.
		raw := doc.Src.Slice(b.Start, b.End)
		if strings.TrimSpace(raw) == "" {
			continue
		}
		if s, ok := m.Score(raw); ok {
			matched[b] = s
		}
	}

	// A block is dropped when a descendant of it also matched. Asking that of
	// each block directly would compare every match against every other; walk
	// up from each match instead and mark what it hangs under, which stops as
	// soon as it reaches an ancestor an earlier match already marked.
	outer := make(map[*mdoc.Block]bool, len(matched))
	for b := range matched {
		for p := b.Parent; p != nil && !outer[p]; p = p.Parent {
			outer[p] = true
		}
	}

	var out []hit
	for _, b := range doc.Blocks {
		if s, ok := matched[b]; ok && !outer[b] {
			out = append(out, hit{b, s})
		}
	}
	return out
}

// promote lifts a hit to the node a reader thinks of as "the match": text
// inside a list item becomes the whole item, a task filter climbs on to the
// checkbox item owning the hit, then extra levels climb the tree. ok is false
// when the task filter rejects the hit.
func promote(b *mdoc.Block, opt Options) (*mdoc.Block, bool) {
	if (b.Kind == mdoc.KindParagraph || b.Kind == mdoc.KindTextBlock) &&
		b.Parent != nil && b.Parent.Kind == mdoc.KindItem {
		b = b.Parent
	}
	if opt.Task != TaskIgnore {
		t := nearestTask(b)
		if t == nil || !opt.Task.accepts(t) {
			return nil, false
		}
		b = t
	}
	for range opt.Expand {
		if b.Parent == nil || b.Parent.Kind == mdoc.KindDocument {
			break
		}
		b = b.Parent
	}
	return b, true
}

// nearestTask returns b or its closest ancestor that is a checkbox item, so a
// hit in a plain sub-bullet still reports the task it hangs under.
func nearestTask(b *mdoc.Block) *mdoc.Block {
	for ; b != nil; b = b.Parent {
		if b.Task {
			return b
		}
	}
	return nil
}

func withSiblings(b *mdoc.Block, start, end, before, after int) (int, int) {
	if b.Parent == nil || (before == 0 && after == 0) {
		return start, end
	}
	sibs := b.Parent.Children
	idx := -1
	for i, s := range sibs {
		if s == b {
			idx = i
			break
		}
	}
	if idx < 0 {
		return start, end
	}
	for i, n := idx-1, 0; i >= 0 && n < before; i-- {
		if !sibs[i].Located {
			continue
		}
		start = min(start, sibs[i].Start)
		n++
	}
	for i, n := idx+1, 0; i < len(sibs) && n < after; i++ {
		if !sibs[i].Located {
			continue
		}
		end = max(end, sibs[i].End)
		n++
	}
	return start, end
}

// trimBlank drops blank padding lines that expansion pulled in, without ever
// cutting into the matched block itself.
func trimBlank(src *mdoc.Source, start, end, keepStart, keepEnd int) (int, int) {
	for start < keepStart && strings.TrimSpace(src.Line(start)) == "" {
		start++
	}
	for end > keepEnd && strings.TrimSpace(src.Line(end)) == "" {
		end--
	}
	return start, end
}

// trimBlankEnds drops blank lines from both ends of a range with nothing to
// protect. A range that is blank throughout collapses to an empty one.
func trimBlankEnds(src *mdoc.Source, start, end int) (int, int) {
	for start <= end && strings.TrimSpace(src.Line(start)) == "" {
		start++
	}
	for end >= start && strings.TrimSpace(src.Line(end)) == "" {
		end--
	}
	return start, end
}

func clamp(start, end, numLines int) (int, int) {
	if start < 0 {
		start = 0
	}
	if end > numLines-1 {
		end = numLines - 1
	}
	if end < start {
		end = start
	}
	return start, end
}

func mergeOverlapping(rs []Result, distinct bool) []Result {
	// Results that touch are one region unless each has to stand alone.
	gap := 1
	if distinct {
		gap = 0
	}
	sort.SliceStable(rs, func(i, j int) bool {
		if rs[i].Start != rs[j].Start {
			return rs[i].Start < rs[j].Start
		}
		return rs[i].End > rs[j].End
	})
	out := rs[:1]
	for _, r := range rs[1:] {
		last := &out[len(out)-1]
		if r.Start > last.End+gap {
			out = append(out, r)
			continue
		}
		if r.End > last.End {
			last.End = r.End
		}
		if r.Score > last.Score {
			last.Score = r.Score
			last.Kind = r.Kind
			last.Task, last.Checked = r.Task, r.Checked
		}
		if r.HitEnd > last.HitEnd {
			last.HitEnd = r.HitEnd
		}
	}
	return out
}

// Package search turns a matcher plus expansion options into line ranges.
package search

import (
	"sort"
	"strings"

	"mdgrep/internal/match"
	"mdgrep/internal/mdoc"
)

// Options controls which blocks qualify and how far a hit is widened.
type Options struct {
	Kinds   map[mdoc.Kind]bool // nil means every kind
	Before  int                // sibling blocks before the hit
	After   int                // sibling blocks after the hit
	Lines   int                // raw lines padded on both sides
	Expand  int                // ancestor levels to climb from the hit
	Section bool               // widen to the enclosing heading section
	Max     int                // cap on results per file, 0 for unlimited
}

// Result is one region of a file to print.
type Result struct {
	Path       string
	Kind       mdoc.Kind
	Score      float64
	Start, End int // inclusive, zero-based lines, after expansion
	HitStart   int // first line of the matched block itself
	HitEnd     int // last line of the matched block itself
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
		sel := promote(h.block, opt.Expand)
		start, end := sel.Start, sel.End
		start, end = withSiblings(sel, start, end, opt.Before, opt.After)
		if opt.Section {
			if s, e, ok := doc.Section(sel.Start); ok {
				start, end = min(start, s), max(end, e)
			}
		}
		start, end = clamp(start-opt.Lines, end+opt.Lines, doc.Src.NumLines())
		start, end = trimBlank(doc.Src, start, end, sel.Start, sel.End)
		out = append(out, Result{
			Path:       doc.Src.Path,
			Kind:       sel.Kind,
			Score:      h.score,
			Start:      start,
			End:        end,
			HitStart:   sel.Start,
			HitEnd:     sel.End,
			Breadcrumb: doc.Breadcrumb(sel.Start),
		})
	}

	out = mergeOverlapping(out)
	if opt.Max > 0 && len(out) > opt.Max {
		out = out[:opt.Max]
	}
	return out
}

type hit struct {
	block *mdoc.Block
	score float64
}

// candidates scores every eligible block, then keeps only the tightest ones:
// a block whose own descendant also matched is dropped, so a hit inside a
// nested bullet reports that bullet and not the whole list.
func candidates(doc *mdoc.Doc, m match.Matcher, opt Options) []hit {
	matched := map[*mdoc.Block]float64{}
	for _, b := range doc.Blocks {
		if !b.Located || b.Text == "" {
			continue
		}
		if opt.Kinds != nil && !opt.Kinds[b.Kind] {
			continue
		}
		if s, ok := m.Score(b.Text); ok {
			matched[b] = s
		}
	}

	var out []hit
	for _, b := range doc.Blocks {
		s, ok := matched[b]
		if !ok {
			continue
		}
		nested := false
		for other := range matched {
			if other != b && b.Contains(other) {
				nested = true
				break
			}
		}
		if !nested {
			out = append(out, hit{b, s})
		}
	}
	return out
}

// promote lifts a hit to the node a reader thinks of as "the match": text
// inside a list item becomes the whole item, then extra levels climb the tree.
func promote(b *mdoc.Block, levels int) *mdoc.Block {
	if (b.Kind == mdoc.KindParagraph || b.Kind == mdoc.KindTextBlock) &&
		b.Parent != nil && b.Parent.Kind == mdoc.KindItem {
		b = b.Parent
	}
	for range levels {
		if b.Parent == nil || b.Parent.Kind == mdoc.KindDocument {
			break
		}
		b = b.Parent
	}
	return b
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

func mergeOverlapping(rs []Result) []Result {
	sort.SliceStable(rs, func(i, j int) bool {
		if rs[i].Start != rs[j].Start {
			return rs[i].Start < rs[j].Start
		}
		return rs[i].End > rs[j].End
	})
	out := rs[:1]
	for _, r := range rs[1:] {
		last := &out[len(out)-1]
		if r.Start > last.End+1 {
			out = append(out, r)
			continue
		}
		if r.End > last.End {
			last.End = r.End
		}
		if r.Score > last.Score {
			last.Score = r.Score
			last.Kind = r.Kind
		}
		if r.HitEnd > last.HitEnd {
			last.HitEnd = r.HitEnd
		}
	}
	return out
}

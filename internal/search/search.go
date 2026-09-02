// Package search turns a matcher plus expansion options into line ranges.
package search

import (
	"fmt"
	"sort"
	"strings"

	"github.com/riadafridishibly/mdgrep/internal/match"
	"github.com/riadafridishibly/mdgrep/internal/mdoc"
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
	Kinds    map[mdoc.Kind]bool // nil means every kind
	Task     TaskFilter         // checkbox state a hit must sit in
	Siblings int                // sibling blocks kept on each side of the hit
	Expand   int                // rungs of the expand ladder to climb
	// ExpandSet is whether --expand was given at all. Bare --expand is the
	// node that matched, which is the same region a search that did not ask
	// reports -- except under an address, where the node is the address and
	// the first rung is the block containing it.
	ExpandSet bool
	Section   bool    // widen to the enclosing heading section
	Body      bool    // that section without its heading line
	Anchor    *Anchor // when set, headings are selected by link anchor
	Rank      bool    // order by score rather than by position
	Max       int     // cap on results per file, 0 for unlimited
	// Distinct keeps results that merely touch apart. Printing runs two
	// neighbouring hits together so the page reads as one passage; an edit
	// wants them separate, because each is a node it could be asked to
	// rewrite on its own.
	Distinct bool
	// Scope restricts the search to these line ranges, as an earlier stage of
	// a pipeline selected them: a block lying outside every one of them is not
	// a candidate. Nil searches the whole file.
	Scope []Region
	// At names lines outright. When set these regions are the results and no
	// matcher runs: the address is the selection, the way an anchor is.
	At []Region
	// Hits is whether the caller reads which lines of a result matched.
	// Working them out runs the matcher a second time over every line of the
	// selected rung, which is the most expensive thing a search does per
	// result -- and a tally, a list of file names, a yes-or-no answer, an
	// outline and a stream of regions read none of them. A result whose hits
	// were not worked out claims its node whole, which is what a matcher with
	// no line to name reports anyway.
	Hits bool
}

// Region is a range of lines a search may look at: zero-based and inclusive,
// the way a Result reports one. It is what one stage of a pipeline hands the
// next, in place of the text that stage printed.
type Region struct{ Start, End int }

// inScope reports whether a region lies inside the ones an earlier stage
// selected. A region straddling a boundary is out: narrowing hands on the
// nodes the last stage selected, not the ones it would have cut in half.
func inScope(start, end int, scope []Region) bool {
	if scope == nil {
		return true
	}
	for _, r := range scope {
		if r.Start <= start && end <= r.End {
			return true
		}
	}
	return false
}

// Rung is one step of the expand ladder: a region the result could be widened
// to, named by what draws it. There is no count on a Rung because position is
// the count -- the first is bare --expand, the second --expand 1, and the last
// is what --section selects.
type Rung struct {
	Kind       mdoc.Kind // KindSection on the last rung
	Start, End int       // inclusive, zero-based
}

// Result is one region of a file to print.
type Result struct {
	Path       string
	Kind       mdoc.Kind
	Level      int // heading level, else 0
	Score      float64
	Start, End int // inclusive, zero-based lines, after expansion
	HitStart   int // first line of the matched block itself
	HitEnd     int // last line of the matched block itself
	// Hits are the lines of the node the matcher pointed at, zero-based and
	// ascending. Empty means every line of HitStart..HitEnd: a matcher that
	// cannot name a line -- -v, or the empty pattern behind a filter --
	// claims the node whole, since no line in it is more the answer than
	// another.
	Hits []int
	// Rungs is the expand ladder, the matched node first and the enclosing
	// section last. It is always the whole ladder; leaving out the rungs a
	// page already covered is the printer's business, because position is
	// what carries the --expand count.
	Rungs      []Rung
	Task       bool
	Checked    bool
	Breadcrumb []string
}

// MatchLines is the lines a result claims, with the empty Hits of a node
// matcher spelled out as the lines it stands for.
func (r Result) MatchLines() []int {
	if len(r.Hits) > 0 {
		return r.Hits
	}
	out := make([]int, 0, r.HitEnd-r.HitStart+1)
	for n := r.HitStart; n <= r.HitEnd; n++ {
		out = append(out, n)
	}
	return out
}

// rung is a Rung with the block it was drawn from still attached: the block
// itself on a block rung, the heading on a section rung, and nil where the
// lines are no node at all.
type rung struct {
	kind       mdoc.Kind
	start, end int
	block      *mdoc.Block
}

// File searches one parsed document.
func File(doc *mdoc.Doc, m match.Matcher, opt Options) []Result {
	if len(opt.At) > 0 {
		return atResults(doc, opt)
	}
	hits := candidates(doc, m, opt)
	if len(hits) == 0 {
		return nil
	}

	var out []Result
	for _, h := range hits {
		base, ok := promote(h.block, opt)
		if !ok {
			continue
		}
		lad := ladder(doc, base, base.Start)
		sel := lad[min(opt.Expand, len(lad)-1)]
		// The node a stage selects is the node it hands on, and an edit at the
		// end of a pipeline rewrites. So containment is asked of the lift as
		// well as of the block that matched: --todo climbing to the task a
		// sub-bullet hangs under, or --expand climbing the tree, must not
		// reach past the region the stage was given.
		if !inScope(sel.start, sel.end, opt.Scope) {
			continue
		}
		out = append(out, result(doc, m, opt, sel, lad, h.score))
	}
	if len(out) == 0 {
		return nil
	}

	out = mergeOverlapping(doc, out, opt.Distinct)
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

// result turns one selected rung into the region a caller prints or rewrites:
// the rung's own lines, whatever --siblings and --section widen them by, the
// lines the matcher pointed at inside them, and the ladder they sit on.
func result(doc *mdoc.Doc, m match.Matcher, opt Options, sel rung, lad []rung, score float64) Result {
	start, end := sel.start, sel.end
	if sel.block != nil && sel.kind == sel.block.Kind {
		start, end = withSiblings(sel.block, start, end, opt.Siblings, opt.Siblings)
	}
	switch {
	case opt.Body:
		// A body stands on its own rather than widening the hit: asking
		// for a section without its heading cannot pull the heading back
		// in through the block that matched it.
		if s, e, ok := doc.SectionBody(sel.start); ok {
			start, end = trimBlankEnds(doc.Src, s, e)
		}
	case opt.Section:
		if s, e, ok := doc.Section(sel.start); ok {
			start, end = min(start, s), max(end, e)
		}
	}
	if end >= start {
		start, end = clamp(start, end, doc.Src.NumLines())
		start, end = trimBlank(doc.Src, start, end, sel.start, sel.end)
	}
	r := Result{
		Path:       doc.Src.Path,
		Kind:       sel.kind,
		Score:      score,
		Start:      start,
		End:        end,
		HitStart:   sel.start,
		HitEnd:     sel.end,
		Hits:       matchLines(doc, m, opt, sel, start, end),
		Rungs:      note(lad),
		Breadcrumb: trail(doc, sel, start, end),
	}
	if sel.block != nil {
		r.Level = sel.block.Level
		if sel.kind == sel.block.Kind {
			r.Task, r.Checked = sel.block.Task, sel.block.Checked
		}
	}
	return r
}

// matchLines is which lines of a node the matcher pointed at. A matcher that
// reports spans names them; one that cannot -- an inverted match, or the empty
// pattern a filter searches with -- names none, and the node is claimed whole.
// An anchor consults no matcher at all: it selected the heading outright, so
// its match line is the heading's own first line.
//
// The scan is the node clipped to start..end, the region the result reports,
// so a hit is always a line of what was reported. The two come apart under
// --section-body, where the node is the heading and the region is the body
// below it: none of the heading's lines are the body's, so the body is claimed
// whole -- which is what a page of it prints and what an empty hits array in a
// machine format already means.
func matchLines(doc *mdoc.Doc, m match.Matcher, opt Options, sel rung, start, end int) []int {
	if !opt.Hits {
		return nil
	}
	if opt.Anchor != nil {
		if sel.start < start || sel.start > end {
			return nil
		}
		return []int{sel.start}
	}
	if m == nil {
		return nil
	}
	var out []int
	for n := max(sel.start, start); n <= min(sel.end, end) && n < doc.Src.NumLines(); n++ {
		if len(m.Spans(doc.Src.Line(n))) > 0 {
			out = append(out, n)
		}
	}
	return out
}

// ladder is every region a hit could be widened to, in the order --expand
// climbs them: the node itself, its block ancestors, and then the sections
// enclosing it, innermost first.
//
// CommonMark's syntax tree is not the tree a document reads as. Headings parse
// as flat siblings of the document, so climbing block parents never reaches
// one; the ladder carries on up the heading hierarchy where the parents run
// out, which is what makes --expand reach a section at all.
func ladder(doc *mdoc.Doc, b *mdoc.Block, at int) []rung {
	// A bullet's text is a block of its own, spanning exactly the lines the
	// item does. It is what a matcher scores and what encloses an address, but
	// it is never what a reader means by the node, so the ladder starts where
	// promote would have put it.
	if insideItem(b) {
		b = b.Parent
	}
	var out []rung
	for n := b; n != nil && n.Kind != mdoc.KindDocument; n = n.Parent {
		out = append(out, rung{kind: n.Kind, start: n.Start, end: n.End, block: n})
	}
	stack := doc.HeadingStack(at)
	for i := len(stack) - 1; i >= 0; i-- {
		h := stack[i]
		if s, e, ok := doc.Section(h.Start); ok {
			// A section runs to the line before the next heading, which is
			// usually the blank line parting the two. The rung is the section
			// as a reader sees it, and as --section prints it.
			s, e = trimBlankEnds(doc.Src, s, e)
			out = append(out, rung{kind: mdoc.KindSection, start: s, end: e, block: h})
		}
	}
	if len(out) == 0 {
		out = append(out, rung{kind: b.Kind, start: b.Start, end: b.End, block: b})
	}
	return out
}

// note is the part of the ladder a result reports: every rung up to and
// including the first section. Past that you are reading the file rather than
// widening a result, and nobody chooses between a section and the document.
func note(lad []rung) []Rung {
	out := make([]Rung, 0, len(lad))
	for _, r := range lad {
		out = append(out, Rung{Kind: r.kind, Start: r.start, End: r.end})
		if r.kind == mdoc.KindSection {
			break
		}
	}
	return out
}

// atResults answers an address. No matcher runs: the lines were named, so
// every one of them is a match line and the region prints whole. The ladder is
// built from the smallest block containing the address, the rule a merged
// result already follows, so the note reads the same whether the address named
// a node or a run of lines.
func atResults(doc *mdoc.Doc, opt Options) []Result {
	var out []Result
	for _, at := range opt.At {
		if at.End < at.Start || at.Start < 0 || at.End >= doc.Src.NumLines() {
			continue
		}
		base := doc.Enclosing(at.Start, at.End)
		lad := ladder(doc, base, at.Start)
		sel := rung{kind: mdoc.KindRegion, start: at.Start, end: at.End}
		// A node the address names exactly is reported as that node; lines no
		// block draws are a region, the one kind no parse produces.
		if first := lad[0]; first.start == at.Start && first.end == at.End {
			sel.kind, sel.block = first.kind, first.block
		}
		if opt.ExpandSet {
			sel = lad[min(opt.Expand, len(lad)-1)]
		}
		if !inScope(sel.start, sel.end, opt.Scope) {
			continue
		}
		out = append(out, result(doc, nil, opt, sel, lad, 1))
	}
	if len(out) == 0 {
		return nil
	}
	out = mergeOverlapping(doc, out, opt.Distinct)
	if opt.Max > 0 && len(out) > opt.Max {
		out = out[:opt.Max]
	}
	return out
}

// AddressHolds holds an address to the file it names: that every line it asks
// for is there, and that a guard pattern beside it still finds something inside
// those lines. A start after the end or an end past the last line refuses the
// run rather than being clipped to it -- context lines clip because they pad
// something that was found, but an address is the thing being found, and one
// that does not fit the file is a stale note or a typo.
//
// An address is written by one run and read back by another, which is the one
// failure a line number has that a pattern does not: the file may have moved
// under it. m is the guard, and nil where there is none.
//
// at and pattern are how the caller spells the two, so one rule can answer in
// the words of the command line or of a plan entry.
func AddressHolds(doc *mdoc.Doc, m match.Matcher, opt Options, at, pattern string) error {
	if len(opt.At) == 0 {
		return nil
	}
	n := doc.Src.NumLines()
	for _, r := range opt.At {
		if r.End >= n {
			return fmt.Errorf("%s %d-%d: %s has %d lines", at, r.Start+1, r.End+1, doc.Src.Path, n)
		}
	}
	if m == nil {
		return nil
	}
	// The guard is an ordinary search run inside the address, and only its
	// answer matters: the address still says which lines to take, so nothing
	// that would widen the search or move it off those lines is carried in.
	inside := opt
	inside.At, inside.Scope = nil, opt.At
	inside.Expand, inside.ExpandSet = 0, false
	inside.Section, inside.Body, inside.Siblings = false, false, 0
	if len(File(doc, m, inside)) == 0 {
		return fmt.Errorf("%s: %s is not in the lines %s named", doc.Src.Path, pattern, at)
	}
	return nil
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
		if h.Located && inScope(h.Start, h.End, opt.Scope) && a.matches(doc.Src.Path, anchors[i]) {
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
		if !inScope(b.Start, b.End, opt.Scope) {
			continue
		}
		if !searchable(b, opt) {
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

// searchable reports whether a block is one the kind filter admits. A bullet's
// text lives in a child block, so those children have to be scored for a
// search for items to find anything -- but they are admitted on their parent's
// account, and promote is what decides whether they earned it.
func searchable(b *mdoc.Block, opt Options) bool {
	if opt.Kinds == nil || opt.Kinds[b.Kind] {
		return true
	}
	return opt.Kinds[mdoc.KindItem] && insideItem(b)
}

// insideItem reports whether b is the text of a list item rather than a block
// standing on its own.
func insideItem(b *mdoc.Block) bool {
	return (b.Kind == mdoc.KindParagraph || b.Kind == mdoc.KindTextBlock) &&
		b.Parent != nil && b.Parent.Kind == mdoc.KindItem
}

// promote lifts a hit to the node a reader thinks of as "the match": text
// inside a list item becomes the whole item, and a task filter climbs on to
// the checkbox item owning the hit. ok is false when the task filter rejects
// the hit, or when the lift did not reach a kind the caller asked for. What
// --expand climbs from there is the ladder's business.
func promote(b *mdoc.Block, opt Options) (*mdoc.Block, bool) {
	if insideItem(b) {
		b = b.Parent
	}
	// --kind says what matches, not what a match is widened to, so the filter
	// sees the lifted block and the ladder climbs past it unfiltered.
	if opt.Kinds != nil && !opt.Kinds[b.Kind] {
		return nil, false
	}
	if opt.Task != TaskIgnore {
		t := nearestTask(b)
		if t == nil || !opt.Task.accepts(t) {
			return nil, false
		}
		b = t
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

// trail is the heading trail that names a result. A region holding the
// heading's own line already says what the trail's last element says, so the
// trail stops at the parent; a --section-body region leaves that line out, and
// there the whole trail is what names the hit.
func trail(doc *mdoc.Doc, sel rung, start, end int) []string {
	crumb := doc.Breadcrumb(sel.start)
	named := sel.kind == mdoc.KindHeading || sel.kind == mdoc.KindSection
	holdsHeading := named && sel.block != nil &&
		start <= sel.block.Start && sel.block.Start <= end
	if holdsHeading && len(crumb) > 0 {
		return crumb[:len(crumb)-1]
	}
	return crumb
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

func mergeOverlapping(doc *mdoc.Doc, rs []Result, distinct bool) []Result {
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
	joined := map[int]bool{}
	for _, r := range rs[1:] {
		last := &out[len(out)-1]
		if r.Start > last.End+gap {
			out = append(out, r)
			continue
		}
		if r.End > last.End {
			last.End = r.End
		}
		// A merged region is ranked by the best thing in it, but it is still
		// described by the node it begins on. Taking Kind from one hit while
		// HitStart, Level and Breadcrumb stayed with another left the region
		// saying it was a heading whose trail and first line belonged to a
		// paragraph somewhere else -- and the trail then had a real ancestor
		// stripped from it as though it were the heading's own name.
		last.Score = max(last.Score, r.Score)
		// Two nodes run together are two sets of match lines, not one node
		// spanning both: taking HitEnd from the second while keeping the
		// first's Hits would claim every line between them. Only where both
		// claimed their nodes whole does the pair stay unnamed, and there the
		// widened HitStart..HitEnd is exactly what it claims.
		if len(last.Hits) > 0 || len(r.Hits) > 0 {
			last.Hits = append(last.MatchLines(), r.MatchLines()...)
		}
		if r.HitEnd > last.HitEnd {
			last.HitEnd = r.HitEnd
		}
		joined[len(out)-1] = true
	}
	// No single node draws a merged region, so its ladder starts at the
	// smallest block that holds all of it.
	for i := range out {
		if !joined[i] {
			continue
		}
		out[i].Hits = tidy(out[i].Hits)
		r := &out[i]
		r.Rungs = note(ladder(doc, doc.Enclosing(r.HitStart, r.HitEnd), r.HitStart))
	}
	return out
}

// tidy puts a concatenated set of match lines back in order, with the lines
// two overlapping results both claimed said once.
func tidy(hits []int) []int {
	if len(hits) < 2 {
		return hits
	}
	sort.Ints(hits)
	out := hits[:1]
	for _, n := range hits[1:] {
		if n != out[len(out)-1] {
			out = append(out, n)
		}
	}
	return out
}

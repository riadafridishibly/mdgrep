package mdoc

import (
	"strings"
	"testing"
)

// seeds are markdown fragments worth starting from: one per construct the
// parser has to widen, mask or nest, so the fuzzer begins with coverage of the
// syntax rather than having to rediscover it.
var seeds = []string{
	"# a\n\n- [ ] b\n- [x] c\n",
	"---\ntitle: x\ntags: [a]\n---\n# h\n\nbody\n",
	"+++\nk = 1\n+++\n## h\n",
	"| a | b |\n|---|---|\n| 1 | 2 |\n",
	"a\n=\n\nb\n-\n\n> quote\n> more\n",
	"```go\nfunc x() {}\n```\n\n~~~\nplain\n~~~\n",
	"    indented code\n\n<div>\nhtml\n</div>\n",
	"- a\n  - b\n    - c\n      - d\n",
	"# `code` and [link](/url) and ![img](/i.png)\n",
	"#### h4 ####\n\n## dup\n\n## dup\n\n## dup\n",
	"text with `a  b` span and <em>raw</em>\n",
	"---\nunclosed frontmatter\n",
	"\n\n\n",
	"no trailing newline",
	"\r\n# crlf\r\n\r\n- [ ] item\r\n",
}

func addSeeds(f *testing.F, extra ...any) {
	for _, s := range seeds {
		f.Add(append([]any{s}, extra...)...)
	}
}

// FuzzParse holds the parser to the promise its doc comment makes: every block
// is an inclusive line range inside the file, so a block can always be sliced
// back out of the source it came from.
func FuzzParse(f *testing.F) {
	addSeeds(f)
	f.Fuzz(func(t *testing.T, src string) {
		if len(src) > 1<<16 {
			return
		}
		d := Parse("f.md", []byte(src))
		n := d.Src.NumLines()
		if n < 1 {
			t.Fatalf("NumLines = %d, want at least 1", n)
		}

		for _, b := range d.Blocks {
			if !b.Located {
				if b.Start != -1 || b.End != -1 {
					t.Fatalf("%v: unlocated block carries range [%d,%d]", b.Kind, b.Start, b.End)
				}
				continue
			}
			if b.Start < 0 || b.End < b.Start || b.End >= n {
				t.Fatalf("%v: range [%d,%d] outside [0,%d)", b.Kind, b.Start, b.End, n)
			}
			// Slice is what a search matches against, so every located block
			// has to be reproducible from the source.
			if got := d.Src.Slice(b.Start, b.End); got == "" && strings.TrimSpace(src) != "" {
				lines := d.Src.Lines(b.Start, b.End)
				if len(lines) != b.End-b.Start+1 {
					t.Fatalf("%v: Slice empty and Lines short for [%d,%d]", b.Kind, b.Start, b.End)
				}
			}
			if b.Parent != nil && !b.Parent.Contains(b) {
				t.Fatalf("%v: parent does not report containing its own child", b.Kind)
			}
		}

		// Breadcrumb and Section both scan Headings left to right and stop at
		// the first heading past the line they were given, which is only
		// correct while the slice is in document order.
		prev := -1
		for _, h := range d.Headings {
			if h.Kind != KindHeading {
				t.Fatalf("Headings holds a %v", h.Kind)
			}
			if !h.Located {
				continue
			}
			if h.Start < prev {
				t.Fatalf("Headings out of document order: %d after %d", h.Start, prev)
			}
			prev = h.Start
			if h.Level < 1 || h.Level > 6 {
				t.Fatalf("heading level %d", h.Level)
			}
		}

		anchors := d.HeadingAnchors(AllAnchorStyles)
		if len(anchors) != len(d.Headings) {
			t.Fatalf("HeadingAnchors gave %d rows for %d headings", len(anchors), len(d.Headings))
		}
		for _, row := range anchors {
			if len(row) != len(AllAnchorStyles) {
				t.Fatalf("anchor row has %d styles, want %d", len(row), len(AllAnchorStyles))
			}
		}

		for line := range n {
			d.Breadcrumb(line)
			if s, e, ok := d.Section(line); ok {
				if s < 0 || e >= n || s > e {
					t.Fatalf("Section(%d) = [%d,%d], n=%d", line, s, e, n)
				}
			}
			if s, e, ok := d.SectionBody(line); ok {
				// An empty body is an insertion point, reported as e == s-1.
				if s < 0 || s > n || e >= n || e < s-1 {
					t.Fatalf("SectionBody(%d) = [%d,%d], n=%d", line, s, e, n)
				}
			}
		}
	})
}

// FuzzSourceIndex checks the line index on its own, since every range in the
// parser is expressed through it: a byte offset has to land on the line that
// actually holds it, whatever the file ends with.
func FuzzSourceIndex(f *testing.F) {
	addSeeds(f)
	f.Fuzz(func(t *testing.T, src string) {
		if len(src) > 1<<16 {
			return
		}
		s := NewSource("f.md", []byte(src))
		n := s.NumLines()

		for off := range len(src) + 1 {
			i := s.LineIndex(off)
			if i < 0 || i >= n {
				t.Fatalf("LineIndex(%d) = %d, n = %d", off, i, n)
			}
			if s.lineStart[i] > off && off < len(src) {
				t.Fatalf("LineIndex(%d) = %d starts at %d", off, i, s.lineStart[i])
			}
			if i+1 < n && s.lineStart[i+1] <= off {
				t.Fatalf("LineIndex(%d) = %d but line %d starts at %d", off, i, i+1, s.lineStart[i+1])
			}
		}

		// Every line is the raw span between two line starts with nothing but
		// its terminator taken off, which is what lets a range be sliced
		// straight out of the file.
		for i := range n {
			lo, hi := s.lineStart[i], len(src)
			if i+1 < n {
				hi = s.lineStart[i+1]
			}
			raw, line := src[lo:hi], s.Line(i)
			if !strings.HasPrefix(raw, line) || strings.Trim(raw[len(line):], "\r\n") != "" {
				t.Fatalf("Line(%d) = %q is not %q without its terminator", i, line, raw)
			}
		}

		for start := range n {
			for end := start - 1; end < n; end++ {
				lo, hi := s.ByteRange(start, end)
				if lo < 0 || hi > len(src) || lo > hi {
					t.Fatalf("ByteRange(%d,%d) = [%d,%d), len = %d", start, end, lo, hi, len(src))
				}
				if end < start && lo != hi {
					t.Fatalf("ByteRange(%d,%d) is an insertion point but spans [%d,%d)", start, end, lo, hi)
				}
			}
		}
	})
}

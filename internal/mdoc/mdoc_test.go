package mdoc

import (
	"strings"
	"testing"
)

const sample = "# Title\n" + // 0
	"\n" + // 1
	"- one\n" + // 2
	"  - nested alpha\n" + // 3
	"- two\n" + // 4
	"\n" + // 5
	"```go\n" + // 6
	"fmt.Println()\n" + // 7
	"```\n" + // 8
	"\n" + // 9
	"## Sub\n" + // 10
	"\n" + // 11
	"Body text.\n" // 12

func firstOfKind(t *testing.T, d *Doc, k Kind) *Block {
	t.Helper()
	for _, b := range d.Blocks {
		if b.Kind == k {
			return b
		}
	}
	t.Fatalf("no block of kind %q", k)
	return nil
}

func TestHeadingRangeIncludesMarker(t *testing.T) {
	d := Parse("t.md", []byte(sample))
	h := firstOfKind(t, d, KindHeading)
	if h.Start != 0 || h.End != 0 {
		t.Fatalf("heading range = %d..%d, want 0..0", h.Start, h.End)
	}
	if got := d.Src.Line(h.Start); got != "# Title" {
		t.Fatalf("heading line = %q", got)
	}
}

func TestSetextHeadingIncludesUnderline(t *testing.T) {
	d := Parse("t.md", []byte("Title\n=====\n\nbody\n"))
	h := firstOfKind(t, d, KindHeading)
	if h.Start != 0 || h.End != 1 {
		t.Fatalf("setext range = %d..%d, want 0..1", h.Start, h.End)
	}
}

func TestFencedCodeIncludesFences(t *testing.T) {
	d := Parse("t.md", []byte(sample))
	c := firstOfKind(t, d, KindCode)
	if c.Start != 6 || c.End != 8 {
		t.Fatalf("code range = %d..%d, want 6..8", c.Start, c.End)
	}
	if !strings.Contains(c.Text, "fmt.Println") {
		t.Fatalf("code text = %q", c.Text)
	}
}

func TestListItemSpansNestedChildren(t *testing.T) {
	d := Parse("t.md", []byte(sample))
	var items []*Block
	for _, b := range d.Blocks {
		if b.Kind == KindItem {
			items = append(items, b)
		}
	}
	if len(items) != 3 {
		t.Fatalf("got %d items, want 3", len(items))
	}
	if items[0].Start != 2 || items[0].End != 3 {
		t.Fatalf("outer item range = %d..%d, want 2..3", items[0].Start, items[0].End)
	}
	if items[1].Start != 3 || items[1].End != 3 {
		t.Fatalf("nested item range = %d..%d, want 3..3", items[1].Start, items[1].End)
	}
	if !items[0].Contains(items[1]) {
		t.Fatal("outer item should contain the nested one")
	}
}

func TestTaskItemsCarryCheckboxState(t *testing.T) {
	src := "- [ ] write docs\n" + // 0
		"- [x] ship it\n" + // 1
		"  - plain child\n" + // 2
		"- plain\n" // 3
	d := Parse("t.md", []byte(src))
	var items []*Block
	for _, b := range d.Blocks {
		if b.Kind == KindItem {
			items = append(items, b)
		}
	}
	if len(items) != 4 {
		t.Fatalf("got %d items, want 4", len(items))
	}
	want := []struct{ task, checked bool }{
		{true, false}, {true, true}, {false, false}, {false, false},
	}
	for i, w := range want {
		if items[i].Task != w.task || items[i].Checked != w.checked {
			t.Fatalf("item %d: task=%v checked=%v, want %v/%v",
				i, items[i].Task, items[i].Checked, w.task, w.checked)
		}
	}
	if got := items[0].Text; got != "write docs" {
		t.Fatalf("task text = %q, want the text without the checkbox", got)
	}
}

func TestBreadcrumbAndSection(t *testing.T) {
	d := Parse("t.md", []byte(sample))
	if got := d.Breadcrumb(12); len(got) != 2 || got[0] != "Title" || got[1] != "Sub" {
		t.Fatalf("breadcrumb = %v, want [Title Sub]", got)
	}
	start, end, ok := d.Section(12)
	if !ok || start != 10 || end != 12 {
		t.Fatalf("section = %d..%d ok=%v, want 10..12", start, end, ok)
	}
}

func TestFrontmatterIsOneBlock(t *testing.T) {
	src := "---\ntitle: Notes\ntags: [a, b]\n---\n\n# Heading\n"
	d := Parse("t.md", []byte(src))
	fm := firstOfKind(t, d, KindFrontmatter)
	if fm.Start != 0 || fm.End != 3 {
		t.Fatalf("frontmatter range = %d..%d, want 0..3", fm.Start, fm.End)
	}
	if !strings.Contains(fm.Text, "tags: [a, b]") {
		t.Fatalf("frontmatter text = %q", fm.Text)
	}
	// The masked region must not leak into the parsed tree as a heading.
	for _, b := range d.Blocks {
		if b.Kind == KindHeading && b.Start < 4 {
			t.Fatalf("front matter parsed as heading at line %d", b.Start)
		}
	}
	h := firstOfKind(t, d, KindHeading)
	if h.Start != 5 {
		t.Fatalf("heading line = %d, want 5", h.Start)
	}
}

func TestLinkDestinationIsSearchable(t *testing.T) {
	d := Parse("t.md", []byte("See [the runbook](https://example.com/runbook).\n"))
	p := firstOfKind(t, d, KindParagraph)
	if !strings.Contains(p.Text, "example.com/runbook") {
		t.Fatalf("paragraph text = %q", p.Text)
	}
}

func TestSourceLineIndex(t *testing.T) {
	s := NewSource("t.md", []byte("ab\ncd\nef\n"))
	if s.NumLines() != 3 {
		t.Fatalf("NumLines = %d, want 3", s.NumLines())
	}
	for off, want := range map[int]int{0: 0, 2: 0, 3: 1, 5: 1, 6: 2, 8: 2} {
		if got := s.LineIndex(off); got != want {
			t.Fatalf("LineIndex(%d) = %d, want %d", off, got, want)
		}
	}
	if got := s.Line(1); got != "cd" {
		t.Fatalf("Line(1) = %q", got)
	}
}

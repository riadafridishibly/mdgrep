package edit

import (
	"strings"
	"testing"

	"github.com/riadafridishibly/mdgrep/internal/match"
	"github.com/riadafridishibly/mdgrep/internal/mdoc"
	"github.com/riadafridishibly/mdgrep/internal/search"
)

var editSeeds = []string{
	"# a\n\n- [ ] b\n- [x] c\n",
	"---\ntitle: x\n---\n## H\n\nbody text\n",
	"| a | b |\n|---|---|\n| 1 | 2 |\n",
	"Setext\n======\n\n> quote\n> more\n\n```go\nx := 1\n```\n",
	"- outer\n  - [ ] nested task\n    - leaf\n",
	"## Empty\n\n## Next\n",
	"#### h ####\n\n## kram {#id}\n",
	"    indented code block\n",
	"no trailing newline",
	"crlf\r\n\r\n- [ ] item\r\n",
	"# h\n\npara one\n\npara two\n",
}

var ops = []Op{OpCheck, OpUncheck, OpToggle, OpReplace, OpSetText, OpDelete, OpAppend, OpPrepend}

func opOf(n int) Op { return ops[((n%len(ops))+len(ops))%len(ops)] }

// runPlan searches src and plans one edit over the result, returning the
// changes and the rewritten document.
func runPlan(t *testing.T, src, pat string, op Op, text string, opt search.Options) ([]Change, string, bool) {
	t.Helper()
	m, err := match.New(pat, match.Options{Mode: match.Substring})
	if err != nil {
		return nil, "", false
	}
	doc := mdoc.Parse("f.md", []byte(src))
	opt.Distinct = true
	res := search.File(doc, m, opt)
	changes, err := Plan(doc.Src, res, Options{Op: op, Text: text})
	if err != nil {
		return nil, "", false
	}
	return changes, Apply(doc.Src, changes), true
}

// FuzzEditPipeline drives a whole run: parse, search, plan, apply. Apply
// documents that changes "arrive in ascending order and never overlap" and
// silently drops any that do not, so a plan that breaks the promise loses an
// edit rather than reporting anything.
func FuzzEditPipeline(f *testing.F) {
	for _, s := range editSeeds {
		f.Add(s, "b", 0, "new text")
		f.Add(s, "a", 3, "line one\nline two")
	}
	f.Fuzz(func(t *testing.T, src, pat string, opn int, text string) {
		if len(src) > 1<<14 || len(pat) > 256 || len(text) > 1024 {
			return
		}
		op := opOf(opn)
		changes, out, ok := runPlan(t, src, pat, op, text, search.Options{})
		if !ok {
			return
		}

		prev := -1
		for _, c := range changes {
			if c.Start < 0 || c.End < c.Start-1 {
				t.Fatalf("%v: change range [%d,%d]", op, c.Start, c.End)
			}
			if c.Start <= prev {
				t.Fatalf("%v: change at %d overlaps the one ending at %d", op, c.Start, prev)
			}
			prev = c.End
			if c.NoOp && !equalLines(c.Old, c.New) {
				t.Fatalf("%v: change marked NoOp rewrites %q as %q", op, c.Old, c.New)
			}
		}

		if len(changes) == 0 && out != src {
			t.Fatalf("%v: no changes planned but the document moved", op)
		}
		if allNoOp(changes) && out != src {
			t.Fatalf("%v: every change was a no-op but the document moved", op)
		}

		// A file that ended in a newline keeps ending in one, which is what
		// stops an edit showing up as a whole-file diff.
		if strings.HasSuffix(src, "\n") && out != "" && !strings.HasSuffix(out, "\n") {
			t.Fatalf("%v: trailing newline lost", op)
		}
		if !strings.HasSuffix(src, "\n") && strings.HasSuffix(out, "\n") && len(changes) > 0 {
			last := changes[len(changes)-1]
			if last.End < mdoc.Parse("f.md", []byte(src)).Src.NumLines()-1 {
				t.Fatalf("%v: gained a trailing newline the file never had", op)
			}
		}
		if op == OpDelete && len(out) > len(src) {
			t.Fatalf("delete grew the document from %d to %d bytes", len(src), len(out))
		}

		// The result has to be a document in its own right: an edit is written
		// back to disk, and the next run parses what this one produced.
		mdoc.Parse("f.md", []byte(out))
	})
}

// FuzzEditIdempotent asserts the edits that describe a state rather than a
// change. Ticking a box that is already ticked, or setting text that is
// already there, has to leave the file alone the second time round.
func FuzzEditIdempotent(f *testing.F) {
	for _, s := range editSeeds {
		f.Add(s, "b", 0)
		f.Add(s, "item", 1)
	}
	f.Fuzz(func(t *testing.T, src, pat string, opn int) {
		if len(src) > 1<<14 || len(pat) > 256 {
			return
		}
		// Only the settling ops are idempotent: toggle flips every time, and
		// append and prepend are meant to stack.
		op := []Op{OpCheck, OpUncheck}[((opn%2)+2)%2]

		_, once, ok := runPlan(t, src, pat, op, "", search.Options{})
		if !ok {
			return
		}
		changes, twice, ok := runPlan(t, once, pat, op, "", search.Options{})
		if !ok {
			return
		}
		if once != twice {
			t.Fatalf("%v is not idempotent:\nonce  %q\ntwice %q", op, once, twice)
		}
		if !allNoOp(changes) {
			t.Fatalf("%v ran again on an already-settled file without reporting a no-op", op)
		}
	})
}

// FuzzEditWiden runs the edits against the flags that widen what a search
// selected. --section and --expand can hand plan a region that is larger than
// the matched node, and an insertion point where a section has no body at all.
func FuzzEditWiden(f *testing.F) {
	for _, s := range editSeeds {
		f.Add(s, "H", 3, "text", 1, false, false)
		f.Add(s, "a", 5, "", 0, true, false)
		f.Add(s, "b", 7, "x", 0, false, true)
	}
	f.Fuzz(func(t *testing.T, src, pat string, opn int, text string, expand int, section, body bool) {
		if len(src) > 1<<14 || len(pat) > 256 || len(text) > 1024 || expand < 0 || expand > 8 {
			return
		}
		op := opOf(opn)
		opt := search.Options{Expand: expand, Section: section, Body: body}
		changes, out, ok := runPlan(t, src, pat, op, text, opt)
		if !ok {
			return
		}
		prev := -1
		for _, c := range changes {
			if c.Start <= prev {
				t.Fatalf("%v: widened change at %d overlaps the one ending at %d", op, c.Start, prev)
			}
			prev = c.End
		}
		if len(changes) == 0 && out != src {
			t.Fatalf("%v: no changes planned but the document moved", op)
		}
		mdoc.Parse("f.md", []byte(out))
	})
}

func allNoOp(changes []Change) bool {
	for _, c := range changes {
		if !c.NoOp {
			return false
		}
	}
	return true
}

func equalLines(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestDeleteAcrossVerticalTab pins the line a deletion is allowed to swallow.
// A line of vertical tabs is whitespace to strings.TrimSpace but content to the
// parser, so reading it as a blank separator let a delete reach into the block
// below it and hand Apply two overlapping changes, one of which it then
// dropped without a word.
func TestDeleteAcrossVerticalTab(t *testing.T) {
	src := "# 0\n\v\n0"
	changes, out, ok := runPlan(t, src, "0", OpDelete, "", search.Options{})
	if !ok {
		t.Fatal("plan failed")
	}
	if len(changes) != 2 {
		t.Fatalf("got %d changes, want 2", len(changes))
	}
	if changes[0].End >= changes[1].Start {
		t.Fatalf("changes overlap: [%d,%d] then [%d,%d]",
			changes[0].Start, changes[0].End, changes[1].Start, changes[1].End)
	}
	if strings.Contains(out, "0") {
		t.Fatalf("both matches were selected but %q survived the delete", out)
	}
}

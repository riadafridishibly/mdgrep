package search

import (
	"testing"

	"github.com/riadafridishibly/mdgrep/internal/match"
	"github.com/riadafridishibly/mdgrep/internal/mdoc"
)

const linked = "# Guide\n" + // 0
	"\n" + // 1
	"## The Foo Bar\n" + // 2
	"\n" + // 3
	"Body one.\n" + // 4
	"\n" + // 5
	"## Deploy & Rollback!\n" + // 6
	"\n" + // 7
	"Body two.\n" + // 8
	"\n" + // 9
	"## The Foo Bar\n" + // 10
	"\n" + // 11
	"Body three.\n" // 12

func anchorFind(t *testing.T, path, pattern string, opt Options) []Result {
	t.Helper()
	a, err := NewAnchor([]string{pattern}, mdoc.AllAnchorStyles)
	if err != nil {
		t.Fatal(err)
	}
	opt.Anchor = a
	return File(mdoc.Parse(path, []byte(linked)), match.All(), opt)
}

func TestAnchorFindsHeading(t *testing.T) {
	for _, pattern := range []string{"#the-foo-bar", "the-foo-bar", "## The Foo Bar", "guide.md#the-foo-bar"} {
		res := anchorFind(t, "docs/guide.md", pattern, Options{})
		if len(res) != 1 {
			t.Fatalf("%q: got %d results, want 1", pattern, len(res))
		}
		if res[0].Start != 2 || res[0].Kind != mdoc.KindHeading {
			t.Fatalf("%q: got %s at line %d, want heading at 2", pattern, res[0].Kind, res[0].Start)
		}
	}
}

func TestAnchorDistinguishesRepeatedHeadings(t *testing.T) {
	res := anchorFind(t, "guide.md", "#the-foo-bar-1", Options{})
	if len(res) != 1 || res[0].Start != 10 {
		t.Fatalf("got %v, want a single hit on line 10", res)
	}
}

func TestAnchorTriesEveryStyle(t *testing.T) {
	// GitHub leaves the gap the "&" made; the generators that squeeze runs
	// do not. Both spellings have to find the heading.
	for _, pattern := range []string{"#deploy--rollback", "#deploy-rollback"} {
		res := anchorFind(t, "guide.md", pattern, Options{})
		if len(res) != 1 || res[0].Start != 6 {
			t.Fatalf("%q: got %v, want a single hit on line 6", pattern, res)
		}
	}
}

func TestAnchorTakesTheWholeSection(t *testing.T) {
	res := anchorFind(t, "guide.md", "#the-foo-bar", Options{Section: true})
	if len(res) != 1 {
		t.Fatalf("got %d results, want 1", len(res))
	}
	if res[0].Start != 2 || res[0].End != 4 {
		t.Fatalf("range = %d..%d, want 2..4", res[0].Start, res[0].End)
	}
}

func TestAnchorPathHalfScopesTheSearch(t *testing.T) {
	if res := anchorFind(t, "docs/other.md", "guide.md#the-foo-bar", Options{}); len(res) != 0 {
		t.Fatalf("got %d results in a file the link did not name, want 0", len(res))
	}
	url := "https://github.com/o/r/blob/main/docs/guide.md#the-foo-bar"
	if res := anchorFind(t, "docs/guide.md", url, Options{}); len(res) != 1 {
		t.Fatalf("got %d results for a pasted URL, want 1", len(res))
	}
	// A pasted URL carries components above the repository root, so the two
	// paths are only compared as far as the shorter one goes. A link deeper
	// than the file it finds therefore matches too, which is the same rule
	// seen from the other side.
	if res := anchorFind(t, "guide.md", "docs/guide.md#the-foo-bar", Options{}); len(res) != 1 {
		t.Fatalf("got %d results for a link deeper than the file, want 1", len(res))
	}
}

func TestAnchorRejectsAnEmptyFragment(t *testing.T) {
	if _, err := NewAnchor([]string{"docs/guide.md#"}, mdoc.AllAnchorStyles); err == nil {
		t.Fatal("want an error for a link with no fragment")
	}
}

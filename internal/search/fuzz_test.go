package search

import (
	"strings"
	"testing"

	"github.com/riadafridishibly/mdgrep/internal/match"
	"github.com/riadafridishibly/mdgrep/internal/mdoc"
)

var anchorSeeds = []string{
	"# Install\n\n## The Foo Bar\n\n### Café & crème\n",
	"## dup\n\n## dup\n\n## dup\n",
	"# 123\n\n# ---\n\n# _under_score_\n",
	"## A  b -- c!\n\n## [link](/url) heading\n",
	"# `code` heading\n\n## heading with {#kramdown}\n",
	"Setext\n======\n\nAnother\n-------\n",
	"# Élève\n\n# İstanbul\n\n# 中文标题\n",
	"# a/b:c;d|e_f-g.h\n",
}

// FuzzAnchorRoundTrip closes the loop the anchor search is built on: an anchor
// mdgrep generated for a heading, handed back as the pattern, has to find that
// heading again. Slug normalises both the heading and the query, so if the two
// sides ever disagree a link stops resolving.
func FuzzAnchorRoundTrip(f *testing.F) {
	for _, s := range anchorSeeds {
		f.Add(s, 0)
	}
	f.Fuzz(func(t *testing.T, src string, styleN int) {
		if len(src) > 1<<14 {
			return
		}
		style := mdoc.AllAnchorStyles[((styleN%len(mdoc.AllAnchorStyles))+len(mdoc.AllAnchorStyles))%len(mdoc.AllAnchorStyles)]
		styles := []mdoc.AnchorStyle{style}

		doc := mdoc.Parse("docs/f.md", []byte(src))
		anchors := doc.HeadingAnchors(styles)

		for i, want := range anchors {
			slug := want[0]
			if slug == "" {
				continue // a heading with no sluggable text is not addressable
			}
			for _, pattern := range []string{"#" + slug, slug, "docs/f.md#" + slug} {
				a, err := NewAnchor([]string{pattern}, styles)
				if err != nil {
					t.Fatalf("NewAnchor(%q): %v", pattern, err)
				}
				// Distinct keeps two headings that merely touch from being
				// reported as one region, and a lossy style can legitimately
				// give several headings the same anchor, so the test asks only
				// that the heading named is among what came back.
				res := File(doc, match.All(), Options{Anchor: a, Distinct: true})
				found := false
				for _, r := range res {
					if r.Start <= doc.Headings[i].Start && doc.Headings[i].Start <= r.End {
						found = true
					}
				}
				if !found {
					t.Fatalf("style %s: anchor %q did not find the heading it names at line %d\nsrc %q",
						style, pattern, doc.Headings[i].Start, src)
				}
			}
		}
	})
}

// FuzzSlug checks the normaliser on its own. A slug is what both a heading and
// a query are reduced to before they are compared, so putting one through
// twice has to leave it alone: otherwise a link typed as its own anchor
// normalises to something the heading never produces.
func FuzzSlug(f *testing.F) {
	for _, s := range anchorSeeds {
		f.Add(s, 0)
	}
	f.Fuzz(func(t *testing.T, text string, styleN int) {
		if len(text) > 4096 {
			return
		}
		style := mdoc.AllAnchorStyles[((styleN%len(mdoc.AllAnchorStyles))+len(mdoc.AllAnchorStyles))%len(mdoc.AllAnchorStyles)]

		slug := mdoc.Slug(style, text)
		if again := mdoc.Slug(style, slug); again != slug {
			t.Fatalf("style %s: Slug is not stable: %q then %q\nfrom %q", style, slug, again, text)
		}
		if strings.ContainsAny(slug, " \t\n") {
			t.Fatalf("style %s: slug %q holds whitespace", style, slug)
		}
	})
}

// FuzzSearchOptions drives the selection flags together. Every result has to be
// a line range inside the file with the block that matched still inside it,
// because that is what both the printer and an edit read off it.
func FuzzSearchOptions(f *testing.F) {
	for _, s := range anchorSeeds {
		f.Add(s, "a", 0, 0, 0, 0, false, false, false, 0)
	}
	f.Add("# h\n\n- [ ] a\n  - b\n\n## h2\n\ntext\n", "b", 1, 1, 1, 2, true, false, true, 3)
	f.Fuzz(func(t *testing.T, src, pat string, mode, before, after, lines int, section, body, rank bool, expand int) {
		if len(src) > 1<<14 || len(pat) > 256 {
			return
		}
		clampSmall := func(n int) int {
			if n < 0 {
				return 0
			}
			return min(n, 8)
		}
		m, err := match.New(pat, match.Options{Mode: match.Mode(((mode % 3) + 3) % 3), MinScore: 0.3})
		if err != nil {
			return
		}
		doc := mdoc.Parse("f.md", []byte(src))
		n := doc.Src.NumLines()

		opt := Options{
			Before:  clampSmall(before),
			After:   clampSmall(after),
			Lines:   clampSmall(lines),
			Expand:  clampSmall(expand),
			Section: section,
			Body:    body,
			Rank:    rank,
		}
		for _, distinct := range []bool{false, true} {
			opt.Distinct = distinct
			res := File(doc, m, opt)
			prevEnd := -1
			for _, r := range res {
				if r.Start < 0 || r.End >= n || r.End < r.Start-1 {
					t.Fatalf("result [%d,%d] outside [0,%d)", r.Start, r.End, n)
				}
				if r.HitStart < 0 || r.HitEnd >= n || r.HitEnd < r.HitStart {
					t.Fatalf("hit [%d,%d] outside [0,%d)", r.HitStart, r.HitEnd, n)
				}
				// --section-body deliberately drops the heading, so the hit
				// sits outside the region only there.
				if !body && r.End >= r.Start && (r.Start > r.HitStart || r.End < r.HitEnd) {
					t.Fatalf("region [%d,%d] does not hold its hit [%d,%d]", r.Start, r.End, r.HitStart, r.HitEnd)
				}
				if !rank {
					if r.Start <= prevEnd {
						t.Fatalf("result at %d overlaps the one ending at %d", r.Start, prevEnd)
					}
					prevEnd = r.End
				}
			}
		}
	})
}

// TestAnchorSuffixSurvivesSlugging pins the anchor a repeated heading gets. The
// counter that tells two alike headings apart is appended after the text is
// slugged, so on a style that collapses punctuation the finished anchor is not
// something slugging it again reproduces. A link carrying it therefore has to
// be accepted as it was written.
func TestAnchorSuffixSurvivesSlugging(t *testing.T) {
	const src = "# ! !\n# ! !"
	styles := []mdoc.AnchorStyle{mdoc.AnchorGitLab}
	doc := mdoc.Parse("f.md", []byte(src))
	anchors := doc.HeadingAnchors(styles)
	if len(anchors) != 2 {
		t.Fatalf("got %d headings, want 2", len(anchors))
	}
	second := anchors[1][0]
	if second == anchors[0][0] {
		t.Fatalf("the repeated heading was given the same anchor %q", second)
	}
	a, err := NewAnchor([]string{"#" + second}, styles)
	if err != nil {
		t.Fatal(err)
	}
	res := File(doc, match.All(), Options{Anchor: a, Distinct: true})
	found := false
	for _, r := range res {
		if r.Start <= doc.Headings[1].Start && doc.Headings[1].Start <= r.End {
			found = true
		}
	}
	if !found {
		t.Fatalf("anchor %q did not find the heading it was generated from", second)
	}
}

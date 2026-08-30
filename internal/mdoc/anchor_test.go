package mdoc

import "testing"

func TestSlugPerStyle(t *testing.T) {
	cases := []struct {
		text string
		want map[AnchorStyle]string
	}{
		{"The Foo Bar", map[AnchorStyle]string{
			AnchorGitHub: "the-foo-bar", AnchorGitLab: "the-foo-bar",
			AnchorPython: "the-foo-bar", AnchorKramdown: "the-foo-bar",
			AnchorPandoc: "the-foo-bar", AnchorLoose: "the-foo-bar",
		}},
		// "&" leaves a gap behind: only the generators that squeeze runs close it.
		{"Deploy & Rollback!", map[AnchorStyle]string{
			AnchorGitHub: "deploy--rollback", AnchorGitLab: "deploy-rollback",
			AnchorPython: "deploy-rollback", AnchorKramdown: "deploy--rollback",
			AnchorPandoc: "deploy-rollback", AnchorLoose: "deploy-rollback",
		}},
		{"1. Getting Started", map[AnchorStyle]string{
			AnchorGitHub: "1-getting-started", AnchorGitLab: "1-getting-started",
			AnchorPython: "1-getting-started", AnchorKramdown: "getting-started",
			AnchorPandoc: "getting-started", AnchorLoose: "1-getting-started",
		}},
		{"deploy_key rotation", map[AnchorStyle]string{
			AnchorGitHub: "deploy_key-rotation", AnchorGitLab: "deploy_key-rotation",
			AnchorPython: "deploy_key-rotation", AnchorKramdown: "deploykey-rotation",
			AnchorPandoc: "deploy_key-rotation", AnchorLoose: "deploy-key-rotation",
		}},
		{"Café Notes", map[AnchorStyle]string{
			AnchorGitHub: "café-notes", AnchorGitLab: "café-notes",
			AnchorPython: "cafe-notes", AnchorKramdown: "caf-notes",
			AnchorPandoc: "café-notes", AnchorLoose: "cafe-notes",
		}},
		{"2024", map[AnchorStyle]string{
			AnchorGitHub: "2024", AnchorGitLab: "anchor-2024",
			AnchorPython: "2024", AnchorKramdown: "section",
			AnchorPandoc: "section", AnchorLoose: "2024",
		}},
	}
	for _, c := range cases {
		for style, want := range c.want {
			if got := Slug(style, c.text); got != want {
				t.Errorf("Slug(%q, %q) = %q, want %q", style, c.text, got, want)
			}
		}
	}
}

func TestSlugIsIdempotent(t *testing.T) {
	// A pattern may be typed as the anchor rather than as the heading, so
	// slugging an anchor again has to leave it alone.
	for _, style := range AllAnchorStyles {
		for _, text := range []string{"The Foo Bar", "deploy_key rotation", "1. Getting Started"} {
			once := Slug(style, text)
			if twice := Slug(style, once); twice != once {
				t.Errorf("Slug(%q, %q) = %q, then %q", style, text, once, twice)
			}
		}
	}
}

func TestHeadingAnchorsNumberRepeats(t *testing.T) {
	d := Parse("t.md", []byte("## Notes\n\n## Notes\n\n## Notes\n"))
	got := d.HeadingAnchors([]AnchorStyle{AnchorGitHub, AnchorPython})
	want := [][]string{{"notes", "notes"}, {"notes-1", "notes_1"}, {"notes-2", "notes_2"}}
	for i := range want {
		for j := range want[i] {
			if got[i][j] != want[i][j] {
				t.Errorf("heading %d anchor %d = %q, want %q", i, j, got[i][j], want[i][j])
			}
		}
	}
}

func TestHeadingAnchorIgnoresLinkDestination(t *testing.T) {
	d := Parse("t.md", []byte("## See [the docs](https://example.com/x)\n"))
	if got := d.HeadingAnchors([]AnchorStyle{AnchorGitHub})[0][0]; got != "see-the-docs" {
		t.Fatalf("anchor = %q, want see-the-docs", got)
	}
}

func TestHeadingAnchorReadsCodeSpanContent(t *testing.T) {
	d := Parse("t.md", []byte("### Using `brew install`, quickly\n"))
	if got := d.HeadingAnchors([]AnchorStyle{AnchorGitHub})[0][0]; got != "using-brew-install-quickly" {
		t.Fatalf("anchor = %q, want using-brew-install-quickly", got)
	}
}

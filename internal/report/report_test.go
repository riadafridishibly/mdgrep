package report

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/riadafridishibly/mdgrep/internal/match"
	"github.com/riadafridishibly/mdgrep/internal/mdoc"
	"github.com/riadafridishibly/mdgrep/internal/render"
	"github.com/riadafridishibly/mdgrep/internal/search"
)

func expecting(n int) *int { return &n }

func TestCountGate(t *testing.T) {
	tests := []struct {
		name   string
		total  int
		expect *int
		multi  bool
		kind   string
		code   int
	}{
		{"one match is the whole instruction", 1, nil, false, "", 0},
		{"nothing matched", 0, nil, false, "nomatch", 1},
		{"two without --multi", 2, nil, false, "ambiguous", 2},
		{"two with --multi", 2, nil, true, "", 0},
		{"the count that was expected", 3, expecting(3), false, "", 0},
		{"expecting more than matched", 2, expecting(3), false, "expect", 2},
		{"expecting fewer than matched", 9, expecting(3), false, "expect", 2},
		{"expecting more than one waives --multi", 4, expecting(4), false, "", 0},
		{"expecting a match and finding none", 0, expecting(1), false, "expect", 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			why, code := Gate(tt.total, tt.expect, tt.multi, FlagWords)
			if why.Kind != tt.kind || code != tt.code {
				t.Errorf("got (%q, %d), want (%q, %d)", why.Kind, code, tt.kind, tt.code)
			}
			if code != 0 && tt.kind != "nomatch" && why.Text == "" {
				t.Error("a refusal a caller can read should say why")
			}
		})
	}
}

// found searches src and returns it in the shape runEdits reports on.
func found(t *testing.T, src, pat string) ([]File, int) {
	t.Helper()
	m, err := match.New(pat, match.Options{Mode: match.Substring})
	if err != nil {
		t.Fatal(err)
	}
	doc := mdoc.Parse("d.md", []byte(src))
	res := search.File(doc, m, search.Options{Distinct: true})
	return []File{{doc.Src, res}}, len(res)
}

// manyItems is a list long enough to run past the cap a refusal lists.
func manyItems(n int) string {
	var b strings.Builder
	b.WriteString("# H\n\n")
	for i := range n {
		fmt.Fprintf(&b, "- item %d needle\n", i)
	}
	return b.String()
}

func TestReportRefusedText(t *testing.T) {
	results, total := found(t, "# H\n\n- alpha needle\n- beta needle\n", "needle")
	if total != 2 {
		t.Fatalf("total = %d, want 2", total)
	}
	why, code := Gate(total, nil, false, FlagWords)
	if code != 2 {
		t.Fatalf("code = %d, want 2", code)
	}
	var buf bytes.Buffer
	Refused(&buf, results, total, why, render.Plain)
	out := buf.String()
	for _, want := range []string{"2 matches", "--multi", "d.md:3", "alpha needle", "d.md:4", "beta needle"} {
		if !strings.Contains(out, want) {
			t.Errorf("refusal does not mention %q:\n%s", want, out)
		}
	}
}

func TestReportRefusedTextCaps(t *testing.T) {
	results, total := found(t, manyItems(25), "needle")
	if total != 25 {
		t.Fatalf("total = %d, want 25", total)
	}
	var buf bytes.Buffer
	why, _ := Gate(total, nil, false, FlagWords)
	Refused(&buf, results, total, why, render.Plain)
	if n := strings.Count(buf.String(), "d.md:"); n != shownMatches {
		t.Errorf("listed %d matches, want %d", n, shownMatches)
	}
	if !strings.Contains(buf.String(), fmt.Sprintf("… and %d more", 25-shownMatches)) {
		t.Errorf("a capped list should say how many it left out:\n%s", buf.String())
	}
}

// decodeRefusal reads back what --json wrote, and insists it was the single
// object a caller would parse rather than a stream or a line of English.
func decodeRefusal(t *testing.T, b []byte) Refusal {
	t.Helper()
	var out Refusal
	dec := json.NewDecoder(bytes.NewReader(b))
	if err := dec.Decode(&out); err != nil {
		t.Fatalf("decode %q: %v", b, err)
	}
	if dec.More() {
		t.Errorf("a refusal should be one object, got %q", b)
	}
	return out
}

func TestReportRefusedJSON(t *testing.T) {
	results, total := found(t, "# H\n\n- alpha needle\n- beta needle\n", "needle")
	why, _ := Gate(total, nil, false, FlagWords)
	var buf bytes.Buffer
	Refused(&buf, results, total, why, render.JSON)

	got := decodeRefusal(t, buf.Bytes())
	if got.Error != "ambiguous" {
		t.Errorf("error = %q, want ambiguous", got.Error)
	}
	if got.Total != 2 || len(got.Matches) != 2 {
		t.Errorf("total = %d with %d matches, want 2 and 2", got.Total, len(got.Matches))
	}
	if got.Expected != 0 {
		t.Errorf("expected = %d, want it left out when --expect was not given", got.Expected)
	}
	want := []Match{
		{Path: "d.md", Line: 3, Text: "- alpha needle"},
		{Path: "d.md", Line: 4, Text: "- beta needle"},
	}
	for i, m := range want {
		if got.Matches[i] != m {
			t.Errorf("match %d = %+v, want %+v", i, got.Matches[i], m)
		}
	}
}

func TestReportRefusedJSONExpect(t *testing.T) {
	results, total := found(t, "# H\n\n- alpha needle\n- beta needle\n", "needle")
	expect := expecting(5)
	why, code := Gate(total, expect, false, FlagWords)
	if code != 2 {
		t.Fatalf("code = %d, want 2", code)
	}
	var buf bytes.Buffer
	Refused(&buf, results, total, why, render.JSON)

	got := decodeRefusal(t, buf.Bytes())
	if got.Error != "expect" {
		t.Errorf("error = %q, want expect", got.Error)
	}
	if got.Expected != 5 || got.Total != 2 {
		t.Errorf("expected/total = %d/%d, want 5/2", got.Expected, got.Total)
	}
	if got.Message == "" {
		t.Error("a refusal should carry the sentence a human would have read")
	}
}

// TestReportRefusedJSONNoMatches covers the shape an empty list takes: the
// field is still an array, so a caller can range over it without a nil check.
func TestReportRefusedJSONNoMatches(t *testing.T) {
	expect := expecting(1)
	why, _ := Gate(0, expect, false, FlagWords)
	var buf bytes.Buffer
	Refused(&buf, nil, 0, why, render.JSON)

	if !bytes.Contains(buf.Bytes(), []byte(`"matches":[]`)) {
		t.Errorf("want an empty array, got %s", buf.Bytes())
	}
	if got := decodeRefusal(t, buf.Bytes()); got.Total != 0 || got.Expected != 1 {
		t.Errorf("total/expected = %d/%d, want 0/1", got.Total, got.Expected)
	}
}

func TestReportRefusedJSONCaps(t *testing.T) {
	results, total := found(t, manyItems(25), "needle")
	why, _ := Gate(total, nil, false, FlagWords)
	var buf bytes.Buffer
	Refused(&buf, results, total, why, render.JSON)

	got := decodeRefusal(t, buf.Bytes())
	if len(got.Matches) != shownMatches {
		t.Errorf("listed %d matches, want %d", len(got.Matches), shownMatches)
	}
	if got.Total != 25 {
		t.Errorf("total = %d, want the whole count 25 even though the list is capped", got.Total)
	}
}

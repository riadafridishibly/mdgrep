package report

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/riadafridishibly/mdgrep/internal/match"
	"github.com/riadafridishibly/mdgrep/internal/mdoc"
	"github.com/riadafridishibly/mdgrep/internal/render"
	"github.com/riadafridishibly/mdgrep/internal/search"
)

// FuzzRefusalJSON holds the machine-readable refusal to the promise a caller
// parses it on: whatever the matched lines contain — quotes, control bytes,
// half a code fence — stderr is one JSON object, and every line it reports is
// the line the file actually has at that number.
func FuzzRefusalJSON(f *testing.F) {
	seeds := []struct{ src, pat string }{
		{"# a\n\n- one x\n- two x\n", "x"},
		{"- \"quoted\" x\n- back\\slash x\n", "x"},
		{"| a | b |\n|---|---|\n| x | x |\n", "x"},
		{"```\nx\n```\n\nx in prose\n", "x"},
		{"- \tx tab\n-  x nbsp\n- 🙂 x\n", "x"},
		{"x\r\nx\r\n", "x"},
		{"---\ntitle: x\n---\n\nx\n", "x"},
		{strings.Repeat("- x item\n", 30), "x"},
		{"no match here\n", "x"},
	}
	for _, s := range seeds {
		f.Add(s.src, s.pat)
	}

	f.Fuzz(func(t *testing.T, src, pat string) {
		m, err := match.New(pat, match.Options{Mode: match.Substring})
		if err != nil {
			t.Skip()
		}
		d := mdoc.Parse("d.md", []byte(src))
		res := search.File(d, m, search.Options{Distinct: true})
		results := []File{{Src: d.Src, Res: res}}
		total := len(res)

		for _, expect := range []*int{nil, expecting(total + 1)} {
			why, code := Gate(total, expect, false, FlagWords)
			if code == 0 || why.Kind == "nomatch" {
				continue
			}
			var buf bytes.Buffer
			Refused(&buf, results, total, why, render.JSON)

			var got Refusal
			dec := json.NewDecoder(&buf)
			if err := dec.Decode(&got); err != nil {
				t.Fatalf("refusal is not JSON: %v", err)
			}
			if dec.More() {
				t.Fatal("refusal was more than one object")
			}
			if got.Total != total {
				t.Errorf("total = %d, want %d", got.Total, total)
			}
			if len(got.Matches) > shownMatches {
				t.Errorf("listed %d matches, cap is %d", len(got.Matches), shownMatches)
			}
			if got.Error != why.Kind {
				t.Errorf("error = %q, want %q", got.Error, why.Kind)
			}
			for _, mt := range got.Matches {
				if mt.Line < 1 || mt.Line > d.Src.NumLines() {
					t.Fatalf("line %d is outside a file of %d lines", mt.Line, d.Src.NumLines())
				}
				if want := validUTF8(strings.TrimSpace(d.Src.Line(mt.Line - 1))); mt.Text != want {
					t.Errorf("line %d reads %q, but the file has %q", mt.Line, mt.Text, want)
				}
			}
		}
	})
}

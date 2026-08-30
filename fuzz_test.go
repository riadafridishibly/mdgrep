package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/riadafridishibly/mdgrep/internal/edit"
	"github.com/riadafridishibly/mdgrep/internal/match"
	"github.com/riadafridishibly/mdgrep/internal/mdoc"
	"github.com/riadafridishibly/mdgrep/internal/search"
)

// editFlags is every flag buildEdit reads as an instruction to change a file,
// in the order the fuzzer's bit mask names them. The four that take text are
// listed twice, once for each spelling, because saying both is the mistake the
// pairing has to catch.
var editFlags = []struct {
	group  string // flags sharing a group are two spellings of one edit
	takes  bool   // the edit carries text at all
	inline bool   // that text is the argument rather than a file to read
	set    func(c *config, text, path string)
}{
	{"check", false, false, func(c *config, _, _ string) { c.check = true }},
	{"uncheck", false, false, func(c *config, _, _ string) { c.uncheck = true }},
	{"toggle", false, false, func(c *config, _, _ string) { c.toggle = true }},
	{"delete", false, false, func(c *config, _, _ string) { c.del = true }},
	{"replace", true, true, func(c *config, t, _ string) { c.replace.Set(t) }},
	{"replace", true, false, func(c *config, _, p string) { c.replFrom.Set(p) }},
	{"set-text", true, true, func(c *config, t, _ string) { c.setText.Set(t) }},
	{"set-text", true, false, func(c *config, _, p string) { c.setFrom.Set(p) }},
	{"append", true, true, func(c *config, t, _ string) { c.appendTo.Set(t) }},
	{"append", true, false, func(c *config, _, p string) { c.appFrom.Set(p) }},
	{"prepend", true, true, func(c *config, t, _ string) { c.prependTo.Set(t) }},
	{"prepend", true, false, func(c *config, _, p string) { c.preFrom.Set(p) }},
}

// FuzzBuildEdit walks the flag combinations a caller can type. buildEdit is the
// gate that turns them into one operation, and the -from spellings doubled the
// ways two flags can collide, so what matters is that every combination is
// either refused or resolved to exactly one edit carrying exactly one text —
// never quietly resolved to the wrong one.
func FuzzBuildEdit(f *testing.F) {
	f.Add(uint16(0), "text", 0, false, false)
	f.Add(uint16(1<<4), "text", 0, false, false)       // --replace
	f.Add(uint16(1<<4|1<<5), "text", 0, false, false)  // --replace and --replace-from
	f.Add(uint16(1<<8|1<<10), "text", 0, false, false) // --append and --prepend
	f.Add(uint16(1<<9), "", 3, false, true)            // --append-from --expect 3
	f.Add(uint16(0), "", 2, true, true)                // --expect with no edit
	f.Add(uint16(1<<0|1<<1|1<<2), "x", 0, true, true)  // every checkbox at once
	f.Add(uint16(0xffff), "x", -1, true, true)         // everything
	f.Add(uint16(1<<6), "multi\nline\ntext", 1, false, true)

	f.Fuzz(func(t *testing.T, mask uint16, text string, expect int, multi, wantExpect bool) {
		const body = "text read from a file\nsecond line\n"
		path := filepath.Join(t.TempDir(), "body.md")
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}

		var c config
		var groups []string
		var clashed bool
		seen := map[string]bool{}
		wantText := map[string]string{}
		for i, fl := range editFlags {
			if mask&(1<<i) == 0 {
				continue
			}
			fl.set(&c, text, path)
			if seen[fl.group] {
				clashed = true
			} else {
				seen[fl.group] = true
				groups = append(groups, fl.group)
			}
			switch {
			case !fl.takes:
				wantText[fl.group] = ""
			case fl.inline:
				wantText[fl.group] = text
			default:
				wantText[fl.group] = body
			}
		}
		c.multi = multi
		if wantExpect {
			c.expect = optInt{val: expect, set: true}
		}

		e, err := buildEdit(&c)
		switch {
		case clashed && err == nil:
			t.Fatalf("mask %#x names one edit twice and was accepted", mask)
		case len(groups) > 1 && err == nil:
			t.Fatalf("mask %#x names %v and was accepted as one edit", mask, groups)
		case len(groups) == 0 && err == nil && (multi || wantExpect):
			t.Fatalf("--multi or --expect without an edit was accepted")
		case wantExpect && expect < 1 && len(groups) == 1 && !clashed && err == nil:
			t.Fatalf("--expect %d was accepted", expect)
		}
		if err != nil {
			return
		}
		if len(groups) == 0 {
			if e.Op != edit.OpNone {
				t.Fatalf("no edit flag produced op %v", e.Op)
			}
			return
		}
		// One group survived, so the operation is that group's and the text is
		// whichever spelling of it was given.
		if want := wantText[groups[0]]; e.Text != want {
			t.Errorf("op %v carries %q, want %q", e.Op, e.Text, want)
		}
	})
}

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
		results := []fileResult{{d.Src, res}}
		total := len(res)

		for _, expect := range []optInt{{}, {val: total + 1, set: true}} {
			why, code := countGate(total, expect, false)
			if code == 0 || why.kind == "nomatch" {
				continue
			}
			var buf bytes.Buffer
			reportRefused(&buf, results, total, why, true)

			var got jsonRefusal
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
			if got.Error != why.kind {
				t.Errorf("error = %q, want %q", got.Error, why.kind)
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

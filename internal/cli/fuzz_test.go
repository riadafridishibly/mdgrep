package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/riadafridishibly/mdgrep/internal/edit"
)

// editFlags is every flag Edit reads as an instruction to change a file,
// in the order the fuzzer's bit mask names them. The four that take text are
// listed twice, once for each spelling, because saying both is the mistake the
// pairing has to catch.
var editFlags = []struct {
	group  string // flags sharing a group are two spellings of one edit
	takes  bool   // the edit carries text at all
	inline bool   // that text is the argument rather than a file to read
	set    func(c *Config, text, path string)
}{
	{"check", false, false, func(c *Config, _, _ string) { c.Check = true }},
	{"uncheck", false, false, func(c *Config, _, _ string) { c.Uncheck = true }},
	{"toggle", false, false, func(c *Config, _, _ string) { c.Toggle = true }},
	{"delete", false, false, func(c *Config, _, _ string) { c.Del = true }},
	{"replace", true, true, func(c *Config, t, _ string) { c.Replace.Set(t) }},
	{"replace", true, false, func(c *Config, _, p string) { c.ReplFrom.Set(p) }},
	{"set-text", true, true, func(c *Config, t, _ string) { c.SetText.Set(t) }},
	{"set-text", true, false, func(c *Config, _, p string) { c.SetFrom.Set(p) }},
	{"append", true, true, func(c *Config, t, _ string) { c.AppendTo.Set(t) }},
	{"append", true, false, func(c *Config, _, p string) { c.AppFrom.Set(p) }},
	{"prepend", true, true, func(c *Config, t, _ string) { c.PrependTo.Set(t) }},
	{"prepend", true, false, func(c *Config, _, p string) { c.PreFrom.Set(p) }},
}

// FuzzBuildEdit walks the flag combinations a caller can type. Edit is the
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

		var c Config
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
		c.Multi = multi
		if wantExpect {
			c.Expect = OptInt{val: expect, set: true}
		}

		e, err := c.Edit()
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

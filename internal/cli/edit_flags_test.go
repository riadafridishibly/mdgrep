package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/riadafridishibly/mdgrep/internal/edit"
)

// writeFile drops text into the test's own directory and hands back the path,
// which is what the --*-from flags take.
func writeFile(t *testing.T, name, text string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// asStdin points os.Stdin at a file for the length of one test, so the "-"
// spelling of a --*-from flag can be exercised.
func asStdin(t *testing.T, text string) {
	t.Helper()
	f, err := os.Open(writeFile(t, "stdin", text))
	if err != nil {
		t.Fatal(err)
	}
	saved := os.Stdin
	os.Stdin = f
	t.Cleanup(func() { os.Stdin = saved; f.Close() })
}

// TestTextFromFile covers the point of the -from spellings: the text arrives
// byte for byte, including the newlines a shell argument makes awkward.
func TestTextFromFile(t *testing.T) {
	const body = "first line\n\n- second\n- third\n"
	tests := []struct {
		name string
		set  func(*Config, string)
		want edit.Op
	}{
		{"replace-from", func(c *Config, p string) { c.ReplFrom.Set(p) }, edit.OpReplace},
		{"set-text-from", func(c *Config, p string) { c.SetFrom.Set(p) }, edit.OpSetText},
		{"append-from", func(c *Config, p string) { c.AppFrom.Set(p) }, edit.OpAppend},
		{"prepend-from", func(c *Config, p string) { c.PreFrom.Set(p) }, edit.OpPrepend},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var c Config
			tt.set(&c, writeFile(t, "body.md", body))
			e, err := c.Edit()
			if err != nil {
				t.Fatalf("Edit: %v", err)
			}
			if e.Op != tt.want {
				t.Errorf("op = %v, want %v", e.Op, tt.want)
			}
			if e.Text != body {
				t.Errorf("text = %q, want %q", e.Text, body)
			}
		})
	}
}

func TestTextFromStdin(t *testing.T) {
	const body = "piped\nlines\n"
	asStdin(t, body)
	var c Config
	c.AppFrom.Set("-")
	e, err := c.Edit()
	if err != nil {
		t.Fatalf("Edit: %v", err)
	}
	if e.Op != edit.OpAppend || e.Text != body {
		t.Errorf("got %v %q, want append %q", e.Op, e.Text, body)
	}
}

func TestTextFromMissingFile(t *testing.T) {
	var c Config
	c.PreFrom.Set(filepath.Join(t.TempDir(), "absent.md"))
	if _, err := c.Edit(); err == nil {
		t.Fatal("a --prepend-from that cannot be read should fail the run")
	}
}

// TestInlineAndFromClash keeps the pair honest: two spellings of one flag are
// not two edits, and saying both is a mistake worth naming.
func TestInlineAndFromClash(t *testing.T) {
	tests := []struct {
		name string
		set  func(*Config, string)
	}{
		{"replace", func(c *Config, p string) { c.Replace.Set("x"); c.ReplFrom.Set(p) }},
		{"set-text", func(c *Config, p string) { c.SetText.Set("x"); c.SetFrom.Set(p) }},
		{"append", func(c *Config, p string) { c.AppendTo.Set("x"); c.AppFrom.Set(p) }},
		{"prepend", func(c *Config, p string) { c.PrependTo.Set("x"); c.PreFrom.Set(p) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var c Config
			tt.set(&c, writeFile(t, "body.md", "text"))
			_, err := c.Edit()
			if err == nil {
				t.Fatalf("--%s with --%s-from should be refused", tt.name, tt.name)
			}
			if !strings.Contains(err.Error(), tt.name) {
				t.Errorf("error %q does not name --%s", err, tt.name)
			}
		})
	}
}

// TestTwoEditsFromDifferentFlags checks that the -from spellings did not open a
// way round the one-edit-at-a-time rule.
func TestTwoEditsFromDifferentFlags(t *testing.T) {
	path := writeFile(t, "body.md", "text")
	var c Config
	c.AppFrom.Set(path)
	c.PreFrom.Set(path)
	if _, err := c.Edit(); err == nil {
		t.Fatal("--append-from and --prepend-from are two edits and should be refused")
	}
}

func TestExpectNeedsAnEdit(t *testing.T) {
	var c Config
	c.Expect.Set("2")
	if _, err := c.Edit(); err == nil {
		t.Fatal("--expect without an edit should be refused")
	}
}

func TestExpectWantsAPositiveCount(t *testing.T) {
	for _, n := range []string{"0", "-1"} {
		var c Config
		c.Del = true
		c.Expect.Set(n)
		if _, err := c.Edit(); err == nil {
			t.Errorf("--expect %s should be refused", n)
		}
	}
	var c Config
	c.Del = true
	c.Expect.Set("3")
	if _, err := c.Edit(); err != nil {
		t.Errorf("--expect 3 with an edit: %v", err)
	}
}

func TestExpectNotANumber(t *testing.T) {
	var o OptInt
	if err := o.Set("many"); err == nil {
		t.Fatal("--expect many should be refused")
	}
	if o.set {
		t.Error("a rejected value should leave --expect unset")
	}
}

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mdgrep/internal/edit"
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
		set  func(*config, string)
		want edit.Op
	}{
		{"replace-from", func(c *config, p string) { c.replFrom.Set(p) }, edit.OpReplace},
		{"set-text-from", func(c *config, p string) { c.setFrom.Set(p) }, edit.OpSetText},
		{"append-from", func(c *config, p string) { c.appFrom.Set(p) }, edit.OpAppend},
		{"prepend-from", func(c *config, p string) { c.preFrom.Set(p) }, edit.OpPrepend},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var c config
			tt.set(&c, writeFile(t, "body.md", body))
			e, err := buildEdit(&c)
			if err != nil {
				t.Fatalf("buildEdit: %v", err)
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
	var c config
	c.appFrom.Set("-")
	e, err := buildEdit(&c)
	if err != nil {
		t.Fatalf("buildEdit: %v", err)
	}
	if e.Op != edit.OpAppend || e.Text != body {
		t.Errorf("got %v %q, want append %q", e.Op, e.Text, body)
	}
}

func TestTextFromMissingFile(t *testing.T) {
	var c config
	c.preFrom.Set(filepath.Join(t.TempDir(), "absent.md"))
	if _, err := buildEdit(&c); err == nil {
		t.Fatal("a --prepend-from that cannot be read should fail the run")
	}
}

// TestInlineAndFromClash keeps the pair honest: two spellings of one flag are
// not two edits, and saying both is a mistake worth naming.
func TestInlineAndFromClash(t *testing.T) {
	tests := []struct {
		name string
		set  func(*config, string)
	}{
		{"replace", func(c *config, p string) { c.replace.Set("x"); c.replFrom.Set(p) }},
		{"set-text", func(c *config, p string) { c.setText.Set("x"); c.setFrom.Set(p) }},
		{"append", func(c *config, p string) { c.appendTo.Set("x"); c.appFrom.Set(p) }},
		{"prepend", func(c *config, p string) { c.prependTo.Set("x"); c.preFrom.Set(p) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var c config
			tt.set(&c, writeFile(t, "body.md", "text"))
			_, err := buildEdit(&c)
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
	var c config
	c.appFrom.Set(path)
	c.preFrom.Set(path)
	if _, err := buildEdit(&c); err == nil {
		t.Fatal("--append-from and --prepend-from are two edits and should be refused")
	}
}

func TestExpectNeedsAnEdit(t *testing.T) {
	var c config
	c.expect.Set("2")
	if _, err := buildEdit(&c); err == nil {
		t.Fatal("--expect without an edit should be refused")
	}
}

func TestExpectWantsAPositiveCount(t *testing.T) {
	for _, n := range []string{"0", "-1"} {
		var c config
		c.del = true
		c.expect.Set(n)
		if _, err := buildEdit(&c); err == nil {
			t.Errorf("--expect %s should be refused", n)
		}
	}
	var c config
	c.del = true
	c.expect.Set("3")
	if _, err := buildEdit(&c); err != nil {
		t.Errorf("--expect 3 with an edit: %v", err)
	}
}

func TestExpectNotANumber(t *testing.T) {
	var o optInt
	if err := o.Set("many"); err == nil {
		t.Fatal("--expect many should be refused")
	}
	if o.set {
		t.Error("a rejected value should leave --expect unset")
	}
}

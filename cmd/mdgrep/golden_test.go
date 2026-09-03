package main

import (
	"flag"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// A case file is one run of mdgrep written out in full: the documents it reads,
// the command line, and everything the command produced. The format is txtar --
// a sequence of sections, each opened by a line of the form "-- name --" and
// running to the next such line.
//
//	-- notes.md --      an input document, written into the run's directory
//	-- args --          the command line, as you would type it
//	-- stdin --         what the run reads from stdin (optional)
//	-- stdout --        generated
//	-- stderr --        generated, left out when empty
//	-- exit --          generated
//	-- notes.md.after --  generated, present only when the run rewrote the file
//
// Everything above "args" is an input document; everything below is produced by
// running the command, so the way to write a case is to write the first three
// sections and run:
//
//	go test -run TestCases -update
//
// which fills in the rest. A section whose text does not end in a newline is
// followed by the line "-- \ --", the way diff marks the same thing.
var update = flag.Bool("update", false, "rewrite the generated sections of testdata/cases/*.txt")

const casesDir = "testdata/cases"

type section struct{ name, body string }

func TestCases(t *testing.T) {
	files, err := filepath.Glob(filepath.Join(casesDir, "*.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatalf("no case files under %s", casesDir)
	}
	for _, file := range files {
		// The run happens in a directory of its own, so the case file is
		// addressed absolutely: -update writes it back, not a copy inside the
		// temporary directory the run moved into.
		abs, err := filepath.Abs(file)
		if err != nil {
			t.Fatal(err)
		}
		name := strings.TrimSuffix(filepath.Base(file), ".txt")
		t.Run(name, func(t *testing.T) { runCase(t, abs) })
	}
}

func runCase(t *testing.T, file string) {
	t.Helper()
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	want := string(data)
	got := replay(t, parseArchive(want))
	if got == want {
		return
	}
	if *update {
		if err := os.WriteFile(file, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("updated %s", file)
		return
	}
	t.Errorf("%s is out of date; rerun with -update to accept\n\nwant:\n%s\ngot:\n%s", file, want, got)
}

// replay writes the case's documents into a directory of its own, runs the
// command there, and returns the case file that run would have written.
func replay(t *testing.T, secs []section) string {
	t.Helper()
	dir := t.TempDir()

	var inputs []section
	var args, stdin string
	haveStdin := false
	for _, s := range secs {
		switch s.name {
		case "args":
			args = s.body
		case "stdin":
			stdin, haveStdin = s.body, true
		case "stdout", "stderr", "exit":
		default:
			if strings.HasSuffix(s.name, ".after") {
				continue
			}
			inputs = append(inputs, s)
		}
	}
	if strings.TrimSpace(args) == "" {
		t.Fatal("case has no -- args -- section")
	}

	for _, in := range inputs {
		path := filepath.Join(dir, filepath.FromSlash(in.name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(in.body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Chdir(dir)

	if haveStdin {
		path := filepath.Join(t.TempDir(), "stdin")
		if err := os.WriteFile(path, []byte(stdin), 0o644); err != nil {
			t.Fatal(err)
		}
		f, err := os.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		saved := os.Stdin
		os.Stdin = f
		defer func() { os.Stdin = saved }()
	}

	stdout, stderr, code := capture(t, splitArgs(t, args)...)

	out := make([]section, 0, len(secs)+4)
	out = append(out, inputs...)
	out = append(out, section{"args", args})
	if haveStdin {
		out = append(out, section{"stdin", stdin})
	}
	out = append(out, section{"stdout", stdout})
	if stderr != "" {
		out = append(out, section{"stderr", stderr})
	}
	out = append(out, section{"exit", strconv.Itoa(code) + "\n"})
	out = append(out, rewritten(t, dir, inputs)...)
	for _, s := range out {
		for _, line := range splitLines(s.body) {
			if isHeader(line) {
				t.Fatalf("section %q holds a line that reads as a section header: %s", s.name, line)
			}
		}
	}
	return formatArchive(out)
}

// rewritten reports the documents the run left different from how it found
// them, so an edit shows its result beside the document it started from.
func rewritten(t *testing.T, dir string, inputs []section) []section {
	t.Helper()
	var out []section
	for _, in := range inputs {
		data, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(in.name)))
		if os.IsNotExist(err) {
			out = append(out, section{in.name + ".after", "(deleted)\n"})
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		if string(data) != in.body {
			out = append(out, section{in.name + ".after", string(data)})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].name < out[j].name })
	return out
}

// splitArgs reads the args section the way a shell would: quotes hold a word
// together, a backslash escapes the next character. A leading "mdgrep" is
// dropped, so the section can be written as the command it stands for.
func splitArgs(t *testing.T, line string) []string {
	t.Helper()
	var words []string
	var word strings.Builder
	quote := byte(0)
	started := false
	for i := 0; i < len(line); i++ {
		c := line[i]
		switch {
		case c == '\\' && quote != '\'' && i+1 < len(line):
			i++
			word.WriteByte(line[i])
			started = true
		case quote != 0 && c == quote:
			quote = 0
		case quote != 0:
			word.WriteByte(c)
		case c == '\'' || c == '"':
			quote, started = c, true
		case c == ' ' || c == '\t' || c == '\n':
			if started {
				words = append(words, word.String())
				word.Reset()
				started = false
			}
		default:
			word.WriteByte(c)
			started = true
		}
	}
	if quote != 0 {
		t.Fatalf("unclosed %c in args: %s", quote, line)
	}
	if started {
		words = append(words, word.String())
	}
	if len(words) > 0 && words[0] == "mdgrep" {
		words = words[1:]
	}
	return words
}

const noNewline = "-- \\ --"

func parseArchive(text string) []section {
	var secs []section
	var body strings.Builder
	name := ""
	open := false
	flush := func() {
		if open {
			secs = append(secs, section{name, body.String()})
		}
		body.Reset()
	}
	for _, line := range splitLines(text) {
		switch {
		case trimNewline(line) == noNewline:
			if open {
				s := trimNewline(body.String())
				body.Reset()
				body.WriteString(s)
			}
		case isHeader(line):
			flush()
			name, open = header(line), true
		default:
			if open {
				body.WriteString(line)
			}
		}
	}
	flush()
	return secs
}

func formatArchive(secs []section) string {
	var b strings.Builder
	for _, s := range secs {
		b.WriteString("-- " + s.name + " --\n")
		b.WriteString(s.body)
		if s.body != "" && !strings.HasSuffix(s.body, "\n") {
			b.WriteString("\n" + noNewline + "\n")
		}
	}
	return b.String()
}

func isHeader(line string) bool {
	l := trimNewline(line)
	return strings.HasPrefix(l, "-- ") && strings.HasSuffix(l, " --") && len(l) >= 6
}

func header(line string) string {
	l := trimNewline(line)
	return strings.TrimSpace(l[3 : len(l)-3])
}

func trimNewline(s string) string { return strings.TrimSuffix(s, "\n") }

func splitLines(s string) []string {
	var lines []string
	for len(s) > 0 {
		i := strings.IndexByte(s, '\n')
		if i < 0 {
			lines = append(lines, s)
			break
		}
		lines = append(lines, s[:i+1])
		s = s[i+1:]
	}
	return lines
}

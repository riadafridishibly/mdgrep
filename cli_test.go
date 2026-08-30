package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"testing"
)

// capture runs the command the way main() would and hands back what each
// stream saw. Files rather than pipes, so a run that writes more than a pipe
// buffer holds cannot wedge the test.
func capture(t *testing.T, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	dir := t.TempDir()
	outFile, err := os.Create(filepath.Join(dir, "stdout"))
	if err != nil {
		t.Fatal(err)
	}
	errFile, err := os.Create(filepath.Join(dir, "stderr"))
	if err != nil {
		t.Fatal(err)
	}
	savedOut, savedErr, savedArgs := os.Stdout, os.Stderr, os.Args
	os.Stdout, os.Stderr, os.Args = outFile, errFile, append([]string{"mdgrep"}, args...)
	defer func() {
		os.Stdout, os.Stderr, os.Args = savedOut, savedErr, savedArgs
		outFile.Close()
		errFile.Close()
	}()

	code = run()
	return read(t, outFile.Name()), read(t, errFile.Name()), code
}

func read(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// doc writes a small document for one test and returns its path.
func doc(t *testing.T, text string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "d.md")
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

const sample = `# Guide

## Install

Run the installer.

- [ ] verify checksum

## Usage

Call the binary.
`

// TestErrorsAreShort is the whole of the change: a caller that mistypes a flag
// gets the line that says so, not the manual. The help is still one flag away,
// and --help still prints it in full.
func TestErrorsAreShort(t *testing.T) {
	tests := []struct {
		name string
		args []string
		says string
	}{
		{"unknown flag", []string{"--nope", "x"}, "not defined"},
		{"missing pattern", nil, "missing PATTERN"},
		{"flag wants a value", []string{"--expect", "many", "x"}, `not a number: "many"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, stderr, code := capture(t, tt.args...)
			if code != 2 {
				t.Errorf("exit = %d, want 2", code)
			}
			if !strings.Contains(stderr, tt.says) {
				t.Errorf("stderr does not say %q:\n%s", tt.says, stderr)
			}
			if !strings.Contains(stderr, hint) {
				t.Errorf("stderr does not point at --help:\n%s", stderr)
			}
			if strings.Contains(stderr, "Selection") {
				t.Errorf("an error printed the whole manual:\n%s", stderr)
			}
			if n := strings.Count(stderr, "\n"); n > 2 {
				t.Errorf("an error ran to %d lines:\n%s", n, stderr)
			}
		})
	}
}

func TestHelpStillPrintsInFull(t *testing.T) {
	stdout, _, code := capture(t, "--help")
	if code != 0 {
		t.Errorf("exit = %d, want 0", code)
	}
	for _, want := range []string{"Selection", "--append-from", "--expect"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("help does not mention %q", want)
		}
	}
}

func TestAppendFromWritesTheFile(t *testing.T) {
	path := doc(t, sample)
	body := filepath.Join(t.TempDir(), "body.md")
	if err := os.WriteFile(body, []byte("- [ ] one\n- [ ] two\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, stderr, code := capture(t, "verify checksum", path, "--append-from", body, "-q")
	if code != 0 {
		t.Fatalf("exit = %d (%s)", code, stderr)
	}
	got := read(t, path)
	if !strings.Contains(got, "- [ ] verify checksum\n- [ ] one\n- [ ] two\n") {
		t.Errorf("appended text did not land as its own lines:\n%s", got)
	}
}

func TestExpectRefusesTheWrongCount(t *testing.T) {
	path := doc(t, sample)
	before := read(t, path)

	_, stderr, code := capture(t, "the", path, "--replace", "X", "--expect", "5")
	if code != 2 {
		t.Errorf("exit = %d, want 2", code)
	}
	if !strings.Contains(stderr, "--expect 5") {
		t.Errorf("stderr does not name the count that was asked for:\n%s", stderr)
	}
	if read(t, path) != before {
		t.Error("a refused edit wrote to the file")
	}
}

func TestExpectAllowsTheCountItStates(t *testing.T) {
	path := doc(t, sample)
	_, stderr, code := capture(t, "the", path, "--replace", "X", "--expect", "2", "-q")
	if code != 0 {
		t.Fatalf("exit = %d (%s)", code, stderr)
	}
	if n := strings.Count(read(t, path), "\nX\n"); n != 2 {
		t.Errorf("rewrote %d nodes, want 2", n)
	}
}

// TestRefusalJSONGoesToStderr pins where a --json caller looks for a refusal:
// stdout carries results, so the object that says there are none goes beside
// them rather than into the stream being parsed.
func TestRefusalJSONGoesToStderr(t *testing.T) {
	path := doc(t, sample)
	stdout, stderr, code := capture(t, "the", path, "--replace", "X", "--json", "--dry-run")
	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if stdout != "" {
		t.Errorf("stdout should be empty when nothing was edited:\n%s", stdout)
	}
	var got jsonRefusal
	if err := json.Unmarshal([]byte(stderr), &got); err != nil {
		t.Fatalf("stderr is not one JSON object: %v\n%s", err, stderr)
	}
	if got.Error != "ambiguous" || got.Total != 2 {
		t.Errorf("got %+v, want ambiguous over 2 matches", got)
	}
}

// TestModuleVersion covers what -V reports. A binary the module system placed
// carries the tag it was built from; anything else falls back to the constant,
// which is all a build from a bare clone has.
func TestModuleVersion(t *testing.T) {
	tests := []struct {
		name string
		info *debug.BuildInfo
		ok   bool
		want string
	}{
		{"no build info at all", nil, false, version},
		{"built from a clone", &debug.BuildInfo{Main: debug.Module{Version: "(devel)"}}, true, version},
		{"no version recorded", &debug.BuildInfo{Main: debug.Module{Version: ""}}, true, version},
		{"installed at a tag", &debug.BuildInfo{Main: debug.Module{Version: "v0.2.0"}}, true, "0.2.0"},
		{"installed at a commit", &debug.BuildInfo{Main: debug.Module{Version: "v0.1.1-0.20260830120000-1bada3ecafe0"}}, true, "0.1.1-0.20260830120000-1bada3ecafe0"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := moduleVersion(tt.info, tt.ok); got != tt.want {
				t.Errorf("moduleVersion = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestVersionFlagPrints(t *testing.T) {
	stdout, _, code := capture(t, "--version")
	if code != 0 {
		t.Errorf("exit = %d, want 0", code)
	}
	if want := "mdgrep " + buildVersion() + "\n"; stdout != want {
		t.Errorf("stdout = %q, want %q", stdout, want)
	}
}

// TestCompactKeepsOneRecordPerLine is the property the format exists for: a
// reader splits on newline and then on tab, so a node that spans lines has to
// arrive with its newlines escaped rather than as several records.
func TestCompactKeepsOneRecordPerLine(t *testing.T) {
	path := doc(t, "# Guide\n\nA paragraph that runs\nacross three separate\nsource lines.\n")
	stdout, stderr, code := capture(t, "paragraph that runs", path, "--format", "compact")
	if code != 0 {
		t.Fatalf("exit = %d (%s)", code, stderr)
	}
	lines := strings.Split(strings.TrimRight(stdout, "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("want a path line and one record, got %d lines:\n%s", len(lines), stdout)
	}
	if strings.Contains(lines[0], "\t") {
		t.Errorf("the path line should have no tab in it: %q", lines[0])
	}
	fields := strings.Split(lines[1], "\t")
	if len(fields) != 3 {
		t.Fatalf("want 3 tab-separated fields, got %d: %q", len(fields), lines[1])
	}
	if fields[0] != "3-5" {
		t.Errorf("span = %q, want 3-5", fields[0])
	}
	if fields[1] != "paragraph" {
		t.Errorf("kind = %q, want paragraph", fields[1])
	}
	if !strings.Contains(fields[2], `runs\nacross`) {
		t.Errorf("newlines are not escaped: %q", fields[2])
	}
}

// A single-line node says its line once rather than as a span of itself.
func TestCompactNumbersASingleLineOnce(t *testing.T) {
	path := doc(t, sample)
	stdout, _, _ := capture(t, "^## Usage", path, "--format", "compact")
	if !strings.Contains(stdout, "9\theading\t## Usage") {
		t.Errorf("want an unspanned line number:\n%s", stdout)
	}
}

func TestCompactReportsEditStatus(t *testing.T) {
	tests := []struct {
		name  string
		args  []string
		want  string
		twice bool
	}{
		{name: "dry run", args: []string{"--dry-run"}, want: "\tcheck\tdry\t- [x] verify checksum"},
		{name: "applied", want: "\tcheck\tapplied\t- [x] verify checksum"},
		{name: "already ticked", want: "\tcheck\tunchanged\t", twice: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := doc(t, sample)
			args := append([]string{"verify checksum", path, "--check", "--format", "compact"}, tt.args...)
			if tt.twice {
				capture(t, args...)
			}
			stdout, stderr, code := capture(t, args...)
			if code != 0 {
				t.Fatalf("exit = %d (%s)", code, stderr)
			}
			if !strings.Contains(stdout, tt.want) {
				t.Errorf("want a record saying %q:\n%s", tt.want, stdout)
			}
		})
	}
}

// The machine formats are for a parser, so a terminal escape has no business in
// them however loudly --color asks.
func TestMachineFormatsAreNeverColoured(t *testing.T) {
	path := doc(t, sample)
	for _, format := range []string{"compact", "json"} {
		stdout, _, _ := capture(t, "verify checksum", path, "--format", format, "--color", "always")
		if strings.Contains(stdout, "\x1b[") {
			t.Errorf("--format %s emitted an escape sequence:\n%q", format, stdout)
		}
	}
}

func TestFormatJSONIsTheJSONFlag(t *testing.T) {
	path := doc(t, sample)
	viaFlag, _, _ := capture(t, "verify checksum", path, "--json")
	viaFormat, _, _ := capture(t, "verify checksum", path, "--format", "json")
	if viaFlag != viaFormat {
		t.Errorf("--json and --format json disagree:\n%s\n%s", viaFlag, viaFormat)
	}
}

func TestFormatRejectsWhatItCannotDo(t *testing.T) {
	tests := []struct {
		name string
		args []string
		says string
	}{
		{"unknown format", []string{"x", "--format", "yaml"}, `unknown format "yaml"`},
		{"contradicting --json", []string{"x", "--json", "--format", "compact"}, "ask for different output"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, stderr, code := capture(t, tt.args...)
			if code != 2 {
				t.Errorf("exit = %d, want 2", code)
			}
			if !strings.Contains(stderr, tt.says) {
				t.Errorf("stderr does not say %q:\n%s", tt.says, stderr)
			}
		})
	}
}

// TestHelpTopicNarrowsTheManual is the point of the topic: a caller after one
// flag pays for one section rather than the whole screen.
func TestHelpTopicNarrowsTheManual(t *testing.T) {
	tests := []struct {
		topic string
		wants string
		omits string
	}{
		{"editing", "--expect", "--anchor-style"},
		{"edit", "--expect", "--anchor-style"},        // a prefix of the title
		{"anchor", "--anchor-style", "--expect"},      // found by flag name
		{"format", "--format WHEN", "--anchor-style"}, // found by flag name too
		{"output", "--json", "--expect"},
	}
	for _, tt := range tests {
		t.Run(tt.topic, func(t *testing.T) {
			stdout, stderr, code := capture(t, "--help", tt.topic)
			if code != 0 {
				t.Fatalf("exit = %d (%s)", code, stderr)
			}
			if !strings.Contains(stdout, tt.wants) {
				t.Errorf("topic %q does not mention %q:\n%s", tt.topic, tt.wants, stdout)
			}
			if strings.Contains(stdout, tt.omits) {
				t.Errorf("topic %q dragged in %q as well", tt.topic, tt.omits)
			}
			if !strings.Contains(stdout, "usage: mdgrep") {
				t.Errorf("topic %q does not say how to invoke the command", tt.topic)
			}
			if n := len(stdout); n >= len(usage) {
				t.Errorf("topic %q printed %d bytes of a %d byte manual", tt.topic, n, len(usage))
			}
		})
	}
}

func TestUnknownHelpTopicNamesTheRealOnes(t *testing.T) {
	_, stderr, code := capture(t, "--help", "wombat")
	if code != 2 {
		t.Errorf("exit = %d, want 2", code)
	}
	for _, want := range []string{`no help topic "wombat"`, "matching", "editing", "output"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr does not say %q:\n%s", want, stderr)
		}
	}
}

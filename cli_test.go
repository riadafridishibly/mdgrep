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

// long is a document with a fenced block big enough to be worth capping and
// two headings to hang trails off.
const long = "# Orchard\n\n## Winter Pruning\n\nCut the leader.\n\n```bash\none\ntwo\nthree\nfour\nfive\n```\n\n## Summer Pruning\n\n- [ ] thin the fruit\n"

// The trail above a heading ends with that heading, and the heading is the
// next line printed. Saying it twice is the single largest redundancy in a
// heading search, so the trail stops at the parent instead.
func TestHeadingTrailStopsAtTheParent(t *testing.T) {
	path := doc(t, long)
	stdout, _, code := capture(t, "^## Winter", path)
	if code != 0 {
		t.Fatalf("exit = %d", code)
	}
	if strings.Contains(stdout, "Orchard › Winter Pruning") {
		t.Errorf("trail repeats the heading below it:\n%s", stdout)
	}
	if !strings.Contains(stdout, "  Orchard\n") {
		t.Errorf("want the parent trail:\n%s", stdout)
	}
}

// The trail is only redundant when the heading is printed. --section-body is
// the case where it is not, and there the last element is the only place the
// section's own name appears.
func TestSectionBodyKeepsTheWholeTrail(t *testing.T) {
	path := doc(t, long)
	stdout, _, code := capture(t, "^## Winter", path, "--section-body")
	if code != 0 {
		t.Fatalf("exit = %d", code)
	}
	if !strings.Contains(stdout, "Orchard › Winter Pruning") {
		t.Errorf("want the full trail when the heading is not printed:\n%s", stdout)
	}
}

// A hit that is not a heading has nothing on the next line to duplicate, so
// nothing is dropped: this is the case where the trail carries the result's
// only context.
func TestOtherKindsKeepTheWholeTrail(t *testing.T) {
	path := doc(t, long)
	stdout, _, code := capture(t, "thin the fruit", path)
	if code != 0 {
		t.Fatalf("exit = %d", code)
	}
	if !strings.Contains(stdout, "Orchard › Summer Pruning") {
		t.Errorf("want the full trail on a list item:\n%s", stdout)
	}
}

func TestSeparatorIsWhatTheCallerSays(t *testing.T) {
	tests := []struct {
		name  string
		args  []string
		wants string
		omits string
	}{
		{name: "default", wants: "  --\n"},
		{name: "left out", args: []string{"--separator", ""}, omits: "  --\n"},
		{name: "chosen", args: []string{"--separator", "~~"}, wants: "  ~~\n", omits: "  --\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := doc(t, long)
			stdout, _, code := capture(t, append([]string{"^## ", path}, tt.args...)...)
			if code != 0 {
				t.Fatalf("exit = %d", code)
			}
			if tt.wants != "" && !strings.Contains(stdout, tt.wants) {
				t.Errorf("want %q between results:\n%s", tt.wants, stdout)
			}
			if tt.omits != "" && strings.Contains(stdout, tt.omits) {
				t.Errorf("did not want %q:\n%s", tt.omits, stdout)
			}
		})
	}
}

// --truncate caps one node, which is the guard against a hit inside a large
// fenced block printing the whole block.
func TestTruncateCapsOneResult(t *testing.T) {
	path := doc(t, long)
	stdout, _, code := capture(t, "one", path, "--truncate", "3")
	if code != 0 {
		t.Fatalf("exit = %d", code)
	}
	if strings.Contains(stdout, "five") {
		t.Errorf("printed past the cap:\n%s", stdout)
	}
	if !strings.Contains(stdout, "… +4 lines") {
		t.Errorf("want the count of what was held back:\n%s", stdout)
	}
}

func TestTruncateSaysOneLineOnce(t *testing.T) {
	path := doc(t, long)
	stdout, _, code := capture(t, "one", path, "--truncate", "6")
	if code != 0 {
		t.Fatalf("exit = %d", code)
	}
	if !strings.Contains(stdout, "… +1 line\n") {
		t.Errorf("want a singular line count:\n%s", stdout)
	}
}

func TestTruncateLeavesAShortResultAlone(t *testing.T) {
	path := doc(t, long)
	stdout, _, code := capture(t, "thin the fruit", path, "--truncate", "5")
	if code != 0 {
		t.Fatalf("exit = %d", code)
	}
	if strings.Contains(stdout, "…") {
		t.Errorf("capped a node that fits:\n%s", stdout)
	}
}

// The machine formats have to stay machine-readable when they truncate:
// compact keeps its one record per line, and json says how much it held back
// rather than hiding it inside the text.
func TestTruncateStaysParseable(t *testing.T) {
	path := doc(t, long)

	stdout, _, code := capture(t, "one", path, "--truncate", "3", "--format", "compact")
	if code != 0 {
		t.Fatalf("exit = %d", code)
	}
	if lines := strings.Count(strings.TrimSpace(stdout), "\n"); lines != 1 {
		t.Errorf("want a path line and one record, got %d newlines:\n%s", lines, stdout)
	}
	if !strings.Contains(stdout, `\n… +4 lines`) {
		t.Errorf("want the elision escaped into the record:\n%s", stdout)
	}

	stdout, _, code = capture(t, "one", path, "--truncate", "3", "--json")
	if code != 0 {
		t.Fatalf("exit = %d", code)
	}
	var got struct {
		Text      string `json:"text"`
		Truncated int    `json:"truncated"`
	}
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("json: %v\n%s", err, stdout)
	}
	if got.Truncated != 4 {
		t.Errorf("truncated = %d, want 4", got.Truncated)
	}
	if strings.Contains(got.Text, "…") {
		t.Errorf("json text carries a display marker: %q", got.Text)
	}
	if n := strings.Count(got.Text, "\n") + 1; n != 3 {
		t.Errorf("text has %d lines, want 3: %q", n, got.Text)
	}
}

// An edit reports what it wrote, and a cap on that report would hide part of
// the change from the caller who asked for it.
func TestTruncateIsRefusedWithAnEdit(t *testing.T) {
	path := doc(t, sample)
	_, stderr, code := capture(t, "verify checksum", path, "--check", "--truncate", "2")
	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if !strings.Contains(stderr, "--truncate") {
		t.Errorf("want the refusal to name the flag: %s", stderr)
	}
}

func TestNegativeTruncateIsRejected(t *testing.T) {
	path := doc(t, sample)
	_, stderr, code := capture(t, "Install", path, "--truncate", "-1")
	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if !strings.Contains(stderr, "--truncate") {
		t.Errorf("want the refusal to name the flag: %s", stderr)
	}
}

// --outline is the one-flag spelling of the most common question asked of a
// markdown tree, and it takes paths where a search takes a pattern.
func TestOutlineNeedsNoPattern(t *testing.T) {
	path := doc(t, long)
	stdout, stderr, code := capture(t, "--outline", path)
	if code != 0 {
		t.Fatalf("exit = %d (%s)", code, stderr)
	}
	want := "# Orchard\n"
	if !strings.Contains(stdout, want) {
		t.Errorf("want the top heading:\n%s", stdout)
	}
	if !strings.Contains(stdout, "  ## Winter Pruning\n") {
		t.Errorf("want the child heading indented under it:\n%s", stdout)
	}
	if strings.Contains(stdout, "thin the fruit") {
		t.Errorf("outline printed something that is not a heading:\n%s", stdout)
	}
}

// A path that would be read as PATTERN by a search has to stay a path here,
// or an outline of two files would silently become an outline of one.
func TestOutlineReadsEveryPositionalAsAPath(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"a.md", "b.md"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("# "+name+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	stdout, _, code := capture(t, "--outline", filepath.Join(dir, "a.md"), filepath.Join(dir, "b.md"))
	if code != 0 {
		t.Fatalf("exit = %d", code)
	}
	for _, want := range []string{"# a.md", "# b.md"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("want %q in the outline:\n%s", want, stdout)
		}
	}
}

func TestOutlineIsItsOwnFormat(t *testing.T) {
	path := doc(t, long)
	for _, args := range [][]string{{"--json"}, {"--format", "compact"}} {
		_, stderr, code := capture(t, append([]string{"--outline", path}, args...)...)
		if code != 2 {
			t.Errorf("%v: exit = %d, want 2", args, code)
		}
		if !strings.Contains(stderr, "--outline") {
			t.Errorf("%v: want the refusal to name the flag: %s", args, stderr)
		}
	}
}

func TestOutlineIsRefusedWithAnEdit(t *testing.T) {
	path := doc(t, sample)
	_, stderr, code := capture(t, "verify checksum", path, "--check", "--outline")
	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if !strings.Contains(stderr, "--outline") {
		t.Errorf("want the refusal to name the flag: %s", stderr)
	}
}

// A pattern is still allowed, and narrows which headings the outline shows.
func TestOutlineTakesAPatternThroughDashE(t *testing.T) {
	path := doc(t, long)
	stdout, _, code := capture(t, "--outline", "-e", "Winter", path)
	if code != 0 {
		t.Fatalf("exit = %d", code)
	}
	if !strings.Contains(stdout, "## Winter Pruning") {
		t.Errorf("want the matching heading:\n%s", stdout)
	}
	if strings.Contains(stdout, "Summer") {
		t.Errorf("outline ignored the pattern:\n%s", stdout)
	}
}

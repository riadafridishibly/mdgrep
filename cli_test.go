package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"testing"

	"github.com/riadafridishibly/mdgrep/internal/help"
	"github.com/riadafridishibly/mdgrep/internal/report"
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
			if !strings.Contains(stderr, help.Hint) {
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
	var got report.Refusal
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
	if len(fields) != 5 {
		t.Fatalf("want 5 tab-separated fields, got %d: %q", len(fields), lines[1])
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
		// --apply was thirteen lines of prose inside Editing's flag column,
		// which is a section wearing a flag's clothes. It has one of its own,
		// so Editing no longer carries it and "--help apply" finds Plans.
		{"plans", "--apply", "--expect"},
		{"apply", "--apply", "--dry-run"},
		{"editing", "--dry-run", "--apply"},
		// --uncheck and --toggle used to be named in --check's prose rather
		// than in the flag column, where pickSection cannot see them.
		{"uncheck", "--uncheck", "--apply"},
		{"toggle", "--toggle", "--apply"},
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
			if n := len(stdout); n >= len(help.Usage) {
				t.Errorf("topic %q printed %d bytes of a %d byte manual", tt.topic, n, len(help.Usage))
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
	stdout, _, code := capture(t, "^## Winter", path, "--breadcrumb")
	if code != 0 {
		t.Fatalf("exit = %d", code)
	}
	if strings.Contains(stdout, "Orchard › Winter Pruning") {
		t.Errorf("trail repeats the heading below it:\n%s", stdout)
	}
	if !strings.Contains(stdout, "Orchard\n") {
		t.Errorf("want the parent trail:\n%s", stdout)
	}
}

// The trail is only redundant when the heading is printed. --section-body is
// the case where it is not, and there the last element is the only place the
// section's own name appears.
func TestSectionBodyKeepsTheWholeTrail(t *testing.T) {
	path := doc(t, long)
	stdout, _, code := capture(t, "^## Winter", path, "--section-body", "--breadcrumb")
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
	stdout, _, code := capture(t, "thin the fruit", path, "--breadcrumb")
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
		{name: "default", omits: "--\n"},
		{name: "asked for", args: []string{"--separator", "--"}, wants: "--\n"},
		{name: "chosen", args: []string{"--separator", "~~"}, wants: "~~\n", omits: "--\n"},
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
	stdout, _, code := capture(t, "one", path, "--truncate", "3", "--heading")
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

// A block is scored whole, so a match deep inside a long fence used to be cut
// away by the very flag meant to make it readable: the window began at the
// fence and printed its opening lines. It slides down to hold the hit.
func TestTruncateKeepsTheMatchedLine(t *testing.T) {
	path := doc(t, long)
	stdout, _, code := capture(t, "five", path, "--truncate", "3", "--heading")
	if code != 0 {
		t.Fatalf("exit = %d", code)
	}
	if !strings.Contains(stdout, "five") {
		t.Errorf("truncated away the line that matched:\n%s", stdout)
	}
	if !strings.Contains(stdout, "\u2026 +4 lines") {
		t.Errorf("want the count of what was skipped to reach it:\n%s", stdout)
	}

	// The machine formats report the same two counts, and start plus before
	// is the line the text begins on.
	stdout, _, code = capture(t, "five", path, "--truncate", "3", "--format", "compact")
	if code != 0 {
		t.Fatalf("exit = %d", code)
	}
	fields := strings.Split(strings.Split(stdout, "\n")[1], "\t")
	if len(fields) != 5 || fields[3] != "4" || fields[4] != "0" {
		t.Errorf("fields = %q, want 5 ending in 4, 0:\n%s", fields, stdout)
	}
	// The window fills its budget rather than starting flush at the hit, so
	// the text runs from start+before -- "four" -- and holds the match.
	if !strings.HasPrefix(fields[2], "four") || !strings.Contains(fields[2], "five") {
		t.Errorf("text = %q, want it to run from line 11 and hold the match", fields[2])
	}
}

func TestTruncateSaysOneLineOnce(t *testing.T) {
	path := doc(t, long)
	stdout, _, code := capture(t, "one", path, "--truncate", "6", "--heading")
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
	if strings.Contains(stdout, "…") {
		t.Errorf("the count belongs in its own field, not in the text:\n%s", stdout)
	}
	fields := strings.Split(strings.Split(strings.TrimSpace(stdout), "\n")[1], "\t")
	if len(fields) != 5 || fields[3] != "0" || fields[4] != "4" {
		t.Errorf("truncated fields = %q, want 5 fields ending in 0, 4:\n%s", fields, stdout)
	}

	stdout, _, code = capture(t, "one", path, "--truncate", "3", "--json")
	if code != 0 {
		t.Fatalf("exit = %d", code)
	}
	var got struct {
		Text   string `json:"text"`
		Before int    `json:"truncated_before"`
		After  int    `json:"truncated_after"`
	}
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("json: %v\n%s", err, stdout)
	}
	if got.Before != 0 || got.After != 4 {
		t.Errorf("truncated_before, truncated_after = %d, %d, want 0, 4", got.Before, got.After)
	}
	if strings.Contains(got.Text, "…") {
		t.Errorf("json text carries a display marker: %q", got.Text)
	}
	if n := strings.Count(got.Text, "\n") + 1; n != 3 {
		t.Errorf("text has %d lines, want 3: %q", n, got.Text)
	}
}

// Two results that touch are run together so the page reads as one passage.
// Under --truncate that was the wrong trade: the cap was spent on the first
// node of the merged region and every later match fell off the page rather
// than being shortened, so a search for five task items printed one line and
// a count. The cap belongs to a node, so --truncate keeps them apart.
func TestTruncateCapsEachTouchingResultOnItsOwn(t *testing.T) {
	path := doc(t, "# Orchard\n\n- [ ] thin the fruit\n      before the drop\n- [ ] order the oil\n      before the frost\n- [ ] stake the leader\n      before the wind\n")
	stdout, _, code := capture(t, "", path, "--todo", "--truncate", "1", "--heading")
	if code != 0 {
		t.Fatalf("exit = %d", code)
	}
	for _, want := range []string{"thin the fruit", "order the oil", "stake the leader"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("dropped the result holding %q:\n%s", want, stdout)
		}
	}
	for _, unwanted := range []string{"before the drop", "before the frost", "before the wind"} {
		if strings.Contains(stdout, unwanted) {
			t.Errorf("printed past the cap of one line (%q):\n%s", unwanted, stdout)
		}
	}
	if n := strings.Count(stdout, "… +1 line"); n != 3 {
		t.Errorf("held-back counts = %d, want one per result:\n%s", n, stdout)
	}
}

// Without --truncate the merge stands: a person reading three touching
// bullets wants the passage, not three blocks with a separator between them.
func TestTouchingResultsStillRunTogetherWithoutTruncate(t *testing.T) {
	path := doc(t, "# Orchard\n\n- [ ] thin the fruit\n- [ ] order the oil\n- [ ] stake the leader\n")
	// A separator is asked for so that three results would show as three:
	// without one, three results and one merged passage print the same bytes.
	stdout, _, code := capture(t, "", path, "--todo", "--separator", "--")
	if code != 0 {
		t.Fatalf("exit = %d", code)
	}
	if strings.Contains(stdout, "--\n") {
		t.Errorf("split a passage the page used to run together:\n%s", stdout)
	}
}

// Where the output is going decides the shape, the way it does for grep and
// rg: a terminal gets the file name above a file's results and numbered
// lines, and a pipe gets neither unless it asks.
func TestPipedOutputIsBareByDefault(t *testing.T) {
	path := doc(t, long)
	stdout, _, code := capture(t, "thin the fruit", path)
	if code != 0 {
		t.Fatalf("exit = %d", code)
	}
	if stdout != "- [ ] thin the fruit\n" {
		t.Errorf("stdout = %q, want the markdown alone", stdout)
	}
}

// A file named outright answers for itself, so its name is not worth
// printing; a directory could have answered from more than one file, so it
// is. -H and --no-filename each override the rule they are the answer to.
func TestFilenameFollowsHowManyFilesCouldAnswer(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.md")
	if err := os.WriteFile(path, []byte(long), 0o644); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name  string
		args  []string
		named bool
	}{
		{name: "one file", args: []string{path}, named: false},
		{name: "a directory", args: []string{dir}, named: true},
		{name: "forced on", args: []string{path, "-H"}, named: true},
		{name: "forced off", args: []string{dir, "--no-filename"}, named: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stdout, _, code := capture(t, append([]string{"thin the fruit"}, tt.args...)...)
			if code != 0 {
				t.Fatalf("exit = %d", code)
			}
			if got := strings.Contains(stdout, "a.md"); got != tt.named {
				t.Errorf("named = %v, want %v:\n%s", got, tt.named, stdout)
			}
		})
	}
}

// --heading puts the name above a file's results; without it the name rides
// every line, which is what makes the output greppable in turn.
func TestHeadingChoosesWhereTheNameGoes(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.md"), []byte(long), 0o644); err != nil {
		t.Fatal(err)
	}
	stdout, _, code := capture(t, "thin the fruit", dir, "--heading", "-n", "--no-breadcrumb")
	if code != 0 {
		t.Fatalf("exit = %d", code)
	}
	if !strings.Contains(stdout, "a.md\n17:- [ ] thin the fruit\n") {
		t.Errorf("want the name above and the number in front:\n%s", stdout)
	}

	stdout, _, code = capture(t, "thin the fruit", dir, "--no-heading", "-n")
	if code != 0 {
		t.Fatalf("exit = %d", code)
	}
	if !strings.HasSuffix(stdout, "a.md:17:- [ ] thin the fruit\n") {
		t.Errorf("want path:line:text on the line itself:\n%s", stdout)
	}
}

// Every line mdgrep prints belongs to a node that matched or to the region a
// widening flag grew it to, and neither is context in grep's sense -- so
// every line takes the colon and the output stays one shape to read.
func TestEveryPrintedLineTakesTheColon(t *testing.T) {
	path := doc(t, long)
	stdout, _, code := capture(t, "^## Winter", path, "--section", "-n")
	if code != 0 {
		t.Fatalf("exit = %d", code)
	}
	for _, line := range strings.Split(strings.TrimSuffix(stdout, "\n"), "\n") {
		if n := strings.IndexByte(line, ':'); n < 1 {
			t.Errorf("line %q carries no number", line)
		}
	}
	if strings.Contains(stdout, "\u2502") {
		t.Errorf("want no gutter bar:\n%s", stdout)
	}
}

// A breadcrumb has no counterpart in grep, so a pipe gets none until asked;
// it goes wherever a heading goes, since a heading is what says a person is
// reading; and there is nowhere to put one where the name rides every line.
func TestBreadcrumbGoesWithTheHeading(t *testing.T) {
	path := doc(t, long)
	tests := []struct {
		name  string
		args  []string
		trail bool
	}{
		{name: "a pipe", trail: false},
		{name: "asked for", args: []string{"--breadcrumb"}, trail: true},
		{name: "with a heading", args: []string{"--heading"}, trail: true},
		{name: "declined under a heading", args: []string{"--heading", "--no-breadcrumb"}, trail: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stdout, _, code := capture(t, append([]string{"thin the fruit", path}, tt.args...)...)
			if code != 0 {
				t.Fatalf("exit = %d", code)
			}
			if got := strings.Contains(stdout, "Orchard › Summer Pruning"); got != tt.trail {
				t.Errorf("trail = %v, want %v:\n%s", got, tt.trail, stdout)
			}
		})
	}

	_, stderr, code := capture(t, "thin the fruit", path, "--breadcrumb", "--no-heading")
	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if !strings.Contains(stderr, "--no-heading") {
		t.Errorf("want the contradiction named:\n%s", stderr)
	}
}

// A flag withdrawn by its opposite is withdrawn everywhere it was read:
// --breadcrumb asks for a heading to stand under, and taking it back takes
// the heading back too, so the pair together is the same as neither.
func TestBreadcrumbWithdrawnTakesItsHeadingWithIt(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.md"), []byte(long), 0o644); err != nil {
		t.Fatal(err)
	}
	stdout, _, code := capture(t, "thin the fruit", dir, "--breadcrumb", "--no-breadcrumb")
	if code != 0 {
		t.Fatalf("exit = %d", code)
	}
	if !strings.HasSuffix(stdout, "a.md:- [ ] thin the fruit\n") || strings.Contains(stdout, "›") {
		t.Errorf("want the piped layout and no trail:\n%s", stdout)
	}
}

// A stream is a list of regions, so a flag about how a page is decorated
// would be read and then change nothing the next stage sees.
func TestPageFlagsAreRefusedOnAStream(t *testing.T) {
	for _, flag := range []string{"--heading", "--no-heading", "-H", "--no-filename", "--breadcrumb"} {
		t.Run(flag, func(t *testing.T) {
			path := doc(t, long)
			_, stderr, code := capture(t, "^## ", path, flag, "--stream")
			if code != 2 {
				t.Fatalf("exit = %d, want 2", code)
			}
			if !strings.Contains(stderr, flag) {
				t.Errorf("want the flag named:\n%s", stderr)
			}
		})
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

// Neighbouring hits are run together so a person reads one passage. A machine
// format is counted and iterated over, so there the nodes stay apart.
const adjacent = `# Top

## A
## B

- [ ] ship the docs
- [ ] ship the tests
`

func TestMachineFormatsKeepNeighbouringHitsApart(t *testing.T) {
	path := doc(t, adjacent)
	stdout, _, code := capture(t, "", path, "--todo", "--format", "compact")
	if code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if got := strings.Count(stdout, "\titem\t"); got != 2 {
		t.Errorf("compact records = %d, want 2:\n%s", got, stdout)
	}
	stdout, _, _ = capture(t, "", path, "-k", "heading", "--json")
	if got := strings.Count(stdout, "\n"); got != 3 {
		t.Errorf("JSON objects = %d, want 3:\n%s", got, stdout)
	}
}

func TestPlainOutputStillReadsAsOnePassage(t *testing.T) {
	stdout, _, _ := capture(t, "", doc(t, adjacent), "--todo", "--no-breadcrumb")
	if strings.Contains(stdout, "--") {
		t.Errorf("two adjacent items were parted:\n%s", stdout)
	}
}

func TestCountTalliesNodesRatherThanPassages(t *testing.T) {
	// One file named outright answers for itself, so the tally stands alone,
	// the way grep writes one.
	stdout, _, _ := capture(t, "", doc(t, adjacent), "--todo", "-c")
	if strings.TrimSpace(stdout) != "2" {
		t.Errorf("count = %q, want 2 items", strings.TrimSpace(stdout))
	}
}

// A directory could have answered from more than one file, so the tally says
// which one it came from.
func TestCountNamesTheFileWhenMoreThanOneCouldAnswer(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.md"), []byte(adjacent), 0o644); err != nil {
		t.Fatal(err)
	}
	stdout, _, _ := capture(t, "", dir, "--todo", "-c")
	if !strings.HasSuffix(strings.TrimSpace(stdout), ":2") {
		t.Errorf("count = %q, want the file named beside it", strings.TrimSpace(stdout))
	}
}

// An outline prints the line each result starts on, so headings that follow one
// another have to arrive as results of their own or the tree loses them.
func TestOutlineKeepsHeadingsThatFollowOneAnother(t *testing.T) {
	stdout, _, _ := capture(t, "--outline", doc(t, adjacent))
	for _, want := range []string{"# Top", "## A", "## B"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("outline drops %q:\n%s", want, stdout)
		}
	}
}

// --help is documented as taking a topic, and took one only as a trailing
// positional: "--help=editing" answered "invalid boolean value" for -help.
// OptTopic reports IsBoolFlag, so both spellings reach the same manual.
func TestHelpTakesItsTopicAsAnArgument(t *testing.T) {
	for _, spelling := range []string{"--help=editing", "-h=editing"} {
		stdout, stderr, code := capture(t, spelling)
		if code != 0 {
			t.Fatalf("%s: exit = %d (%s)", spelling, code, stderr)
		}
		if !strings.Contains(stdout, "--expect") {
			t.Errorf("%s did not print Editing:\n%s", spelling, stdout)
		}
		if len(stdout) >= len(help.Usage) {
			t.Errorf("%s printed the whole manual rather than one part", spelling)
		}
	}
}

// The bare flag still stands alone, and still lets a pattern already typed
// stand: neither may be read as the name of a topic.
func TestBareHelpIsUnchangedByTheTopicArgument(t *testing.T) {
	for _, args := range [][]string{{"--help"}, {"-h"}, {"installer", "--help"}} {
		stdout, stderr, code := capture(t, args...)
		if code != 0 {
			t.Fatalf("%v: exit = %d (%s)", args, code, stderr)
		}
		if len(stdout) != len(help.Usage) {
			t.Errorf("%v printed %d bytes of a %d byte manual", args, len(stdout), len(help.Usage))
		}
	}
}

// A topic the manual does not have has to say so whichever way it was asked
// for, rather than printing the whole manual and calling it an answer.
func TestUnknownTopicAsAnArgumentIsRefused(t *testing.T) {
	stdout, stderr, code := capture(t, "--help=wombat")
	if code != 2 {
		t.Errorf("exit = %d, want 2", code)
	}
	if !strings.Contains(stderr, `no help topic "wombat"`) {
		t.Errorf("stderr does not name the topic:\n%s", stderr)
	}
	if strings.Contains(stdout, "usage: mdgrep") {
		t.Errorf("a refused topic printed the manual anyway:\n%s", stdout)
	}
}

// TestMinScoreRejectsNaN covers the one value that passed the range check by
// failing every comparison in it, and then matched nothing for the same
// reason -- an empty result that reads like an answer about the document.
func TestMinScoreRejectsNaN(t *testing.T) {
	path := doc(t, sample)
	for _, arg := range []string{"nan", "NaN"} {
		_, stderr, code := capture(t, "--fuzzy", "--min-score", arg, "instaler", path)
		if code != 2 {
			t.Fatalf("--min-score %s: code %d, want 2", arg, code)
		}
		if !strings.Contains(stderr, "a fuzzy score runs from 0 to 1") {
			t.Fatalf("--min-score %s: %q", arg, stderr)
		}
	}
}

// TestMinScoreKeepsItsRange is the other half: the ends of the scale are still
// values, not typos.
func TestMinScoreKeepsItsRange(t *testing.T) {
	path := doc(t, sample)
	for _, arg := range []string{"0", "0.5", "1"} {
		if _, stderr, code := capture(t, "--fuzzy", "--min-score", arg, "installer", path); code != 0 {
			t.Fatalf("--min-score %s: code %d\n%s", arg, code, stderr)
		}
	}
}

// A dry run prints the same lines a real edit does, so the one thing that
// tells them apart has to be printed whatever the layout -- a page and a pipe
// alike, and a plan as well as a search.
func TestDryRunSaysSoInEveryLayout(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{name: "a pipe"},
		{name: "a heading", args: []string{"--heading"}},
		{name: "a heading without a name", args: []string{"--heading", "--no-filename"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := doc(t, sample)
			stdout, _, code := capture(t, append([]string{"verify checksum", path, "--check", "--dry-run"}, tt.args...)...)
			if code != 0 {
				t.Fatalf("exit = %d", code)
			}
			if !strings.Contains(stdout, "(dry run)") {
				t.Errorf("nothing says the file was left alone:\n%s", stdout)
			}
			if !strings.Contains(stdout, "+ ") || read(t, path) != sample {
				t.Errorf("want the change shown and the file untouched:\n%s", stdout)
			}
		})
	}

	t.Run("a plan", func(t *testing.T) {
		path := doc(t, sample)
		p := planFile(t, `{"path":"`+path+`","match":"verify checksum","op":"check"}`)
		stdout, _, code := capture(t, "--apply", p, "--dry-run")
		if code != 0 {
			t.Fatalf("exit = %d", code)
		}
		if !strings.Contains(stdout, "(dry run)") || read(t, path) != sample {
			t.Errorf("want the plan marked as a dry run and the file untouched:\n%s", stdout)
		}
	})
}

// A real edit is not marked, so the marker means something when it is there.
func TestARealEditIsNotMarkedAsADryRun(t *testing.T) {
	path := doc(t, sample)
	stdout, _, code := capture(t, "verify checksum", path, "--check")
	if code != 0 {
		t.Fatalf("exit = %d", code)
	}
	if strings.Contains(stdout, "dry run") {
		t.Errorf("a write was reported as a dry run:\n%s", stdout)
	}
}

// A node already as asked is reported with "=" before each of its lines, the
// way a change is reported with "-" and "+": the mark is the report, so no
// bare line of prose follows it to break the shape.
func TestAnEditAlreadyAsAskedIsMarkedEquals(t *testing.T) {
	path := doc(t, "- [x] done\n")
	stdout, _, code := capture(t, "done", path, "--check")
	if code != 0 {
		t.Fatalf("exit = %d", code)
	}
	if stdout != "= - [x] done\n" {
		t.Errorf("stdout = %q, want the line marked = and nothing else", stdout)
	}
}

// What --truncate held back is said wherever it was held back, and the note
// names its file the way every other line does: a pipe that cut a node and
// said nothing would hand on a short node.
func TestTruncateNoteIsPrintedOnAPipe(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.md")
	if err := os.WriteFile(path, []byte(long), 0o644); err != nil {
		t.Fatal(err)
	}
	stdout, _, code := capture(t, "^one$", path, "--truncate", "2")
	if code != 0 {
		t.Fatalf("exit = %d", code)
	}
	if !strings.Contains(stdout, "\n… +") {
		t.Errorf("the cut was not reported:\n%s", stdout)
	}

	stdout, _, code = capture(t, "^one$", dir, "--truncate", "2")
	if code != 0 {
		t.Fatalf("exit = %d", code)
	}
	if !strings.Contains(stdout, "a.md:… +") {
		t.Errorf("want the note to name its file like any other line:\n%s", stdout)
	}
}

// An outline keeps its files apart the way a search does: a heading and a
// blank line on a page, and the separator, if any, where the name rides
// every line.
func TestOutlineKeepsFilesApart(t *testing.T) {
	dir := t.TempDir()
	for _, f := range []string{"a.md", "b.md"} {
		if err := os.WriteFile(filepath.Join(dir, f), []byte("# "+f+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	stdout, _, code := capture(t, "--outline", dir, "--heading")
	if code != 0 {
		t.Fatalf("exit = %d", code)
	}
	if !strings.Contains(stdout, "# a.md\n\n") {
		t.Errorf("want a blank line between two files on a page:\n%s", stdout)
	}

	stdout, _, code = capture(t, "--outline", dir, "--separator", "~~")
	if code != 0 {
		t.Fatalf("exit = %d", code)
	}
	if !strings.Contains(stdout, "# a.md\n~~\n") {
		t.Errorf("want the separator between two files on a pipe:\n%s", stdout)
	}
}

// -c and -l are written by the same printer as a result, so a path is
// coloured wherever it appears and the flags that place it are honoured.
func TestCountAndFileListAreWrittenLikeAPath(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.md"), []byte(long), 0o644); err != nil {
		t.Fatal(err)
	}
	stdout, _, code := capture(t, "Pruning", dir, "-c", "--color", "always")
	if code != 0 {
		t.Fatalf("exit = %d", code)
	}
	if !strings.Contains(stdout, "\x1b[35m") || !strings.HasSuffix(stdout, "2\n") {
		t.Errorf("want the path coloured beside the tally:\n%q", stdout)
	}

	stdout, _, code = capture(t, "Pruning", dir, "-c", "--no-filename")
	if code != 0 {
		t.Fatalf("exit = %d", code)
	}
	if stdout != "2\n" {
		t.Errorf("stdout = %q, want the bare tally", stdout)
	}

	stdout, _, code = capture(t, "Pruning", dir, "-l", "--color", "always")
	if code != 0 {
		t.Fatalf("exit = %d", code)
	}
	if !strings.Contains(stdout, "\x1b[35m") {
		t.Errorf("want the path coloured on its own:\n%q", stdout)
	}
}

// A plan prints its changes through the same printer a search does, so every
// flag that places a name or a number is honoured beside it.
func TestApplyTakesThePageFlags(t *testing.T) {
	for _, flag := range []string{"--heading", "--no-heading", "-H", "--no-filename", "--breadcrumb", "--no-breadcrumb"} {
		t.Run(flag, func(t *testing.T) {
			path := doc(t, sample)
			p := planFile(t, `{"path":"`+path+`","match":"verify checksum","op":"check"}`)
			_, stderr, code := capture(t, "--apply", p, "--dry-run", flag)
			if code != 0 {
				t.Fatalf("exit = %d, want 0:\n%s", code, stderr)
			}
		})
	}
}

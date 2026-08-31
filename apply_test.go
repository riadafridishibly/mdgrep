package main

import (
	"encoding/json"
	"fmt"
	"github.com/riadafridishibly/mdgrep/internal/report"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// planFile writes a plan file for one test and returns its path.
func planFile(t *testing.T, entries ...string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "plan.jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(entries, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// onStdin hands the run a plan the way a caller piping one does, and puts the
// real stdin back afterwards.
func onStdin(t *testing.T, text string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "stdin")
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	saved := os.Stdin
	os.Stdin = f
	t.Cleanup(func() { os.Stdin = saved; f.Close() })
}

const tasks = `# Guide

## Setup

- [ ] ship the docs
- [ ] ship the tests

## Usage

Call the binary.
`

func TestApplyRunsEveryEntryInOnePass(t *testing.T) {
	path := doc(t, tasks)
	p := planFile(t,
		`{"path":"`+path+`","match":"ship the docs","op":"check"}`,
		`{"path":"`+path+`","match":"^## Setup","op":"set-text","text":"Install"}`,
		`{"path":"`+path+`","match":"ship the tests","op":"check"}`,
	)
	_, stderr, code := capture(t, "--apply", p)
	if code != 0 {
		t.Fatalf("exit = %d, want 0; stderr:\n%s", code, stderr)
	}
	got := read(t, path)
	for _, want := range []string{"## Install", "- [x] ship the docs", "- [x] ship the tests"} {
		if !strings.Contains(got, want) {
			t.Errorf("file does not say %q:\n%s", want, got)
		}
	}
}

func TestApplyReadsThePlanFromStdin(t *testing.T) {
	path := doc(t, tasks)
	onStdin(t, `{"path":"`+path+`","match":"ship the docs","op":"check"}`+"\n")
	_, stderr, code := capture(t, "--apply", "-")
	if code != 0 {
		t.Fatalf("exit = %d, want 0; stderr:\n%s", code, stderr)
	}
	if !strings.Contains(read(t, path), "- [x] ship the docs") {
		t.Errorf("the box was not ticked:\n%s", read(t, path))
	}
}

// A plan is one instruction, so an entry that cannot be carried out stops the
// whole of it: half a plan applied is worse than none of it.
func TestApplyRefusesTheWholePlanForOneBadEntry(t *testing.T) {
	path := doc(t, tasks)
	p := planFile(t,
		`{"path":"`+path+`","match":"ship the docs","op":"check"}`,
		`{"path":"`+path+`","match":"ship","op":"check"}`,
	)
	_, stderr, code := capture(t, "--apply", p)
	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if !strings.Contains(stderr, "entry 2: 2 matches") {
		t.Errorf("stderr does not name the ambiguous entry:\n%s", stderr)
	}
	if !strings.Contains(stderr, "nothing was written") {
		t.Errorf("stderr does not say the plan was dropped:\n%s", stderr)
	}
	if read(t, path) != tasks {
		t.Errorf("the file was written anyway:\n%s", read(t, path))
	}
}

func TestApplyRefusesAnEntryThatMatchesNothing(t *testing.T) {
	path := doc(t, tasks)
	p := planFile(t, `{"path":"`+path+`","match":"ship the moon","op":"check"}`)
	_, stderr, code := capture(t, "--apply", p)
	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if !strings.Contains(stderr, "entry 1: nothing matched") {
		t.Errorf("stderr does not say which entry missed:\n%s", stderr)
	}
}

func TestApplyRefusalJSONCarriesTheEntryNumber(t *testing.T) {
	path := doc(t, tasks)
	p := planFile(t,
		`{"path":"`+path+`","match":"ship the docs","op":"check"}`,
		`{"path":"`+path+`","match":"ship","op":"check"}`,
	)
	_, stderr, code := capture(t, "--apply", p, "--json")
	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	var got report.Refusal
	line, _, _ := strings.Cut(stderr, "\n")
	if err := json.Unmarshal([]byte(line), &got); err != nil {
		t.Fatalf("stderr is not JSON: %v\n%s", err, stderr)
	}
	if got.Entry != 2 || got.Error != "ambiguous" || got.Total != 2 {
		t.Errorf("refusal = %+v, want entry 2, ambiguous, total 2", got)
	}
}

// A misspelled key would otherwise be a silently different edit.
func TestApplyRefusesAnUnknownKey(t *testing.T) {
	p := planFile(t, `{"path":"x.md","matches":"ship","op":"check"}`)
	_, stderr, code := capture(t, "--apply", p)
	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if !strings.Contains(stderr, `unknown field "matches"`) {
		t.Errorf("stderr does not name the key:\n%s", stderr)
	}
}

func TestApplyChecksWhatAnEntryAsksFor(t *testing.T) {
	tests := []struct {
		name  string
		entry string
		says  string
	}{
		{"no op", `{"path":"x.md","match":"a"}`, `no "op"`},
		{"unknown op", `{"path":"x.md","match":"a","op":"chekc"}`, `unknown op "chekc"`},
		{"no path", `{"match":"a","op":"check"}`, `no "path"`},
		{"no match", `{"path":"x.md","op":"check"}`, `no "match"`},
		{"text an op cannot use", `{"path":"x.md","match":"a","op":"check","text":"b"}`, `takes no "text"`},
		{"missing text", `{"path":"x.md","match":"a","op":"replace"}`, `wants "text"`},
		{"expect below one", `{"path":"x.md","match":"a","op":"check","expect":0}`, `above zero`},
		{"negative expand", `{"path":"x.md","match":"a","op":"check","expand":-1}`, "cannot be negative"},
		{"section on a node edit", `{"path":"x.md","match":"a","op":"check","section":true}`, "has nothing to widen"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, stderr, code := capture(t, "--apply", planFile(t, tt.entry))
			if code != 2 {
				t.Fatalf("exit = %d, want 2", code)
			}
			if !strings.Contains(stderr, tt.says) {
				t.Errorf("stderr does not say %q:\n%s", tt.says, stderr)
			}
		})
	}
}

// Two entries reaching for the same lines were planned against the same
// version of the file, so the second would rewrite text the first took away.
func TestApplyRefusesTwoEntriesOverTheSameLines(t *testing.T) {
	tests := []struct {
		name    string
		entries []string
		says    string
	}{
		{
			"the same node twice",
			[]string{
				`{"path":"%s","match":"^## Setup","op":"set-text","text":"Install"}`,
				`{"path":"%s","match":"^## Setup","op":"delete","section":true}`,
			},
			"entry 2 edits lines 3-7, which entry 1 already rewrites",
		},
		{
			"a node inside another entry's region",
			[]string{
				`{"path":"%s","match":"^## Setup","op":"replace","section":true,"text":"## Setup\n"}`,
				`{"path":"%s","match":"ship the docs","op":"check"}`,
			},
			"entry 2 edits line 5, which entry 1 already rewrites",
		},
		{
			"one of the nodes a multi entry took",
			[]string{
				`{"path":"%s","match":"ship","op":"check","multi":true}`,
				`{"path":"%s","match":"ship the tests","op":"uncheck"}`,
			},
			"entry 2 edits line 6, which entry 1 already rewrites",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := doc(t, tasks)
			entries := make([]string, len(tt.entries))
			for i, e := range tt.entries {
				entries[i] = fmt.Sprintf(e, path)
			}
			_, stderr, code := capture(t, "--apply", planFile(t, entries...))
			if code != 2 {
				t.Fatalf("exit = %d, want 2", code)
			}
			if !strings.Contains(stderr, tt.says) {
				t.Errorf("stderr does not say %q:\n%s", tt.says, stderr)
			}
			if read(t, path) != tasks {
				t.Errorf("the file was written anyway:\n%s", read(t, path))
			}
		})
	}
}

// Entries are planned against the file as it was read, so a plan is a set of
// independent edits rather than a script: an entry cannot reach for what an
// earlier one writes, and saying it did would be the plan quietly meaning
// something other than what it says.
func TestApplyCannotMatchWhatAnotherEntryWrites(t *testing.T) {
	path := doc(t, tasks)
	p := planFile(t,
		`{"path":"`+path+`","match":"^## Usage","op":"set-text","text":"Running"}`,
		`{"path":"`+path+`","match":"^## Running","op":"append","text":"see below"}`,
	)
	_, stderr, code := capture(t, "--apply", p)
	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if !strings.Contains(stderr, "entry 2: nothing matched") {
		t.Errorf("stderr does not refuse the second entry:\n%s", stderr)
	}
	if read(t, path) != tasks {
		t.Errorf("the file was written anyway:\n%s", read(t, path))
	}
}

// Every change carries the line numbers of the file as it was read, and they
// are applied in one pass, so an entry that lengthens the file early does not
// move what a later entry rewrites.
func TestApplyKeepsLaterEntriesInPlaceWhenAnEarlierOneShiftsTheFile(t *testing.T) {
	path := doc(t, tasks)
	p := planFile(t,
		`{"path":"`+path+`","match":"^# Guide","op":"append","text":"one\ntwo\nthree"}`,
		`{"path":"`+path+`","match":"Call the binary","op":"set-text","text":"Run the binary."}`,
		`{"path":"`+path+`","match":"ship the tests","op":"check"}`,
	)
	_, stderr, code := capture(t, "--apply", p)
	if code != 0 {
		t.Fatalf("exit = %d, want 0; stderr:\n%s", code, stderr)
	}
	want := `# Guide

one
two
three

## Setup

- [ ] ship the docs
- [x] ship the tests

## Usage

Run the binary.
`
	if got := read(t, path); got != want {
		t.Errorf("file =\n%s\nwant\n%s", got, want)
	}
}

// Two entries inserting at one point do not collide: neither takes any line
// away, so both land, in the order the plan wrote them.
func TestApplyTakesTwoInsertionsAtTheSamePoint(t *testing.T) {
	path := doc(t, "# Guide\n\nFirst para.\n\nSecond para.\n")
	p := planFile(t,
		`{"path":"`+path+`","match":"First para","op":"append","text":"after first"}`,
		`{"path":"`+path+`","match":"Second para","op":"prepend","text":"before second"}`,
	)
	_, stderr, code := capture(t, "--apply", p)
	if code != 0 {
		t.Fatalf("exit = %d, want 0; stderr:\n%s", code, stderr)
	}
	want := "# Guide\n\nFirst para.\n\nafter first\n\nbefore second\n\nSecond para.\n"
	if got := read(t, path); got != want {
		t.Errorf("file = %q, want %q", got, want)
	}
}

func TestApplyTakesTheKeysThatStandInForFlags(t *testing.T) {
	path := doc(t, tasks)
	p := planFile(t, `{"path":"`+path+`","match":"ship","op":"check","multi":true,"expect":2,"kind":"item"}`)
	_, stderr, code := capture(t, "--apply", p)
	if code != 0 {
		t.Fatalf("exit = %d, want 0; stderr:\n%s", code, stderr)
	}
	if got := read(t, path); strings.Count(got, "- [x]") != 2 {
		t.Errorf("both boxes were not ticked:\n%s", got)
	}
}

func TestApplyMatchesLiterallyWhenAskedTo(t *testing.T) {
	path := doc(t, "- [ ] ship v1.0 (final)\n- [ ] ship v1x0 final\n")
	p := planFile(t, `{"path":"`+path+`","match":"v1.0 (final)","op":"check","fixed":true}`)
	_, stderr, code := capture(t, "--apply", p)
	if code != 0 {
		t.Fatalf("exit = %d, want 0; stderr:\n%s", code, stderr)
	}
	if !strings.Contains(read(t, path), "- [x] ship v1.0 (final)") {
		t.Errorf("the literal match did not land:\n%s", read(t, path))
	}
}

func TestApplyDryRunWritesNothing(t *testing.T) {
	path := doc(t, tasks)
	p := planFile(t, `{"path":"`+path+`","match":"ship the docs","op":"check"}`)
	stdout, stderr, code := capture(t, "--apply", p, "--dry-run", "--format", "compact")
	if code != 0 {
		t.Fatalf("exit = %d, want 0; stderr:\n%s", code, stderr)
	}
	if !strings.Contains(stdout, "check\tdry") {
		t.Errorf("the edit was not reported as a dry run:\n%s", stdout)
	}
	if read(t, path) != tasks {
		t.Errorf("the file was written anyway:\n%s", read(t, path))
	}
}

// The plan says what to search for and what to do about it, so a flag that
// would have said either is a misunderstanding rather than an extra.
func TestApplyRefusesTheFlagsItSupersedes(t *testing.T) {
	p := planFile(t, `{"path":"x.md","match":"a","op":"check"}`)
	tests := []struct {
		name string
		args []string
		says string
	}{
		{"an edit flag", []string{"--apply", p, "--check"}, "nothing left for --check"},
		{"a search flag", []string{"--apply", p, "-k", "heading"}, "nothing left for -k"},
		{"a path", []string{"--apply", p, "docs"}, "no PATTERN and no PATH"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, stderr, code := capture(t, tt.args...)
			if code != 2 {
				t.Fatalf("exit = %d, want 2", code)
			}
			if !strings.Contains(stderr, tt.says) {
				t.Errorf("stderr does not say %q:\n%s", tt.says, stderr)
			}
		})
	}
}

func TestApplyWantsObjectsRatherThanAnArray(t *testing.T) {
	p := planFile(t, `[{"path":"x.md","match":"a","op":"check"}]`)
	_, stderr, code := capture(t, "--apply", p)
	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if !strings.Contains(stderr, "one JSON object per line") {
		t.Errorf("stderr does not say what shape it wants:\n%s", stderr)
	}
}

func TestApplyRefusesAnEmptyPlan(t *testing.T) {
	path := filepath.Join(t.TempDir(), "plan.jsonl")
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	_, stderr, code := capture(t, "--apply", path)
	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if !strings.Contains(stderr, "the plan is empty") {
		t.Errorf("stderr does not say the plan is empty:\n%s", stderr)
	}
}

func TestApplyReportsAFileItCannotRead(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "gone.md")
	p := planFile(t, `{"path":"`+missing+`","match":"a","op":"check"}`)
	_, stderr, code := capture(t, "--apply", p)
	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if !strings.Contains(stderr, "entry 1: ") || !strings.Contains(stderr, missing) {
		t.Errorf("stderr does not name the file:\n%s", stderr)
	}
}

// Two entries can name one file differently. Taking those for two files would
// plan each against the original and write the file twice, and the second write
// would undo the first.
func TestApplyTakesOneFileNamedTwoWaysAsOneFile(t *testing.T) {
	path := doc(t, tasks)
	alias := filepath.Dir(path) + "/./" + filepath.Base(path)

	t.Run("both edits land", func(t *testing.T) {
		p := planFile(t,
			`{"path":"`+path+`","match":"ship the docs","op":"check"}`,
			`{"path":"`+alias+`","match":"ship the tests","op":"check"}`,
		)
		_, stderr, code := capture(t, "--apply", p)
		if code != 0 {
			t.Fatalf("exit = %d, want 0; stderr:\n%s", code, stderr)
		}
		if got := read(t, path); strings.Count(got, "- [x]") != 2 {
			t.Errorf("an edit was written over:\n%s", got)
		}
	})

	t.Run("a collision through the other name is still a collision", func(t *testing.T) {
		if err := os.WriteFile(path, []byte(tasks), 0o644); err != nil {
			t.Fatal(err)
		}
		p := planFile(t,
			`{"path":"`+path+`","match":"ship the docs","op":"check"}`,
			`{"path":"`+alias+`","match":"ship the docs","op":"uncheck"}`,
		)
		_, stderr, code := capture(t, "--apply", p)
		if code != 2 {
			t.Fatalf("exit = %d, want 2", code)
		}
		if !strings.Contains(stderr, "entry 2 edits line 5, which entry 1 already rewrites") {
			t.Errorf("stderr does not name the pair:\n%s", stderr)
		}
		if read(t, path) != tasks {
			t.Errorf("the file was written anyway:\n%s", read(t, path))
		}
	})
}

// Two spellings of one path are one file, however many entries use either. The
// second and later entries under an alias answer from the cache, and they have
// to answer with the name the plan first used or their changes would be
// gathered apart from the rest and the file written twice.
func TestApplyGathersEverySpellingOfOnePath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "notes.md")
	if err := os.WriteFile(path, []byte(tasks), 0o644); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(dir, ".", "notes.md")
	p := planFile(t,
		fmt.Sprintf(`{"path":%q,"match":"ship the docs","op":"check"}`, path),
		fmt.Sprintf(`{"path":%q,"match":"ship the tests","op":"check"}`, alias),
		fmt.Sprintf(`{"path":%q,"match":"^## Setup","op":"set-text","text":"Install"}`, alias),
	)
	_, stderr, code := capture(t, "--apply", p)
	if code != 0 {
		t.Fatalf("exit = %d (%s)", code, stderr)
	}
	got := read(t, path)
	for _, want := range []string{"- [x] ship the docs", "- [x] ship the tests", "## Install"} {
		if !strings.Contains(got, want) {
			t.Errorf("want %q; every entry has to reach one copy of the file:\n%s", want, got)
		}
	}
}

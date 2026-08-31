package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A pipeline in one process narrows the same way a pipe of streams does: each
// stage searches only inside the nodes the stage before it selected.
func TestThenNarrowsThroughEveryStage(t *testing.T) {
	path := doc(t, pipedDoc)

	out, stderr, code := capture(t, "^## Some header", "--section", path,
		"--then", "", "-k", "list",
		"--then", "", "--todo")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	// The line numbers are the file's own, and the box outside the section the
	// first stage chose never comes into it.
	for _, want := range []string{"alpha task", "gamma task", " 8 │", "13 │", path} {
		if !strings.Contains(out, want) {
			t.Errorf("out = %q, want %s", out, want)
		}
	}
	if strings.Contains(out, "delta task") {
		t.Errorf("a stage reached past the section it was given:\n%s", out)
	}
}

// --then and a pipe of --stream are the same pipeline, one with a process
// boundary in it and one without, so they have to answer alike.
func TestThenAgreesWithAPipeOfStreams(t *testing.T) {
	path := doc(t, pipedDoc)

	once, _, code := capture(t, "^## Some header", "--section", path,
		"--then", "", "-k", "list",
		"--then", "", "--todo", "--json")
	if code != 0 {
		t.Fatalf("--then exit %d", code)
	}

	first, _, _ := capture(t, "^## Some header", "--section", path, "--stream")
	second, _, _ := runStage(t, first, "", "-k", "list", "--stream")
	piped, _, code := runStage(t, second, "", "--todo", "--json")
	if code != 0 {
		t.Fatalf("stream exit %d", code)
	}
	if once != piped {
		t.Errorf("--then and --stream disagree:\n--then:\n%s\n--stream:\n%s", once, piped)
	}
}

// The last stage holds real paths, so a pipeline can end in the change it was
// narrowing towards.
func TestThenEndsInAnEdit(t *testing.T) {
	path := doc(t, pipedDoc)

	_, stderr, code := capture(t, "^## Some header", "--section", path,
		"--then", "-k", "list",
		"--then", "--todo", "--check", "--multi")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, stderr)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	got := string(data)
	for _, want := range []string{"- [x] alpha task", "- [x] gamma task"} {
		if !strings.Contains(got, want) {
			t.Errorf("want %q in:\n%s", want, got)
		}
	}
	if !strings.Contains(got, "- [ ] delta task") {
		t.Errorf("the edit reached outside the stages' regions:\n%s", got)
	}
}

// Only the first stage names files, so a word on a later one can only be its
// pattern -- and a stage that writes none selects by its filters alone.
func TestStageWithNoPatternSelectsByFilterAlone(t *testing.T) {
	path := doc(t, pipedDoc)

	out, stderr, code := capture(t, "^### Sub", "--section", path, "--then", "--todo")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	if !strings.Contains(out, "gamma task") || strings.Contains(out, "alpha task") {
		t.Errorf("out = %q, want the sub-section's box and no others", out)
	}
}

// A stage that selects nothing ends the file there: there is nothing left for
// the next stage to look inside, and the run says so the way any search that
// found nothing does.
func TestStageThatSelectsNothingEndsTheRun(t *testing.T) {
	path := doc(t, pipedDoc)

	out, _, code := capture(t, "^## Nowhere", "--section", path, "--then", "--todo")
	if code != 1 {
		t.Errorf("exit = %d, want 1", code)
	}
	if out != "" {
		t.Errorf("out = %q, want nothing", out)
	}
}

// Narrowing goes by containment: a node a region holds whole is a candidate,
// and one it would have cut in half is not.
func TestThenAdmitsOnlyWholeNodes(t *testing.T) {
	path := doc(t, pipedDoc)

	item, _, code := capture(t, "alpha task", path, "--then", "-k", "item")
	if code != 0 || !strings.Contains(item, "alpha task") {
		t.Errorf("the item the region holds should still match: exit %d, %q", code, item)
	}
	list, _, code := capture(t, "alpha task", path, "--then", "-k", "list")
	if code != 1 {
		t.Errorf("a list straddling the region should not match: exit %d, %q", code, list)
	}
}

// A stage carries the whole search vocabulary, which is the point of spelling
// one as a command line: --fuzzy ranks and -m caps inside a stage, so the
// first one can pick the section that fits best before anything looks in it.
func TestAStageRanksAndCapsOnItsOwn(t *testing.T) {
	path := doc(t, pipedDoc)

	out, stderr, code := capture(t, "some hdr", "--fuzzy", "--min-score", "0.4",
		"-k", "heading", "--section", "-m", "1", path,
		"--then", "--todo")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	if !strings.Contains(out, "alpha task") || strings.Contains(out, "delta task") {
		t.Errorf("out = %q, want the best-scoring section's boxes only", out)
	}
}

// A pipeline narrows every file it was given, each on its own account.
func TestThenNarrowsEveryFile(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"a.md", "b.md"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(pipedDoc), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	out, stderr, code := capture(t, "^## Some header", "--section", dir, "--then", "--todo", "-c")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	for _, name := range []string{"a.md:2", "b.md:2"} {
		if !strings.Contains(out, name) {
			t.Errorf("out = %q, want %s", out, name)
		}
	}
}

// The last stage of a pipeline is a stage like any other, so it can hand its
// nodes to a further mdgrep across a pipe.
func TestThenCanEndInAStream(t *testing.T) {
	path := doc(t, pipedDoc)

	out, stderr, code := capture(t, "^## Some header", "--section", path,
		"--then", "-k", "list", "--stream")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	next, _, code := runStage(t, out, "", "--todo")
	if code != 0 || !strings.Contains(next, "alpha task") {
		t.Errorf("the stream should carry on: exit %d, %q", code, next)
	}
}

// Each stage does one job: the first names the files, the last prints or
// writes, and the ones between select. A flag given where it would be read and
// then change nothing is refused by name.
func TestThenKeepsEveryStageInItsPlace(t *testing.T) {
	path := doc(t, pipedDoc)
	tests := []struct {
		name string
		args []string
		says string
	}{
		{"prints", []string{"", path, "--json", "--then", "--todo"}, "only the last stage prints"},
		{"counts", []string{"", path, "-c", "--then", "--todo"}, "only the last stage prints"},
		{"reads", []string{"", path, "--then", "--todo", "--ext", "md"}, "reads its files once"},
		{"hidden", []string{"", path, "--then", "--todo", "--hidden"}, "reads its files once"},
		{"edits", []string{"", path, "--check", "--then", "--todo"}, "belongs on the last stage"},
		{"expects", []string{"", path, "--expect", "1", "--then", "--todo"}, "belongs on the last stage"},
		{"plans", []string{"", path, "--then", "--todo", "--apply", "-"}, "a whole run rather than a stage"},
		{"asks", []string{"-h", "--then", "--todo"}, "takes no --then"},
		{"version", []string{"", path, "--then", "--todo", "-V"}, "takes no --then"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, stderr, code := capture(t, tt.args...)
			if code != 2 {
				t.Fatalf("exit = %d, want 2", code)
			}
			if !strings.Contains(stderr, tt.says) {
				t.Errorf("stderr = %q, want %q", stderr, tt.says)
			}
		})
	}
}

// --then joins two searches, so it wants one on each side of it.
func TestThenWantsAStageOnEachSide(t *testing.T) {
	path := doc(t, pipedDoc)
	tests := []struct {
		name string
		args []string
		says string
	}{
		{"leading", []string{"--then", "", path}, "wants one before it"},
		{"trailing", []string{"", path, "--then"}, "wants one after it"},
		{"doubled", []string{"", path, "--then", "--then", "--todo"}, "two --then in a row"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, stderr, code := capture(t, tt.args...)
			if code != 2 {
				t.Fatalf("exit = %d, want 2", code)
			}
			if !strings.Contains(stderr, tt.says) {
				t.Errorf("stderr = %q, want %q", stderr, tt.says)
			}
		})
	}
}

// A later stage searches what it was handed, so it has no files to name and a
// second word on it is a path nothing would read.
func TestALaterStageNamesNoFiles(t *testing.T) {
	path := doc(t, pipedDoc)
	tests := []struct {
		name string
		args []string
		says string
	}{
		{"two words", []string{"", path, "--then", "", path}, "one PATTERN at most"},
		{"pattern twice", []string{"", path, "--then", "-e", "x", "y"}, "has nowhere to go"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, stderr, code := capture(t, tt.args...)
			if code != 2 {
				t.Fatalf("exit = %d, want 2", code)
			}
			if !strings.Contains(stderr, tt.says) {
				t.Errorf("stderr = %q, want %q", stderr, tt.says)
			}
		})
	}
}

// A message about a flag says which of several look-alike command lines it is
// about, and a run of one stage says nothing about stages at all.
func TestAMessageNamesTheStageItIsAbout(t *testing.T) {
	path := doc(t, pipedDoc)

	_, stderr, code := capture(t, "", path, "--then", "", "-k", "nonsense")
	if code != 2 || !strings.Contains(stderr, "stage 2 of 2") {
		t.Errorf("exit %d, stderr %q", code, stderr)
	}
	_, stderr, code = capture(t, "", path, "-k", "nonsense")
	if code != 2 || strings.Contains(stderr, "stage") {
		t.Errorf("a single stage should not be numbered: exit %d, stderr %q", code, stderr)
	}
}

// A pipeline written as one string runs the pipeline it spells, so the two
// spellings are interchangeable and a query can be kept in a variable.
func TestExecRunsThePipelineItSpells(t *testing.T) {
	path := doc(t, pipedDoc)

	spelled, stderr, code := capture(t, "--exec",
		`"^## Some header" --section `+path+` | -k list | --todo --json`)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	along, _, code := capture(t, "^## Some header", "--section", path,
		"--then", "-k", "list",
		"--then", "--todo", "--json")
	if code != 0 {
		t.Fatalf("--then exit %d", code)
	}
	if spelled != along {
		t.Errorf("--exec and --then disagree:\n--exec:\n%s\n--then:\n%s", spelled, along)
	}
}

// Only paths may stand beside --exec, and they belong to the stage that walks
// them, so one query can be pointed at whichever files the caller means.
func TestExecTakesItsPathsBesideIt(t *testing.T) {
	path := doc(t, pipedDoc)

	out, stderr, code := capture(t, "--exec", `"^### Sub" --section | --todo`, path)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	if !strings.Contains(out, "gamma task") || strings.Contains(out, "alpha task") {
		t.Errorf("out = %q, want the sub-section's box and no others", out)
	}
	if _, stderr, code := capture(t, "--exec", `"" --todo`, path, "--json"); code != 2 ||
		!strings.Contains(stderr, "belongs inside it") {
		t.Errorf("a flag beside --exec: exit %d, stderr %q", code, stderr)
	}
}

// The quoting inside --exec is mdgrep's own, so a pattern is free to hold the
// character that divides two stages.
func TestExecKeepsAPipeInAPattern(t *testing.T) {
	path := doc(t, pipedDoc)

	out, stderr, code := capture(t, "--exec", `"(alpha|delta) task" `+path)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	for _, want := range []string{"alpha task", "delta task"} {
		if !strings.Contains(out, want) {
			t.Errorf("out = %q, want %s", out, want)
		}
	}
}

// The last stage of a spelled pipeline writes, the same as the last stage of
// one written along the line.
func TestExecEndsInAnEdit(t *testing.T) {
	path := doc(t, pipedDoc)

	_, stderr, code := capture(t, "--exec",
		`"^### Sub" --section `+path+` | --todo | --check`)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	got := string(data)
	if !strings.Contains(got, "- [x] gamma task") {
		t.Errorf("the edit did not reach the file:\n%s", got)
	}
	if !strings.Contains(got, "- [ ] alpha task") {
		t.Errorf("the edit reached outside the stages' regions:\n%s", got)
	}
}

// A stage of a spelled pipeline is held to its place the way one written along
// the line is, and a message says which stage it is about.
func TestExecKeepsEveryStageInItsPlace(t *testing.T) {
	path := doc(t, pipedDoc)

	_, stderr, code := capture(t, "--exec", `"" `+path+` --json | --todo`)
	if code != 2 || !strings.Contains(stderr, "only the last stage prints") {
		t.Errorf("exit %d, stderr %q", code, stderr)
	}
	if !strings.Contains(stderr, "stage 1 of 2") {
		t.Errorf("stderr = %q, want the stage named", stderr)
	}
}

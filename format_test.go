package main

import (
	"os"
	"strings"
	"testing"
)

// ladder is the document the two edit formats are checked against: enough lines
// either side of a change for a hunk to have its full context, and two nodes
// far enough apart to fall in separate hunks.
const ladder = `# Doc

alpha

beta

gamma

delta

epsilon

zeta
`

// A patch says what the edit planned. edit.Plan already knows the exact lines
// that go and the exact lines that come, so the hunk is those lines with the
// context around them, numbered against the file they were planned on.
func TestDiffIsAUnifiedPatch(t *testing.T) {
	path := doc(t, ladder)
	stdout, stderr, code := capture(t, "alpha", path, "--replace-node", "REPL", "--format", "diff")
	if code != 0 {
		t.Fatalf("exit = %d (%s)", code, stderr)
	}
	want := "@@ -1,6 +1,6 @@\n # Doc\n \n-alpha\n+REPL\n \n beta\n \n"
	if !strings.HasSuffix(stdout, want) {
		t.Errorf("want a hunk reading\n%s\ngot\n%s", want, stdout)
	}
	// doc() hands back an absolute path, which takes no "a/": prefixing one
	// would make a name no strip level turns back into the file.
	if !strings.HasPrefix(stdout, "--- "+path+"\n+++ "+path+"\n") {
		t.Errorf("want the file named on both sides:\n%s", stdout)
	}
	if read(t, path) != ladder {
		t.Errorf("a patch is not a write, but the file changed:\n%s", read(t, path))
	}
}

// Changes far enough apart are separate hunks, and the second one's new side
// is numbered against every line the first added or removed.
func TestDiffNumbersTheSecondHunkAgainstTheFirst(t *testing.T) {
	path := doc(t, ladder)
	stdout, stderr, code := capture(t,
		"alpha|zeta", path, "--replace-node", "one\ntwo", "--multi", "--format", "diff")
	if code != 0 {
		t.Fatalf("exit = %d (%s)", code, stderr)
	}
	heads := []string{}
	for _, line := range strings.Split(stdout, "\n") {
		if strings.HasPrefix(line, "@@") {
			heads = append(heads, line)
		}
	}
	want := []string{"@@ -1,6 +1,7 @@", "@@ -10,4 +11,5 @@"}
	if len(heads) != 2 || heads[0] != want[0] || heads[1] != want[1] {
		t.Errorf("want hunks %v, got %v:\n%s", want, heads, stdout)
	}
}

// A change and its neighbour inside twice the context share a hunk: the lines
// between them are context for both, and printing them twice would make a
// patch that overlaps itself.
func TestDiffRunsNeighbouringChangesIntoOneHunk(t *testing.T) {
	path := doc(t, ladder)
	stdout, stderr, code := capture(t,
		"alpha|beta", path, "--replace-node", "X", "--multi", "--format", "diff")
	if code != 0 {
		t.Fatalf("exit = %d (%s)", code, stderr)
	}
	if n := strings.Count(stdout, "@@ -"); n != 1 {
		t.Errorf("want one hunk, got %d:\n%s", n, stdout)
	}
}

// A file that does not end in a newline says so, which is what lets patch put
// it back the way it found it.
func TestDiffMarksAMissingFinalNewline(t *testing.T) {
	path := doc(t, "# Doc\n\nalpha")
	stdout, stderr, code := capture(t, "alpha", path, "--replace-node", "REPL", "--format", "diff")
	if code != 0 {
		t.Fatalf("exit = %d (%s)", code, stderr)
	}
	if n := strings.Count(stdout, "\\ No newline at end of file"); n != 2 {
		t.Errorf("want the note on both sides of the last line:\n%s", stdout)
	}
}

// A node already as asked contributes no line to a patch, so a file whose
// every change is a no-op prints nothing rather than a header with no hunk.
func TestDiffLeavesOutANodeAlreadyAsAsked(t *testing.T) {
	path := doc(t, "# Doc\n\n- [x] done\n")
	stdout, stderr, code := capture(t, "done", path, "--check", "--format", "diff")
	if code != 0 {
		t.Fatalf("exit = %d (%s)", code, stderr)
	}
	if stdout != "" {
		t.Errorf("want nothing for a change that changes nothing:\n%s", stdout)
	}
}

// --format doc is what --write would have put in the file, on stdout instead.
func TestDocPrintsTheDocumentTheEditProduced(t *testing.T) {
	path := doc(t, ladder)
	stdout, stderr, code := capture(t, "alpha", path, "--replace-node", "REPL", "--format", "doc")
	if code != 0 {
		t.Fatalf("exit = %d (%s)", code, stderr)
	}
	if want := strings.Replace(ladder, "alpha", "REPL", 1); stdout != want {
		t.Errorf("want the edited document:\n%s\ngot\n%s", want, stdout)
	}
	if read(t, path) != ladder {
		t.Errorf("--format doc wrote the file:\n%s", read(t, path))
	}
}

// The format means the document as the edit left it, and an edit that matched
// nothing left it as it was. Printing it is what keeps a miss from emptying
// the file the run was redirected into.
func TestDocPassesTheDocumentThroughOnAMiss(t *testing.T) {
	path := doc(t, ladder)
	stdout, _, code := capture(t, "nothing here", path, "--replace-node", "X", "--format", "doc")
	if code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
	if stdout != ladder {
		t.Errorf("want the document unchanged:\n%s", stdout)
	}
}

// A refusal is not a miss: the run failed rather than found nothing to do, so
// nothing is printed and the caller is told why.
func TestDocPrintsNothingWhenTheEditIsRefused(t *testing.T) {
	path := doc(t, ladder)
	stdout, stderr, code := capture(t, "alpha|zeta", path, "--replace-node", "X", "--format", "doc")
	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if stdout != "" {
		t.Errorf("a refused edit printed a document:\n%s", stdout)
	}
	if !strings.Contains(stderr, "--multi") {
		t.Errorf("want the refusal to say how to narrow:\n%s", stderr)
	}
}

// Two documents run together are not a document: nothing in the format says
// where one ends, so the run is refused rather than answered.
func TestDocRefusesMoreThanOneFile(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"a.md", "b.md"} {
		if err := os.WriteFile(dir+"/"+name, []byte(ladder), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	_, stderr, code := capture(t, "alpha", dir, "--replace-node", "X", "--multi", "--format", "doc")
	if code != 2 {
		t.Fatalf("exit = %d, want 2 (%s)", code, stderr)
	}
	if !strings.Contains(stderr, "2 files are not one document") {
		t.Errorf("want the count in the refusal:\n%s", stderr)
	}
}

// Both formats name what an edit produced, so neither has anything to say
// about a search.
func TestEditFormatsNeedAnEdit(t *testing.T) {
	for _, f := range []string{"diff", "doc"} {
		_, stderr, code := capture(t, "alpha", doc(t, ladder), "--format", f)
		if code != 2 {
			t.Errorf("--format %s: exit = %d, want 2", f, code)
		}
		if !strings.Contains(stderr, "there is no edit here") {
			t.Errorf("--format %s: want the reason:\n%s", f, stderr)
		}
	}
}

// Neither format prints a ladder, so a flag that lays one out is refused by
// name rather than read and dropped.
func TestEditFormatsRefuseThePageFlags(t *testing.T) {
	for _, flag := range []string{"-n", "-H", "--heading", "--span", "--breadcrumb"} {
		_, stderr, code := capture(t,
			"alpha", doc(t, ladder), "--replace-node", "X", "--format", "diff", flag)
		if code != 2 {
			t.Errorf("%s: exit = %d, want 2", flag, code)
		}
		if !strings.Contains(stderr, "to lay out") {
			t.Errorf("%s: want the reason:\n%s", flag, stderr)
		}
	}
}

// An edit reads a document to work out the change, which a pipe can answer
// for. The input shape does not choose the output: a piped document reports
// itself the way a named one does.
func TestAnEditOnStdinReportsTheChange(t *testing.T) {
	onStdin(t, ladder)
	stdout, stderr, code := capture(t, "alpha", "--replace-node", "REPL")
	if code != 0 {
		t.Fatalf("exit = %d (%s)", code, stderr)
	}
	for _, want := range []string{"- alpha", "+ REPL"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("want %q in the report:\n%s", want, stdout)
		}
	}
}

// The filter shape: a document in, the document the edit produced out.
func TestAnEditOnStdinPrintsTheDocument(t *testing.T) {
	onStdin(t, ladder)
	stdout, stderr, code := capture(t, "alpha", "--replace-node", "REPL", "--format", "doc")
	if code != 0 {
		t.Fatalf("exit = %d (%s)", code, stderr)
	}
	if want := strings.Replace(ladder, "alpha", "REPL", 1); stdout != want {
		t.Errorf("want the edited document:\n%s\ngot\n%s", want, stdout)
	}
}

// Writing the change back needs somewhere to write it, and a pipe is not a
// place.
func TestWriteRefusesStdin(t *testing.T) {
	onStdin(t, ladder)
	_, stderr, code := capture(t, "alpha", "--replace-node", "REPL", "-W")
	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if !strings.Contains(stderr, "stdin is not one") {
		t.Errorf("want the reason:\n%s", stderr)
	}
}

// A plan is an edit like any other, so the formats an edit has are the
// formats a plan has.
func TestPlanTakesTheEditFormats(t *testing.T) {
	t.Run("diff", func(t *testing.T) {
		path := doc(t, ladder)
		p := planFile(t, `{"path":`+quote(path)+`,"match":"alpha","op":"replace-node","text":"REPL"}`)
		stdout, stderr, code := capture(t, "--apply", p, "--format", "diff")
		if code != 0 {
			t.Fatalf("exit = %d (%s)", code, stderr)
		}
		if !strings.Contains(stdout, "-alpha\n+REPL\n") {
			t.Errorf("want the change as a patch:\n%s", stdout)
		}
	})
	t.Run("doc", func(t *testing.T) {
		path := doc(t, ladder)
		p := planFile(t, `{"path":`+quote(path)+`,"match":"alpha","op":"replace-node","text":"REPL"}`)
		stdout, stderr, code := capture(t, "--apply", p, "--format", "doc")
		if code != 0 {
			t.Fatalf("exit = %d (%s)", code, stderr)
		}
		if want := strings.Replace(ladder, "alpha", "REPL", 1); stdout != want {
			t.Errorf("want the edited document:\n%s\ngot\n%s", want, stdout)
		}
	})
	t.Run("doc refuses two files", func(t *testing.T) {
		first, second := doc(t, ladder), doc(t, ladder)
		p := planFile(t,
			`{"path":`+quote(first)+`,"match":"alpha","op":"replace-node","text":"X"}`,
			`{"path":`+quote(second)+`,"match":"zeta","op":"replace-node","text":"Y"}`,
		)
		_, stderr, code := capture(t, "--apply", p, "--format", "doc")
		if code != 2 {
			t.Fatalf("exit = %d, want 2", code)
		}
		if !strings.Contains(stderr, "not one document") {
			t.Errorf("want the refusal:\n%s", stderr)
		}
	})
}

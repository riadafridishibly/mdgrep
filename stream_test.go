package main

import (
	"os"
	"strings"
	"testing"
)

// piped is the document every pipeline test narrows down: two sections, a
// list with a plain bullet among its task items, and a nested list under a
// sub-heading, so a stage can select something a later stage has to narrow.
const pipedDoc = `# Project

## Some header

Intro text.

- plain bullet
- [ ] alpha task
- [x] beta task

### Sub

- [ ] gamma task

## Other header

- [ ] delta task
`

// stage runs one stage of a pipeline: what an earlier stage wrote arrives on
// stdin, and what this one writes comes back for the next.
func stage(t *testing.T, stdin string, args ...string) (out, errOut string, code int) {
	t.Helper()
	onStdin(t, stdin)
	return capture(t, args...)
}

// A stream is the whole point of the feature: each stage hands on the regions
// it selected, and the next searches only inside them.
func TestStreamNarrowsThroughEveryStage(t *testing.T) {
	path := doc(t, pipedDoc)

	first, stderr, code := capture(t, "^## Some header", "--section", path, "--stream")
	if code != 0 {
		t.Fatalf("stage 1 exit %d: %s", code, stderr)
	}
	if want := `{"path":"` + path + `","start":3,"end":13}`; !strings.Contains(first, want) {
		t.Fatalf("stage 1 = %q, want a region %s", first, want)
	}

	second, stderr, code := stage(t, first, "", "-k", "list", "--stream")
	if code != 0 {
		t.Fatalf("stage 2 exit %d: %s", code, stderr)
	}
	for _, want := range []string{`"start":7,"end":9`, `"start":13,"end":13`} {
		if !strings.Contains(second, want) {
			t.Errorf("stage 2 = %q, want %s", second, want)
		}
	}

	third, stderr, code := stage(t, second, "", "--todo")
	if code != 0 {
		t.Fatalf("stage 3 exit %d: %s", code, stderr)
	}
	// The line numbers are the file's own, not the fragment's, and the box
	// outside the section the first stage chose never comes into it.
	for _, want := range []string{"alpha task", "gamma task", " 8 │", "13 │", path} {
		if !strings.Contains(third, want) {
			t.Errorf("stage 3 = %q, want %s", third, want)
		}
	}
	if strings.Contains(third, "delta task") {
		t.Errorf("stage 3 reached past the section it was given:\n%s", third)
	}
}

// A pipe of text loses the path, which loses the edit with it. A pipe of
// regions keeps both, so a pipeline can end in the change it was narrowing
// towards.
func TestStreamEndsInAnEdit(t *testing.T) {
	path := doc(t, pipedDoc)

	first, _, _ := capture(t, "^## Some header", "--section", path, "--stream")
	second, _, _ := stage(t, first, "", "-k", "list", "--stream")
	_, stderr, code := stage(t, second, "", "--todo", "--check", "--multi")
	if code != 0 {
		t.Fatalf("edit exit %d: %s", code, stderr)
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
		t.Errorf("the edit reached outside the stream's regions:\n%s", got)
	}
}

// Narrowing goes by containment: a node the region holds whole is a
// candidate, and one it would have cut in half is not.
func TestStreamAdmitsOnlyWholeNodes(t *testing.T) {
	path := doc(t, pipedDoc)

	// Line 8 alone is one item of a list that runs from 7 to 9.
	one := `{"mdgrep":1}` + "\n" + `{"path":"` + path + `","start":8,"end":8}` + "\n"

	item, _, code := stage(t, one, "", "-k", "item")
	if code != 0 || !strings.Contains(item, "alpha task") {
		t.Errorf("the item the region holds should still match: exit %d, %q", code, item)
	}
	list, _, code := stage(t, one, "", "-k", "list")
	if code != 1 {
		t.Errorf("a list straddling the region should not match: exit %d, %q", code, list)
	}
}

// A stream says it is one up front, and says so even when nothing matched: a
// search that ran and selected nothing is not the same as no search at all.
func TestStreamHeaderIsWrittenWithoutResults(t *testing.T) {
	path := doc(t, pipedDoc)

	out, _, code := capture(t, "nothing here matches", path, "--stream")
	if code != 1 {
		t.Errorf("exit = %d, want 1", code)
	}
	if strings.TrimSpace(out) != `{"mdgrep":1}` {
		t.Errorf("empty stream = %q, want the header alone", out)
	}
	if _, _, code := stage(t, out, "", "--todo"); code != 1 {
		t.Errorf("an empty stream selects nothing: exit = %d, want 1", code)
	}
}

// Markdown on stdin is still markdown. The header is what tells the two
// apart, so a document keeps arriving as one.
func TestMarkdownOnStdinIsStillMarkdown(t *testing.T) {
	out, _, code := stage(t, pipedDoc, "alpha task")
	if code != 0 || !strings.Contains(out, "alpha task") {
		t.Fatalf("exit %d, out %q", code, out)
	}
	if !strings.Contains(out, "<stdin>") {
		t.Errorf("a document read from stdin is still <stdin>: %q", out)
	}
}

// A stream carries the files it is about, so a PATH beside it names a second
// set the stream said nothing about. "-" is how a stage says outright that it
// reads stdin, and mixing the two is refused rather than half honoured.
func TestStreamBesideAPathIsRefused(t *testing.T) {
	path := doc(t, pipedDoc)
	first, _, _ := capture(t, "^## Some header", "--section", path, "--stream")

	_, stderr, code := stage(t, first, "", "-", path)
	if code != 2 || !strings.Contains(stderr, "takes no PATH") {
		t.Errorf("exit %d, stderr %q", code, stderr)
	}
}

// A stage naming "-" reads the stream the way one naming nothing does: grep
// spells stdin that way, and a pipeline is clearer for saying so.
func TestStreamIsReadWhenStdinIsNamed(t *testing.T) {
	path := doc(t, pipedDoc)
	first, _, _ := capture(t, "^## Some header", "--section", path, "--stream")

	out, stderr, code := stage(t, first, "", "-", "--todo")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	if !strings.Contains(out, "alpha task") || strings.Contains(out, "delta task") {
		t.Errorf("out = %q, want the section's boxes and no others", out)
	}
}

// A stream is a stage in the middle of a pipeline, so the flags that end one
// or decorate a printed page are refused rather than quietly dropped.
func TestStreamRefusesWhatItCannotHonour(t *testing.T) {
	path := doc(t, pipedDoc)
	tests := []struct {
		name string
		args []string
		says string
	}{
		{"edit", []string{"", path, "--stream", "--check"}, "belongs on that stage"},
		{"plan", []string{"", path, "--stream", "--apply", "-"}, "belongs on that stage"},
		{"count", []string{"", path, "--stream", "-c"}, "nothing for -c to say"},
		{"files", []string{"", path, "--stream", "-l"}, "nothing for -l to say"},
		{"quiet", []string{"", path, "--stream", "-q"}, "nothing for -q to say"},
		{"truncate", []string{"", path, "--stream", "--truncate", "2"}, "nothing for --truncate to say"},
		{"outline", []string{path, "--stream", "--outline"}, "--outline is its own format"},
		{"json", []string{"", path, "--stream", "--json"}, "ask for one"},
		{"format", []string{"", path, "--stream", "--format", "json"}, "ask for different output"},
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

// A record that cannot be read refuses the run. The regions are the whole
// subject of the search here, so one lost to a typo is a search over less
// than was asked for, which would come back as "no matches" and be believed.
func TestBrokenStreamRefusesTheRun(t *testing.T) {
	tests := []struct {
		name, text, says string
	}{
		{"version", `{"mdgrep":99}` + "\n", "this mdgrep speaks 1"},
		{"unknown key", `{"mdgrep":1}` + "\n" + `{"path":"a.md","start":1,"end":2,"kind":"item"}` + "\n", `unknown field "kind"`},
		{"zero start", `{"mdgrep":1}` + "\n" + `{"path":"a.md","start":0,"end":2}` + "\n", "a line number starts at 1"},
		{"backwards", `{"mdgrep":1}` + "\n" + `{"path":"a.md","start":4,"end":2}` + "\n", "is before start"},
		{"no path", `{"mdgrep":1}` + "\n" + `{"start":1,"end":2}` + "\n", "no path"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, stderr, code := stage(t, tt.text, "")
			if code != 2 {
				t.Fatalf("exit = %d, want 2", code)
			}
			if !strings.Contains(stderr, tt.says) {
				t.Errorf("stderr = %q, want %q", stderr, tt.says)
			}
		})
	}
}

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The properties here hold for every result, not for one worked example. They
// run over the repository's own long documents, which is the closest thing to
// real input the suite has.

type spanJSON struct {
	Kind  string `json:"kind"`
	Start int    `json:"start"`
	End   int    `json:"end"`
}

type resultJSON struct {
	Path  string     `json:"path"`
	Kind  string     `json:"kind"`
	Start int        `json:"start"`
	End   int        `json:"end"`
	Spans []spanJSON `json:"spans"`
}

// realDocs are the long hand-written documents in the repository. They carry
// nesting no fixture bothers to build: tables inside sections, fences inside
// list items, headings four deep.
func realDocs() []string { return []string{"README.md", "SPEC.md"} }

// results runs one search in dir and decodes what --format json wrote.
func results(t *testing.T, dir string, args ...string) []resultJSON {
	t.Helper()
	defer inDir(t, dir)()
	out, _, _ := capture(t, append(args, "--format", "json")...)
	var got []resultJSON
	for _, line := range strings.Split(out, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var r resultJSON
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			t.Fatalf("decoding %q: %v", line, err)
		}
		got = append(got, r)
	}
	return got
}

// copyIn puts one of the repository's documents in a scratch directory under
// its own name, so a run can name it without a path.
func copyIn(t *testing.T, name string) string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("..", "..", name))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, name), body, 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// TestAtTakesBackEverySpanTheNoteNames is the promise the span note rests on:
// the note is written so the next run can ask for one of its entries by the
// numbers it printed, and get exactly those lines.
func TestAtTakesBackEverySpanTheNoteNames(t *testing.T) {
	for _, name := range realDocs() {
		t.Run(name, func(t *testing.T) {
			dir := copyIn(t, name)
			lines := strings.Split(readFile(t, filepath.Join(dir, name)), "\n")
			checked := 0
			for _, res := range results(t, dir, "", name) {
				for _, sp := range res.Spans {
					checked++
					want := strings.Join(lines[sp.Start-1:sp.End], "\n")
					got := func() string {
						defer inDir(t, dir)()
						out, _, _ := capture(t, "--at", span(sp.Start, sp.End), name, "--no-span")
						return out
					}()
					if strings.TrimRight(got, "\n") != strings.TrimRight(want, "\n") {
						t.Fatalf("--at %s on %s did not give back lines %d-%d:\ngot:\n%s\nwant:\n%s",
							span(sp.Start, sp.End), name, sp.Start, sp.End, got, want)
					}
				}
			}
			if checked == 0 {
				t.Fatal("no spans checked")
			}
			t.Logf("%d spans taken back", checked)
		})
	}
}

// TestAnEditLeavesTheRestOfTheFileAlone holds every edit to the region it was
// given: what sits above the region is untouched, and what it wrote still
// parses as the markdown it replaced.
func TestAnEditLeavesTheRestOfTheFileAlone(t *testing.T) {
	kinds := []string{"heading", "item", "paragraph", "code", "quote", "table", "row", "cell", "list"}
	ops := [][]string{
		{"--set-text", "REPLACEMENT"},
		{"--replace-node", "REPLACEMENT"},
		{"--delete"},
		{"--append", "ADDED"},
		{"--prepend", "ADDED"},
	}
	for _, name := range realDocs() {
		for _, kind := range kinds {
			dir := copyIn(t, name)
			hits := results(t, dir, "", name, "-k", kind, "-m", "3")
			for _, res := range hits {
				for _, op := range ops {
					t.Run(name+"/"+kind+"/"+op[0], func(t *testing.T) {
						work := copyIn(t, name)
						path := filepath.Join(work, name)
						before := strings.Split(readFile(t, path), "\n")
						args := append([]string{"--at", span(res.Start, res.End), name}, op...)
						code := func() int {
							defer inDir(t, work)()
							_, _, c := capture(t, append(args, "-W")...)
							return c
						}()
						if code == 2 {
							return // the edit was refused, which is an answer
						}
						after := strings.Split(readFile(t, path), "\n")
						head := res.Start - 1
						if !equal(before[:head], after[:min(head, len(after))]) {
							t.Errorf("%s %s at %d-%d changed the lines above the region",
								kind, op[0], res.Start, res.End)
						}
						out, errOut, code := func() (string, string, int) {
							defer inDir(t, work)()
							return capture(t, "", name, "-q")
						}()
						if code == 2 {
							t.Errorf("%s %s at %d-%d left a document that will not parse: %s%s",
								kind, op[0], res.Start, res.End, out, errOut)
						}
					})
				}
			}
		}
	}
}

func span(a, b int) string { return itoa(a) + "-" + itoa(b) }

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var d []byte
	for n > 0 {
		d = append([]byte{byte('0' + n%10)}, d...)
		n /= 10
	}
	return string(d)
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

// TestAnAcceptedEditLeavesTheStructureAlone is the promise behind every
// refusal in edit: a run either writes what was asked for or fails, and never
// lands a document whose shape the parser reads differently. Tables and fenced
// blocks are where a written line is most readily read as markup -- a bare
// pipe opens a column, a fence closes a block and leaves the rest of the file
// as code -- so they are what the count watches.
func TestAnAcceptedEditLeavesTheStructureAlone(t *testing.T) {
	ops := [][]string{
		{"--replace-node", "REPLACEMENT"},
		{"--delete"},
		{"--append", "ADDED"},
		{"--prepend", "ADDED"},
	}
	for _, name := range realDocs() {
		for _, kind := range []string{"row", "cell", "code", "paragraph", "item"} {
			dir := copyIn(t, name)
			for _, res := range results(t, dir, "", name, "-k", kind, "-m", "4") {
				if res.Kind == "code" || res.Kind == "table" {
					// The region is that block whole, so losing it is the
					// edit doing what it was told.
					continue
				}
				for _, op := range ops {
					t.Run(name+"/"+kind+"/"+op[0], func(t *testing.T) {
						work := copyIn(t, name)
						before := shape(t, work, name)
						code := func() int {
							defer inDir(t, work)()
							args := append([]string{"--at", span(res.Start, res.End), name}, op...)
							_, _, c := capture(t, append(args, "-W")...)
							return c
						}()
						if code == 2 {
							return // refused, which is the promise holding
						}
						if after := shape(t, work, name); after != before {
							t.Errorf("%s %s at %d-%d changed the document's shape: %s became %s",
								kind, op[0], res.Start, res.End, before, after)
						}
					})
				}
			}
		}
	}
}

// shape counts the blocks a stray line would forge or destroy.
func shape(t *testing.T, dir, name string) string {
	t.Helper()
	// Rows are left out: --delete is meant to take one. What may not change
	// is whether the table and the fence are still there at all.
	return "tables=" + itoa(len(results(t, dir, "", name, "-k", "table"))) +
		" fences=" + itoa(len(results(t, dir, "", name, "-k", "code")))
}

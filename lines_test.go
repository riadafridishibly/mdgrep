package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// notes is the document the line-oriented specification is written against.
// Its nodes are heading 1-1, paragraph 3-3, heading 5-5, paragraph 7-9,
// heading 11-11 and list 13-15 holding items 13-14 and 15-15.
const notes = `# Notes

Intro paragraph about foo.

## Setup

Install foo, then run foo doctor.
Check the log.
Then foo again.

## Tasks

- [ ] rotate the foo key
      the old key is in the vault
- [ ] archive the logs
`

func page(t *testing.T, args ...string) string {
	t.Helper()
	stdout, stderr, code := capture(t, args...)
	if code != 0 {
		t.Fatalf("exit = %d (%s)", code, stderr)
	}
	return stdout
}

// A result prints the lines that matched, not the node they sit in, and closes
// with the spans it could be widened to. Two runs of match lines inside one
// node are two groups, so a separator stands between them the way it stands
// between two results.
func TestAResultPrintsTheLinesThatMatched(t *testing.T) {
	path := doc(t, notes)
	want := strings.Join([]string{
		"3:Intro paragraph about foo.",
		"(paragraph 3-3, section 1-15)",
		"--",
		"7:Install foo, then run foo doctor.",
		"--",
		"9:Then foo again.",
		"(paragraph 7-9, section 5-9)",
		"--",
		"13:- [ ] rotate the foo key",
		"(item 13-14, list 13-15, section 11-15)",
		"",
	}, "\n")
	if got := page(t, "foo", path, "-n"); got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

// "Print the node when the node matched" is emergent rather than a rule: a
// contiguous run of match lines is the node, and nothing was skipped, so no
// separator appears inside it.
func TestANodeWhoseEveryLineMatchedPrintsWhole(t *testing.T) {
	path := doc(t, notes)
	want := strings.Join([]string{
		"7:Install foo, then run foo doctor.",
		"8:Check the log.",
		"9:Then foo again.",
		"(paragraph 7-9, section 5-9)",
		"",
	}, "\n")
	if got := page(t, "-e", "Install", "-e", "Check", "-e", "Then", path, "-n"); got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

// A matcher that cannot name a line claims every line of the node: -v selected
// the node by what it does not hold, and the empty pattern behind a filter
// selected it by the filter. That is what keeps a task item's sub-bullets on
// the page.
func TestAMatcherWithNoLineToNameClaimsThemAll(t *testing.T) {
	path := doc(t, notes)
	want := strings.Join([]string{
		"15:- [ ] archive the logs",
		"(item 15-15, list 13-15, section 11-15)",
		"",
	}, "\n")
	if got := page(t, "-v", "vault", "-k", "item", path, "-n"); got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}

	// The two items merge into one result, so the ladder starts at the
	// smallest block containing all of it and one note closes three lines.
	want = strings.Join([]string{
		"13:- [ ] rotate the foo key",
		"14:      the old key is in the vault",
		"15:- [ ] archive the logs",
		"(list 13-15, section 11-15)",
		"",
	}, "\n")
	if got := page(t, "", "--todo", path, "-n"); got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

// Asking for a widener asks to see the region whole. --section covers every
// rung of the ladder, which is the one case the note drops entirely.
func TestAWidenerPrintsTheRegionWhole(t *testing.T) {
	path := doc(t, notes)
	want := strings.Join([]string{
		"13:- [ ] rotate the foo key",
		"14:      the old key is in the vault",
		"(item 13-14, list 13-15, section 11-15)",
		"",
	}, "\n")
	if got := page(t, "vault", path, "-n", "--expand"); got != want {
		t.Errorf("--expand got:\n%s\nwant:\n%s", got, want)
	}

	want = strings.Join([]string{
		"11:## Tasks",
		"12:",
		"13:- [ ] rotate the foo key",
		"14:      the old key is in the vault",
		"15:- [ ] archive the logs",
		"",
	}, "\n")
	if got := page(t, "vault", path, "-n", "--section"); got != want {
		t.Errorf("--section got:\n%s\nwant:\n%s", got, want)
	}
}

// Context lines carry the dash, are counted in the file rather than in the
// node, and count as printed when the note asks what the page already covers.
func TestContextLinesTakeTheDash(t *testing.T) {
	path := doc(t, notes)
	want := strings.Join([]string{
		"13-- [ ] rotate the foo key",
		"14:      the old key is in the vault",
		"15-- [ ] archive the logs",
		"(item 13-14, list 13-15, section 11-15)",
		"",
	}, "\n")
	if got := page(t, "vault", path, "-n", "-C", "1"); got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

// An anchor selects its heading outright, so its match line is the heading's
// own first line and the ladder is two rungs.
func TestAnAnchorNamesItsMatchLine(t *testing.T) {
	path := doc(t, notes)
	want := "11:## Tasks\n(heading 11-11, section 11-15)\n"
	if got := page(t, "--anchor", "#tasks", path, "-n"); got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

// The ladder climbs block ancestors and then the heading hierarchy, where
// climbing block parents alone would have saturated at the list.
func TestExpandClimbsIntoTheHeadingHierarchy(t *testing.T) {
	path := doc(t, notes)
	for _, tt := range []struct {
		args  []string
		first string
		last  string
	}{
		{[]string{"--expand"}, "13:- [ ] rotate the foo key", "14:      the old key is in the vault"},
		{[]string{"--expand", "1"}, "13:- [ ] rotate the foo key", "15:- [ ] archive the logs"},
		{[]string{"--expand", "2"}, "11:## Tasks", "15:- [ ] archive the logs"},
		{[]string{"--expand", "3"}, "1:# Notes", "15:- [ ] archive the logs"},
		{[]string{"--expand", "9"}, "1:# Notes", "15:- [ ] archive the logs"},
		{[]string{"--expand=2"}, "11:## Tasks", "15:- [ ] archive the logs"},
	} {
		t.Run(strings.Join(tt.args, " "), func(t *testing.T) {
			got := page(t, append([]string{"vault", path, "-n", "--no-span"}, tt.args...)...)
			lines := strings.Split(strings.TrimRight(got, "\n"), "\n")
			if lines[0] != tt.first || lines[len(lines)-1] != tt.last {
				t.Errorf("got:\n%s\nwant %q .. %q", got, tt.first, tt.last)
			}
		})
	}
}

// --expand takes its count optionally, so a following word that is not a count
// is still a path. The flag package cannot express that on its own: bare
// --expand needs IsBoolFlag, and a bool flag never consumes the word after it.
func TestExpandLeavesAPathAlone(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.md"), []byte(notes), 0o644); err != nil {
		t.Fatal(err)
	}
	got := page(t, "vault", "--expand", dir, "-n")
	if !strings.Contains(got, "a.md:13:- [ ] rotate the foo key") {
		t.Errorf("want the directory read as a path:\n%s", got)
	}
	got = page(t, "vault", "--expand", "2", dir, "-n")
	if !strings.Contains(got, "a.md:11:## Tasks") {
		t.Errorf("want the 2 read as the count and the directory as a path:\n%s", got)
	}
}

// An address selects by construction, so every line of it is a match line and
// the region prints whole with no widener asked for. The note reads the same
// as it does for the search that found those lines.
func TestAtSelectsTheLinesItNames(t *testing.T) {
	path := doc(t, notes)
	want := strings.Join([]string{
		"13:- [ ] rotate the foo key",
		"14:      the old key is in the vault",
		"(item 13-14, list 13-15, section 11-15)",
		"",
	}, "\n")
	if got := page(t, "--at", "13-14", path, "-n"); got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
	// One line is an address too, and lines no block draws are a region.
	if got := page(t, "--at", "12", path, "--format", "compact"); !strings.Contains(got, "\tregion\t") {
		t.Errorf("want a region kind:\n%s", got)
	}
}

// An address is the thing being found, so one that does not fit the file is a
// stale note or a typo rather than a request to be answered with fewer lines.
func TestAtIsHeldToTheFile(t *testing.T) {
	path := doc(t, notes)
	for _, args := range [][]string{{"--at", "1-99"}, {"--at", "0"}, {"--at", "9-3"}, {"--at", "x"}} {
		_, stderr, code := capture(t, append(args, path)...)
		if code != 2 {
			t.Errorf("%v: exit = %d, want 2", args, code)
		}
		if !strings.Contains(stderr, "at") {
			t.Errorf("%v: want an error naming the address:\n%s", args, stderr)
		}
	}
}

// The numbers belong to one file, so a run that could answer from more than
// one is refused rather than answered with the same lines of each.
func TestAtNamesOneFile(t *testing.T) {
	dir := t.TempDir()
	for _, f := range []string{"a.md", "b.md"} {
		if err := os.WriteFile(filepath.Join(dir, f), []byte(notes), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	_, stderr, code := capture(t, "--at", "13-14", dir)
	if code != 2 || !strings.Contains(stderr, "one file") {
		t.Errorf("exit = %d, want 2 and a refusal naming the count:\n%s", code, stderr)
	}
}

// A pattern beside an address is a guard: the address says which lines to
// take, the pattern says what they should still say. It is the one failure a
// line number has that a pattern does not.
func TestAtsGuardHasToStillBeThere(t *testing.T) {
	path := doc(t, notes)
	if got := page(t, "-e", "vault", "--at", "13-14", path, "-n"); !strings.Contains(got, "13:- [ ]") {
		t.Errorf("want the addressed lines:\n%s", got)
	}
	_, stderr, code := capture(t, "-e", "no such text", "--at", "13-14", path)
	if code != 2 || !strings.Contains(stderr, "guard") {
		t.Errorf("exit = %d, want 2 and a refusal naming the guard:\n%s", code, stderr)
	}
}

// One selects a heading by name, one is a line per heading, and two of them
// state how many nodes a search should have found. An address is none of
// those, so each is refused beside it rather than quietly ignored.
func TestAtRefusesTheFlagsThatSelectAnotherWay(t *testing.T) {
	path := doc(t, notes)
	for _, args := range [][]string{
		{"--anchor", "#tasks"},
		{"--outline"},
		{"--delete", "--expect", "1"},
		{"--delete", "--multi"},
	} {
		_, stderr, code := capture(t, append([]string{"--at", "13-14", path}, args...)...)
		if code != 2 {
			t.Errorf("%v: exit = %d, want 2", args, code)
		}
		if !strings.Contains(stderr, "at") {
			t.Errorf("%v: want an error naming --at:\n%s", args, stderr)
		}
	}
}

// The region an address names is the region an edit rewrites, and a node edit
// acts on the node it resolves to -- refusing one that resolves to none in the
// words it already uses.
func TestAtIsWhatAnEditRewrites(t *testing.T) {
	path := doc(t, notes)
	if _, stderr, code := capture(t, "--at", "15", path, "--replace", "- [x] archived"); code != 0 {
		t.Fatalf("exit = %d (%s)", code, stderr)
	}
	if got := read(t, path); !strings.Contains(got, "- [x] archived\n") {
		t.Errorf("want the addressed line rewritten:\n%s", got)
	}

	path = doc(t, notes)
	if _, stderr, code := capture(t, "-e", "vault", "--at", "13-14", path, "--check"); code != 0 {
		t.Fatalf("exit = %d (%s)", code, stderr)
	}
	if got := read(t, path); !strings.Contains(got, "- [x] rotate the foo key\n") {
		t.Errorf("want the addressed item ticked:\n%s", got)
	}

	// Lines no block draws are a region, and --set-text has no markup to keep.
	_, stderr, code := capture(t, "--at", "12-14", doc(t, notes), "--set-text", "x")
	if code != 2 || !strings.Contains(stderr, "--set-text does not apply to a region") {
		t.Errorf("exit = %d, wanted the region refusal:\n%s", code, stderr)
	}
}

// json reports the two facts the plain note carries: which lines matched, and
// the ladder, whose array index is the --expand count.
func TestJSONCarriesTheHitsAndTheLadder(t *testing.T) {
	path := doc(t, notes)
	got := page(t, "vault", path, "--json")
	for _, want := range []string{
		`"hits":[14]`,
		`"spans":[{"kind":"item","start":13,"end":14},` +
			`{"kind":"list","start":13,"end":15},` +
			`{"kind":"section","start":11,"end":15}]`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("want %s in:\n%s", want, got)
		}
	}
	// A node matcher names no line, which is how a reader tells "every line"
	// from "these lines".
	if got := page(t, "", "--todo", path, "--json"); !strings.Contains(got, `"hits":[]`) {
		t.Errorf("want an empty hits array:\n%s", got)
	}
}

// bullets is a document whose list items sit on consecutive lines, so that a
// page of one line per result has groups that really are next to each other.
const bullets = `## List

- alpha
- beta
- gamma

tail
`

// grep prints "--" only between groups of file lines that were never next to
// each other. Two results whose lines are consecutive are one such run, so
// nothing stands between them however many results drew them.
func TestASeparatorStandsOnlyBetweenGroupsThatAreApart(t *testing.T) {
	path := doc(t, bullets)
	want := strings.Join([]string{
		"3:- alpha",
		"(item 3-3, list 3-5, section 1-7)",
		"4:- beta",
		"(item 4-4, list 3-5, section 1-7)",
		"5:- gamma",
		"(item 5-5, list 3-5, section 1-7)",
		"",
	}, "\n")
	if got := page(t, "^- ", path, "-n", "--truncate", "1"); got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

// Context is counted in the file, so two windows that reach the same line are
// one group of file lines rather than two. A line prints once, and it prints
// as a match wherever the matcher pointed at it.
func TestOverlappingContextPrintsALineOnce(t *testing.T) {
	path := doc(t, notes)
	want := strings.Join([]string{
		"1-# Notes",
		"2-",
		"3:Intro paragraph about foo.",
		"4-",
		"5-## Setup",
		"6-",
		"(paragraph 3-3, section 1-15)",
		"7:Install foo, then run foo doctor.",
		"8-Check the log.",
		"9:Then foo again.",
		"10-",
		"11-## Tasks",
		"12-",
		"13:- [ ] rotate the foo key",
		"14-      the old key is in the vault",
		"15-- [ ] archive the logs",
		"",
	}, "\n")
	if got := page(t, "foo", path, "-n", "-C", "3"); got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

// -C is shorthand for -B N -A N, so a -B after it narrows what it opened, the
// way it does in grep. Each pair is read once and the last spelling wins.
func TestTheLastContextFlagWins(t *testing.T) {
	path := doc(t, notes)
	for _, tt := range []struct {
		args  []string
		first string
		last  string
	}{
		{[]string{"-C", "3", "-B", "1"}, "13-- [ ] rotate the foo key", "15-- [ ] archive the logs"},
		{[]string{"-C", "3", "-A", "0"}, "11-## Tasks", "14:      the old key is in the vault"},
		{[]string{"-B", "1", "-C", "3"}, "11-## Tasks", "15-- [ ] archive the logs"},
	} {
		t.Run(strings.Join(tt.args, " "), func(t *testing.T) {
			got := page(t, append([]string{"vault", path, "-n", "--no-span"}, tt.args...)...)
			lines := strings.Split(strings.TrimRight(got, "\n"), "\n")
			if lines[0] != tt.first || lines[len(lines)-1] != tt.last {
				t.Errorf("got:\n%s\nwant %q .. %q", got, tt.first, tt.last)
			}
		})
	}
}

// --siblings widens a result to blocks the matcher never pointed at, so it
// says which neighbours to print rather than which node to rewrite. An edit
// beside it is refused rather than quietly given three nodes to replace with
// one, the way its predecessor spelling was.
func TestSiblingsDoesNotSelectWhatAnEditRewrites(t *testing.T) {
	path := doc(t, bullets)
	_, stderr, code := capture(t, "alpha", path, "--siblings", "1", "--replace", "ZZZ")
	if code != 2 || !strings.Contains(stderr, "--siblings") {
		t.Errorf("exit = %d, want 2 and a refusal naming --siblings:\n%s", code, stderr)
	}
	if got := read(t, path); got != bullets {
		t.Errorf("want the file untouched:\n%s", got)
	}
}

// A body is the region the result reports, so the lines it says matched are
// lines of it. The heading the matcher pointed at is not one of them.
func TestSectionBodyReportsHitsInsideItsSpan(t *testing.T) {
	path := doc(t, notes)
	got := page(t, "## Tasks", path, "--json", "--section-body")
	var r struct {
		Start int   `json:"start"`
		End   int   `json:"end"`
		Hits  []int `json:"hits"`
	}
	if err := json.Unmarshal([]byte(got), &r); err != nil {
		t.Fatalf("%v: %s", err, got)
	}
	for _, h := range r.Hits {
		if h < r.Start || h > r.End {
			t.Errorf("hit %d lies outside %d-%d:\n%s", h, r.Start, r.End, got)
		}
	}
}

// An address names lines of one file, and it is the first stage that says
// which files a run reads. On a later stage the numbers would be applied to
// whatever the stage before it handed over, in however many files, and neither
// the count nor the bounds are checked there -- so it is refused by name.
func TestAtBelongsToTheFirstStage(t *testing.T) {
	dir := t.TempDir()
	for _, f := range []string{"a.md", "b.md"} {
		if err := os.WriteFile(filepath.Join(dir, f), []byte(notes), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	_, stderr, code := capture(t, "foo", dir, "--then", "--at", "13")
	if code != 2 || !strings.Contains(stderr, "--at") {
		t.Errorf("exit = %d, want 2 and a refusal naming --at:\n%s", code, stderr)
	}
}

// An address takes its lines outright, so nothing filters them: a kind or a
// checkbox state beside it is read and would then change nothing. Each is
// refused by name rather than dropped in silence.
func TestAtRefusesTheFiltersItCannotHonour(t *testing.T) {
	path := doc(t, notes)
	for _, args := range [][]string{{"-k", "heading"}, {"--todo"}, {"--task"}, {"--done"}} {
		_, stderr, code := capture(t, append([]string{"--at", "13-14", path}, args...)...)
		if code != 2 {
			t.Errorf("%v: exit = %d, want 2", args, code)
		}
		if !strings.Contains(stderr, "--at") {
			t.Errorf("%v: want an error naming --at:\n%s", args, stderr)
		}
	}
}

// A machine format is one record per result, carrying the region and the lines
// that matched inside it. The flags that pad a page or write a note under one
// have nothing to say about that, so they are refused there the way --stream
// and --outline already refuse them.
func TestMachineFormatsRefuseThePageFlags(t *testing.T) {
	path := doc(t, notes)
	for _, args := range [][]string{
		{"--json", "-A", "3"},
		{"--json", "--no-span"},
		{"--format", "compact", "-C", "2"},
		{"--format", "compact", "--span"},
	} {
		if _, stderr, code := capture(t, append([]string{"foo", path}, args...)...); code != 2 {
			t.Errorf("%v: exit = %d, want 2:\n%s", args, code, stderr)
		}
	}
}

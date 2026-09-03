package cli

import (
	"slices"
	"strings"
	"testing"
)

// A command line with no separator in it is one stage, which is every command
// mdgrep took before there were stages at all.
func TestALineWithoutASeparatorIsOneStage(t *testing.T) {
	got, err := Stages([]string{"-k", "heading", "foo", "docs"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || !slices.Equal(got[0], []string{"-k", "heading", "foo", "docs"}) {
		t.Errorf("Stages = %q, want the line whole", got)
	}
}

// The separator is read before the flags are, the way a shell reads a pipe, so
// each stage arrives as a command line of its own.
func TestStagesSplitOnTheSeparator(t *testing.T) {
	got, err := Stages([]string{"foo", "docs", "--then", "", "--todo", "--then", "--check"})
	if err != nil {
		t.Fatal(err)
	}
	want := [][]string{{"foo", "docs"}, {"", "--todo"}, {"--check"}}
	if len(got) != len(want) {
		t.Fatalf("Stages = %q, want %q", got, want)
	}
	for i := range want {
		if !slices.Equal(got[i], want[i]) {
			t.Errorf("stage %d = %q, want %q", i+1, got[i], want[i])
		}
	}
}

// A pattern holding the separator's own spelling is not the separator: only a
// word that is the whole of it divides two stages.
func TestOnlyTheWholeWordSeparates(t *testing.T) {
	got, err := Stages([]string{"--then=x", "a--then", "foo"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Errorf("Stages = %q, want one stage", got)
	}
}

// --then joins two searches, so it wants one on each side of it.
func TestStagesWantASearchOnEachSide(t *testing.T) {
	tests := []struct {
		name string
		args []string
		says string
	}{
		{"leading", []string{"--then", "foo"}, "wants one before it"},
		{"trailing", []string{"foo", "--then"}, "wants one after it"},
		{"doubled", []string{"foo", "--then", "--then", "bar"}, "two --then in a row"},
		{"alone", []string{"--then"}, "wants one before it"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Stages(tt.args)
			if err == nil {
				t.Fatal("want an error")
			}
			if !strings.Contains(err.Error(), tt.says) {
				t.Errorf("err = %v, want %q", err, tt.says)
			}
		})
	}
}

// An empty command line is the same one stage it has always been: the run goes
// on to say it is missing a pattern, which is a better answer than one about
// stages.
func TestNoArgumentsIsStillOneStage(t *testing.T) {
	got, err := Stages(nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || len(got[0]) != 0 {
		t.Errorf("Stages = %q, want one empty stage", got)
	}
}

// Each stage does one job -- the first names the files, the last prints or
// writes -- and a flag is let through wherever the stage it is on can honour
// it.
func TestStageFlagsHoldEachStageToItsJob(t *testing.T) {
	tests := []struct {
		name string
		args []string
		i, n int
		says string
	}{
		{"reads on the first", []string{"--ext", "md", "foo"}, 0, 3, ""},
		{"reads later", []string{"--ext", "md", "foo"}, 1, 3, "reads its files once"},
		{"prints on the last", []string{"--json", "foo"}, 2, 3, ""},
		{"prints early", []string{"--json", "foo"}, 0, 3, "only the last stage prints"},
		{"edits on the last", []string{"--check", "foo"}, 2, 3, ""},
		{"edits early", []string{"--check", "foo"}, 1, 3, "belongs on the last stage"},
		{"selects anywhere", []string{"--section", "--expand", "1", "-m", "2", "foo"}, 1, 3, ""},
		{"plans nowhere", []string{"--apply", "p.jsonl"}, 2, 3, "a whole run rather than a stage"},
		{"asks nowhere", []string{"--help"}, 0, 3, "takes no --then"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, fs, err := Parse(tt.args)
			if err != nil {
				t.Fatal(err)
			}
			err = StageFlags(fs, tt.i, tt.n)
			switch {
			case tt.says == "" && err != nil:
				t.Errorf("unexpected error: %v", err)
			case tt.says != "" && err == nil:
				t.Errorf("want an error saying %q", tt.says)
			case tt.says != "" && !strings.Contains(err.Error(), tt.says):
				t.Errorf("err = %v, want %q", err, tt.says)
			}
		})
	}
}

// A pipeline written as one string comes to the same stages the command line
// would have made, so a query can be kept in a variable and still mean what it
// reads as.
func TestExecSplitsAStringIntoStages(t *testing.T) {
	got, err := Stages([]string{"--exec", `-k heading foo docs | "" --todo | --check`})
	if err != nil {
		t.Fatal(err)
	}
	want := [][]string{{"-k", "heading", "foo", "docs"}, {"", "--todo"}, {"--check"}}
	if len(got) != len(want) {
		t.Fatalf("Stages = %q, want %q", got, want)
	}
	for i := range want {
		if !slices.Equal(got[i], want[i]) {
			t.Errorf("stage %d = %q, want %q", i+1, got[i], want[i])
		}
	}
}

// The quoting inside --exec is mdgrep's own rather than the shell's, which is
// what keeps the pipe character usable in a pattern: only a bare word that is
// nothing but the separator divides two stages.
func TestExecSeparatesOnlyOnABareWord(t *testing.T) {
	tests := []struct {
		name string
		line string
		want [][]string
	}{
		{"in a pattern", `"^(alpha|beta)" docs`, [][]string{{"^(alpha|beta)", "docs"}}},
		{"unquoted in a word", `^(alpha|beta) docs`, [][]string{{"^(alpha|beta)", "docs"}}},
		{"quoted alone", `-F '|' docs`, [][]string{{"-F", "|", "docs"}}},
		{"escaped alone", `-F \| docs`, [][]string{{"-F", "|", "docs"}}},
		{"bare alone", `a | b`, [][]string{{"a"}, {"b"}}},
		{"the other spelling", `a --then b`, [][]string{{"a"}, {"b"}}},
		{"a quoted spelling", `-F '--then' docs`, [][]string{{"-F", "--then", "docs"}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Stages([]string{"--exec", tt.line})
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != len(tt.want) {
				t.Fatalf("Stages = %q, want %q", got, tt.want)
			}
			for i := range tt.want {
				if !slices.Equal(got[i], tt.want[i]) {
					t.Errorf("stage %d = %q, want %q", i+1, got[i], tt.want[i])
				}
			}
		})
	}
}

// Quoted emptiness is a word, since "" is how a stage asks for the pattern
// that matches everything, and words run together the way a shell joins them.
func TestExecReadsWordsTheWayAShellWould(t *testing.T) {
	tests := []struct {
		name string
		line string
		want []string
	}{
		{"empty word", `"" --todo`, []string{"", "--todo"}},
		{"empty single", `'' --todo`, []string{"", "--todo"}},
		{"joined pieces", `-k"heading"`, []string{"-kheading"}},
		{"quote inside a word", `--replace-node "say \"hi\""`, []string{"--replace-node", `say "hi"`}},
		{"backslash kept", `-F "a\\b"`, []string{"-F", `a\b`}},
		{"single quotes are literal", `-F 'a\b'`, []string{"-F", `a\b`}},
		{"tabs and newlines divide", "a\tb\nc", []string{"a", "b", "c"}},
		{"spaces inside quotes do not", `"two words"`, []string{"two words"}},
		{"a backslash continues a line", "--section \\\n docs", []string{"--section", "docs"}},
		{"a continuation joins a word", "do\\\ncs", []string{"docs"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Stages([]string{"--exec", tt.line})
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != 1 || !slices.Equal(got[0], tt.want) {
				t.Errorf("Stages = %q, want one stage %q", got, tt.want)
			}
		})
	}
}

// Only paths may stand beside --exec: a flag outside the string is one the
// string was written to carry, and no stage of it asked for that flag.
func TestExecTakesOnlyPathsBesideIt(t *testing.T) {
	got, err := Stages([]string{"--exec", `"" --todo | --check`, "docs", "notes.md"})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got[0], []string{"", "--todo", "docs", "notes.md"}) {
		t.Errorf("first stage = %q, want the paths appended to it", got[0])
	}
	if _, err := Stages([]string{"--exec", `"" --todo`, "--json"}); err == nil {
		t.Error("a flag beside --exec should be refused")
	}
}

// Everything past a "--" the caller wrote is a path, dashes and all, which is
// exactly what may stand beside --exec.
func TestExecTakesAPathThatLooksLikeAFlag(t *testing.T) {
	got, err := Stages([]string{"--exec", `"" --todo`, "--", "--odd.md"})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got[0], []string{"", "--todo", "--", "--odd.md"}) {
		t.Errorf("first stage = %q, want the terminator and the path", got[0])
	}
}

// --exec is refused what it cannot read, rather than half honoured.
func TestExecRefusesWhatItCannotRead(t *testing.T) {
	tests := []struct {
		name string
		args []string
		says string
	}{
		{"no argument", []string{"--exec"}, "wants a pipeline to run"},
		{"twice", []string{"--exec", "a", "--exec", "b"}, "given once"},
		{"beside --then", []string{"--exec", "a", "--then", "b"}, "use one"},
		{"inside itself", []string{"--exec", "a --exec b"}, "cannot stand inside one"},
		{"open quote", []string{"--exec", `a "b`}, "never closed"},
		{"open single quote", []string{"--exec", "a 'b"}, "never closed"},
		{"leading pipe", []string{"--exec", "| a"}, "wants one before it"},
		{"trailing pipe", []string{"--exec", "a |"}, "wants one after it"},
		{"doubled pipe", []string{"--exec", "a | | b"}, "two | in a row"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Stages(tt.args)
			if err == nil {
				t.Fatal("want an error")
			}
			if !strings.Contains(err.Error(), tt.says) {
				t.Errorf("err = %v, want %q", err, tt.says)
			}
		})
	}
}

// The attached spelling is the same flag.
func TestExecTakesItsValueAttached(t *testing.T) {
	got, err := Stages([]string{"--exec=a | b"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Errorf("Stages = %q, want two stages", got)
	}
}

// A bare "--" ends the flags, and the separators are read before the flags, so
// it ends them too: a word past it is a path however it is spelled. Without
// that a caller who wrote "mdgrep -F "$pat" -- "$@"" to keep a filename from
// being read as a flag would have handed the file the run of the command line.
func TestDoubleDashEndsTheSeparatorScan(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{"then", []string{"-F", "p", "--", "--then", "docs"}},
		{"exec", []string{"-F", "p", "--", "--exec", "'a' | -k item"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Stages(tt.args)
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != 1 || !slices.Equal(got[0], tt.args) {
				t.Errorf("Stages = %q, want the line whole", got)
			}
		})
	}
}

// --exec spells the whole pipeline, so a hole in it is refused whatever else
// stands on the line: the paths beside the flag name files and never stand in
// for the search a stage is missing.
func TestExecWantsASearchOnEachSideEvenWithAPath(t *testing.T) {
	if _, err := Stages([]string{"--exec", "| -k item", "docs"}); err == nil ||
		!strings.Contains(err.Error(), "wants one before it") {
		t.Errorf("err = %v, want a complaint about the missing first stage", err)
	}
}

// Quoting is what makes a word literal inside --exec, and it makes every word
// literal: a quoted "--exec" is a pattern like any other rather than the flag
// the string cannot carry.
func TestQuotingHidesTheExecFlagInsideExec(t *testing.T) {
	got, err := Stages([]string{"--exec", `-F '--exec' | -k item`})
	if err != nil {
		t.Fatal(err)
	}
	want := [][]string{{"-F", "--exec"}, {"-k", "item"}}
	if len(got) != len(want) {
		t.Fatalf("Stages = %q, want %q", got, want)
	}
	for i := range want {
		if !slices.Equal(got[i], want[i]) {
			t.Errorf("stage %d = %q, want %q", i+1, got[i], want[i])
		}
	}
}

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

package main

import (
	"flag"
	"io"
	"slices"
	"strings"
	"testing"
)

// testFlags mirrors the shape run() binds: a few string and float flags that
// take a value, a few booleans that do not, and both the long and the short
// spelling of each, since that is what decides how permute reads an argument.
func testFlags() *flag.FlagSet {
	fs := flag.NewFlagSet("mdgrep", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var s string
	var f float64
	var b bool
	for _, n := range []string{"e", "regexp", "kind", "k", "replace", "anchor-style"} {
		fs.StringVar(&s, n, "", "")
	}
	for _, n := range []string{"min-score", "B", "A", "C", "m"} {
		fs.Float64Var(&f, n, 0, "")
	}
	for _, n := range []string{"i", "v", "w", "fuzzy", "anchor", "F", "q", "n", "todo"} {
		fs.BoolVar(&b, n, false, "")
	}
	return fs
}

// isSubsequence reports whether every element of want appears in args, in the
// same order.
func isSubsequence(want, args []string) bool {
	i := 0
	for _, a := range args {
		if i < len(want) && want[i] == a {
			i++
		}
	}
	return i == len(want)
}

// positional is what the flag package would be left with: everything from the
// first argument it does not recognise as a flag, or everything after "--".
func positional(fs *flag.FlagSet, args []string) []string {
	if err := fs.Parse(args); err != nil {
		return nil
	}
	return fs.Args()
}

// FuzzPermute holds the argument reordering to the one thing it must not do:
// lose or reorder what the user meant as a path. permute exists so a pattern
// can be typed before its options, and it hands its result straight to the flag
// package, so anything it drops there is gone.
func FuzzPermute(f *testing.F) {
	seeds := [][]string{
		{"pattern", "file.md"},
		{"pattern", "-i", "file.md"},
		{"-i", "pattern", "docs"},
		{"pattern", "-C2", "a.md", "b.md"},
		{"pattern", "-k", "heading", "a.md"},
		{"--kind=heading", "pattern", "a.md"},
		{"-e", "one", "-e", "two", "a.md"},
		{"--", "-dash.md"},
		{"pattern", "--", "-dash.md"},
		{"-e", "deploy", "--", "-dash.md"},
		{"pattern", "-"},
		{"--fuzzy", "-s0.7", "pattern"},
		{"-"},
		{"--"},
		{"-i"},
	}
	for _, s := range seeds {
		f.Add(strings.Join(s, "\x00"))
	}

	f.Fuzz(func(t *testing.T, joined string) {
		if len(joined) > 512 {
			return
		}
		args := strings.Split(joined, "\x00")
		if len(args) > 32 {
			return
		}

		out := permute(testFlags(), args)

		// Nothing may be invented. Every argument out is one that went in, bar
		// the halves an attached short flag such as "-C2" is split into.
		for _, a := range out {
			if slices.Contains(args, a) {
				continue
			}
			// The one argument permute rewrites is the attached short form
			// grep takes, "-C2", which it parts into "-C" and "2". Either half
			// is allowed; nothing else is.
			half := slices.ContainsFunc(args, func(in string) bool {
				return len(in) > 2 && in[0] == '-' && in[1] != '-' && (in[:2] == a || in[2:] == a)
			})
			if !half {
				t.Fatalf("permute invented %q\nin  %q\nout %q", a, args, out)
			}
		}

		// Reordering the line is the whole point, so the paths the flag package
		// ends up with cannot be compared against what it would have found
		// unaided. What must hold is that they were all typed, and typed in
		// that order: permute may move a flag past a path, never a path past a
		// path, and never conjure a path that was not there.
		paths := positional(testFlags(), out)
		if !isSubsequence(paths, args) {
			t.Fatalf("paths are not the ones typed, in order\nin    %q\nout   %q\npaths %q", args, out, paths)
		}

		// Permuting an already-permuted line has nothing left to move.
		if again := permute(testFlags(), out); !slices.Equal(again, out) {
			t.Fatalf("permute is not settled\nonce  %q\ntwice %q", out, again)
		}
	})
}

// TestPermuteKeepsTerminator pins the separator through the move. permute
// hoists flags in front of the paths, so a "--" the caller wrote has to travel
// with them: without it a path that opens with a dash arrives back at the flag
// package and is read as a flag.
func TestPermuteKeepsTerminator(t *testing.T) {
	for _, tc := range []struct {
		args  []string
		paths []string
	}{
		{[]string{"-e", "deploy", "--", "-dash.md"}, []string{"-dash.md"}},
		{[]string{"--", "-dash.md"}, []string{"-dash.md"}},
		{[]string{"-i", "pattern", "--", "-a.md", "-b.md"}, []string{"pattern", "-a.md", "-b.md"}},
	} {
		out := permute(testFlags(), tc.args)
		if !slices.Contains(out, "--") {
			t.Errorf("permute(%q) dropped the terminator: %q", tc.args, out)
			continue
		}
		fs := testFlags()
		if err := fs.Parse(out); err != nil {
			t.Errorf("permute(%q) = %q, which no longer parses: %v", tc.args, out, err)
			continue
		}
		if !slices.Equal(fs.Args(), tc.paths) {
			t.Errorf("permute(%q) left the paths %q, want %q", tc.args, fs.Args(), tc.paths)
		}
	}
}

// TestPermuteRejectsMistypedFlag guards the other side of the same change: a
// terminator is only kept where the caller wrote one, so a mistyped flag still
// reports itself rather than being quietly taken for a file name.
func TestPermuteRejectsMistypedFlag(t *testing.T) {
	fs := testFlags()
	if err := fs.Parse(permute(fs, []string{"--kindd=heading", "pattern", "a.md"})); err == nil {
		t.Fatal("a mistyped flag parsed cleanly")
	}
}

package cli

import (
	"slices"
	"strings"
	"testing"
	"unicode"

	"github.com/riadafridishibly/mdgrep/internal/help"
)

// The alias tables were readable only in the source for as long as they
// existed: -k took "bullet" and "fm", --anchor-style took "gh" and "jekyll",
// and the manual named none of them. Documenting a table by hand is a copy
// that drifts, so these tests fail the next alias that is added without one.
//
// They check the direction that drifts in practice -- an alias the code takes
// and the manual omits. An alias the manual invents is caught by ParseKinds
// and parseAnchorStyles rejecting it, which the runs below do not exercise.
func TestKindAliasesAreAllInTheManual(t *testing.T) {
	block := flagBlock(t, "-k, --kind LIST")
	for name := range kindAliases {
		if !namesWord(block, name) {
			t.Errorf("-k accepts %q, and the manual does not name it:\n%s", name, block)
		}
	}
}

func TestAnchorStyleAliasesAreAllInTheManual(t *testing.T) {
	block := flagBlock(t, "--anchor-style LIST")
	for name := range anchorStyleAliases {
		if !namesWord(block, name) {
			t.Errorf("--anchor-style accepts %q, and the manual does not name it:\n%s", name, block)
		}
	}
	// "all" is not in the table: parseAnchorStyles reads it as every style
	// rather than as one of them, so only the manual can say it is allowed.
	if !namesWord(block, "all") {
		t.Errorf("--anchor-style takes \"all\", and the manual does not name it:\n%s", block)
	}
}

// flagBlock is what the manual says about one flag: the line defining it with
// the flag itself cut off, joined to the continuation lines under it. Reading
// the entry rather than the whole manual is what keeps a stray "h" or "any"
// somewhere else in the text from standing in for the alias.
func flagBlock(t *testing.T, head string) string {
	t.Helper()
	const column = 24
	var out []string
	found := false
	for line := range strings.SplitSeq(help.Usage, "\n") {
		if !found {
			trimmed := strings.TrimLeft(line, " ")
			if len(line)-len(trimmed) < column && strings.HasPrefix(trimmed, head) {
				out = append(out, strings.TrimPrefix(trimmed, head))
				found = true
			}
			continue
		}
		if !strings.HasPrefix(line, strings.Repeat(" ", column)) {
			break
		}
		out = append(out, strings.TrimSpace(line))
	}
	if !found {
		t.Fatalf("the manual defines no flag %q", head)
	}
	return strings.Join(out, " ")
}

// namesWord reports whether text names word on its own, so that "(h," counts
// as "h" and "heading" does not.
func namesWord(text, word string) bool {
	split := func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }
	return slices.Contains(strings.FieldsFunc(text, split), word)
}

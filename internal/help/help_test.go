package help

import "testing"

// A section title is judged by its first character. Reading one byte of a
// multi-byte character judges a byte that is not a character at all: 0xC3, the
// lead byte of every C-with-cedilla and every U-with-diaeresis, is 'Ã' on its
// own, which is uppercase.
func TestIsTitleReadsAWholeRune(t *testing.T) {
	tests := []struct {
		line string
		want bool
	}{
		{"Filters", true},
		{"filters", false},
		{"", false},
		{"Output formats", false}, // a space is not a letter
		{"über", false},           // lower case, whatever its encoding
	}
	for _, tt := range tests {
		if got := isTitle(tt.line); got != tt.want {
			t.Errorf("isTitle(%q) = %v, want %v", tt.line, got, tt.want)
		}
	}
}

// A flag that exists only as an alias is still a flag someone types, and
// "--help todo" is how they look it up. The alias is written in the prose of
// the entry it belongs to rather than in the flag column, so the lookup has to
// read it there or answer that the flag has no help at all.
func TestHelpFindsAFlagByItsAlias(t *testing.T) {
	for _, tc := range []struct{ alias, canonical string }{
		{"todo", "unchecked"},
		{"done", "checked"},
	} {
		t.Run(tc.alias, func(t *testing.T) {
			got, err := Text(tc.alias)
			if err != nil {
				t.Fatalf("Text(%q) = %v", tc.alias, err)
			}
			want, err := Text(tc.canonical)
			if err != nil {
				t.Fatal(err)
			}
			if got != want {
				t.Errorf("--help %s and --help %s read differently", tc.alias, tc.canonical)
			}
		})
	}
}

// The alias reader takes the spellings an entry claims, not every flag its
// description happens to name.
func TestAliasesReadsOnlyTheAliasNote(t *testing.T) {
	tests := []struct {
		desc string
		want int
	}{
		{"only unticked task items (alias --todo)", 1},
		{"only ticked task items (aliases --done, --ticked)", 2},
		{"a filter that behaves like --task but is not it", 0},
		{"plain prose", 0},
	}
	for _, tt := range tests {
		if got := aliases(tt.desc); len(got) != tt.want {
			t.Errorf("aliases(%q) = %v, want %d", tt.desc, got, tt.want)
		}
	}
}

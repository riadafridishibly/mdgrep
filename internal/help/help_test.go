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

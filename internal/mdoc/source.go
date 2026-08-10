package mdoc

import "strings"

// Source holds a markdown file's text together with a line index so byte
// offsets reported by goldmark can be translated into line numbers. The text
// is kept as a string so every line and range handed out is a slice of it
// rather than a fresh allocation.
type Source struct {
	Path string

	text      string
	lineStart []int // byte offset where each line begins
}

func NewSource(path string, data []byte) *Source {
	s := &Source{Path: path, text: string(data), lineStart: []int{0}}
	for off := 0; ; {
		i := strings.IndexByte(s.text[off:], '\n')
		if i < 0 {
			break
		}
		off += i + 1
		s.lineStart = append(s.lineStart, off)
	}
	// A trailing newline creates a phantom empty last line; drop it.
	if n := len(s.lineStart); n > 1 && s.lineStart[n-1] == len(s.text) {
		s.lineStart = s.lineStart[:n-1]
	}
	return s
}

func (s *Source) NumLines() int { return len(s.lineStart) }

// LineIndex returns the zero-based line containing the given byte offset.
func (s *Source) LineIndex(off int) int {
	lo, hi := 0, len(s.lineStart)-1
	for lo < hi {
		mid := (lo + hi + 1) / 2
		if s.lineStart[mid] <= off {
			lo = mid
		} else {
			hi = mid - 1
		}
	}
	return lo
}

// Line returns the zero-based line without its trailing newline.
func (s *Source) Line(i int) string {
	if i < 0 || i >= len(s.lineStart) {
		return ""
	}
	end := len(s.text)
	if i+1 < len(s.lineStart) {
		end = s.lineStart[i+1]
	}
	return strings.TrimRight(s.text[s.lineStart[i]:end], "\r\n")
}

// Slice returns the raw source of an inclusive line range, newlines included,
// clamped to the file. This is what patterns are matched against, so a search
// sees the markdown exactly as it is written.
func (s *Source) Slice(start, end int) string {
	if start < 0 {
		start = 0
	}
	if end >= s.NumLines() {
		end = s.NumLines() - 1
	}
	if start > end {
		return ""
	}
	stop := len(s.text)
	if end+1 < len(s.lineStart) {
		stop = s.lineStart[end+1]
	}
	return strings.TrimRight(s.text[s.lineStart[start]:stop], "\r\n")
}

// Lines returns the inclusive line range, clamped to the file.
func (s *Source) Lines(start, end int) []string {
	if start < 0 {
		start = 0
	}
	if end >= s.NumLines() {
		end = s.NumLines() - 1
	}
	out := make([]string, 0, end-start+1)
	for i := start; i <= end; i++ {
		out = append(out, s.Line(i))
	}
	return out
}

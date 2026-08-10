package mdoc

import "bytes"

// Source holds a markdown file's bytes together with a line index so byte
// offsets reported by goldmark can be translated into line numbers.
type Source struct {
	Path string
	Data []byte

	lineStart []int // byte offset where each line begins
}

func NewSource(path string, data []byte) *Source {
	s := &Source{Path: path, Data: data, lineStart: []int{0}}
	for i, b := range data {
		if b == '\n' && i+1 <= len(data) {
			s.lineStart = append(s.lineStart, i+1)
		}
	}
	// A trailing newline creates a phantom empty last line; drop it.
	if n := len(s.lineStart); n > 1 && s.lineStart[n-1] == len(data) {
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
	start := s.lineStart[i]
	end := len(s.Data)
	if i+1 < len(s.lineStart) {
		end = s.lineStart[i+1]
	}
	return string(bytes.TrimRight(s.Data[start:end], "\r\n"))
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

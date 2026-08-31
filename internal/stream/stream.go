// Package stream carries a search from one mdgrep to the next: one region per
// line, as JSON, under a header that says the pipe holds a stream and not the
// markdown that otherwise arrives on stdin.
//
// A region names a file and a span of it rather than the text that was
// printed, so the next stage reads the file again and searches only there.
// That is what keeps a line number the number it had, a breadcrumb the trail
// it was, and a path a path -- which is why an edit can stand at the end of a
// pipeline where a stream of text would have left it nothing to write to.
package stream

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/riadafridishibly/mdgrep/internal/search"
)

// Version is the stream this mdgrep writes, and the only one it reads.
const Version = 1

// header is the first line of a stream. It is written even when nothing
// matched, so that a stage which searched and found nothing is told from a
// pipe that carries no stream at all.
type header struct {
	Version int `json:"mdgrep"`
}

// wireRegion is one line of the wire. Lines are 1-based and inclusive, the
// way --json reports a span, so a record read by eye says the same numbers
// the plain output would have printed.
type wireRegion struct {
	Path  string `json:"path"`
	Start int    `json:"start"`
	End   int    `json:"end"`
}

// Scope is what a stream selected: the files it named, in the order it named
// them, and which lines of each are still in play.
type Scope struct {
	// Paths is every file the stream named, once each, first mention first.
	Paths   []string
	regions map[string][]search.Region
}

// For is the regions of one file, zero-based, or nil when the stream did not
// name that file. Nil is "search everything" to search.Options, so a caller
// must only ask about the paths the stream gave it.
func (s *Scope) For(path string) []search.Region {
	if s == nil {
		return nil
	}
	return s.regions[filepath.Clean(path)]
}

// WriteHeader opens a stream on w.
func WriteHeader(w io.Writer) {
	fmt.Fprintf(w, "{\"mdgrep\":%d}\n", Version)
}

// WriteRegion writes one region of a result. start and end are zero-based and
// inclusive, as a search reports them, and go out one-based.
func WriteRegion(w io.Writer, path string, start, end int) {
	enc := json.NewEncoder(w)
	enc.Encode(wireRegion{Path: path, Start: start + 1, End: max(end, start) + 1})
}

// Parse reads a stream from data. ok is false when data is not one, which is
// how stdin holding markdown is told from stdin holding a pipeline: a stream
// opens with its header, and a document does not.
//
// A malformed line refuses the stream rather than being skipped. The regions
// are a search's whole subject here, so one that cannot be read is a search
// over less than was asked for, reported as "no matches" if it were let past.
func Parse(data []byte) (*Scope, bool, error) {
	first, rest, _ := bytes.Cut(data, []byte("\n"))
	v, ok := headerVersion(first)
	if !ok {
		return nil, false, nil
	}
	if v != Version {
		return nil, true, fmt.Errorf("stream version %d: this mdgrep speaks %d", v, Version)
	}

	s := &Scope{regions: map[string][]search.Region{}}
	sc := bufio.NewScanner(bytes.NewReader(rest))
	sc.Buffer(nil, maxLine)
	for n := 2; sc.Scan(); n++ {
		text := strings.TrimSpace(sc.Text())
		if text == "" {
			continue
		}
		r, err := readRegion(text)
		if err != nil {
			return nil, true, fmt.Errorf("stream line %d: %v", n, err)
		}
		// Two spellings of one path are one file, keyed the way the walk keys
		// what it found. A file read twice is parsed twice and, at the end of
		// a pipeline, written twice -- and the second write would carry none
		// of the first one's changes. The list keeps the spelling the stream
		// used, as the walk keeps the caller's.
		key := filepath.Clean(r.Path)
		if _, seen := s.regions[key]; !seen {
			s.Paths = append(s.Paths, r.Path)
		}
		s.regions[key] = append(s.regions[key], search.Region{Start: r.Start - 1, End: r.End - 1})
	}
	if err := sc.Err(); err != nil {
		return nil, true, err
	}
	return s, true, nil
}

// headerVersion reads a stream header off one line. A document does not open
// with a JSON object naming mdgrep, so a line that does is a stream.
func headerVersion(line []byte) (int, bool) {
	var h header
	if err := json.Unmarshal(bytes.TrimSpace(line), &h); err != nil || h.Version == 0 {
		return 0, false
	}
	return h.Version, true
}

// maxLine caps how long one record may be. A record is a path and two
// numbers, so the room is for a deep path rather than for a document.
const maxLine = 1 << 20

// readRegion reads one record. An unknown key is an error rather than a
// silently narrower search, the same way a plan entry refuses one.
func readRegion(line string) (wireRegion, error) {
	dec := json.NewDecoder(strings.NewReader(line))
	dec.DisallowUnknownFields()
	var r wireRegion
	if err := dec.Decode(&r); err != nil {
		return r, err
	}
	// A record is the whole of its line. What follows one is either a second
	// region the read would drop or a typo it would honour, and each leaves
	// the next stage searching less than it was handed.
	if _, err := dec.Token(); err != io.EOF {
		return r, fmt.Errorf("a record is the whole of its line")
	}
	switch {
	case r.Path == "":
		return r, fmt.Errorf("no path")
	case r.Start < 1:
		return r, fmt.Errorf("start %d: a line number starts at 1", r.Start)
	case r.End < r.Start:
		return r, fmt.Errorf("end %d is before start %d", r.End, r.Start)
	}
	return r, nil
}

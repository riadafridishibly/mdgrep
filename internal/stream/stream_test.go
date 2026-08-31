package stream

import (
	"strings"
	"testing"

	"github.com/riadafridishibly/mdgrep/internal/search"
)

// The header is the only thing telling a stream from the markdown that
// otherwise arrives on stdin, so what it does and does not accept is what
// decides whether a document is read as a document.
func TestParseTellsAStreamFromADocument(t *testing.T) {
	tests := []struct {
		name, text string
		want       bool
	}{
		{"stream", `{"mdgrep":1}` + "\n", true},
		{"spaced", `  {"mdgrep": 1}  ` + "\n", true},
		{"heading", "# Title\n\nbody\n", false},
		{"empty", "", false},
		{"object", "{}\n", false},
		{"regions without a header", `{"path":"a.md","start":1,"end":2}` + "\n", false},
		{"prose that opens with a brace", "{ not json\n", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, ok, err := Parse([]byte(tt.text))
			if err != nil {
				t.Fatal(err)
			}
			if ok != tt.want {
				t.Errorf("Parse(%q) is a stream = %v, want %v", tt.text, ok, tt.want)
			}
		})
	}
}

// A file named twice is parsed once, so the scope holds each path once with
// every region against it, in the order the stream first mentioned it.
func TestParseGroupsRegionsByFileInOrder(t *testing.T) {
	text := strings.Join([]string{
		`{"mdgrep":1}`,
		`{"path":"b.md","start":1,"end":3}`,
		`{"path":"a.md","start":10,"end":12}`,
		`{"path":"b.md","start":8,"end":8}`,
		``,
	}, "\n")

	s, ok, err := Parse([]byte(text))
	if err != nil || !ok {
		t.Fatalf("Parse = %v, %v", ok, err)
	}
	if want := []string{"b.md", "a.md"}; !equal(s.Paths, want) {
		t.Errorf("Paths = %v, want %v", s.Paths, want)
	}
	// Lines come back zero-based, the way a search counts them.
	want := []search.Region{{Start: 0, End: 2}, {Start: 7, End: 7}}
	if got := s.For("b.md"); !equalRegions(got, want) {
		t.Errorf("For(b.md) = %v, want %v", got, want)
	}
	if got := s.For("c.md"); got != nil {
		t.Errorf("For(c.md) = %v, want nil", got)
	}
}

// A nil scope is the whole file, which is what a run with no stream behind it
// has to mean.
func TestNilScopeIsEveryLine(t *testing.T) {
	var s *Scope
	if got := s.For("a.md"); got != nil {
		t.Errorf("For on no scope = %v, want nil", got)
	}
}

// A header with nothing after it is still a header, which is what a stage
// that searched and matched nothing sends on.
func TestHeaderStandsWithoutARecordOrANewline(t *testing.T) {
	for _, text := range []string{`{"mdgrep":1}`, `{"mdgrep":1}` + "\n"} {
		s, ok, err := Parse([]byte(text))
		if err != nil || !ok {
			t.Fatalf("Parse(%q) = %v, %v", text, ok, err)
		}
		if len(s.Paths) != 0 {
			t.Errorf("Parse(%q) named %v, want no files", text, s.Paths)
		}
	}
}

// Written and read back, a region is the same two line numbers: the wire is
// one-based because that is what a person reading it expects, and a search is
// zero-based, so the pair has to agree on which end converts.
func TestRegionSurvivesTheRoundTrip(t *testing.T) {
	var b strings.Builder
	WriteHeader(&b)
	WriteRegion(&b, "a.md", 6, 8)
	// A one-line node arrives with End below Start nowhere: a search reports
	// the same line twice, and so does the wire.
	WriteRegion(&b, "a.md", 12, 12)

	s, ok, err := Parse([]byte(b.String()))
	if err != nil || !ok {
		t.Fatalf("Parse = %v, %v", ok, err)
	}
	want := []search.Region{{Start: 6, End: 8}, {Start: 12, End: 12}}
	if got := s.For("a.md"); !equalRegions(got, want) {
		t.Errorf("round trip = %v, want %v", got, want)
	}
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func equalRegions(a, b []search.Region) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

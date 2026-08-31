package stream

import "testing"

var parseSeeds = []string{
	`{"mdgrep":1}` + "\n",
	`{"mdgrep":1}` + "\n" + `{"path":"a.md","start":1,"end":3}` + "\n",
	`{"mdgrep":1}` + "\n\n\n" + `{"path":"a.md","start":7,"end":7}`,
	`{"mdgrep":1}` + "\n" + `{"path":"a.md","start":0,"end":3}` + "\n",
	`{"mdgrep":1}` + "\n" + `{"path":"a.md","start":9,"end":2}` + "\n",
	`{"mdgrep":1}` + "\n" + `{"path":"","start":1,"end":1}` + "\n",
	`{"mdgrep":2}` + "\n",
	"# A document\n\n- [ ] not a stream\n",
	"{}\n",
	"",
}

// FuzzParseRegions holds the line numbers to what a search can use. The wire
// is one-based and a search is zero-based, so every record crosses that line
// once; a record that came back short of it would point a later stage at a
// line one off, or at line -1, which is a slice of the file nobody named.
func FuzzParseRegions(f *testing.F) {
	for _, s := range parseSeeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, text string) {
		if len(text) > 1<<14 {
			return
		}
		s, ok, err := Parse([]byte(text))
		if err != nil {
			if !ok {
				t.Fatalf("Parse(%q) refused something it did not claim was a stream", text)
			}
			return
		}
		if !ok {
			if s != nil {
				t.Fatalf("Parse(%q) is not a stream but handed back a scope", text)
			}
			return
		}
		for _, path := range s.Paths {
			if path == "" {
				t.Fatalf("Parse(%q) kept a nameless file", text)
			}
			regions := s.For(path)
			if len(regions) == 0 {
				t.Fatalf("Parse(%q) named %q with no regions", text, path)
			}
			for _, r := range regions {
				if r.Start < 0 || r.End < r.Start {
					t.Fatalf("Parse(%q) gave %q the region %d..%d", text, path, r.Start, r.End)
				}
			}
		}
		// A path is listed once however often the stream named it.
		seen := map[string]bool{}
		for _, path := range s.Paths {
			if seen[path] {
				t.Fatalf("Parse(%q) listed %q twice", text, path)
			}
			seen[path] = true
		}
	})
}

// Package report says why a run would not do what it was asked, in the two
// shapes a caller can read: a sentence, or one JSON object.
package report

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/riadafridishibly/mdgrep/internal/mdoc"
	"github.com/riadafridishibly/mdgrep/internal/render"
	"github.com/riadafridishibly/mdgrep/internal/search"
)

// File is one file's results, in the shape a refusal lists them: the source
// to read a matched line back out of, and what matched in it. The parse comes
// with them because a substitution asks the block tree what may be written at
// each match, which no line range can answer.
type File struct {
	Doc *mdoc.Doc
	Src *mdoc.Source
	Res []search.Result
}

// Words spells the two ways out of a refusal. A plan entry cannot pass a
// flag, so it is pointed at the keys it can set instead.
type Words struct{ Expect, Narrow string }

var (
	FlagWords = Words{"--expect", "narrow the search or pass --multi"}
	PlanWords = Words{`"expect"`, `narrow "match" or set "multi": true`}
)

// Gate decides whether an edit may go ahead on the number of nodes the
// search found. --expect states the count outright; without it a lone match is
// the only unambiguous instruction, and --multi waives that.
func Gate(total int, expect *int, multi bool, w Words) (Reason, int) {
	switch {
	case expect != nil && total != *expect:
		return Reason{
			Kind:     "expect",
			Text:     fmt.Sprintf("%s %d, but %d matched", w.Expect, *expect, total),
			Expected: *expect,
		}, 2
	case expect != nil:
		return Reason{}, 0
	case total == 0:
		return Reason{Kind: "nomatch", Text: "nothing matched"}, 1
	case total > 1 && !multi:
		return Reason{
			Kind: "ambiguous",
			Text: fmt.Sprintf("%d matches; %s", total, w.Narrow),
		}, 2
	}
	return Reason{}, 0
}

// Reason is why an edit was refused: a kind an --json reader can branch on, a
// sentence for everyone else, and the count --expect asked for when that is
// what went wrong.
type Reason struct {
	Kind     string
	Text     string
	Expected int
	// Entry is which entry of an --apply plan was refused, 1-based, and zero
	// when the refusal is a single edit's own.
	Entry int
	// Path is the file the refusal is about, when it is about a file rather
	// than an entry: one that cannot be written.
	Path string
	// Written names the files a run left changed before it stopped. A plan
	// applies whole or not at all, so this is empty except in the one case
	// that promise cannot be kept: a rename failing part way through.
	Written []string
	// Entries is how many entries a plan refused, on the record that closes
	// a refused run.
	Entries int
}

// entryPrefix says which entry of a plan a refusal belongs to, since a plan
// reports as many refusals as it has entries that cannot be carried out.
func entryPrefix(n int) string {
	if n == 0 {
		return ""
	}
	return fmt.Sprintf("entry %d: ", n)
}

// shownMatches caps how many hits a refusal lists. The list is there to make
// the next attempt narrower, and the whole point of a refusal is that there
// were too many to act on.
const shownMatches = 10

// Refused shows what the edit would have hit, so the next attempt can
// narrow the search rather than guess at it. A refusal is written in the
// format the caller asked its results in, so a program that can read the run
// that worked can read the one that did not.
func Refused(w io.Writer, files []File, total int, why Reason, f render.Format) {
	switch f {
	case render.JSON:
		refusedJSON(w, files, total, why)
		return
	case render.Compact:
		refusedCompact(w, files, total, why)
		return
	}
	fmt.Fprintf(w, "mdgrep: %s%s\n", entryPrefix(why.Entry), why.Text)
	n := 0
	for _, r := range files {
		for _, res := range r.Res {
			if n == shownMatches {
				fmt.Fprintf(w, "  … and %d more\n", total-shownMatches)
				return
			}
			fmt.Fprintf(w, "  %s:%d: %s\n", r.Src.Path, res.Start+1,
				strings.TrimSpace(r.Src.Line(res.Start)))
			n++
		}
	}
}

// refusedCompact says the same thing as tab-separated records, so a caller
// reading --format compact parses a refusal with the reader it already has:
//
//	error <TAB> kind <TAB> entry <TAB> total <TAB> expected <TAB> entries <TAB> path <TAB> message
//	match <TAB> path <TAB> line <TAB> text
//	written <TAB> path
//
// The fields of the error record are always all there, zero or empty where
// they do not apply, and the records that follow it are the hits that caused
// the refusal and the files a failed run left changed.
func refusedCompact(w io.Writer, files []File, total int, why Reason) {
	fmt.Fprintf(w, "error\t%s\t%d\t%d\t%d\t%d\t%s\t%s\n",
		why.Kind, why.Entry, total, why.Expected, why.Entries,
		render.Escape(why.Path), render.Escape(why.Text))
	shown := 0
	for _, r := range files {
		if shown == shownMatches {
			break
		}
		for _, res := range r.Res {
			if shown == shownMatches {
				break
			}
			fmt.Fprintf(w, "match\t%s\t%d\t%s\n", render.Escape(r.Src.Path), res.Start+1,
				render.Escape(validUTF8(strings.TrimSpace(r.Src.Line(res.Start)))))
			shown++
		}
	}
	for _, path := range why.Written {
		fmt.Fprintf(w, "written\t%s\n", render.Escape(path))
	}
}

// validUTF8 stands in for what a JSON encoder would do to a line of bytes that
// is not text, but does it here, so the object says the same thing the encoder
// would have written and a reader comparing it against the file knows why.
func validUTF8(s string) string { return strings.ToValidUTF8(s, "\uFFFD") }

// Match is one hit a refusal lists, so a reader can narrow the next attempt
// without searching again.
type Match struct {
	Path string `json:"path"`
	Line int    `json:"line"`
	Text string `json:"text"`
}

// Refusal is what --json says instead of a sentence: one object per refusal,
// with the kind a caller branches on and the hits that caused it.
type Refusal struct {
	Error    string   `json:"error"`
	Message  string   `json:"message"`
	Entry    int      `json:"entry,omitempty"`
	Path     string   `json:"path,omitempty"`
	Entries  int      `json:"entries,omitempty"`
	Written  []string `json:"written,omitempty"`
	Total    int      `json:"total"`
	Expected int      `json:"expected,omitempty"`
	Matches  []Match  `json:"matches"`
}

// refusedJSON says the same thing as one object, so a caller that asked
// for --json parses the refusal with the reader it already has rather than
// reading English back out of stderr.
func refusedJSON(w io.Writer, files []File, total int, why Reason) {
	out := Refusal{
		Error:    why.Kind,
		Message:  why.Text,
		Entry:    why.Entry,
		Path:     why.Path,
		Entries:  why.Entries,
		Written:  why.Written,
		Total:    total,
		Expected: why.Expected,
		Matches:  []Match{},
	}
	for _, r := range files {
		for _, res := range r.Res {
			if len(out.Matches) == shownMatches {
				json.NewEncoder(w).Encode(out)
				return
			}
			out.Matches = append(out.Matches, Match{
				Path: r.Src.Path,
				Line: res.Start + 1,
				Text: validUTF8(strings.TrimSpace(r.Src.Line(res.Start))),
			})
		}
	}
	json.NewEncoder(w).Encode(out)
}

func BestScore(res []search.Result) float64 {
	if len(res) == 0 {
		return 0
	}
	return res[0].Score
}

// WroteSoFar says which files a failed rename left changed, since a run that
// promised all or nothing owes the caller the list when it cannot keep that.
func WroteSoFar(written []string) string {
	if len(written) == 0 {
		return "nothing was written"
	}
	return "already written: " + strings.Join(written, ", ")
}

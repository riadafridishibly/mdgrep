// Package plan carries out a plan of edits: a file of entries, each naming its
// own file, search and edit, applied whole or not at all.
package plan

import (
	"bufio"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/riadafridishibly/mdgrep/internal/cli"
	"github.com/riadafridishibly/mdgrep/internal/edit"
	"github.com/riadafridishibly/mdgrep/internal/help"
	"github.com/riadafridishibly/mdgrep/internal/match"
	"github.com/riadafridishibly/mdgrep/internal/mdoc"
	"github.com/riadafridishibly/mdgrep/internal/render"
	"github.com/riadafridishibly/mdgrep/internal/report"
	"github.com/riadafridishibly/mdgrep/internal/search"
)

// planEntry is one edit of an --apply plan: which file, which node of it, and
// what to do with that node. The keys are the flags of the same names, so an
// entry says what one run of mdgrep would have said, and a plan says what a
// series of them would have — at one process, one parse per file, and one
// write per file.
type planEntry struct {
	Path string `json:"path"`
	// Match and Text are pointers because leaving a key out and giving it an
	// empty string are different instructions, the way --replace "" asks for
	// an empty replacement rather than for nothing.
	Match   *string `json:"match"`
	Op      edit.Op `json:"op"`
	Text    *string `json:"text"`
	Kind    string  `json:"kind"`
	Fixed   bool    `json:"fixed"`
	Expand  int     `json:"expand"`
	Section bool    `json:"section"`
	Body    bool    `json:"section-body"`
	Expect  *int    `json:"expect"`
	Multi   bool    `json:"multi"`
}

// planOps is every edit an entry may ask for, and whether it carries text.
var planOps = []struct {
	op   edit.Op
	text bool
}{
	{edit.OpCheck, false}, {edit.OpUncheck, false}, {edit.OpToggle, false},
	{edit.OpReplace, true}, {edit.OpSetText, true}, {edit.OpDelete, false},
	{edit.OpAppend, true}, {edit.OpPrepend, true},
}

func planOp(op edit.Op) (text, ok bool) {
	for _, p := range planOps {
		if p.op == op {
			return p.text, true
		}
	}
	return false, false
}

func planOpNames() string {
	names := make([]string, len(planOps))
	for i, p := range planOps {
		names[i] = string(p.op)
	}
	return strings.Join(names, ", ")
}

// applyKeeps names the flags that still mean something beside a plan: how the
// run reports itself, and whether it writes at all. Everything else either
// selects nodes or rewrites them, which is what the entries are for, so one
// passed alongside a plan is a misunderstanding worth reporting.
var applyKeeps = map[string]bool{
	"apply": true, "dry-run": true,
	"q": true, "quiet": true,
	"format": true, "json": true,
	"n": true, "line-number": true, "N": true, "no-line-number": true,
	"no-breadcrumb": true, "separator": true, "color": true,
}

// Run carries out a plan of edits. Every entry is planned against the
// files as they were read, and one that cannot be carried out refuses the whole
// run: a plan is a single instruction, and half of one applied is worse than
// none of it.
func Run(c *cli.Config, fs *flag.FlagSet, format render.Format) int {
	if err := applyFlags(fs); err != nil {
		fmt.Fprintf(os.Stderr, "mdgrep: %v\n%s\n", err, help.Hint)
		return 2
	}
	path, _ := c.Apply.Value()
	text, err := cli.ReadText(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "mdgrep: --apply: %v\n", err)
		return 2
	}
	entries, err := readPlan(text)
	if err != nil {
		fmt.Fprintf(os.Stderr, "mdgrep: --apply: %v\n", err)
		return 2
	}

	cache := newDocCache()
	planned := map[string][]planChange{}
	refused := 0
	for i, e := range entries {
		changes, path, ok := planOne(i+1, e, cache, format)
		if !ok {
			refused++
			continue
		}
		planned[path] = append(planned[path], changes...)
	}
	if refused > 0 {
		refuse(format, report.Reason{
			Kind:    "refused",
			Text:    fmt.Sprintf("%d of %d entries refused; nothing was written", refused, len(entries)),
			Entries: refused,
		})
		return 2
	}
	for _, path := range cache.order {
		if err := orderChanges(planned[path]); err != nil {
			refuse(format, report.Reason{
				Kind: "conflict",
				Text: fmt.Sprintf("%s: %v", path, err),
				Path: path,
			})
			return 2
		}
	}

	// A plan applies whole or not at all, so every file is written beside
	// itself first and only renamed into place once all of them are there.
	// A file that cannot be written is then found before any has been.
	if !c.DryRun {
		if err := stageAll(cache, planned, format); err != nil {
			return 2
		}
	}

	out := bufio.NewWriter(os.Stdout)
	defer out.Flush()
	p := c.Printer(out, format)
	for _, path := range cache.order {
		changes := changesOf(planned[path])
		if len(changes) == 0 || c.Quiet {
			continue
		}
		p.PrintEdits(cache.docs[path].Src, changes, c.DryRun)
	}
	out.Flush()
	return 0
}

// stageAll writes every file the plan touches beside itself, then renames
// them all, so a plan that cannot be carried out in full leaves nothing
// behind. A rename that fails part way through has already put some files in
// place, and the refusal names them.
func stageAll(cache *docCache, planned map[string][]planChange, format render.Format) error {
	var files []edit.File
	for _, path := range cache.order {
		changes := changesOf(planned[path])
		if len(changes) == 0 || !edit.Changed(changes) {
			continue
		}
		files = append(files, edit.File{Path: path, Content: edit.Apply(cache.docs[path].Src, changes)})
	}
	failed, written, err := edit.CommitAll(files)
	if err != nil {
		refuse(format, report.Reason{
			Kind:    "write",
			Text:    fmt.Sprintf("%s: %v; %s", failed, err, report.WroteSoFar(written)),
			Path:    failed,
			Written: written,
		})
		return err
	}
	return nil
}

// refuse reports a refusal that has no matches to show -- a malformed entry, a
// file that cannot be read or written, two entries over one node -- through the
// same reader a caller uses for the refusals that do.
func refuse(format render.Format, why report.Reason) {
	report.Refused(os.Stderr, nil, 0, why, format)
}

// applyFlags rejects the flags a plan supersedes, rather than accepting them
// and quietly doing what the entries say instead.
func applyFlags(fs *flag.FlagSet) error {
	if extra := cli.Given(fs, func(name string) bool { return !applyKeeps[name] }); extra != "" {
		return fmt.Errorf("--apply carries its own search and edit in every entry, so there is nothing left for %s to say", extra)
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("--apply names its files in the plan, so it takes no PATTERN and no PATH: %s",
			strings.Join(fs.Args(), " "))
	}
	return nil
}

// readPlan reads the plan as JSON objects, one per line the way a caller
// generates them, though any whitespace between them will do. Unknown keys are
// an error: a misspelled one would otherwise be a silently different edit.
func readPlan(text string) ([]planEntry, error) {
	if strings.HasPrefix(strings.TrimLeft(text, " \t\r\n"), "[") {
		return nil, errors.New("a plan is one JSON object per line, not an array of them")
	}
	dec := json.NewDecoder(strings.NewReader(text))
	dec.DisallowUnknownFields()
	var out []planEntry
	for {
		var e planEntry
		err := dec.Decode(&e)
		if errors.Is(err, io.EOF) {
			if len(out) == 0 {
				return nil, errors.New("the plan is empty")
			}
			return out, nil
		}
		if err != nil {
			return nil, fmt.Errorf("entry %d: %v", len(out)+1, err)
		}
		out = append(out, e)
	}
}

// planChange remembers which entry asked for a change, so two entries reaching
// for the same lines can be reported as the pair they are.
type planChange struct {
	entry int
	edit.Change
}

func changesOf(changes []planChange) []edit.Change {
	out := make([]edit.Change, len(changes))
	for i, c := range changes {
		out[i] = c.Change
	}
	return out
}

// planOne turns one entry into the changes it asks for, reporting a refusal the
// way a single edit reports one and answering whether the entry can be carried
// out at all.
func planOne(n int, e planEntry, cache *docCache, format render.Format) ([]planChange, string, bool) {
	fail := func(kind string, err error) ([]planChange, string, bool) {
		refuse(format, report.Reason{Kind: kind, Text: err.Error(), Entry: n})
		return nil, "", false
	}
	opt, ed, matcher, err := planSearch(e)
	if err != nil {
		return fail("entry", err)
	}
	doc, path, err := cache.get(e.Path)
	if err != nil {
		return fail("file", err)
	}
	res := search.File(doc, matcher, opt)
	if why, code := report.Gate(len(res), e.Expect, e.Multi, report.PlanWords); code != 0 {
		why.Entry = n
		report.Refused(os.Stderr, []report.File{{Src: doc.Src, Res: res}}, len(res), why, format)
		return nil, "", false
	}
	changes, err := edit.Plan(doc.Src, res, ed)
	if err != nil {
		return fail("edit", err)
	}
	out := make([]planChange, len(changes))
	for i, c := range changes {
		out[i] = planChange{entry: n, Change: c}
	}
	return out, path, true
}

// planSearch reads an entry as the search and the edit it stands for, and
// rejects the combinations a run of the same flags would reject.
func planSearch(e planEntry) (search.Options, edit.Options, match.Matcher, error) {
	var opt search.Options
	var ed edit.Options
	bad := func(format string, args ...any) (search.Options, edit.Options, match.Matcher, error) {
		return opt, ed, nil, fmt.Errorf(format, args...)
	}
	switch {
	case e.Path == "":
		return bad(`no "path": an entry says which file it edits`)
	case e.Match == nil:
		return bad(`no "match": the pattern that selects the node to edit`)
	case e.Op == edit.OpNone:
		return bad(`no "op": one of %s`, planOpNames())
	}
	takesText, ok := planOp(e.Op)
	switch {
	case !ok:
		return bad("unknown op %q: one of %s", e.Op, planOpNames())
	case takesText && e.Text == nil:
		return bad(`op %q is the text to write, so it wants "text"`, e.Op)
	case !takesText && e.Text != nil:
		return bad(`op %q writes no text of its own, so it takes no "text"`, e.Op)
	case e.Expect != nil && *e.Expect < 1:
		return bad(`"expect" states how many nodes the match should find, so it wants a count above zero`)
	case e.Expand < 0:
		return bad(`"expand" is how many levels to climb from the matched node, so it cannot be negative`)
	case e.Op.Node() && (e.Section || e.Body):
		return bad(`op %q edits the matched node, so "section" has nothing to widen; use "replace"`, e.Op)
	}

	kinds, err := cli.ParseKinds(e.Kind)
	if err != nil {
		return bad("%v", err)
	}
	opt = search.Options{
		Kinds:   kinds,
		Expand:  e.Expand,
		Section: e.Section,
		Body:    e.Body,
		// Each entry names one node, so neighbouring hits stay apart rather
		// than being run together the way printing runs them.
		Distinct: true,
	}
	switch e.Op {
	case edit.OpCheck, edit.OpUncheck, edit.OpToggle:
		opt.Task = search.TaskAny
	}

	mode := match.Regexp
	if e.Fixed {
		mode = match.Substring
	}
	// A plan says what it searches for and nothing more, so every other input
	// to the matcher is named here rather than left to a default a command
	// line would have supplied.
	matcher, err := cli.BuildMatcher(cli.Matching{
		Patterns: []string{*e.Match},
		Mode:     mode,
		Case:     cli.Smart,
		MinScore: cli.DefaultMinScore,
	})
	if err != nil {
		return bad("%v", err)
	}
	ed = edit.Options{Op: e.Op}
	if takesText {
		ed.Text = *e.Text
	}
	return opt, ed, matcher, nil
}

// orderChanges sorts one file's changes into the ascending, non-overlapping sequence
// applying them expects, and refuses a plan where two entries reach for the
// same lines: they were planned against the same version of the file, so the
// second would be rewriting text the first has already taken away.
func orderChanges(changes []planChange) error {
	sort.SliceStable(changes, func(i, j int) bool {
		if changes[i].Start != changes[j].Start {
			return changes[i].Start < changes[j].Start
		}
		return changes[i].End < changes[j].End
	})
	if len(changes) == 0 {
		return nil
	}
	// The reach is the furthest line anything so far rewrites, and holder is
	// the entry that reaches it -- which is not always the entry before, since
	// one long change can cover several short ones.
	reach, holder := changes[0].End, 0
	for i, c := range changes[1:] {
		if c.Start <= reach {
			return fmt.Errorf("entry %d edits %s, which entry %d already rewrites",
				c.entry, span(c.Change), changes[holder].entry)
		}
		if c.End > reach {
			reach, holder = c.End, i+1
		}
	}
	return nil
}

// span names a change's lines the way an error can point at them: the range it
// rewrites, or the line an insertion lands beside.
func span(c edit.Change) string {
	if c.End <= c.Start {
		return fmt.Sprintf("line %d", c.Start+1)
	}
	return fmt.Sprintf("lines %d-%d", c.Start+1, c.End+1)
}

// docCache parses each file of a plan once, however many entries name it, and
// keeps the order they were first named in so the output follows the plan.
type docCache struct {
	docs  map[string]*mdoc.Doc
	info  map[string]os.FileInfo
	order []string
	// alias maps a spelling that reached a file already held under another
	// name to that name, so the second entry to use it costs a map lookup
	// rather than another stat.
	alias map[string]string
	// sameSize groups the names held so far by what two spellings of one file
	// almost always agree on, so a new spelling is compared against the few
	// files that could be it before it is compared against all of them.
	sameSize map[fileID][]string
}

// fileID is the part of a file's identity a map can be keyed on. It does not
// decide identity -- os.SameFile does that -- it only says which held files
// are worth asking about first.
type fileID struct {
	size int64
	mod  int64
}

func idOf(fi os.FileInfo) fileID { return fileID{fi.Size(), fi.ModTime().UnixNano()} }

func newDocCache() *docCache {
	return &docCache{
		docs:     map[string]*mdoc.Doc{},
		info:     map[string]os.FileInfo{},
		alias:    map[string]string{},
		sameSize: map[fileID][]string{},
	}
}

// get answers with the parsed file and the name the plan is holding it under.
// "docs/x.md", "./docs/x.md", a symlink and an absolute path are one file, and
// answering each with the spelling the plan used first gathers the changes of
// every entry that reaches it in one place -- rather than planning each against
// the original and writing the file twice, the second write undoing the first.
func (d *docCache) get(path string) (*mdoc.Doc, string, error) {
	if doc, ok := d.docs[path]; ok {
		return doc, path, nil
	}
	if seen, ok := d.alias[path]; ok {
		return d.docs[seen], seen, nil
	}
	fi, err := os.Stat(path)
	if err != nil {
		return nil, "", err
	}
	if seen := d.heldAs(fi); seen != "" {
		d.alias[path] = seen
		return d.docs[seen], seen, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, "", err
	}
	doc := mdoc.Parse(path, data)
	d.docs[path], d.info[path] = doc, fi
	d.order = append(d.order, path)
	id := idOf(fi)
	d.sameSize[id] = append(d.sameSize[id], path)
	return doc, path, nil
}

// heldAs is the name the plan already holds this file under, or "" if it has
// not read the file yet. Size and modification time are what two spellings of
// one file agree on in the ordinary case, so they narrow the question to a
// handful of candidates -- but the two spellings are stat'd at different
// moments, and anything writing the file in between leaves them disagreeing.
// A file the bucket misses is therefore not yet a file the plan has not seen:
// the rest have to be asked before it can be read a second time, since taking
// one file for two plans each change against the original and writes the file
// twice, the second write undoing the first.
func (d *docCache) heldAs(fi os.FileInfo) string {
	id := idOf(fi)
	for _, seen := range d.sameSize[id] {
		if os.SameFile(fi, d.info[seen]) {
			return seen
		}
	}
	for _, seen := range d.order {
		// The bucket has answered for these already.
		if idOf(d.info[seen]) == id {
			continue
		}
		if os.SameFile(fi, d.info[seen]) {
			return seen
		}
	}
	return ""
}

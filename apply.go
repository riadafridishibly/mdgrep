package main

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

	"github.com/riadafridishibly/mdgrep/internal/edit"
	"github.com/riadafridishibly/mdgrep/internal/match"
	"github.com/riadafridishibly/mdgrep/internal/mdoc"
	"github.com/riadafridishibly/mdgrep/internal/render"
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

// runApply carries out a plan of edits. Every entry is planned against the
// files as they were read, and one that cannot be carried out refuses the whole
// run: a plan is a single instruction, and half of one applied is worse than
// none of it.
func runApply(c config, fs *flag.FlagSet, format render.Format) int {
	if err := applyFlags(fs); err != nil {
		fmt.Fprintf(os.Stderr, "mdgrep: %v\n%s\n", err, hint)
		return 2
	}
	text, err := readText(c.apply.val)
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
		changes, path, ok := planOne(i+1, e, cache, format == render.JSON)
		if !ok {
			refused++
			continue
		}
		planned[path] = append(planned[path], changes...)
	}
	if refused > 0 {
		fmt.Fprintf(os.Stderr, "mdgrep: %d of %d entries refused; nothing was written\n",
			refused, len(entries))
		return 2
	}
	for _, path := range cache.order {
		if err := orderChanges(planned[path]); err != nil {
			fmt.Fprintf(os.Stderr, "mdgrep: %s: %v\n", path, err)
			return 2
		}
	}

	out := bufio.NewWriter(os.Stdout)
	defer out.Flush()
	p := newPrinter(out, c, format)
	for _, path := range cache.order {
		changes := changesOf(planned[path])
		if len(changes) == 0 {
			continue
		}
		src := cache.docs[path].Src
		if !c.dryRun && changed(changes) {
			if err := edit.Write(path, edit.Apply(src, changes)); err != nil {
				fmt.Fprintf(os.Stderr, "mdgrep: %s: %v\n", path, err)
				return 2
			}
		}
		if !c.quiet {
			p.PrintEdits(src, changes, c.dryRun)
		}
	}
	out.Flush()
	return 0
}

// applyFlags rejects the flags a plan supersedes, rather than accepting them
// and quietly doing what the entries say instead.
func applyFlags(fs *flag.FlagSet) error {
	var extra []string
	fs.Visit(func(f *flag.Flag) {
		if applyKeeps[f.Name] {
			return
		}
		dash := "--"
		if len(f.Name) == 1 {
			dash = "-"
		}
		extra = append(extra, dash+f.Name)
	})
	if len(extra) > 0 {
		return fmt.Errorf("--apply carries its own search and edit in every entry, so there is nothing left for %s to say",
			strings.Join(extra, ", "))
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
func planOne(n int, e planEntry, cache *docCache, asJSON bool) ([]planChange, string, bool) {
	fail := func(err error) ([]planChange, string, bool) {
		fmt.Fprintf(os.Stderr, "mdgrep: entry %d: %v\n", n, err)
		return nil, "", false
	}
	opt, ed, matcher, err := planSearch(e)
	if err != nil {
		return fail(err)
	}
	doc, path, err := cache.get(e.Path)
	if err != nil {
		return fail(err)
	}
	res := search.File(doc, matcher, opt)
	expect := optInt{}
	if e.Expect != nil {
		expect = optInt{val: *e.Expect, set: true}
	}
	if why, code := countGate(len(res), expect, e.Multi, planWords); code != 0 {
		why.entry = n
		reportRefused(os.Stderr, []fileResult{{doc.Src, res}}, len(res), why, asJSON)
		return nil, "", false
	}
	changes, err := edit.Plan(doc.Src, res, ed)
	if err != nil {
		return fail(err)
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
	case e.Op.Node() && (e.Section || e.Body):
		return bad(`op %q edits the matched node, so "section" has nothing to widen; use "replace"`, e.Op)
	}

	kinds, err := parseKinds(e.Kind)
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
	matcher, err := buildMatcher(config{patterns: patternList{*e.Match}, minScore: 0.7}, mode, false)
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
	end := -1
	for i, c := range changes {
		if c.Start <= end {
			return fmt.Errorf("entry %d edits %s, which entry %d already rewrites",
				c.entry, span(c.Change), changes[i-1].entry)
		}
		end = max(end, c.End)
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
}

func newDocCache() *docCache {
	return &docCache{docs: map[string]*mdoc.Doc{}, info: map[string]os.FileInfo{}}
}

// get answers with the parsed file and the name the plan is holding it under.
// Two entries can name one file differently — "docs/x.md" and "./docs/x.md", a
// symlink, an absolute path — and taking those for two files would plan each
// against the original and then write the file twice, the second write undoing
// the first. The answer is the spelling the plan used first, so the changes of
// every entry that reaches this file are gathered in one place.
func (d *docCache) get(path string) (*mdoc.Doc, string, error) {
	if doc, ok := d.docs[path]; ok {
		return doc, path, nil
	}
	fi, err := os.Stat(path)
	if err != nil {
		return nil, "", err
	}
	for _, seen := range d.order {
		if os.SameFile(fi, d.info[seen]) {
			return d.docs[seen], seen, nil
		}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, "", err
	}
	doc := mdoc.Parse(path, data)
	d.docs[path], d.info[path] = doc, fi
	d.order = append(d.order, path)
	return doc, path, nil
}

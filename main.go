// Command mdgrep searches markdown by node instead of by line: a hit inside a
// bullet prints the whole bullet, a hit in a heading can print its whole
// section, and the surrounding context is counted in blocks rather than lines.
package main

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"os"
	"runtime"
	"runtime/debug"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/riadafridishibly/mdgrep/internal/edit"
	"github.com/riadafridishibly/mdgrep/internal/help"
	"github.com/riadafridishibly/mdgrep/internal/match"
	"github.com/riadafridishibly/mdgrep/internal/mdoc"
	"github.com/riadafridishibly/mdgrep/internal/render"
	"github.com/riadafridishibly/mdgrep/internal/report"
	"github.com/riadafridishibly/mdgrep/internal/search"
	"github.com/riadafridishibly/mdgrep/internal/walk"
)

// version names a build the module system cannot place: go build from a clone
// with no tag over it. An installed binary knows better, and buildVersion asks
// it first.
const version = "0.2.0"

// buildVersion is what -V reports. A binary from "go install <path>@v0.2.0"
// carries the tag it was built from, so it should say so rather than repeat
// whatever the source tree last hardcoded.
func buildVersion() string {
	info, ok := debug.ReadBuildInfo()
	return moduleVersion(info, ok)
}

func moduleVersion(info *debug.BuildInfo, ok bool) string {
	if !ok || info.Main.Version == "" || info.Main.Version == "(devel)" {
		return version
	}
	return strings.TrimPrefix(info.Main.Version, "v")
}

type config struct {
	patterns  patternList
	forceCase bool
	forceFold bool
	word      bool
	invert    bool
	minScore  float64
	kinds     string
	anchorSty string
	task      bool
	checked   bool
	unchecked bool
	opt       search.Options
	context   int
	noNums    bool
	noCrumb   bool
	separator optString
	truncate  int
	outline   bool
	color     string
	jsonOut   bool
	format    optString
	count     bool
	filesOnly bool
	quiet     bool
	exts      string
	hidden    bool
	noIgnore  bool
	help      bool
	showVer   bool

	check     bool
	uncheck   bool
	toggle    bool
	del       bool
	replace   optString
	replFrom  optString
	setText   optString
	setFrom   optString
	appendTo  optString
	appFrom   optString
	prependTo optString
	preFrom   optString
	multi     bool
	expect    optInt
	dryRun    bool
	apply     optString
}

// optString remembers whether a text flag was given at all, so --replace ""
// asks for an empty replacement rather than for nothing.
type optString struct {
	val string
	set bool
}

func (o *optString) String() string { return o.val }

func (o *optString) Set(v string) error {
	o.val, o.set = v, true
	return nil
}

// optInt is optString's counterpart for a count, so "--expect 0" is a claim
// the run can reject rather than the same thing as leaving --expect out.
type optInt struct {
	val int
	set bool
}

func (o *optInt) String() string { return strconv.Itoa(o.val) }

// ptr is the count --expect asked for, or nil when it did not ask: the shape
// a reader outside the flag package can take without knowing about flags.
func (o optInt) ptr() *int {
	if !o.set {
		return nil
	}
	return &o.val
}

func (o *optInt) Set(v string) error {
	n, err := strconv.Atoi(v)
	if err != nil {
		return fmt.Errorf("not a number: %q", v)
	}
	o.val, o.set = n, true
	return nil
}

// patternList collects repeated -e flags, which are alternatives to one
// another the way they are in grep.
type patternList []string

func (p *patternList) String() string { return strings.Join(*p, "|") }

func (p *patternList) Set(v string) error {
	*p = append(*p, v)
	return nil
}

func main() {
	os.Exit(run())
}

func run() int {
	var c config
	var fuzzy, fixed, anchor, smart bool

	fs := flag.NewFlagSet("mdgrep", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	bind := func(set func(name string), names ...string) {
		for _, n := range names {
			set(n)
		}
	}
	bind(func(n string) { fs.Var(&c.patterns, n, "") }, "e", "regexp")
	bind(func(n string) { fs.BoolVar(&fixed, n, false, "") }, "F", "fixed-strings")
	fs.BoolVar(&fuzzy, "fuzzy", false, "")
	fs.BoolVar(&anchor, "anchor", false, "")
	fs.StringVar(&c.anchorSty, "anchor-style", "", "")
	fs.Float64Var(&c.minScore, "min-score", 0.7, "")
	bind(func(n string) { fs.BoolVar(&c.word, n, false, "") }, "w", "word-regexp")
	bind(func(n string) { fs.BoolVar(&c.invert, n, false, "") }, "v", "invert-match")
	bind(func(n string) { fs.BoolVar(&c.forceFold, n, false, "") }, "i", "ignore-case")
	bind(func(n string) { fs.BoolVar(&c.forceCase, n, false, "") }, "s", "case-sensitive")
	bind(func(n string) { fs.BoolVar(&smart, n, false, "") }, "S", "smart-case")
	bind(func(n string) { fs.StringVar(&c.kinds, n, "", "") }, "k", "kind")
	fs.BoolVar(&c.task, "task", false, "")
	bind(func(n string) { fs.BoolVar(&c.checked, n, false, "") }, "checked", "done")
	bind(func(n string) { fs.BoolVar(&c.unchecked, n, false, "") }, "unchecked", "todo")
	fs.IntVar(&c.opt.Expand, "expand", 0, "")
	fs.BoolVar(&c.opt.Section, "section", false, "")
	fs.BoolVar(&c.opt.Body, "section-body", false, "")
	fs.BoolVar(&c.check, "check", false, "")
	fs.BoolVar(&c.uncheck, "uncheck", false, "")
	fs.BoolVar(&c.toggle, "toggle", false, "")
	fs.BoolVar(&c.del, "delete", false, "")
	fs.Var(&c.replace, "replace", "")
	fs.Var(&c.replFrom, "replace-from", "")
	fs.Var(&c.setText, "set-text", "")
	fs.Var(&c.setFrom, "set-text-from", "")
	fs.Var(&c.appendTo, "append", "")
	fs.Var(&c.appFrom, "append-from", "")
	fs.Var(&c.prependTo, "prepend", "")
	fs.Var(&c.preFrom, "prepend-from", "")
	fs.BoolVar(&c.multi, "multi", false, "")
	fs.Var(&c.expect, "expect", "")
	fs.BoolVar(&c.dryRun, "dry-run", false, "")
	fs.Var(&c.apply, "apply", "")
	bind(func(n string) { fs.IntVar(&c.opt.Before, n, 0, "") }, "B", "before")
	bind(func(n string) { fs.IntVar(&c.opt.After, n, 0, "") }, "A", "after")
	bind(func(n string) { fs.IntVar(&c.context, n, 0, "") }, "C", "context")
	fs.IntVar(&c.opt.Lines, "lines", 0, "")
	bind(func(n string) { fs.IntVar(&c.opt.Max, n, 0, "") }, "m", "max-count")
	bind(func(n string) { fs.BoolVar(&c.noNums, n, false, "") }, "N", "no-line-number")
	// Numbering is already on; -n exists so a grep habit does not error out.
	bind(func(n string) { fs.Bool(n, false, "") }, "n", "line-number")
	fs.BoolVar(&c.noCrumb, "no-breadcrumb", false, "")
	fs.BoolVar(&c.outline, "outline", false, "")
	fs.Var(&c.separator, "separator", "")
	fs.IntVar(&c.truncate, "truncate", 0, "")
	fs.StringVar(&c.color, "color", "auto", "")
	fs.BoolVar(&c.jsonOut, "json", false, "")
	fs.Var(&c.format, "format", "")
	bind(func(n string) { fs.BoolVar(&c.count, n, false, "") }, "c", "count")
	bind(func(n string) { fs.BoolVar(&c.filesOnly, n, false, "") }, "l", "files-with-matches")
	bind(func(n string) { fs.BoolVar(&c.quiet, n, false, "") }, "q", "quiet")
	fs.StringVar(&c.exts, "ext", "md,markdown,mdown,mkd,mdx", "")
	fs.BoolVar(&c.hidden, "hidden", false, "")
	fs.BoolVar(&c.noIgnore, "no-ignore", false, "")
	bind(func(n string) { fs.BoolVar(&c.help, n, false, "") }, "h", "help")
	bind(func(n string) { fs.BoolVar(&c.showVer, n, false, "") }, "V", "version")

	if err := fs.Parse(permute(fs, os.Args[1:])); err != nil {
		fmt.Fprintf(os.Stderr, "mdgrep: %v\n%s\n", err, help.Hint)
		return 2
	}
	if c.help {
		topic := ""
		if fs.NArg() == 1 {
			topic = helpTopic(os.Args[1:], fs.Arg(0))
		}
		text, err := help.Text(topic)
		if err != nil {
			fmt.Fprintf(os.Stderr, "mdgrep: %v\n", err)
			return 2
		}
		fmt.Fprint(os.Stdout, text)
		return 0
	}
	if c.showVer {
		fmt.Fprintf(os.Stdout, "mdgrep %s\n", buildVersion())
		return 0
	}
	format, err := parseFormat(c.format, c.jsonOut, c.outline)
	if err != nil {
		fmt.Fprintf(os.Stderr, "mdgrep: %v\n%s\n", err, help.Hint)
		return 2
	}
	if c.truncate < 0 {
		fmt.Fprintf(os.Stderr, "mdgrep: --truncate %d: a cap on printed lines cannot be negative\n", c.truncate)
		return 2
	}
	// A plan is a whole run of its own: it names its files, its searches and
	// its edits, so nothing below this point has anything left to work out.
	if c.apply.set {
		return runApply(c, fs, format)
	}
	if c.outline {
		if err := outlineFlags(fs); err != nil {
			fmt.Fprintf(os.Stderr, "mdgrep: %v\n%s\n", err, help.Hint)
			return 2
		}
		// --outline is a question about structure, so it fills in the search a
		// caller would otherwise spell out: every heading, matched by nothing
		// in particular. Either half can still be overridden.
		if c.kinds == "" {
			c.kinds = "heading"
		}
	}
	kinds, err := parseKinds(c.kinds)
	if err != nil {
		fmt.Fprintf(os.Stderr, "mdgrep: %v\n", err)
		return 2
	}
	c.opt.Kinds = kinds
	c.opt.Task = taskFilter(c)

	ed, err := buildEdit(&c)
	// Neighbouring hits are run together for a person reading the page as one
	// passage, and kept apart for everyone else: an edit rewrites each node on
	// its own, a machine format is counted and iterated over, an outline is one
	// line per heading, and -c is a tally of nodes.
	c.opt.Distinct = ed.Op != edit.OpNone || format != render.Plain || c.count
	if err != nil {
		fmt.Fprintf(os.Stderr, "mdgrep: %v\n", err)
		return 2
	}

	// The first positional is PATTERN unless -e already supplied one, in which
	// case every positional is a path. Filters never stand in for a pattern:
	// an empty one matches everything, so "mdgrep '' docs --todo" scopes a
	// filter to a directory the way grep would.
	paths := fs.Args()
	if len(c.patterns) == 0 {
		switch {
		case c.outline:
			// An outline names no pattern, so every positional is a path.
			c.patterns = patternList{""}
		case fs.NArg() == 0:
			fmt.Fprintf(os.Stderr, "mdgrep: missing PATTERN\n%s\n", help.Hint)
			return 2
		default:
			c.patterns, paths = patternList{fs.Arg(0)}, paths[1:]
		}
	}

	mode := match.Regexp
	switch {
	case fuzzy && fixed:
		fmt.Fprintln(os.Stderr, "mdgrep: --fuzzy and --fixed-strings are mutually exclusive")
		return 2
	case anchor && (fuzzy || fixed || c.word || c.invert):
		fmt.Fprintln(os.Stderr, "mdgrep: --anchor selects a heading by name and takes no other matching flag")
		return 2
	case fuzzy:
		mode = match.Fuzzy
		// A fuzzy pattern is a question about which node fits best, so the
		// answer is ordered by score. An exact search is a filter, and keeps
		// grep's order.
		c.opt.Rank = true
	case fixed:
		mode = match.Substring
	}
	if c.context > 0 {
		c.opt.Before, c.opt.After = c.context, c.context
	}
	// An anchor search says which heading it wants, so there is nothing left
	// for a matcher to score or to highlight.
	matcher := match.All()
	if anchor {
		if c.opt.Anchor, err = buildAnchor(c); err != nil {
			fmt.Fprintf(os.Stderr, "mdgrep: %v\n", err)
			return 2
		}
	} else if matcher, err = buildMatcher(c, mode, smart); err != nil {
		fmt.Fprintf(os.Stderr, "mdgrep: %v\n", err)
		return 2
	}

	files, useStdin, err := walk.Files(paths, splitSet(c.exts), c.hidden, c.noIgnore)
	if err != nil {
		fmt.Fprintf(os.Stderr, "mdgrep: %v\n", err)
		return 2
	}
	if ed.Op != edit.OpNone && useStdin {
		fmt.Fprintln(os.Stderr, "mdgrep: an edit needs files to write to, not stdin")
		return 2
	}

	out := bufio.NewWriter(os.Stdout)
	defer out.Flush()
	p := newPrinter(out, c, format)

	found := false
	emit := func(src *mdoc.Source, res []search.Result) {
		if len(res) == 0 {
			return
		}
		found = true
		switch {
		case c.quiet:
		case c.filesOnly:
			fmt.Fprintln(out, src.Path)
		case c.count:
			fmt.Fprintf(out, "%s:%d\n", src.Path, len(res))
		default:
			p.Print(src, res, matcher)
		}
	}

	if useStdin {
		data, err := io.ReadAll(os.Stdin)
		if err != nil {
			fmt.Fprintf(os.Stderr, "mdgrep: stdin: %v\n", err)
			return 2
		}
		doc := mdoc.Parse("<stdin>", data)
		emit(doc.Src, search.File(doc, matcher, c.opt))
	}

	results := make([]report.File, len(files))
	var wg sync.WaitGroup
	jobs := make(chan int)
	workers := min(runtime.NumCPU(), 8)
	for range workers {
		wg.Go(func() {
			for i := range jobs {
				data, err := os.ReadFile(files[i])
				if err != nil {
					fmt.Fprintf(os.Stderr, "mdgrep: %v\n", err)
					continue
				}
				doc := mdoc.Parse(files[i], data)
				results[i] = report.File{Src: doc.Src, Res: search.File(doc, matcher, c.opt)}
			}
		})
	}
	for i := range files {
		jobs <- i
	}
	close(jobs)
	wg.Wait()

	if ed.Op != edit.OpNone {
		return runEdits(out, p, results, ed, c)
	}
	if c.opt.Rank {
		// Each file already holds its results best first, so a file is worth
		// as much as its best one.
		sort.SliceStable(results, func(i, j int) bool {
			return report.BestScore(results[i].Res) > report.BestScore(results[j].Res)
		})
	}
	for _, r := range results {
		if r.Src != nil {
			emit(r.Src, r.Res)
		}
	}
	if !found {
		return 1
	}
	return 0
}

func newPrinter(out *bufio.Writer, c config, format render.Format) *render.Printer {
	return &render.Printer{
		W:           out,
		Color:       useColor(c.color),
		LineNumbers: !c.noNums,
		Breadcrumb:  !c.noCrumb,
		Format:      format,
		Separator:   separator(c.separator),
		Truncate:    c.truncate,
	}
}

// buildEdit reads the editing flags as one operation, and rejects the
// combinations that would make an edit rewrite something other than what the
// same flags would have printed.
func buildEdit(c *config) (edit.Options, error) {
	var ops []edit.Options
	add := func(op edit.Op, text string) { ops = append(ops, edit.Options{Op: op, Text: text}) }
	if c.check {
		add(edit.OpCheck, "")
	}
	if c.uncheck {
		add(edit.OpUncheck, "")
	}
	if c.toggle {
		add(edit.OpToggle, "")
	}
	if c.del {
		add(edit.OpDelete, "")
	}
	// Every edit that takes text takes it either inline or from a file, and
	// the pair is one flag with two spellings rather than two edits.
	for _, t := range textOps(c) {
		switch {
		case t.inline.set && t.from.set:
			return edit.Options{}, fmt.Errorf("--%s and --%s-from both give the text for one edit", t.name, t.name)
		case t.inline.set:
			add(t.op, t.inline.val)
		case t.from.set:
			text, err := readText(t.from.val)
			if err != nil {
				return edit.Options{}, err
			}
			add(t.op, text)
		}
	}

	if len(ops) == 0 {
		if c.multi || c.dryRun || c.expect.set {
			return edit.Options{}, fmt.Errorf("--multi, --expect and --dry-run only mean something with an edit")
		}
		return edit.Options{}, nil
	}
	if len(ops) > 1 {
		return edit.Options{}, fmt.Errorf("one edit at a time: %s and %s were both asked for", ops[0].Op, ops[1].Op)
	}
	e := ops[0]

	switch {
	case c.count || c.filesOnly:
		return e, fmt.Errorf("--%s writes files; -c and -l only report on them", e.Op)
	case c.opt.Before > 0 || c.opt.After > 0 || c.context > 0 || c.opt.Lines > 0:
		return e, fmt.Errorf("-A, -B, -C and --lines pad what is printed; they do not select what an edit rewrites")
	case c.opt.Max > 0:
		return e, fmt.Errorf("-m caps results; an edit wants every match it selects, or --multi")
	case c.truncate > 0:
		return e, fmt.Errorf("--truncate caps what is printed; an edit reports the whole of what it wrote")
	case c.outline:
		return e, fmt.Errorf("--outline reports structure; it does not select what an edit rewrites")
	case c.expect.set && c.expect.val < 1:
		return e, fmt.Errorf("--expect states how many nodes the search should find, so it wants a count above zero")
	case e.Op.Node() && (c.opt.Section || c.opt.Body):
		return e, fmt.Errorf("--%s edits the matched node, so --section has nothing to widen; use --replace", e.Op)
	}
	// A checkbox edit is about task items, so it says so on the search's
	// behalf: the hit climbs to the item owning it the way --task does.
	switch e.Op {
	case edit.OpCheck, edit.OpUncheck, edit.OpToggle:
		if c.opt.Task == search.TaskIgnore {
			c.opt.Task = search.TaskAny
		}
	}
	return e, nil
}

// textOp pairs an edit that takes text with the two flags that can carry it.
type textOp struct {
	op     edit.Op
	name   string
	inline *optString
	from   *optString
}

func textOps(c *config) []textOp {
	return []textOp{
		{edit.OpReplace, "replace", &c.replace, &c.replFrom},
		{edit.OpSetText, "set-text", &c.setText, &c.setFrom},
		{edit.OpAppend, "append", &c.appendTo, &c.appFrom},
		{edit.OpPrepend, "prepend", &c.prependTo, &c.preFrom},
	}
}

func readText(path string) (string, error) {
	if path == "-" {
		data, err := io.ReadAll(os.Stdin)
		return string(data), err
	}
	data, err := os.ReadFile(path)
	return string(data), err
}

// runEdits plans every file's changes before writing any of them, so a run
// that cannot be carried out in full leaves nothing behind.
func runEdits(out *bufio.Writer, p *render.Printer, results []report.File, e edit.Options, c config) int {
	total := 0
	for _, r := range results {
		total += len(r.Res)
	}
	if why, code := report.Gate(total, c.expect.ptr(), c.multi, report.FlagWords); code != 0 {
		// Nothing matching is the search's own answer, and stays as quiet
		// here as it is everywhere else.
		if why.Kind != "nomatch" {
			report.Refused(os.Stderr, results, total, why, p.Format == render.JSON)
		}
		return code
	}

	planned := make([][]edit.Change, len(results))
	for i, r := range results {
		if len(r.Res) == 0 {
			continue
		}
		changes, err := edit.Plan(r.Src, r.Res, e)
		if err != nil {
			fmt.Fprintf(os.Stderr, "mdgrep: %v\n", err)
			return 2
		}
		// A ranked search hands back its results best first, and edit.Apply
		// walks a file once, forwards: a change out of line order is one it
		// steps over, leaving a report that claims an edit the file never
		// took.
		sort.SliceStable(changes, func(a, b int) bool {
			return changes[a].Start < changes[b].Start
		})
		planned[i] = changes
	}

	for i, changes := range planned {
		if len(changes) == 0 {
			continue
		}
		src := results[i].Src
		if !c.dryRun && changed(changes) {
			if err := edit.Write(src.Path, edit.Apply(src, changes)); err != nil {
				fmt.Fprintf(os.Stderr, "mdgrep: %s: %v\n", src.Path, err)
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

func changed(changes []edit.Change) bool {
	for _, c := range changes {
		if !c.NoOp {
			return true
		}
	}
	return false
}

// buildAnchor turns every pattern into a heading anchor to look for, under
// each convention the user left enabled.
func buildAnchor(c config) (*search.Anchor, error) {
	styles, err := parseAnchorStyles(c.anchorSty)
	if err != nil {
		return nil, err
	}
	return search.NewAnchor(c.patterns, styles)
}

var anchorStyleAliases = map[string]mdoc.AnchorStyle{
	"github": mdoc.AnchorGitHub, "gh": mdoc.AnchorGitHub,
	"gitlab": mdoc.AnchorGitLab, "gl": mdoc.AnchorGitLab,
	"python": mdoc.AnchorPython, "mkdocs": mdoc.AnchorPython, "pymd": mdoc.AnchorPython,
	"kramdown": mdoc.AnchorKramdown, "jekyll": mdoc.AnchorKramdown,
	"pandoc": mdoc.AnchorPandoc,
	"loose":  mdoc.AnchorLoose, "any": mdoc.AnchorLoose,
}

func parseAnchorStyles(spec string) ([]mdoc.AnchorStyle, error) {
	spec = strings.TrimSpace(spec)
	if spec == "" || strings.EqualFold(spec, "all") {
		return mdoc.AllAnchorStyles, nil
	}
	var out []mdoc.AnchorStyle
	seen := map[mdoc.AnchorStyle]bool{}
	for raw := range strings.SplitSeq(spec, ",") {
		name := strings.ToLower(strings.TrimSpace(raw))
		if name == "" {
			continue
		}
		s, ok := anchorStyleAliases[name]
		if !ok {
			return nil, fmt.Errorf("unknown anchor style %q", raw)
		}
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	if len(out) == 0 {
		return mdoc.AllAnchorStyles, nil
	}
	return out, nil
}

// buildMatcher folds the case flags and every -e pattern into one matcher.
// Smart case reads all the patterns together: a single upper-case letter
// anywhere in them makes the whole search case sensitive.
func buildMatcher(c config, mode match.Mode, smart bool) (match.Matcher, error) {
	opt := match.Options{Mode: mode, MinScore: c.minScore, Word: c.word}
	switch {
	case smart:
		opt.IgnoreCase = match.SmartCase(strings.Join(c.patterns, " "))
	case c.forceFold:
		opt.IgnoreCase = true
	case c.forceCase:
		opt.IgnoreCase = false
	default:
		opt.IgnoreCase = match.SmartCase(strings.Join(c.patterns, " "))
	}

	ms := make([]match.Matcher, 0, len(c.patterns))
	for _, p := range c.patterns {
		m, err := match.New(p, opt)
		if err != nil {
			return nil, err
		}
		ms = append(ms, m)
	}
	m := match.Any(ms)
	if c.invert {
		m = match.Not(m)
	}
	return m, nil
}

// permute moves flags ahead of positional arguments so the pattern may be
// written before its options, the way people actually type grep invocations.
func permute(fs *flag.FlagSet, args []string) []string {
	var flags, pos []string
	terminated := false
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			pos = append(pos, args[i+1:]...)
			terminated = true
			break
		}
		if len(a) < 2 || a[0] != '-' {
			pos = append(pos, a)
			continue
		}
		name := strings.TrimLeft(a, "-")
		if strings.Contains(name, "=") {
			flags = append(flags, a)
			continue
		}
		// Accept the attached form people type for grep: -C2, -x1, -s0.7.
		if !strings.HasPrefix(a, "--") && len(name) > 1 && fs.Lookup(name) == nil {
			if f := fs.Lookup(name[:1]); f != nil && !isBoolFlag(f) {
				flags = append(flags, "-"+name[:1], name[1:])
				continue
			}
		}
		if f := fs.Lookup(name); f != nil && !isBoolFlag(f) && i+1 < len(args) {
			flags = append(flags, a, args[i+1])
			i++
			continue
		}
		flags = append(flags, a)
	}
	// A "--" the caller wrote has to survive the move, or a path that opens
	// with a dash arrives back in front of the flag parser and is read as a
	// flag again. It is not added where the caller left it out, so a mistyped
	// flag still reports itself rather than being taken for a file.
	if terminated {
		flags = append(flags, "--")
	}
	return append(flags, pos...)
}

func isBoolFlag(f *flag.Flag) bool {
	b, ok := f.Value.(interface{ IsBoolFlag() bool })
	return ok && b.IsBoolFlag()
}

var kindAliases = map[string]mdoc.Kind{
	"heading": mdoc.KindHeading, "h": mdoc.KindHeading, "head": mdoc.KindHeading,
	"item": mdoc.KindItem, "bullet": mdoc.KindItem, "li": mdoc.KindItem,
	"list":      mdoc.KindList,
	"paragraph": mdoc.KindParagraph, "para": mdoc.KindParagraph, "p": mdoc.KindParagraph,
	"code": mdoc.KindCode, "quote": mdoc.KindQuote, "table": mdoc.KindTable,
	"row": mdoc.KindRow, "cell": mdoc.KindCell, "html": mdoc.KindHTML,
	"frontmatter": mdoc.KindFrontmatter, "fm": mdoc.KindFrontmatter,
}

// taskFilter folds the three checkbox flags into one filter. Asking for both
// states is the same as asking for any checkbox item.
func taskFilter(c config) search.TaskFilter {
	switch {
	case c.checked && c.unchecked:
		return search.TaskAny
	case c.checked:
		return search.TaskChecked
	case c.unchecked:
		return search.TaskUnchecked
	case c.task:
		return search.TaskAny
	}
	return search.TaskIgnore
}

// separator reads --separator, where leaving the flag out is the default rule
// and passing an empty string is the deliberate choice to have none.
func separator(o optString) string {
	if !o.set {
		return "--"
	}
	return o.val
}

// widens are the flags that make a result cover more than the node that
// matched.
var widens = map[string]bool{
	"B": true, "before": true, "A": true, "after": true, "C": true, "context": true,
	"lines": true, "expand": true, "section": true, "section-body": true,
}

// outlineFlags rejects the flags that widen a result. An outline is one line
// per heading, and a widened result no longer begins on the heading that line
// is meant to be -- so rather than print a body line where a heading belongs,
// or silently drop the widening, the run says the two cannot be combined.
func outlineFlags(fs *flag.FlagSet) error {
	var extra []string
	fs.Visit(func(f *flag.Flag) {
		if widens[f.Name] {
			extra = append(extra, dashed(f.Name))
		}
	})
	if len(extra) > 0 {
		return fmt.Errorf("--outline is one line per heading, so there is nothing for %s to widen",
			strings.Join(extra, ", "))
	}
	return nil
}

// dashed spells a flag the way the caller would have typed it.
func dashed(name string) string {
	if len(name) == 1 {
		return "-" + name
	}
	return "--" + name
}

// parseFormat folds --format and --json into one answer. --json predates
// --format and stays as its own spelling of the same thing, so the pair is
// only an error when the two disagree.
func parseFormat(spec optString, jsonFlag, outline bool) (render.Format, error) {
	if outline && (spec.set || jsonFlag) {
		return 0, fmt.Errorf("--outline is its own format; drop --format or --json")
	}
	// spec.set, not spec.val: --format with an empty value is what an unset
	// shell variable expands to, and it names a format nobody has rather than
	// standing for the flag being left out.
	if !spec.set {
		switch {
		case outline:
			return render.Outline, nil
		case jsonFlag:
			return render.JSON, nil
		}
		return render.Plain, nil
	}
	f, ok := formats[strings.ToLower(strings.TrimSpace(spec.val))]
	if !ok {
		return 0, fmt.Errorf("unknown format %q: plain, compact or json", spec.val)
	}
	if jsonFlag && f != render.JSON {
		return 0, fmt.Errorf("--json and --format %s ask for different output", spec.val)
	}
	return f, nil
}

var formats = map[string]render.Format{
	"plain": render.Plain, "compact": render.Compact, "json": render.JSON,
}

// helpTopic is the word --help was asked about: the one still standing on the
// line, and standing after the flag. Appending --help to a command already
// half typed is how the manual is usually reached, and the pattern typed
// before it is what the caller wanted help about, not the help they wanted.
func helpTopic(args []string, arg string) string {
	for i, a := range args {
		if !strings.HasPrefix(a, "-") {
			continue
		}
		if name, _, _ := strings.Cut(strings.TrimLeft(a, "-"), "="); name != "h" && name != "help" {
			continue
		}
		if slices.Contains(args[i+1:], arg) {
			return arg
		}
		return ""
	}
	return ""
}

func parseKinds(spec string) (map[mdoc.Kind]bool, error) {
	if strings.TrimSpace(spec) == "" {
		return nil, nil
	}
	out := map[mdoc.Kind]bool{}
	for raw := range strings.SplitSeq(spec, ",") {
		name := strings.ToLower(strings.TrimSpace(raw))
		if name == "" {
			continue
		}
		k, ok := kindAliases[name]
		if !ok {
			return nil, fmt.Errorf("unknown node kind %q", raw)
		}
		out[k] = true
	}
	return out, nil
}

func splitSet(spec string) map[string]bool {
	out := map[string]bool{}
	for e := range strings.SplitSeq(spec, ",") {
		e = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(e), "."))
		if e != "" {
			out["."+strings.ToLower(e)] = true
		}
	}
	return out
}

func useColor(when string) bool {
	switch when {
	case "always":
		return true
	case "never":
		return false
	}
	if os.Getenv("NO_COLOR") != "" || os.Getenv("TERM") == "dumb" {
		return false
	}
	stat, err := os.Stdout.Stat()
	return err == nil && stat.Mode()&os.ModeCharDevice != 0
}

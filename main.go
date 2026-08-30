// Command mdgrep searches markdown by node instead of by line: a hit inside a
// bullet prints the whole bullet, a hit in a heading can print its whole
// section, and the surrounding context is counted in blocks rather than lines.
package main

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"

	"mdgrep/internal/edit"
	"mdgrep/internal/match"
	"mdgrep/internal/mdoc"
	"mdgrep/internal/render"
	"mdgrep/internal/search"
)

const version = "0.1.0"

const usage = `mdgrep — node-aware grep for markdown

usage: mdgrep [OPTIONS] PATTERN [PATH...]

PATTERN is a regular expression by default. A hit prints the markdown node it
landed in — the whole bullet, row or paragraph — rather than the single line.
An empty pattern matches everything, so "mdgrep '' docs --todo" lists every
open checkbox under docs/.

Matching
  -e, --regexp PATTERN  use PATTERN as the pattern; repeat for alternatives
  -F, --fixed-strings   match PATTERN literally
      --fuzzy           fuzzy match: every whitespace-separated token of
                        PATTERN must appear, loosely and in order. Results
                        come back best first rather than in file order
      --min-score N     fuzzy score threshold, 0..1 (default 0.7)
      --anchor          PATTERN is a heading link anchor: "#the-foo-bar",
                        "the-foo-bar" or "docs/x.md#the-foo-bar" all find the
                        heading "## The Foo Bar"
      --anchor-style LIST
                        anchor conventions to try (default all): github,
                        gitlab,python,kramdown,pandoc,loose
  -w, --word-regexp     match only whole words
  -v, --invert-match    select the nodes that do not match
  -i, --ignore-case     force case-insensitive
  -s, --case-sensitive  force case-sensitive
  -S, --smart-case      case-insensitive until PATTERN has an upper-case
                        letter (the default)

Filters
  -k, --kind LIST       only these node kinds: heading,item,paragraph,code,
                        quote,table,row,html,frontmatter,list
      --task            only task list items ("- [ ]" and "- [x]")
      --unchecked       only unticked task items (alias --todo)
      --checked         only ticked task items (alias --done)

Selection
      --expand N        climb N ancestor levels from the matched node
      --section         widen to the enclosing heading section
      --section-body    that section without its heading line
  -B, --before N        include N sibling blocks before
  -A, --after N         include N sibling blocks after
  -C, --context N       shorthand for -B N -A N
      --lines N         pad the result with N raw lines on each side

Editing
      --check           tick the selected task item (--uncheck, --toggle)
      --replace TEXT    replace the selected region with TEXT
      --replace-from FILE
                        the same, with TEXT read from a file ("-" is stdin)
      --set-text TEXT   change what the matched node says, keeping the markup
                        that makes it a heading, an item or a fenced block
      --delete          remove the selected region
      --append TEXT     insert TEXT after the selected region
      --prepend TEXT    insert TEXT before it
      --multi           edit every match; without it, more than one is an error
      --dry-run         show the edit, write nothing

An edit rewrites what the same flags would have printed, so the search comes
first: narrow it until one node is selected, then say what to do with it.
--check and --set-text act on the matched node itself; --replace, --delete,
--append and --prepend act on the region --section and --expand widen it to.
The change is printed unless -q, and every file is written in one atomic go.

Output
  -n, --line-number     number the printed lines (the default)
  -N, --no-line-number
      --no-breadcrumb   hide the heading trail above each result
      --color WHEN      auto, always or never (default auto)
      --json            one JSON object per result
  -c, --count           print only the number of results per file
  -l, --files-with-matches
                        print only the names of files with results
  -m, --max-count N     stop after N results per file
  -q, --quiet           print nothing; the exit status carries the answer
      --ext LIST        file extensions to search (default md,markdown,mdown,mkd,mdx)
      --hidden          descend into hidden directories
      --no-ignore       do not skip node_modules, vendor and friends
  -h, --help
  -V, --version

-B, -A and -C count sibling nodes, not lines; use --lines for raw lines.
Exit status is 0 when something matched, 1 when nothing did, 2 on error.
`

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
	color     string
	jsonOut   bool
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
	appendTo  optString
	prependTo optString
	multi     bool
	dryRun    bool
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
	fs.Var(&c.appendTo, "append", "")
	fs.Var(&c.prependTo, "prepend", "")
	fs.BoolVar(&c.multi, "multi", false, "")
	fs.BoolVar(&c.dryRun, "dry-run", false, "")
	bind(func(n string) { fs.IntVar(&c.opt.Before, n, 0, "") }, "B", "before")
	bind(func(n string) { fs.IntVar(&c.opt.After, n, 0, "") }, "A", "after")
	bind(func(n string) { fs.IntVar(&c.context, n, 0, "") }, "C", "context")
	fs.IntVar(&c.opt.Lines, "lines", 0, "")
	bind(func(n string) { fs.IntVar(&c.opt.Max, n, 0, "") }, "m", "max-count")
	bind(func(n string) { fs.BoolVar(&c.noNums, n, false, "") }, "N", "no-line-number")
	// Numbering is already on; -n exists so a grep habit does not error out.
	bind(func(n string) { fs.Bool(n, false, "") }, "n", "line-number")
	fs.BoolVar(&c.noCrumb, "no-breadcrumb", false, "")
	fs.StringVar(&c.color, "color", "auto", "")
	fs.BoolVar(&c.jsonOut, "json", false, "")
	bind(func(n string) { fs.BoolVar(&c.count, n, false, "") }, "c", "count")
	bind(func(n string) { fs.BoolVar(&c.filesOnly, n, false, "") }, "l", "files-with-matches")
	bind(func(n string) { fs.BoolVar(&c.quiet, n, false, "") }, "q", "quiet")
	fs.StringVar(&c.exts, "ext", "md,markdown,mdown,mkd,mdx", "")
	fs.BoolVar(&c.hidden, "hidden", false, "")
	fs.BoolVar(&c.noIgnore, "no-ignore", false, "")
	bind(func(n string) { fs.BoolVar(&c.help, n, false, "") }, "h", "help")
	bind(func(n string) { fs.BoolVar(&c.showVer, n, false, "") }, "V", "version")

	if err := fs.Parse(permute(fs, os.Args[1:])); err != nil {
		fmt.Fprintf(os.Stderr, "mdgrep: %v\n\n%s", err, usage)
		return 2
	}
	if c.help {
		fmt.Fprint(os.Stdout, usage)
		return 0
	}
	if c.showVer {
		fmt.Fprintf(os.Stdout, "mdgrep %s\n", version)
		return 0
	}
	kinds, err := parseKinds(c.kinds)
	if err != nil {
		fmt.Fprintf(os.Stderr, "mdgrep: %v\n", err)
		return 2
	}
	c.opt.Kinds = kinds
	c.opt.Task = taskFilter(c)

	ed, err := buildEdit(&c)
	// An edit rewrites one node at a time, so neighbouring hits must not be
	// folded into a single region the way printing folds them.
	c.opt.Distinct = ed.Op != edit.OpNone
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
		if fs.NArg() == 0 {
			fmt.Fprintf(os.Stderr, "mdgrep: missing PATTERN\n\n%s", usage)
			return 2
		}
		c.patterns, paths = patternList{fs.Arg(0)}, paths[1:]
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

	files, useStdin, err := collectFiles(paths, splitSet(c.exts), c.hidden, c.noIgnore)
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
	p := &render.Printer{
		W:           out,
		Color:       useColor(c.color),
		LineNumbers: !c.noNums,
		Breadcrumb:  !c.noCrumb,
		JSON:        c.jsonOut,
	}

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

	results := make([]fileResult, len(files))
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
				results[i] = fileResult{doc.Src, search.File(doc, matcher, c.opt)}
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
			return bestScore(results[i].res) > bestScore(results[j].res)
		})
	}
	for _, r := range results {
		if r.src != nil {
			emit(r.src, r.res)
		}
	}
	if !found {
		return 1
	}
	return 0
}

type fileResult struct {
	src *mdoc.Source
	res []search.Result
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
	if c.replace.set {
		add(edit.OpReplace, c.replace.val)
	}
	if c.replFrom.set {
		text, err := readText(c.replFrom.val)
		if err != nil {
			return edit.Options{}, err
		}
		add(edit.OpReplace, text)
	}
	if c.setText.set {
		add(edit.OpSetText, c.setText.val)
	}
	if c.appendTo.set {
		add(edit.OpAppend, c.appendTo.val)
	}
	if c.prependTo.set {
		add(edit.OpPrepend, c.prependTo.val)
	}

	if len(ops) == 0 {
		if c.multi || c.dryRun {
			return edit.Options{}, fmt.Errorf("--multi and --dry-run only mean something with an edit")
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
func runEdits(out *bufio.Writer, p *render.Printer, results []fileResult, e edit.Options, c config) int {
	total := 0
	for _, r := range results {
		total += len(r.res)
	}
	switch {
	case total == 0:
		return 1
	case total > 1 && !c.multi:
		reportAmbiguous(results, total)
		return 2
	}

	planned := make([][]edit.Change, len(results))
	for i, r := range results {
		if len(r.res) == 0 {
			continue
		}
		changes, err := edit.Plan(r.src, r.res, e)
		if err != nil {
			fmt.Fprintf(os.Stderr, "mdgrep: %v\n", err)
			return 2
		}
		planned[i] = changes
	}

	for i, changes := range planned {
		if len(changes) == 0 {
			continue
		}
		src := results[i].src
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

// reportAmbiguous shows what an edit would have hit, so the next attempt can
// narrow the search rather than guess at it.
func reportAmbiguous(results []fileResult, total int) {
	fmt.Fprintf(os.Stderr, "mdgrep: %d matches; narrow the search or pass --multi\n", total)
	const shown = 10
	n := 0
	for _, r := range results {
		for _, res := range r.res {
			if n == shown {
				fmt.Fprintf(os.Stderr, "  … and %d more\n", total-shown)
				return
			}
			fmt.Fprintf(os.Stderr, "  %s:%d: %s\n", r.src.Path, res.Start+1,
				strings.TrimSpace(r.src.Line(res.Start)))
			n++
		}
	}
}

func bestScore(res []search.Result) float64 {
	if len(res) == 0 {
		return 0
	}
	return res[0].Score
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
		// A bullet's text lives in a child block, so keep those searchable and
		// let promotion lift the hit back up to the item.
		if k == mdoc.KindItem {
			out[mdoc.KindParagraph], out[mdoc.KindTextBlock] = true, true
		}
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

var skipDirs = map[string]bool{
	".git": true, "node_modules": true, "vendor": true,
	".venv": true, "__pycache__": true, "target": true,
}

func collectFiles(paths []string, exts map[string]bool, hidden, noIgnore bool) ([]string, bool, error) {
	useStdin := false
	if len(paths) == 0 {
		if stat, err := os.Stdin.Stat(); err == nil && stat.Mode()&os.ModeCharDevice == 0 {
			return nil, true, nil
		}
		paths = []string{"."}
	}

	var files []string
	seen := map[string]bool{}
	for _, p := range paths {
		if p == "-" {
			useStdin = true
			continue
		}
		info, err := os.Stat(p)
		if err != nil {
			return nil, false, err
		}
		if !info.IsDir() {
			if !seen[p] {
				seen[p] = true
				files = append(files, p)
			}
			continue
		}
		err = filepath.WalkDir(p, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			name := d.Name()
			if d.IsDir() {
				if path == p {
					return nil
				}
				if (!noIgnore && skipDirs[name]) || (!hidden && strings.HasPrefix(name, ".")) {
					return filepath.SkipDir
				}
				return nil
			}
			if !hidden && strings.HasPrefix(name, ".") {
				return nil
			}
			if exts[strings.ToLower(filepath.Ext(name))] && !seen[path] {
				seen[path] = true
				files = append(files, path)
			}
			return nil
		})
		if err != nil {
			return nil, false, err
		}
	}
	return files, useStdin, nil
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

// Command mdgrep searches markdown by node instead of by line: a hit inside a
// bullet prints the whole bullet, a hit in a heading can print its whole
// section, and the surrounding context is counted in blocks rather than lines.
package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

	"github.com/riadafridishibly/mdgrep/internal/edit"
	"github.com/riadafridishibly/mdgrep/internal/ignore"
	"github.com/riadafridishibly/mdgrep/internal/match"
	"github.com/riadafridishibly/mdgrep/internal/mdoc"
	"github.com/riadafridishibly/mdgrep/internal/render"
	"github.com/riadafridishibly/mdgrep/internal/search"
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

// hint stands in for the whole of usage on the error paths. A mistyped flag is
// most of a screen of help the caller did not ask for, and it buries the one
// line that says what went wrong.
const hint = "try 'mdgrep --help'"

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
      --set-text TEXT   change what the matched node says, keeping the markup
                        that makes it a heading, an item or a fenced block
      --delete          remove the selected region
      --append TEXT     insert TEXT after the selected region
      --prepend TEXT    insert TEXT before it
      --replace-from FILE, --set-text-from FILE, --append-from FILE,
      --prepend-from FILE
                        the same four edits with TEXT read from a file ("-"
                        is stdin), so a multi-line body needs no quoting
      --multi           edit every match; without it, more than one is an error
      --expect N        edit only if exactly N nodes matched, else fail
      --dry-run         show the edit, write nothing
      --apply FILE      carry out a plan of edits read from FILE ("-" is
                        stdin): one JSON object per line, each with "path",
                        "match" and "op", plus "text" for replace, set-text,
                        append and prepend. "kind", "fixed", "expand",
                        "section", "section-body", "expect" and "multi" say
                        per entry what the flags of those names say here. A
                        plan carries its own search, so it takes no PATTERN,
                        no PATH and no other matching or editing flag. Every
                        entry is planned against the files as they were read,
                        so entries are independent: one cannot match what
                        another writes, and two that reach for the same lines
                        refuse the plan, as does any entry that cannot be
                        carried out. Nothing is written unless all of it can be

  $ cat plan.jsonl
  {"path":"notes.md","match":"ship the docs","op":"check"}
  {"path":"notes.md","match":"^## Setup","op":"set-text","text":"Install"}
  $ mdgrep --apply plan.jsonl

An edit rewrites what the same flags would have printed, so the search comes
first: narrow it until one node is selected, then say what to do with it.
--check and --set-text act on the matched node itself; --replace, --delete,
--append and --prepend act on the region --section and --expand widen it to.
The change is printed unless -q, and every file is written in one atomic go.
A refused edit lists what it would have hit on stderr, as one JSON object when
--json is set, so the next attempt can be narrower. A plan is refused whole:
every entry that cannot be carried out is reported against its number, and no
file is written.

Output
  -n, --line-number     number the printed lines (the default)
  -N, --no-line-number
      --no-breadcrumb   hide the heading trail above each result. The trail
                        stops at the parent when the result is the heading it
                        would otherwise end with, since that line follows it
      --outline         one indented line per heading: what is in these files
                        rather than where something appears. Takes paths and
                        no PATTERN, and is the cheapest view of a tree. One
                        line per heading is all it prints, so it takes none of
                        the flags under Selection either
      --separator STR   what to print between two results of a file (default
                        "--"); pass "" to leave them out
      --truncate N      print at most N lines of any one result, then a line
                        saying how many were held back. json reports the
                        count as "truncated" instead
      --color WHEN      auto, always or never (default auto)
      --format WHEN     plain (default), compact or json. compact prints the
                        path once per file and then one tab-separated record
                        per result — "start[-end] kind text", with newlines
                        escaped so a record is always one line, path included
                        — which costs a fraction of the same results as json.
                        Neither machine format is coloured, and compact leaves
                        out the breadcrumb and the score; ask for json if you
                        want them. One record is one node: two hits that touch
                        are printed as one passage in plain output and kept
                        apart here
      --json            one JSON object per result (same as --format json)
  -c, --count           print only the number of results per file
  -l, --files-with-matches
                        print only the names of files with results
  -m, --max-count N     stop after N results per file
  -q, --quiet           print nothing; the exit status carries the answer
      --ext LIST        file extensions to search (default md,markdown,mdown,mkd,mdx)
      --hidden          descend into hidden directories
      --no-ignore       search everything, including what the ignore files
                        (.gitignore, .ignore, .git/info/exclude) and the skip
                        list (node_modules, vendor and friends) leave out
  -h, --help [TOPIC]    the whole manual, or one part of it: matching,
                        filters, selection, editing, output. A flag name works
                        too, so "mdgrep --help anchor" prints the part that
                        documents --anchor
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
		fmt.Fprintf(os.Stderr, "mdgrep: %v\n%s\n", err, hint)
		return 2
	}
	if c.help {
		topic := ""
		if fs.NArg() == 1 {
			topic = helpTopic(os.Args[1:], fs.Arg(0))
		}
		text, err := help(topic)
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
		fmt.Fprintf(os.Stderr, "mdgrep: %v\n%s\n", err, hint)
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
			fmt.Fprintf(os.Stderr, "mdgrep: %v\n%s\n", err, hint)
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
			fmt.Fprintf(os.Stderr, "mdgrep: missing PATTERN\n%s\n", hint)
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
func runEdits(out *bufio.Writer, p *render.Printer, results []fileResult, e edit.Options, c config) int {
	total := 0
	for _, r := range results {
		total += len(r.res)
	}
	if why, code := countGate(total, c.expect, c.multi, flagWords); code != 0 {
		// Nothing matching is the search's own answer, and stays as quiet
		// here as it is everywhere else.
		if why.kind != "nomatch" {
			reportRefused(os.Stderr, results, total, why, p.Format == render.JSON)
		}
		return code
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

// gateWords spells the two ways out of a refusal. A plan entry cannot pass a
// flag, so it is pointed at the keys it can set instead.
type gateWords struct{ expect, narrow string }

var (
	flagWords = gateWords{"--expect", "narrow the search or pass --multi"}
	planWords = gateWords{`"expect"`, `narrow "match" or set "multi": true`}
)

// countGate decides whether an edit may go ahead on the number of nodes the
// search found. --expect states the count outright; without it a lone match is
// the only unambiguous instruction, and --multi waives that.
func countGate(total int, expect optInt, multi bool, w gateWords) (reason, int) {
	switch {
	case expect.set && total != expect.val:
		return reason{
			kind:     "expect",
			text:     fmt.Sprintf("%s %d, but %d matched", w.expect, expect.val, total),
			expected: expect.val,
		}, 2
	case expect.set:
		return reason{}, 0
	case total == 0:
		return reason{kind: "nomatch", text: "nothing matched"}, 1
	case total > 1 && !multi:
		return reason{
			kind: "ambiguous",
			text: fmt.Sprintf("%d matches; %s", total, w.narrow),
		}, 2
	}
	return reason{}, 0
}

// reason is why an edit was refused: a kind an --json reader can branch on, a
// sentence for everyone else, and the count --expect asked for when that is
// what went wrong.
type reason struct {
	kind     string
	text     string
	expected int
	// entry is which entry of an --apply plan was refused, 1-based, and zero
	// when the refusal is a single edit's own.
	entry int
	// path is the file the refusal is about, when it is about a file rather
	// than an entry: one that cannot be written.
	path string
	// written names the files a run left changed before it stopped. A plan
	// applies whole or not at all, so this is empty except in the one case
	// that promise cannot be kept: a rename failing part way through.
	written []string
	// entries is how many entries a plan refused, on the record that closes
	// a refused run.
	entries int
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

// reportRefused shows what the edit would have hit, so the next attempt can
// narrow the search rather than guess at it.
func reportRefused(w io.Writer, results []fileResult, total int, why reason, asJSON bool) {
	if asJSON {
		reportRefusedJSON(w, results, total, why)
		return
	}
	fmt.Fprintf(w, "mdgrep: %s%s\n", entryPrefix(why.entry), why.text)
	n := 0
	for _, r := range results {
		for _, res := range r.res {
			if n == shownMatches {
				fmt.Fprintf(w, "  … and %d more\n", total-shownMatches)
				return
			}
			fmt.Fprintf(w, "  %s:%d: %s\n", r.src.Path, res.Start+1,
				strings.TrimSpace(r.src.Line(res.Start)))
			n++
		}
	}
}

// validUTF8 stands in for what a JSON encoder would do to a line of bytes that
// is not text, but does it here, so the object says the same thing the encoder
// would have written and a reader comparing it against the file knows why.
func validUTF8(s string) string { return strings.ToValidUTF8(s, "\uFFFD") }

type jsonMatch struct {
	Path string `json:"path"`
	Line int    `json:"line"`
	Text string `json:"text"`
}

type jsonRefusal struct {
	Error    string      `json:"error"`
	Message  string      `json:"message"`
	Entry    int         `json:"entry,omitempty"`
	Path     string      `json:"path,omitempty"`
	Entries  int         `json:"entries,omitempty"`
	Written  []string    `json:"written,omitempty"`
	Total    int         `json:"total"`
	Expected int         `json:"expected,omitempty"`
	Matches  []jsonMatch `json:"matches"`
}

// reportRefusedJSON says the same thing as one object, so a caller that asked
// for --json parses the refusal with the reader it already has rather than
// reading English back out of stderr.
func reportRefusedJSON(w io.Writer, results []fileResult, total int, why reason) {
	out := jsonRefusal{
		Error:    why.kind,
		Message:  why.text,
		Entry:    why.entry,
		Path:     why.path,
		Entries:  why.entries,
		Written:  why.written,
		Total:    total,
		Expected: why.expected,
		Matches:  []jsonMatch{},
	}
	for _, r := range results {
		for _, res := range r.res {
			if len(out.Matches) == shownMatches {
				json.NewEncoder(w).Encode(out)
				return
			}
			out.Matches = append(out.Matches, jsonMatch{
				Path: r.src.Path,
				Line: res.Start + 1,
				Text: validUTF8(strings.TrimSpace(r.src.Line(res.Start))),
			})
		}
	}
	json.NewEncoder(w).Encode(out)
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

// help answers --help, either in full or one titled part of it. Splitting the
// manual rather than keeping a second copy of it means a topic cannot drift
// from what the full text says.
func help(topic string) (string, error) {
	if topic == "" {
		return usage, nil
	}
	secs := helpSections()
	sec, err := pickSection(secs, topic)
	if err != nil {
		return "", err
	}
	return usageLine() + "\n\n" + sec.body, nil
}

// usageLine is the one line of the manual that says how to invoke the command.
// A topic repeats it so a narrowed help still stands on its own.
func usageLine() string {
	for line := range strings.SplitSeq(usage, "\n") {
		if strings.HasPrefix(line, "usage:") {
			return line
		}
	}
	return ""
}

type helpSection struct {
	title string
	body  string
}

// helpSections splits the manual at its titles. A title is a bare word alone on
// a line; everything above the first one introduces the command rather than any
// one part of it, so it is not a topic.
func helpSections() []helpSection {
	var out []helpSection
	for _, line := range strings.SplitAfter(usage, "\n") {
		if title := strings.TrimRight(line, "\n"); isHelpTitle(title) {
			out = append(out, helpSection{title: title, body: line})
			continue
		}
		if len(out) > 0 {
			out[len(out)-1].body += line
		}
	}
	return out
}

func isHelpTitle(line string) bool {
	// The whole first rune, not its leading byte: 0xC3 opens most of the
	// accented Latin letters and is 'Ã' read on its own, which is upper case.
	first, _ := utf8.DecodeRuneInString(line)
	if line == "" || !unicode.IsUpper(first) {
		return false
	}
	for _, r := range line {
		if !unicode.IsLetter(r) {
			return false
		}
	}
	return true
}

// pickSection reads a topic the way a caller is likely to type one: the title
// itself, a prefix of it ("edit" for Editing), or failing that the name of a
// flag, so "--help anchor" finds the part that documents --anchor.
func pickSection(secs []helpSection, topic string) (helpSection, error) {
	want := strings.ToLower(strings.TrimLeft(topic, "-"))
	var hits []helpSection
	for _, s := range secs {
		if strings.HasPrefix(strings.ToLower(s.title), want) {
			hits = append(hits, s)
		}
	}
	if len(hits) == 0 {
		for _, s := range secs {
			if definesFlag(s.body, want) {
				hits = append(hits, s)
			}
		}
	}
	switch len(hits) {
	case 1:
		return hits[0], nil
	case 0:
		return helpSection{}, fmt.Errorf("no help topic %q; try %s", topic, helpTopics(secs))
	}
	return helpSection{}, fmt.Errorf("%q matches %s", topic, helpTopics(hits))
}

// definesFlag reports whether a section documents --name, as opposed to merely
// mentioning it in passing: a definition stands in the flag column, and the
// prose that describes one flag is free to name others.
func definesFlag(body, name string) bool {
	for line := range strings.SplitSeq(body, "\n") {
		if !strings.HasPrefix(line, "  ") {
			continue
		}
		head, _, _ := strings.Cut(strings.TrimSpace(line), "  ")
		for _, spelling := range strings.Split(head, ",") {
			flag, _, _ := strings.Cut(strings.TrimSpace(spelling), " ")
			if flag == "--"+name {
				return true
			}
		}
	}
	return false
}

func helpTopics(secs []helpSection) string {
	names := make([]string, len(secs))
	for i, s := range secs {
		names[i] = strings.ToLower(s.title)
	}
	return strings.Join(names, ", ")
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
	c := collector{exts: exts, hidden: hidden, noIgnore: noIgnore, tokens: make(chan struct{}, walkers)}
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
		var m *ignore.Matcher
		if !noIgnore {
			m = ignore.New(p)
		}
		// The root was asked for by name, so it is searched whatever the rules
		// above it say; everything below it is not.
		var root node
		c.walk(p, m.Root(), &root)
		c.wg.Wait()
		files = root.flatten(seen, files)
	}
	return files, useStdin, nil
}

// walkers bounds how many directories are being read at once. Four is where
// the measurements stop improving: on a 10,000-directory tree the wall clock
// is the same anywhere from three walkers upward, but the kernel time doubles
// on the way from four to eight. The walk waits on the filesystem rather than
// on a core, and the filesystem stops going faster long before the cores run
// out.
var walkers = min(runtime.NumCPU(), 4)

// collector walks a directory tree and keeps the files worth searching. It
// reads each directory itself rather than going through filepath.WalkDir,
// because the listing is also how the ignore rules find their own files, and
// because reading one directory is not work a single goroutine should be doing
// alone.
type collector struct {
	exts     map[string]bool
	hidden   bool
	noIgnore bool
	tokens   chan struct{}
	wg       sync.WaitGroup
}

// node is one directory's share of the walk, in listing order: each child is
// either a file worth searching or the node of a subdirectory that was
// descended into. Keeping the shape of the tree is what lets the branches be
// walked at once and still come back in the order one goroutine would have
// found them in.
type node struct {
	file string
	kids []*node
}

// walk fills n with what dir holds, descending in parallel where there is a
// goroutine to spare and in place where there is not. Only the goroutine that
// read a directory appends to that directory's node, and every node is
// complete before the walk's WaitGroup falls to zero, so flatten reads a tree
// nobody is still writing to.
func (c *collector) walk(dir string, f ignore.Frame, n *node) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	f = f.Enter(dir, entries)
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() {
			if (!c.noIgnore && skipDirs[name]) || (!c.hidden && strings.HasPrefix(name, ".")) {
				continue
			}
			if f.Excluded(name, true) {
				continue
			}
			path := filepath.Join(dir, name)
			kid := &node{}
			n.kids = append(n.kids, kid)
			select {
			case c.tokens <- struct{}{}:
				c.wg.Add(1)
				go func() {
					defer c.wg.Done()
					defer func() { <-c.tokens }()
					c.walk(path, f, kid)
				}()
			default:
				// Every token is out. Walking the subdirectory here rather
				// than waiting for one is what keeps the walk from stalling
				// on itself.
				c.walk(path, f, kid)
			}
			continue
		}
		if !c.hidden && strings.HasPrefix(name, ".") {
			continue
		}
		// Extension first: it settles most files for the price of a suffix,
		// and leaves the rules to run over the few it does not.
		if !c.exts[strings.ToLower(filepath.Ext(name))] {
			continue
		}
		if f.Excluded(name, false) {
			continue
		}
		n.kids = append(n.kids, &node{file: filepath.Join(dir, name)})
	}
}

// flatten appends the tree's files to out in walk order, dropping the ones a
// path given earlier on the command line already brought in.
func (n *node) flatten(seen map[string]bool, out []string) []string {
	if n.file != "" {
		if seen[n.file] {
			return out
		}
		seen[n.file] = true
		return append(out, n.file)
	}
	for _, kid := range n.kids {
		out = kid.flatten(seen, out)
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

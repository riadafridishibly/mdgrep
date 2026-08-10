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
	"strings"
	"sync"

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
                        PATTERN must appear, loosely and in order
      --min-score N     fuzzy score threshold, 0..1 (default 0.55)
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
  -B, --before N        include N sibling blocks before
  -A, --after N         include N sibling blocks after
  -C, --context N       shorthand for -B N -A N
      --lines N         pad the result with N raw lines on each side

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
	var fuzzy, fixed, smart bool

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
	fs.Float64Var(&c.minScore, "min-score", 0.55, "")
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
	case fuzzy:
		mode = match.Fuzzy
	case fixed:
		mode = match.Substring
	}
	if c.context > 0 {
		c.opt.Before, c.opt.After = c.context, c.context
	}
	matcher, err := buildMatcher(c, mode, smart)
	if err != nil {
		fmt.Fprintf(os.Stderr, "mdgrep: %v\n", err)
		return 2
	}

	files, useStdin, err := collectFiles(paths, splitSet(c.exts), c.hidden, c.noIgnore)
	if err != nil {
		fmt.Fprintf(os.Stderr, "mdgrep: %v\n", err)
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

	type fileResult struct {
		src *mdoc.Source
		res []search.Result
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
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			pos = append(pos, args[i+1:]...)
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

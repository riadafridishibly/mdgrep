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

const usage = `mdgrep — loose, node-aware grep for markdown

usage: mdgrep [options] PATTERN [path ...]

Matching
  -f, --fuzzy           fuzzy match, every whitespace token must appear (default)
  -F, --fixed           plain substring match
  -e, --regex           regular expression match
  -i, --ignore-case     force case-insensitive
  -S, --case-sensitive  force case-sensitive (default is smart case)
  -s, --min-score N     fuzzy score threshold, 0..1 (default 0.55)
  -k, --kind LIST       only these node kinds: heading,item,paragraph,code,
                        quote,table,row,html,frontmatter,list

Selection
  -x, --expand N        climb N ancestor levels from the matched node
      --section         widen to the enclosing heading section
  -B, --before N        include N sibling blocks before
  -A, --after N         include N sibling blocks after
  -C, --context N       shorthand for -B N -A N
  -L, --lines N         pad the result with N raw lines on each side

Output
  -n, --no-line-numbers
      --no-breadcrumb   hide the heading trail above each result
      --color WHEN      auto, always or never (default auto)
      --json            one JSON object per result
  -c, --count           print only the number of results per file
  -l, --files           print only the names of files with results
  -m, --max N           stop after N results per file
      --ext LIST        file extensions to search (default md,markdown,mdown,mkd,mdx)
      --hidden          descend into hidden directories
      --no-ignore       do not skip node_modules, vendor and friends

Exit status is 0 when something matched, 1 when nothing did, 2 on error.
`

type config struct {
	mode       match.Mode
	ignoreCase bool
	forceCase  bool
	forceFold  bool
	minScore   float64
	kinds      string
	opt        search.Options
	context    int
	noNums     bool
	noCrumb    bool
	color      string
	jsonOut    bool
	count      bool
	filesOnly  bool
	exts       string
	hidden     bool
	noIgnore   bool
	help       bool
}

func main() {
	os.Exit(run())
}

func run() int {
	var c config
	var fuzzy, fixed, regex bool

	fs := flag.NewFlagSet("mdgrep", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	bind := func(set func(name string), names ...string) {
		for _, n := range names {
			set(n)
		}
	}
	bind(func(n string) { fs.BoolVar(&fuzzy, n, false, "") }, "f", "fuzzy")
	bind(func(n string) { fs.BoolVar(&fixed, n, false, "") }, "F", "fixed")
	bind(func(n string) { fs.BoolVar(&regex, n, false, "") }, "e", "regex")
	bind(func(n string) { fs.BoolVar(&c.forceFold, n, false, "") }, "i", "ignore-case")
	bind(func(n string) { fs.BoolVar(&c.forceCase, n, false, "") }, "S", "case-sensitive")
	bind(func(n string) { fs.Float64Var(&c.minScore, n, 0.55, "") }, "s", "min-score")
	bind(func(n string) { fs.StringVar(&c.kinds, n, "", "") }, "k", "kind")
	bind(func(n string) { fs.IntVar(&c.opt.Expand, n, 0, "") }, "x", "expand")
	fs.BoolVar(&c.opt.Section, "section", false, "")
	bind(func(n string) { fs.IntVar(&c.opt.Before, n, 0, "") }, "B", "before")
	bind(func(n string) { fs.IntVar(&c.opt.After, n, 0, "") }, "A", "after")
	bind(func(n string) { fs.IntVar(&c.context, n, 0, "") }, "C", "context")
	bind(func(n string) { fs.IntVar(&c.opt.Lines, n, 0, "") }, "L", "lines")
	bind(func(n string) { fs.IntVar(&c.opt.Max, n, 0, "") }, "m", "max")
	bind(func(n string) { fs.BoolVar(&c.noNums, n, false, "") }, "n", "no-line-numbers")
	fs.BoolVar(&c.noCrumb, "no-breadcrumb", false, "")
	fs.StringVar(&c.color, "color", "auto", "")
	fs.BoolVar(&c.jsonOut, "json", false, "")
	bind(func(n string) { fs.BoolVar(&c.count, n, false, "") }, "c", "count")
	bind(func(n string) { fs.BoolVar(&c.filesOnly, n, false, "") }, "l", "files")
	fs.StringVar(&c.exts, "ext", "md,markdown,mdown,mkd,mdx", "")
	fs.BoolVar(&c.hidden, "hidden", false, "")
	fs.BoolVar(&c.noIgnore, "no-ignore", false, "")
	bind(func(n string) { fs.BoolVar(&c.help, n, false, "") }, "h", "help")

	if err := fs.Parse(permute(fs, os.Args[1:])); err != nil {
		fmt.Fprintf(os.Stderr, "mdgrep: %v\n\n%s", err, usage)
		return 2
	}
	if c.help || fs.NArg() == 0 {
		fmt.Fprint(os.Stdout, usage)
		if fs.NArg() == 0 && !c.help {
			return 2
		}
		return 0
	}

	switch {
	case regex:
		c.mode = match.Regexp
	case fixed:
		c.mode = match.Substring
	default:
		c.mode = match.Fuzzy
	}
	pattern := fs.Arg(0)
	paths := fs.Args()[1:]

	c.ignoreCase = match.SmartCase(pattern)
	if c.forceFold {
		c.ignoreCase = true
	}
	if c.forceCase {
		c.ignoreCase = false
	}
	if c.context > 0 {
		c.opt.Before, c.opt.After = c.context, c.context
	}
	kinds, err := parseKinds(c.kinds)
	if err != nil {
		fmt.Fprintf(os.Stderr, "mdgrep: %v\n", err)
		return 2
	}
	c.opt.Kinds = kinds

	matcher, err := match.New(c.mode, pattern, c.ignoreCase, c.minScore)
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

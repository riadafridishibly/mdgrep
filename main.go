// Command mdgrep searches markdown by node instead of by line: a hit inside a
// bullet prints the whole bullet, a hit in a heading can print its whole
// section, and the surrounding context is counted in blocks rather than lines.
package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"runtime"
	"runtime/debug"
	"slices"
	"sort"
	"strings"
	"sync"

	"github.com/riadafridishibly/mdgrep/internal/cli"
	"github.com/riadafridishibly/mdgrep/internal/edit"
	"github.com/riadafridishibly/mdgrep/internal/help"
	"github.com/riadafridishibly/mdgrep/internal/mdoc"
	"github.com/riadafridishibly/mdgrep/internal/plan"
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

func main() {
	os.Exit(run())
}

func run() int {
	c, fs, err := cli.Parse(os.Args[1:])
	if err != nil {
		fmt.Fprintf(os.Stderr, "mdgrep: %v\n%s\n", err, help.Hint)
		return 2
	}
	if c.Help.Asked() {
		// --help=editing names its topic outright. Bare --help leaves it to
		// the one positional still standing, if there is one.
		topic := c.Help.Topic()
		if topic == "" && fs.NArg() == 1 {
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
	if c.ShowVer {
		fmt.Fprintf(os.Stdout, "mdgrep %s\n", buildVersion())
		return 0
	}
	format, err := c.Format()
	if err != nil {
		fmt.Fprintf(os.Stderr, "mdgrep: %v\n%s\n", err, help.Hint)
		return 2
	}
	if err := c.Validate(); err != nil {
		fmt.Fprintf(os.Stderr, "mdgrep: %v\n", err)
		return 2
	}
	// A plan is a whole run of its own: it names its files, its searches and
	// its edits, so nothing below this point has anything left to work out.
	if _, given := c.Apply.Value(); given {
		return plan.Run(c, fs, format)
	}
	if c.Outline {
		if err := cli.OutlineFlags(fs); err != nil {
			fmt.Fprintf(os.Stderr, "mdgrep: %v\n%s\n", err, help.Hint)
			return 2
		}
		// --outline is a question about structure, so it fills in the search a
		// caller would otherwise spell out: every heading, matched by nothing
		// in particular. Either half can still be overridden.
		if c.Kinds == "" {
			c.Kinds = "heading"
		}
	}
	kinds, err := cli.ParseKinds(c.Kinds)
	if err != nil {
		fmt.Fprintf(os.Stderr, "mdgrep: %v\n", err)
		return 2
	}
	c.Opt.Kinds = kinds
	c.Opt.Task = c.TaskFilter()

	ed, err := c.Edit()
	// Neighbouring hits are run together for a person reading the page as one
	// passage, and kept apart for everyone else: an edit rewrites each node on
	// its own, a machine format is counted and iterated over, an outline is one
	// line per heading, and -c is a tally of nodes.
	c.Opt.Distinct = ed.Op != edit.OpNone || format != render.Plain || c.Count
	if err != nil {
		fmt.Fprintf(os.Stderr, "mdgrep: %v\n", err)
		return 2
	}

	// The first positional is PATTERN unless -e already supplied one, in which
	// case every positional is a path. Filters never stand in for a pattern:
	// an empty one matches everything, so "mdgrep '' docs --todo" scopes a
	// filter to a directory the way grep would.
	paths := fs.Args()
	if len(c.Patterns) == 0 {
		switch {
		case c.Outline:
			// An outline names no pattern, so every positional is a path.
			c.Patterns = cli.PatternList{""}
		case fs.NArg() == 0:
			fmt.Fprintf(os.Stderr, "mdgrep: missing PATTERN\n%s\n", help.Hint)
			return 2
		default:
			c.Patterns, paths = cli.PatternList{fs.Arg(0)}, paths[1:]
		}
	}

	matcher, err := c.Matcher()
	if err != nil {
		fmt.Fprintf(os.Stderr, "mdgrep: %v\n", err)
		return 2
	}

	files, useStdin, err := walk.Files(paths, c.Exts(), c.Hidden, c.NoIgnore)
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
	p := c.Printer(out, format)

	found := false
	emit := func(src *mdoc.Source, res []search.Result) {
		if len(res) == 0 {
			return
		}
		found = true
		switch {
		case c.Quiet:
		case c.FilesOnly:
			fmt.Fprintln(out, src.Path)
		case c.Count:
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
		emit(doc.Src, search.File(doc, matcher, c.Opt))
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
				results[i] = report.File{Src: doc.Src, Res: search.File(doc, matcher, c.Opt)}
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
	if c.Opt.Rank {
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

// runEdits plans every file's changes and stages every file beside itself
// before renaming any of them into place, so a run that cannot be carried out
// in full leaves nothing behind.
func runEdits(out *bufio.Writer, p *render.Printer, results []report.File, e edit.Options, c *cli.Config) int {
	total := 0
	for _, r := range results {
		total += len(r.Res)
	}
	if why, code := report.Gate(total, c.Expect.Count(), c.Multi, report.FlagWords); code != 0 {
		// Nothing matching is the search's own answer, and stays as quiet
		// here as it is everywhere else.
		if why.Kind != "nomatch" {
			report.Refused(os.Stderr, results, total, why, p.Format)
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

	if !c.DryRun {
		var files []edit.File
		for i, changes := range planned {
			if len(changes) == 0 || !edit.Changed(changes) {
				continue
			}
			src := results[i].Src
			files = append(files, edit.File{Path: src.Path, Content: edit.Apply(src, changes)})
		}
		if failed, written, err := edit.CommitAll(files); err != nil {
			fmt.Fprintf(os.Stderr, "mdgrep: %s: %v; %s\n", failed, err, report.WroteSoFar(written))
			return 2
		}
	}
	// Nothing is reported until every file is in place, so a report of an
	// edit is a report of an edit that happened.
	if !c.Quiet {
		for i, changes := range planned {
			if len(changes) == 0 {
				continue
			}
			p.PrintEdits(results[i].Src, changes, c.DryRun)
		}
	}
	out.Flush()
	return 0
}

// helpTopic is the word --help was asked about: the one still standing on the
// line, and standing after the flag. Appending --help to a command already
// half typed is how the manual is usually reached, so a pattern already on the
// line before the flag is part of that command, not the topic it asks about.
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

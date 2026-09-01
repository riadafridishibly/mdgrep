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
	"strings"
	"sync"
	"sync/atomic"

	"github.com/riadafridishibly/mdgrep/internal/cli"
	"github.com/riadafridishibly/mdgrep/internal/edit"
	"github.com/riadafridishibly/mdgrep/internal/help"
	"github.com/riadafridishibly/mdgrep/internal/match"
	"github.com/riadafridishibly/mdgrep/internal/mdoc"
	"github.com/riadafridishibly/mdgrep/internal/plan"
	"github.com/riadafridishibly/mdgrep/internal/render"
	"github.com/riadafridishibly/mdgrep/internal/report"
	"github.com/riadafridishibly/mdgrep/internal/search"
	"github.com/riadafridishibly/mdgrep/internal/stream"
	"github.com/riadafridishibly/mdgrep/internal/walk"
)

// version names a build the module system cannot place: go build from a clone
// with no tag over it. An installed binary knows better, and buildVersion asks
// it first.
const version = "0.3.1"

// buildVersion is what -V reports. A binary from "go install <path>@v0.3.1"
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

// manyFiles is grep's rule for whether a file name is worth printing: a run
// that could answer from more than one file says which one each result came
// from, and one that could only ever answer from a single named file does
// not. A directory counts as more than one file however few markdown
// documents it turns out to hold, so the test is on what was asked for rather
// than on what the walk came back with -- "mdgrep x docs/" names docs/a.md
// even when a.md is all there is, which is what rg does and what keeps the
// name from appearing and disappearing as a tree is edited.
func manyFiles(paths, files []string, useStdin, streamed bool) bool {
	switch {
	case streamed:
		// A stream names its files outright, so it is read the way a list of
		// paths is rather than the way the walk that made it was.
		return len(files) > 1
	case useStdin && len(files) == 0:
		// Markdown arriving on stdin has no name to print.
		return false
	case len(paths) != 1:
		// No path at all is a walk of ".", which is a directory like any
		// other; more than one is more than one file by inspection.
		return true
	}
	info, err := os.Stat(paths[0])
	return err != nil || info.IsDir()
}

// stage is one step of a search: a command line of its own, and the matcher
// its flags describe. Every stage but the last narrows the document down for
// the one after it; the last stage is the one that prints or writes.
type stage struct {
	c  *cli.Config
	fs *flag.FlagSet
	m  match.Matcher
}

// staged names which stage of a pipeline a message is about, since several of
// them look alike on the line and a complaint about a flag has to say which
// search it was given to. A run of one stage is the whole command, and says
// nothing about stages at all.
func staged(i, n int, err error) error {
	if n == 1 {
		return err
	}
	return fmt.Errorf("stage %d of %d: %w", i+1, n, err)
}

func run() (code int) {
	lines, err := cli.Stages(os.Args[1:])
	if err != nil {
		fmt.Fprintf(os.Stderr, "mdgrep: %v\n%s\n", err, help.Hint)
		return 2
	}
	stages := make([]*stage, len(lines))
	for i, args := range lines {
		c, fs, err := cli.Parse(args)
		if err != nil {
			fmt.Fprintf(os.Stderr, "mdgrep: %v\n%s\n", staged(i, len(lines), err), help.Hint)
			return 2
		}
		stages[i] = &stage{c: c, fs: fs}
	}
	first, last := stages[0], stages[len(stages)-1]
	// Every stage is held to its place before anything dispatches on what it
	// was given, so a pipeline cannot be talked into a plan, or into printing
	// halfway along, by a flag that only the run as a whole could honour.
	if len(stages) > 1 {
		for i, st := range stages {
			if err := cli.StageFlags(st.fs, i, len(stages)); err != nil {
				fmt.Fprintf(os.Stderr, "mdgrep: %v\n%s\n", staged(i, len(stages), err), help.Hint)
				return 2
			}
		}
	}
	if first.c.Help.Asked() {
		// --help=editing names its topic outright. Bare --help leaves it to
		// the one positional still standing, if there is one.
		topic := first.c.Help.Topic()
		if topic == "" && first.fs.NArg() == 1 {
			topic = helpTopic(lines[0], first.fs.Arg(0))
		}
		text, err := help.Text(topic)
		if err != nil {
			fmt.Fprintf(os.Stderr, "mdgrep: %v\n", err)
			return 2
		}
		fmt.Fprint(os.Stdout, text)
		return 0
	}
	if first.c.ShowVer {
		fmt.Fprintf(os.Stdout, "mdgrep %s\n", buildVersion())
		return 0
	}
	// The last stage is the one anybody reads, so it is the one that says what
	// the output looks like and what the run does to the files it selected.
	format, err := last.c.Format()
	if err != nil {
		fmt.Fprintf(os.Stderr, "mdgrep: %v\n%s\n", err, help.Hint)
		return 2
	}
	for i, st := range stages {
		if err := st.c.Validate(); err != nil {
			fmt.Fprintf(os.Stderr, "mdgrep: %v\n", staged(i, len(stages), err))
			return 2
		}
	}
	// A stream is checked before anything dispatches on it, because the flags
	// it cannot honour include the ones that would take the run elsewhere.
	if format == render.Stream {
		if err := cli.StreamFlags(last.fs); err != nil {
			fmt.Fprintf(os.Stderr, "mdgrep: %v\n%s\n", err, help.Hint)
			return 2
		}
	}
	// A plan is a whole run of its own: it names its files, its searches and
	// its edits, so nothing below this point has anything left to work out.
	if _, given := last.c.Apply.Value(); given {
		return plan.Run(last.c, last.fs, format)
	}
	if last.c.Outline {
		if err := cli.OutlineFlags(last.fs); err != nil {
			fmt.Fprintf(os.Stderr, "mdgrep: %v\n%s\n", err, help.Hint)
			return 2
		}
		// --outline is a question about structure, so it fills in the search a
		// caller would otherwise spell out: every heading, matched by nothing
		// in particular. Either half can still be overridden.
		if last.c.Kinds == "" {
			last.c.Kinds = "heading"
		}
	}
	for i, st := range stages {
		kinds, err := cli.ParseKinds(st.c.Kinds)
		if err != nil {
			fmt.Fprintf(os.Stderr, "mdgrep: %v\n", staged(i, len(stages), err))
			return 2
		}
		st.c.Opt.Kinds = kinds
		st.c.Opt.Task = st.c.TaskFilter()
		// A stage that hands its nodes on keeps them apart: each is a region
		// of its own for the next stage to look inside, and two run together
		// would offer that stage a node neither of them selected.
		st.c.Opt.Distinct = true
	}

	ed, err := last.c.Edit()
	// Neighbouring hits are run together for a person reading the page as one
	// passage, and kept apart for everyone else: an edit rewrites each node on
	// its own, a machine format is counted and iterated over, an outline is one
	// line per heading, and -c is a tally of nodes. --truncate is the fourth
	// case and the one that looks like plain output: it caps a result to keep a
	// long one readable, so running the results together first would spend the
	// whole cap on the first node and drop every other match off the page
	// rather than shorten it.
	last.c.Opt.Distinct = ed.Op != edit.OpNone || format != render.Plain ||
		last.c.Count || last.c.Truncate > 0
	if err != nil {
		fmt.Fprintf(os.Stderr, "mdgrep: %v\n", err)
		return 2
	}

	// The first positional is PATTERN unless -e already supplied one, in which
	// case every positional is a path. Filters never stand in for a pattern:
	// an empty one matches everything, so "mdgrep '' docs --todo" scopes a
	// filter to a directory the way grep would.
	paths := first.fs.Args()
	if len(first.c.Patterns) == 0 {
		switch {
		case first.c.Outline:
			// An outline names no pattern, so every positional is a path.
			first.c.Patterns = cli.PatternList{""}
		case first.fs.NArg() == 0:
			fmt.Fprintf(os.Stderr, "mdgrep: missing PATTERN\n%s\n", help.Hint)
			return 2
		default:
			first.c.Patterns, paths = cli.PatternList{first.fs.Arg(0)}, paths[1:]
		}
	}
	for i, st := range stages[1:] {
		if err := stagePattern(st); err != nil {
			fmt.Fprintf(os.Stderr, "mdgrep: %v\n%s\n", staged(i+1, len(stages), err), help.Hint)
			return 2
		}
	}

	for i, st := range stages {
		st.m, err = st.c.Matcher()
		if err != nil {
			fmt.Fprintf(os.Stderr, "mdgrep: %v\n", staged(i, len(stages), err))
			return 2
		}
	}
	matcher := last.m

	files, useStdin, unread, err := walk.Files(paths, first.c.Exts(), first.c.Hidden, first.c.NoIgnore)
	if err != nil {
		fmt.Fprintf(os.Stderr, "mdgrep: %v\n", err)
		return 2
	}
	for _, e := range unread {
		fmt.Fprintf(os.Stderr, "mdgrep: %v\n", e)
	}
	// A directory that could not be read is a hole in the search, and the run
	// says so however well the rest of it went: "no matches" over a tree only
	// half looked at is an answer the caller would act on and should not.
	// A file that could not be read is the same hole, and a stream names its
	// files rather than walking for them: one saved and replayed after a
	// rename names a file nothing can open, and that is an error rather than
	// an answer about the search.
	var unreadable atomic.Bool
	done := func(answer int) int {
		if len(unread) > 0 || unreadable.Load() {
			return 2
		}
		return answer
	}
	// A pipe carrying a stream names its own files, so it stands in for the
	// walk rather than being parsed as a document: the regions say which lines
	// of those files are still in play, and everything downstream of here --
	// line numbers, breadcrumbs, and the path an edit writes to -- comes from
	// the file itself rather than from the text an earlier stage printed.
	var scope *stream.Scope
	var stdinData []byte
	if useStdin {
		stdinData, err = io.ReadAll(os.Stdin)
		if err != nil {
			fmt.Fprintf(os.Stderr, "mdgrep: stdin: %v\n", err)
			return 2
		}
		sc, isStream, err := stream.Parse(stdinData)
		if err != nil {
			fmt.Fprintf(os.Stderr, "mdgrep: %v\n", err)
			return 2
		}
		if isStream {
			if len(files) > 0 {
				fmt.Fprintln(os.Stderr, "mdgrep: a stream names its own files, so it takes no PATH")
				return 2
			}
			// The stage that walked the tree is the one the walk flags belong
			// to, and it was another process. Refusing them by name here is
			// what keeps the two spellings of a pipeline answering alike: the
			// same flag on a later --then stage is refused too.
			if err := cli.StreamWalks(first.fs); err != nil {
				fmt.Fprintf(os.Stderr, "mdgrep: %v\n%s\n", staged(0, len(stages), err), help.Hint)
				return 2
			}
			scope, files, useStdin, stdinData = sc, sc.Paths, false, nil
		}
	}
	if ed.Op != edit.OpNone && useStdin {
		fmt.Fprintln(os.Stderr, "mdgrep: an edit needs files to write to, not stdin")
		return 2
	}
	// A stream names a file for the next stage to read, and stdin is not one:
	// the records would carry "<stdin>", which nothing downstream can open. The
	// run says so where the mistake was made rather than a stage later.
	if format == render.Stream && useStdin {
		fmt.Fprintln(os.Stderr, "mdgrep: a stream names the files the next stage reads, so it cannot be made from stdin")
		return 2
	}

	out := bufio.NewWriter(os.Stdout)
	// Output that could not be written is not output. A run whose last write
	// failed has printed less than it found, so it says so rather than
	// handing back the exit status of the search it did not finish reporting.
	defer func() {
		if err := out.Flush(); err != nil && code != 2 {
			fmt.Fprintf(os.Stderr, "mdgrep: %v\n", err)
			code = 2
		}
	}()
	p := last.c.Printer(out, format, cli.Page{
		TTY:       cli.IsTTY(),
		ManyFiles: manyFiles(paths, files, useStdin, scope != nil),
	})
	p.Begin()

	found := false
	emit := func(src *mdoc.Source, res []search.Result) {
		if len(res) == 0 {
			return
		}
		found = true
		switch {
		case last.c.Quiet:
		case last.c.FilesOnly:
			p.PrintFile(src.Path)
		case last.c.Count:
			p.PrintCount(src.Path, len(res))
		default:
			p.Print(src, res, matcher)
		}
	}

	// reached[i] is whether stage i selected anything in any file, which is
	// what lets a run of several stages say where the narrowing stopped.
	reached := make([]atomic.Bool, len(stages))
	if useStdin {
		doc := mdoc.Parse("<stdin>", stdinData)
		emit(doc.Src, pipeline(doc, stages, nil, reached))
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
					unreadable.Store(true)
					continue
				}
				doc := mdoc.Parse(files[i], data)
				// The scope a stream handed in is the one thing that differs
				// per file, since it is the earlier stage's answer about that
				// file and no other.
				results[i] = report.File{Src: doc.Src, Res: pipeline(doc, stages, scope.For(files[i]), reached)}
			}
		})
	}
	for i := range files {
		jobs <- i
	}
	close(jobs)
	wg.Wait()

	if ed.Op != edit.OpNone {
		return done(runEdits(out, p, results, ed, last.c))
	}
	if last.c.Opt.Rank {
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
		if len(stages) > 1 && !last.c.Quiet {
			fmt.Fprintf(os.Stderr, "mdgrep: %s\n", narrowed(stages, reached))
		}
		return done(1)
	}
	return done(0)
}

// pipeline runs one document through every stage in turn. A stage searches
// only inside the regions the stage before it selected, and the last stage's
// results are the run's. A stage that selects nothing ends the file there,
// since a later stage has nothing left to look inside.
func pipeline(doc *mdoc.Doc, stages []*stage, scope []search.Region, reached []atomic.Bool) []search.Result {
	for i, st := range stages[:len(stages)-1] {
		opt := st.c.Opt
		opt.Scope = scope
		res := search.File(doc, st.m, opt)
		if len(res) == 0 {
			return nil
		}
		reached[i].Store(true)
		scope = regions(res)
	}
	last := stages[len(stages)-1]
	opt := last.c.Opt
	opt.Scope = scope
	out := search.File(doc, last.m, opt)
	if len(out) > 0 {
		reached[len(stages)-1].Store(true)
	}
	return out
}

// narrowed says where a pipeline that ended with nothing ran out: the first
// stage that selected nothing in any file. The answer is still "no matches" --
// this only says which of several searches to look at, which is what one
// process holding every stage knows and a pipe of streams cannot.
func narrowed(stages []*stage, reached []atomic.Bool) string {
	for i := range stages {
		if !reached[i].Load() {
			return fmt.Sprintf("stage %d of %d narrowed to nothing", i+1, len(stages))
		}
	}
	return "no matches"
}

// regions is what one stage hands the next: the span of each result, which is
// the node that matched together with whatever --section or --expand widened
// it by, and nothing about the text.
func regions(res []search.Result) []search.Region {
	out := make([]search.Region, len(res))
	for i, r := range res {
		out[i] = search.Region{Start: r.Start, End: r.End}
	}
	return out
}

// stagePattern reads the positionals of a stage that is not the first one.
// Only the first stage names files, so a word here is the pattern and can be
// nothing else -- and a stage that writes none searches for the empty pattern,
// which matches every node its filters admit. The first stage has to spell
// that "" out, since there a bare word could be a path.
func stagePattern(st *stage) error {
	switch {
	case len(st.c.Patterns) > 0 && st.fs.NArg() > 0:
		return fmt.Errorf("-e gave this stage its pattern and a stage names no files, so %q has nowhere to go", st.fs.Arg(0))
	case st.fs.NArg() > 1:
		return fmt.Errorf("a stage names no files, so it takes one PATTERN at most, not %d words", st.fs.NArg())
	case st.fs.NArg() == 1:
		st.c.Patterns = cli.PatternList{st.fs.Arg(0)}
	case len(st.c.Patterns) == 0:
		st.c.Patterns = cli.PatternList{""}
	}
	return nil
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

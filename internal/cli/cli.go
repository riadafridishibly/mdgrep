// Package cli reads a command line: the flags mdgrep takes, and the search,
// edit and output each combination of them describes.
package cli

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/riadafridishibly/mdgrep/internal/edit"
	"github.com/riadafridishibly/mdgrep/internal/match"
	"github.com/riadafridishibly/mdgrep/internal/mdoc"
	"github.com/riadafridishibly/mdgrep/internal/render"
	"github.com/riadafridishibly/mdgrep/internal/search"
)

type Config struct {
	Patterns   PatternList
	ForceCase  bool
	ForceFold  bool
	Word       bool
	Invert     bool
	MinScore   OptFloat
	Kinds      string
	AnchorSty  string
	Task       bool
	Checked    bool
	Unchecked  bool
	Opt        search.Options
	Context    int
	NoNums     bool
	NoCrumb    bool
	Separator  OptString
	Truncate   int
	Outline    bool
	Color      string
	JsonOut    bool
	StreamOut  bool
	FormatFlag OptString
	Count      bool
	FilesOnly  bool
	Quiet      bool
	Ext        string
	Hidden     bool
	NoIgnore   bool
	Help       OptTopic
	ShowVer    bool

	Check     bool
	Uncheck   bool
	Toggle    bool
	Del       bool
	Replace   OptString
	ReplFrom  OptString
	SetText   OptString
	SetFrom   OptString
	AppendTo  OptString
	AppFrom   OptString
	PrependTo OptString
	PreFrom   OptString
	Multi     bool
	Expect    OptInt
	DryRun    bool
	Apply     OptString

	// The matching flags are read together rather than one at a time: --fuzzy
	// and --fixed-strings exclude each other, --anchor excludes both, and
	// smart case is the default the two case flags override. Matcher is where
	// that reading happens, so nothing outside needs the raw switches.
	fuzzy, fixed, useAnchor, smart bool
}

// OptString remembers whether a text flag was given at all, so --replace ""
// asks for an empty replacement rather than for nothing.
type OptString struct {
	val string
	set bool
}

func (o *OptString) String() string { return o.val }

// Value is the text a flag was given and whether it was given at all, so a
// caller can tell an empty argument from an absent one.
func (o OptString) Value() (string, bool) { return o.val, o.set }

func (o *OptString) Set(v string) error {
	o.val, o.set = v, true
	return nil
}

// OptInt is OptString's counterpart for a count, so "--expect 0" is a claim
// the run can reject rather than the same thing as leaving --expect out.
type OptInt struct {
	val int
	set bool
}

func (o *OptInt) String() string { return strconv.Itoa(o.val) }

// Count is what --expect asked for, or nil when it did not ask: the shape a
// reader outside the flag package can take without knowing about flags.
func (o OptInt) Count() *int {
	if !o.set {
		return nil
	}
	return &o.val
}

func (o *OptInt) Set(v string) error {
	n, err := strconv.Atoi(v)
	if err != nil {
		return fmt.Errorf("not a number: %q", v)
	}
	o.val, o.set = n, true
	return nil
}

// OptFloat is the same for a score, so --min-score can say it was given at
// all: a threshold nothing reads is a flag the caller meant to have an effect.
type OptFloat struct {
	val float64
	set bool
}

func (o *OptFloat) String() string { return strconv.FormatFloat(o.val, 'g', -1, 64) }

// Value is the score a flag was given and whether it was given at all.
func (o OptFloat) Value() (float64, bool) { return o.val, o.set }

// Or is the score to search with: what was asked for, or def when nothing was.
func (o OptFloat) Or(def float64) float64 {
	if !o.set {
		return def
	}
	return o.val
}

func (o *OptFloat) Set(v string) error {
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return fmt.Errorf("not a number: %q", v)
	}
	o.val, o.set = f, true
	return nil
}

// OptTopic is --help, which stands on its own but also names the part of the
// manual it wants. IsBoolFlag is what lets both spellings through: bare
// --help takes no argument, and --help=editing carries one.
type OptTopic struct {
	topic string
	set   bool
}

func (o *OptTopic) String() string   { return o.topic }
func (o *OptTopic) IsBoolFlag() bool { return true }

// Asked reports whether help was wanted, and Topic names the part of it, empty
// when the caller asked for the manual whole.
func (o OptTopic) Asked() bool   { return o.set }
func (o OptTopic) Topic() string { return o.topic }

func (o *OptTopic) Set(v string) error {
	// "true" and "false" are what the flag package synthesises for a bare
	// --help and for --help=false; neither is the name of a topic.
	switch v {
	case "true":
		o.set = true
	case "false":
	default:
		o.set, o.topic = true, v
	}
	return nil
}

// PatternList collects repeated -e flags, which are alternatives to one
// another the way they are in grep.
type PatternList []string

func (p *PatternList) String() string { return strings.Join(*p, "|") }

func (p *PatternList) Set(v string) error {
	*p = append(*p, v)
	return nil
}

// Parse reads a command line into a Config. The FlagSet comes back with it
// because what is left over -- the positionals, and which flags were given at
// all -- is part of the answer.
func Parse(args []string) (*Config, *flag.FlagSet, error) {
	var c Config

	fs := flag.NewFlagSet("mdgrep", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	bind := func(set func(name string), names ...string) {
		for _, n := range names {
			set(n)
		}
	}
	bind(func(n string) { fs.Var(&c.Patterns, n, "") }, "e", "regexp")
	bind(func(n string) { fs.BoolVar(&c.fixed, n, false, "") }, "F", "fixed-strings")
	fs.BoolVar(&c.fuzzy, "fuzzy", false, "")
	fs.BoolVar(&c.useAnchor, "anchor", false, "")
	fs.StringVar(&c.AnchorSty, "anchor-style", "", "")
	fs.Var(&c.MinScore, "min-score", "")
	bind(func(n string) { fs.BoolVar(&c.Word, n, false, "") }, "w", "word-regexp")
	bind(func(n string) { fs.BoolVar(&c.Invert, n, false, "") }, "v", "invert-match")
	bind(func(n string) { fs.BoolVar(&c.ForceFold, n, false, "") }, "i", "ignore-case")
	bind(func(n string) { fs.BoolVar(&c.ForceCase, n, false, "") }, "s", "case-sensitive")
	bind(func(n string) { fs.BoolVar(&c.smart, n, false, "") }, "S", "smart-case")
	bind(func(n string) { fs.StringVar(&c.Kinds, n, "", "") }, "k", "kind")
	fs.BoolVar(&c.Task, "task", false, "")
	bind(func(n string) { fs.BoolVar(&c.Checked, n, false, "") }, "checked", "done")
	bind(func(n string) { fs.BoolVar(&c.Unchecked, n, false, "") }, "unchecked", "todo")
	fs.IntVar(&c.Opt.Expand, "expand", 0, "")
	fs.BoolVar(&c.Opt.Section, "section", false, "")
	fs.BoolVar(&c.Opt.Body, "section-body", false, "")
	fs.BoolVar(&c.Check, "check", false, "")
	fs.BoolVar(&c.Uncheck, "uncheck", false, "")
	fs.BoolVar(&c.Toggle, "toggle", false, "")
	fs.BoolVar(&c.Del, "delete", false, "")
	fs.Var(&c.Replace, "replace", "")
	fs.Var(&c.ReplFrom, "replace-from", "")
	fs.Var(&c.SetText, "set-text", "")
	fs.Var(&c.SetFrom, "set-text-from", "")
	fs.Var(&c.AppendTo, "append", "")
	fs.Var(&c.AppFrom, "append-from", "")
	fs.Var(&c.PrependTo, "prepend", "")
	fs.Var(&c.PreFrom, "prepend-from", "")
	fs.BoolVar(&c.Multi, "multi", false, "")
	fs.Var(&c.Expect, "expect", "")
	fs.BoolVar(&c.DryRun, "dry-run", false, "")
	fs.Var(&c.Apply, "apply", "")
	bind(func(n string) { fs.IntVar(&c.Opt.Before, n, 0, "") }, "B", "before")
	bind(func(n string) { fs.IntVar(&c.Opt.After, n, 0, "") }, "A", "after")
	bind(func(n string) { fs.IntVar(&c.Context, n, 0, "") }, "C", "context")
	fs.IntVar(&c.Opt.Lines, "lines", 0, "")
	bind(func(n string) { fs.IntVar(&c.Opt.Max, n, 0, "") }, "m", "max-count")
	bind(func(n string) { fs.BoolVar(&c.NoNums, n, false, "") }, "N", "no-line-number")
	// Numbering is already on; -n exists so a grep habit does not error out.
	bind(func(n string) { fs.Bool(n, false, "") }, "n", "line-number")
	fs.BoolVar(&c.NoCrumb, "no-breadcrumb", false, "")
	fs.BoolVar(&c.Outline, "outline", false, "")
	fs.Var(&c.Separator, "separator", "")
	fs.IntVar(&c.Truncate, "truncate", 0, "")
	fs.StringVar(&c.Color, "color", "auto", "")
	fs.BoolVar(&c.JsonOut, "json", false, "")
	fs.BoolVar(&c.StreamOut, "stream", false, "")
	fs.Var(&c.FormatFlag, "format", "")
	bind(func(n string) { fs.BoolVar(&c.Count, n, false, "") }, "c", "count")
	bind(func(n string) { fs.BoolVar(&c.FilesOnly, n, false, "") }, "l", "files-with-matches")
	bind(func(n string) { fs.BoolVar(&c.Quiet, n, false, "") }, "q", "quiet")
	fs.StringVar(&c.Ext, "ext", "md,markdown,mdown,mkd,mdx", "")
	fs.BoolVar(&c.Hidden, "hidden", false, "")
	fs.BoolVar(&c.NoIgnore, "no-ignore", false, "")
	bind(func(n string) { fs.Var(&c.Help, n, "") }, "h", "help")
	bind(func(n string) { fs.BoolVar(&c.ShowVer, n, false, "") }, "V", "version")

	if err := fs.Parse(permute(fs, args)); err != nil {
		return nil, nil, err
	}
	return &c, fs, nil
}

// Matcher reads the matching flags together and answers with the matcher the
// search should use. An anchor search says which heading it wants, so there is
// nothing left for a matcher to score or to highlight, and the anchor lands on
// the options instead.
func (c *Config) Matcher() (match.Matcher, error) {
	mode := match.Regexp
	_, minScoreGiven := c.MinScore.Value()
	switch {
	case c.fuzzy && c.fixed:
		return nil, errors.New("--fuzzy and --fixed-strings are mutually exclusive")
	case c.useAnchor && (c.fuzzy || c.fixed || c.Word || c.Invert ||
		c.ForceFold || c.ForceCase || c.smart):
		return nil, errors.New("--anchor selects a heading by name and takes no other matching flag")
	// A flag nothing reads is a flag the caller expected to have an effect, so
	// the run says it will not rather than searching some other way in silence.
	case count(c.ForceFold, c.ForceCase, c.smart) > 1:
		return nil, errors.New("-i, -s and -S each say how case is read; pass one")
	case minScoreGiven && !c.fuzzy:
		return nil, errors.New("--min-score is the threshold --fuzzy scores against, and does nothing without it")
	case c.AnchorSty != "" && !c.useAnchor:
		return nil, errors.New("--anchor-style narrows the conventions --anchor tries, and does nothing without it")
	case c.fuzzy:
		mode = match.Fuzzy
		// A fuzzy pattern is a question about which node fits best, so the
		// answer is ordered by score. An exact search is a filter, and keeps
		// grep's order.
		c.Opt.Rank = true
	case c.fixed:
		mode = match.Substring
	}
	if c.Context > 0 {
		c.Opt.Before, c.Opt.After = c.Context, c.Context
	}
	if !c.useAnchor {
		return BuildMatcher(Matching{
			Patterns: c.Patterns,
			Mode:     mode,
			Case:     c.caseRule(),
			MinScore: c.MinScore.Or(DefaultMinScore),
			Word:     c.Word,
			Invert:   c.Invert,
		})
	}
	anchor, err := c.buildAnchor()
	if err != nil {
		return nil, err
	}
	c.Opt.Anchor = anchor
	return match.All(), nil
}

func count(flags ...bool) int {
	n := 0
	for _, f := range flags {
		if f {
			n++
		}
	}
	return n
}

// Validate rejects the arguments that name a size no run can have. A count of
// lines or of nodes is how many to print or how far to climb, so a negative
// one is a typo -- and left alone it reads as if the flag had been left off,
// which is the one thing the caller did not ask for.
func (c *Config) Validate() error {
	for _, n := range []struct {
		flag string
		val  int
	}{
		{"--expand", c.Opt.Expand},
		{"--before", c.Opt.Before},
		{"--after", c.Opt.After},
		{"--context", c.Context},
		{"--lines", c.Opt.Lines},
		{"--max-count", c.Opt.Max},
		{"--truncate", c.Truncate},
	} {
		if n.val < 0 {
			return fmt.Errorf("%s %d: a count of lines or nodes cannot be negative", n.flag, n.val)
		}
	}
	// The test is written the long way round because every comparison against
	// NaN is false: "s < 0 || s > 1" lets NaN through, and a NaN threshold is
	// not a loose filter but one that scores nothing at all, since the score
	// is kept with ">= min".
	if s, given := c.MinScore.Value(); given && !(s >= 0 && s <= 1) {
		return fmt.Errorf("--min-score %v: a fuzzy score runs from 0 to 1", s)
	}
	if c.Checked && c.Unchecked {
		return errors.New("--checked and --unchecked are opposite filters; --task selects either")
	}
	return nil
}

// Printer is the renderer the output flags describe.
func (c *Config) Printer(w *bufio.Writer, format render.Format) *render.Printer {
	return &render.Printer{
		W:           w,
		Color:       useColor(c.Color),
		LineNumbers: !c.NoNums,
		Breadcrumb:  !c.NoCrumb,
		Format:      format,
		Separator:   separator(c.Separator),
		Truncate:    c.Truncate,
	}
}

// Edit reads the editing flags as one operation, and rejects the
// combinations that would make an edit rewrite something other than what the
// same flags would have printed.
func (c *Config) Edit() (edit.Options, error) {
	var ops []edit.Options
	add := func(op edit.Op, text string) { ops = append(ops, edit.Options{Op: op, Text: text}) }
	if c.Check {
		add(edit.OpCheck, "")
	}
	if c.Uncheck {
		add(edit.OpUncheck, "")
	}
	if c.Toggle {
		add(edit.OpToggle, "")
	}
	if c.Del {
		add(edit.OpDelete, "")
	}
	// Every edit that takes text takes it either inline or from a file, and
	// the pair is one flag with two spellings rather than two edits.
	for _, t := range c.textOps() {
		switch {
		case t.inline.set && t.from.set:
			return edit.Options{}, fmt.Errorf("--%s and --%s-from both give the text for one edit", t.name, t.name)
		case t.inline.set:
			add(t.op, t.inline.val)
		case t.from.set:
			text, err := ReadText(t.from.val)
			if err != nil {
				return edit.Options{}, err
			}
			add(t.op, text)
		}
	}

	if len(ops) == 0 {
		if c.Multi || c.DryRun || c.Expect.set {
			return edit.Options{}, fmt.Errorf("--multi, --expect and --dry-run only mean something with an edit")
		}
		return edit.Options{}, nil
	}
	if len(ops) > 1 {
		return edit.Options{}, fmt.Errorf("one edit at a time: %s and %s were both asked for", ops[0].Op, ops[1].Op)
	}
	e := ops[0]

	switch {
	case c.Count || c.FilesOnly:
		return e, fmt.Errorf("--%s writes files; -c and -l only report on them", e.Op)
	case c.Opt.Before > 0 || c.Opt.After > 0 || c.Context > 0 || c.Opt.Lines > 0:
		return e, fmt.Errorf("-A, -B, -C and --lines pad what is printed; they do not select what an edit rewrites")
	case c.Opt.Max > 0:
		return e, fmt.Errorf("-m caps results; an edit wants every match it selects, or --multi")
	case c.Truncate > 0:
		return e, fmt.Errorf("--truncate caps what is printed; an edit reports the whole of what it wrote")
	case c.Outline:
		return e, fmt.Errorf("--outline reports structure; it does not select what an edit rewrites")
	case c.Expect.set && c.Expect.val < 1:
		return e, fmt.Errorf("--expect states how many nodes the search should find, so it wants a count above zero")
	case e.Op.Node() && (c.Opt.Section || c.Opt.Body):
		return e, fmt.Errorf("--%s edits the matched node, so --section has nothing to widen; use --replace", e.Op)
	}
	// A checkbox edit is about task items, so it says so on the search's
	// behalf: the hit climbs to the item owning it the way --task does.
	switch e.Op {
	case edit.OpCheck, edit.OpUncheck, edit.OpToggle:
		if c.Opt.Task == search.TaskIgnore {
			c.Opt.Task = search.TaskAny
		}
	}
	return e, nil
}

// textOp pairs an edit that takes text with the two flags that can carry it.
type textOp struct {
	op     edit.Op
	name   string
	inline *OptString
	from   *OptString
}

func (c *Config) textOps() []textOp {
	return []textOp{
		{edit.OpReplace, "replace", &c.Replace, &c.ReplFrom},
		{edit.OpSetText, "set-text", &c.SetText, &c.SetFrom},
		{edit.OpAppend, "append", &c.AppendTo, &c.AppFrom},
		{edit.OpPrepend, "prepend", &c.PrependTo, &c.PreFrom},
	}
}

func ReadText(path string) (string, error) {
	if path == "-" {
		data, err := io.ReadAll(os.Stdin)
		return string(data), err
	}
	data, err := os.ReadFile(path)
	return string(data), err
}

// buildAnchor reads --anchor-style before the pattern, so a run that names a
// convention nothing has is refused by the name it gave rather than by the
// heading it went on to miss.
func (c *Config) buildAnchor() (*search.Anchor, error) {
	styles, err := parseAnchorStyles(c.AnchorSty)
	if err != nil {
		return nil, err
	}
	return search.NewAnchor(c.Patterns, styles)
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

// DefaultMinScore is the fuzzy threshold a run that names none searches with.
const DefaultMinScore = 0.7

// Case is how a pattern's own letters are read. Smart is the default and the
// zero value: -i and -s are the two ways of saying it should not be.
type Case int

const (
	Smart Case = iota
	Fold
	Exact
)

// Matching is everything a matcher is built from. A plan entry has no command
// line to read defaults out of, so it says each of these rather than borrowing
// whatever a Config would have held: a field added here is a field both
// callers have to answer for.
type Matching struct {
	Patterns []string
	Mode     match.Mode
	Case     Case
	MinScore float64
	Word     bool
	Invert   bool
}

// BuildMatcher turns every pattern into one matcher, alternatives to each
// other the way repeated -e is in grep.
//
// Smart case reads all the patterns together: a single upper-case letter
// anywhere in them makes the whole search case sensitive.
func BuildMatcher(m Matching) (match.Matcher, error) {
	opt := match.Options{Mode: m.Mode, MinScore: m.MinScore, Word: m.Word}
	switch m.Case {
	case Fold:
		opt.IgnoreCase = true
	case Exact:
		opt.IgnoreCase = false
	default:
		opt.IgnoreCase = match.SmartCase(strings.Join(m.Patterns, " "))
	}

	ms := make([]match.Matcher, 0, len(m.Patterns))
	for _, p := range m.Patterns {
		one, err := match.New(p, opt)
		if err != nil {
			return nil, err
		}
		ms = append(ms, one)
	}
	built := match.Any(ms)
	if m.Invert {
		built = match.Not(built)
	}
	return built, nil
}

// caseRule reads the three case flags, which Matcher has already refused each
// other, into the one rule BuildMatcher takes.
func (c *Config) caseRule() Case {
	switch {
	case c.ForceFold:
		return Fold
	case c.ForceCase:
		return Exact
	}
	return Smart
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
		// Accept the attached form people type for grep: -C2, -m5, -kheading.
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

// TaskFilter folds the three checkbox flags into one filter. Asking for both
// states at once is refused by Validate, which runs first, so the two are read
// here as the opposite filters they are.
func (c *Config) TaskFilter() search.TaskFilter {
	switch {
	case c.Checked:
		return search.TaskChecked
	case c.Unchecked:
		return search.TaskUnchecked
	case c.Task:
		return search.TaskAny
	}
	return search.TaskIgnore
}

// separator reads --separator, where leaving the flag out is the default rule
// and passing an empty string is the deliberate choice to have none.
func separator(o OptString) string {
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

// OutlineFlags rejects the flags that widen a result. An outline is one line
// per heading, and a widened result no longer begins on the heading that line
// is meant to be -- so rather than print a body line where a heading belongs,
// or silently drop the widening, the run says the two cannot be combined.
func OutlineFlags(fs *flag.FlagSet) error {
	if extra := Given(fs, func(name string) bool { return widens[name] }); extra != "" {
		return fmt.Errorf("--outline is one line per heading, so there is nothing for %s to widen", extra)
	}
	return nil
}

// streamIgnores names the flags that describe a page a stream does not print:
// how a result is decorated, and the shapes that stand a tally or a file name
// where a result would have gone. A stream is a list of regions, so each of
// these would be read, understood, and then change nothing the next stage sees.
var streamIgnores = map[string]bool{
	"truncate": true, "no-breadcrumb": true, "separator": true, "color": true,
	"n": true, "line-number": true, "N": true, "no-line-number": true,
	"c": true, "count": true, "l": true, "files-with-matches": true,
	"q": true, "quiet": true,
}

// streamEdits names every flag that writes. A stream is one stage of a
// pipeline handing its nodes to the next, and a file rewritten halfway along
// one is a search whose later stages read something nobody asked for.
var streamEdits = map[string]bool{
	"check": true, "uncheck": true, "toggle": true, "delete": true,
	"replace": true, "replace-from": true, "set-text": true, "set-text-from": true,
	"append": true, "append-from": true, "prepend": true, "prepend-from": true,
	"multi": true, "expect": true, "dry-run": true, "apply": true,
}

// StreamFlags refuses the flags a stream cannot honour, the way OutlineFlags
// refuses the ones an outline has nothing to widen.
func StreamFlags(fs *flag.FlagSet) error {
	if named := Given(fs, func(name string) bool { return streamEdits[name] }); named != "" {
		return fmt.Errorf("a stream hands its nodes to the next stage, so %s belongs on that stage rather than this one", named)
	}
	if named := Given(fs, func(name string) bool { return streamIgnores[name] }); named != "" {
		return fmt.Errorf("a stream is a list of regions, so there is nothing for %s to say about it", named)
	}
	return nil
}

// StreamWalks refuses the flags that say which files a run reads when the
// files were not walked for but named by a stream. They belong to the stage
// that did the walking, and a walk described on a stage handed a stream is one
// nothing carries out -- the same flag a later --then stage refuses by name.
func StreamWalks(fs *flag.FlagSet) error {
	if named := Given(fs, func(name string) bool { return pipeReads[name] }); named != "" {
		return fmt.Errorf("a stream names its own files, so %s belongs on the stage that walked them", named)
	}
	return nil
}

// Given lists the flags a run was given that match want, spelled the way the
// caller would have typed them, and empty when none were: the shape a mode
// that refuses the flags it cannot honour needs to name them.
func Given(fs *flag.FlagSet, want func(name string) bool) string {
	var named []string
	fs.Visit(func(f *flag.Flag) {
		if !want(f.Name) {
			return
		}
		dash := "--"
		if len(f.Name) == 1 {
			dash = "-"
		}
		named = append(named, dash+f.Name)
	})
	return strings.Join(named, ", ")
}

// parseFormat folds --format, --json and --stream into one answer. --json
// predates --format and stays as its own spelling of the same thing, and
// --stream is the same shorthand for the format a pipeline runs on, so a pair
// is only an error when the two disagree.
func parseFormat(spec OptString, jsonFlag, streamFlag, outline bool) (render.Format, error) {
	if outline && (spec.set || jsonFlag || streamFlag) {
		return 0, fmt.Errorf("--outline is its own format; drop --format, --json or --stream")
	}
	if jsonFlag && streamFlag {
		return 0, fmt.Errorf("--json prints the results and --stream hands them on; ask for one")
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
		case streamFlag:
			return render.Stream, nil
		}
		return render.Plain, nil
	}
	f, ok := formats[strings.ToLower(strings.TrimSpace(spec.val))]
	if !ok {
		return 0, fmt.Errorf("unknown format %q: plain, compact, json or stream", spec.val)
	}
	if jsonFlag && f != render.JSON {
		return 0, fmt.Errorf("--json and --format %s ask for different output", spec.val)
	}
	if streamFlag && f != render.Stream {
		return 0, fmt.Errorf("--stream and --format %s ask for different output", spec.val)
	}
	return f, nil
}

var formats = map[string]render.Format{
	"plain": render.Plain, "compact": render.Compact,
	"json": render.JSON, "stream": render.Stream,
}

// ParseKinds reads --kind's list into the set a search filters by. A name no
// alias table has is refused rather than dropped, since a kind that filters
// nothing out reads as a search over every kind -- which is what the caller
// passed the flag to avoid.
func ParseKinds(spec string) (map[mdoc.Kind]bool, error) {
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

// Format is the output shape the format flags name, with --json and --outline
// read as the shorthands for one that they are.
func (c *Config) Format() (render.Format, error) {
	return parseFormat(c.FormatFlag, c.JsonOut, c.StreamOut, c.Outline)
}

// Exts is the set of file extensions a walk should read.
func (c *Config) Exts() map[string]bool { return splitSet(c.Ext) }

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
	Expand     OptExpand
	At         AtList
	Before     int
	After      int
	Nums       bool
	NoNums     bool
	Crumb      bool
	NoCrumb    bool
	Heading    bool
	NoHeading  bool
	WithName   bool
	NoName     bool
	Separator  OptString
	Span       bool
	NoSpan     bool
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
	ReplNode  OptString
	NodeFrom  OptString
	SetText   OptString
	SetFrom   OptString
	AppendTo  OptString
	AppFrom   OptString
	PrependTo OptString
	PreFrom   OptString
	Multi     bool
	Expect    OptInt
	Write     bool
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

// OptExpand is --expand, which climbs the expand ladder and may say how far.
// Bare --expand is the node that matched, so the count is optional:
// IsBoolFlag is what lets the bare spelling through, and OptionalValue is what
// tells permute to hand it a following bare integer -- which the flag package
// will not do for a bool flag, and without which "--expand 2" would leave the
// 2 standing where PATTERN and PATH are waiting for it.
type OptExpand struct {
	n   int
	set bool
}

func (o *OptExpand) String() string      { return strconv.Itoa(o.n) }
func (o *OptExpand) IsBoolFlag() bool    { return true }
func (o *OptExpand) OptionalValue() bool { return true }

// Count is how many rungs to climb, and Asked whether --expand was given at
// all. The two differ under an address, where bare --expand is the block
// holding the lines rather than the lines themselves.
func (o OptExpand) Count() int  { return o.n }
func (o OptExpand) Asked() bool { return o.set }

func (o *OptExpand) Set(v string) error {
	// "true" and "false" are what the flag package synthesises for a bare
	// --expand and for --expand=false; neither is a count.
	switch v {
	case "true":
		o.set = true
		return nil
	case "false":
		return nil
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return fmt.Errorf("--expand climbs a number of rungs: %q", v)
	}
	o.n, o.set = n, true
	return nil
}

// AtList collects --at, which names lines of a file outright rather than
// searching for them. It repeats, so one run can take several regions of the
// one file, and it keeps what it was given so a refusal can quote it back.
type AtList struct {
	regions []search.Region
	raw     []string
}

func (a *AtList) String() string { return strings.Join(a.raw, ",") }

// Regions is the addresses, zero-based and inclusive the way a Result reports
// a region, from the 1-based numbers a note printed.
func (a AtList) Regions() []search.Region { return a.regions }

func (a *AtList) Set(v string) error {
	r, err := ParseAddress(v)
	if err != nil {
		return err
	}
	a.raw = append(a.raw, v)
	a.regions = append(a.regions, r)
	return nil
}

// ParseAddress reads an address -- "N" or "N-M", the 1-based inclusive numbers
// a span note prints -- as the region a search selects. A plan entry spells one
// the same way a command line does, so both read it here. The file's own
// length is checked where the file is read, since that is where the length is
// known.
func ParseAddress(v string) (search.Region, error) {
	bad := func(why string) (search.Region, error) {
		return search.Region{}, errors.New(why)
	}
	shape := "an address is N or N-M, as a span note writes one"
	first, last, ranged := strings.Cut(strings.TrimSpace(v), "-")
	start, err := strconv.Atoi(strings.TrimSpace(first))
	if err != nil {
		return bad(shape)
	}
	end := start
	if ranged {
		if end, err = strconv.Atoi(strings.TrimSpace(last)); err != nil {
			return bad(shape)
		}
	}
	switch {
	case start < 1:
		return bad("lines are numbered from 1")
	case end < start:
		return bad("an address runs from its first line to its last")
	}
	return search.Region{Start: start - 1, End: end - 1}, nil
}

// padFlag is -B, -A or -C: how many lines to pad a page with on each side of
// a matching line. Each writes straight through to the sides it names, so the
// flags are read in the order they were typed and the last one wins -- which
// is what lets "-C 3 -B 1" narrow to one line before, as it does in grep.
// Folding the three afterwards cannot express that: the flag package reports
// what was given, not the order it was given in.
type padFlag struct {
	// name is the spelling the manual documents the flag by, so a refusal
	// answers as "--before" however short the flag was typed.
	name          string
	before, after *int
}

func (p padFlag) String() string { return "" }

func (p padFlag) Set(v string) error {
	n, err := strconv.Atoi(v)
	if err != nil {
		return fmt.Errorf("%s pads a page with a number of lines: %q", p.name, v)
	}
	// The count is checked here rather than in Validate because the three
	// flags now share their storage: by the time Validate runs, a negative -C
	// is indistinguishable from a negative -B.
	if n < 0 {
		return fmt.Errorf("%s %d: a count of lines or nodes cannot be negative", p.name, n)
	}
	if p.before != nil {
		*p.before = n
	}
	if p.after != nil {
		*p.after = n
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

// Empty reports whether the patterns say nothing, which is what makes a search
// a filter over every node rather than a match on text inside one.
func (p PatternList) Empty() bool {
	for _, s := range p {
		if s != "" {
			return false
		}
	}
	return true
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
	fs.Var(&c.Expand, "expand", "")
	fs.Var(&c.At, "at", "")
	fs.BoolVar(&c.Opt.Section, "section", false, "")
	fs.BoolVar(&c.Opt.Body, "section-body", false, "")
	fs.BoolVar(&c.Check, "check", false, "")
	fs.BoolVar(&c.Uncheck, "uncheck", false, "")
	fs.BoolVar(&c.Toggle, "toggle", false, "")
	fs.BoolVar(&c.Del, "delete", false, "")
	fs.Var(&c.Replace, "replace", "")
	fs.Var(&c.ReplFrom, "replace-from", "")
	fs.Var(&c.ReplNode, "replace-node", "")
	fs.Var(&c.NodeFrom, "replace-node-from", "")
	fs.Var(&c.SetText, "set-text", "")
	fs.Var(&c.SetFrom, "set-text-from", "")
	fs.Var(&c.AppendTo, "append", "")
	fs.Var(&c.AppFrom, "append-from", "")
	fs.Var(&c.PrependTo, "prepend", "")
	fs.Var(&c.PreFrom, "prepend-from", "")
	fs.BoolVar(&c.Multi, "multi", false, "")
	fs.Var(&c.Expect, "expect", "")
	bind(func(n string) { fs.BoolVar(&c.Write, n, false, "") }, "W", "write")
	fs.Var(&c.Apply, "apply", "")
	bind(func(n string) { fs.Var(padFlag{"--before", &c.Before, nil}, n, "") }, "B", "before")
	bind(func(n string) { fs.Var(padFlag{"--after", nil, &c.After}, n, "") }, "A", "after")
	bind(func(n string) { fs.Var(padFlag{"--context", &c.Before, &c.After}, n, "") }, "C", "context")
	fs.IntVar(&c.Opt.Siblings, "siblings", 0, "")
	bind(func(n string) { fs.IntVar(&c.Opt.Max, n, 0, "") }, "m", "max-count")
	bind(func(n string) { fs.BoolVar(&c.NoNums, n, false, "") }, "N", "no-line-number")
	// Numbering is already on, so -n exists first so that a grep habit does
	// not error out -- but it is still an answer, and --no-decorate is only a
	// default for the questions nobody answered, so -n keeps the gutter there.
	bind(func(n string) { fs.BoolVar(&c.Nums, n, false, "") }, "n", "line-number")
	fs.BoolVar(&c.Crumb, "breadcrumb", false, "")
	fs.BoolVar(&c.NoCrumb, "no-breadcrumb", false, "")
	fs.BoolVar(&c.Heading, "heading", false, "")
	fs.BoolVar(&c.NoHeading, "no-heading", false, "")
	bind(func(n string) { fs.BoolVar(&c.WithName, n, false, "") }, "H", "with-filename")
	fs.BoolVar(&c.NoName, "no-filename", false, "")
	fs.BoolVar(&c.Outline, "outline", false, "")
	fs.Var(&c.Separator, "separator", "")
	fs.BoolVar(&c.Span, "span", false, "")
	fs.BoolVar(&c.NoSpan, "no-span", false, "")
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
	// c.At rather than c.Opt.At: the address reaches the options in Validate,
	// and a refusal that only fires once some other method has run is a
	// refusal a caller can skip past without meaning to.
	case c.useAnchor && len(c.At.Regions()) > 0:
		return nil, errors.New("--anchor names a heading and --at names lines; ask for one")
	case c.fuzzy:
		mode = match.Fuzzy
		// A fuzzy pattern is a question about which node fits best, so the
		// answer is ordered by score. An exact search is a filter, and keeps
		// grep's order.
		c.Opt.Rank = true
	case c.fixed:
		mode = match.Substring
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
		{"--expand", c.Expand.Count()},
		{"--siblings", c.Opt.Siblings},
		{"--max-count", c.Opt.Max},
		{"--truncate", c.Truncate},
	} {
		if n.val < 0 {
			return fmt.Errorf("%s %d: a count of lines or nodes cannot be negative", n.flag, n.val)
		}
	}
	// The flags a search reads are settled once, here, so every stage of a
	// pipeline carries the same answer to the same question.
	c.Opt.Expand, c.Opt.ExpandSet = c.Expand.Count(), c.Expand.Asked()
	c.Opt.At = c.At.Regions()
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
	if pick(c.Crumb, c.NoCrumb, false) && c.NoHeading {
		return errors.New("a breadcrumb stands above a file's results, which is what --heading is for; --no-heading leaves nowhere to put one")
	}
	return nil
}

// Page is what a run knows about its own output before it writes any: where
// the output is going, and how many files could have answered. grep and rg
// settle three defaults from exactly these two facts, and mdgrep settles the
// same three the same way.
type Page struct {
	// TTY is whether stdout is a terminal, which decides both whether the
	// file name goes above a file's results or in front of every line of
	// them, and whether the lines are numbered at all.
	TTY bool
	// ManyFiles is whether more than one file could answer, which decides
	// whether the name of the one that did is worth printing.
	ManyFiles bool
}

// Printer is the renderer the output flags describe. pg supplies the defaults
// for the three questions the flags may leave open; a flag that was spelled
// out always wins over one of them.
func (c *Config) Printer(w *bufio.Writer, format render.Format, pg Page) *render.Printer {
	// A breadcrumb is the one part of the page that only a heading has room
	// for, so asking for one asks for the mode (--no-heading beside it is
	// refused rather than silently answered), and the trail then goes
	// wherever a heading goes unless --no-breadcrumb says otherwise: a
	// heading is what says a person is reading, and the trail is what tells
	// a person where in the document they are. Each pair is read once, so
	// a flag withdrawn by its opposite is withdrawn everywhere it was read.
	heading := pick(c.Heading || pick(c.Crumb, c.NoCrumb, false), c.NoHeading, pg.TTY)
	return &render.Printer{
		W:           w,
		Color:       useColor(c.Color, pg.TTY),
		LineNumbers: pick(c.Nums, c.NoNums, pg.TTY),
		Filename:    pick(c.WithName, c.NoName, pg.ManyFiles),
		Heading:     heading,
		Breadcrumb:  pick(c.Crumb, c.NoCrumb, heading),
		Format:      format,
		Separator:   separator(c.Separator, format),
		Before:      c.Before,
		After:       c.After,
		// Asking for a widener asks to see the region whole rather than the
		// lines that matched inside it. That is the only switch between line
		// output and node output; there is no separate flag for it.
		Whole:    c.Expand.Asked() || c.Opt.Section || c.Opt.Body || c.Opt.Siblings > 0,
		Span:     pick(c.Span, c.NoSpan, true),
		Truncate: c.Truncate,
	}
}

// pick reads a pair of opposite flags over a default. Neither given leaves the
// default standing; both given is read as the negative, since a caller who
// asked for a thing and then asked against it is most safely taken at the
// narrower word -- and it is what "-n -N" has always meant here.
func pick(yes, no, byDefault bool) bool {
	switch {
	case no:
		return false
	case yes:
		return true
	}
	return byDefault
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
		if c.Multi || c.Write || c.Expect.set {
			return edit.Options{}, fmt.Errorf("--multi, --expect and --write only mean something with an edit")
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
	case c.Before > 0 || c.After > 0:
		return e, fmt.Errorf("-A, -B and -C pad a page with lines around a match; an edit reports the whole of what it wrote")
	// --siblings widens a result to blocks the matcher never pointed at. As
	// context that is what it is for; as the region an edit rewrites it would
	// destroy the neighbours to write the one node that matched.
	case c.Opt.Siblings > 0:
		return e, fmt.Errorf("--siblings keeps the blocks either side of a match on the page; it does not select what an edit rewrites")
	case len(c.At.Regions()) > 0 && (c.Multi || c.Expect.set):
		return e, fmt.Errorf("--multi and --expect say how many nodes a search should have found; --at found one by saying so")
	case c.Opt.Max > 0:
		return e, fmt.Errorf("-m caps results; an edit wants every match it selects, or --multi")
	case c.Truncate > 0:
		return e, fmt.Errorf("--truncate caps what is printed; an edit reports the whole of what it wrote")
	case c.Outline:
		return e, fmt.Errorf("--outline reports structure; it does not select what an edit rewrites")
	case c.Expect.set && c.Expect.val < 1:
		return e, fmt.Errorf("--expect states how many nodes the search should find, so it wants a count above zero")
	case e.Op.Node() && (c.Opt.Section || c.Opt.Body):
		return e, fmt.Errorf("--%s edits the matched node, so --section has nothing to widen; use --replace-node", e.Op)
	}
	if e.Op == edit.OpReplace {
		if err := c.substMatcher(); err != nil {
			return e, err
		}
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

// substMatcher refuses the searches that select nodes without pointing at text
// inside them. A substitution stands its replacement in for what the pattern
// matched, so a search that matched by not matching, by scattering a fuzzy
// score across a block, or by naming lines outright has nothing for it to
// stand in for -- and would otherwise rewrite every line it selected into
// itself and report an edit that did nothing.
func (c *Config) substMatcher() error {
	switch {
	case c.Invert:
		return errors.New("-v selects what a pattern did not match, so --replace has no matched text to stand in for; use --replace-node")
	case c.fuzzy:
		return errors.New("--fuzzy scores a block on characters spread across it rather than on a run of text, so --replace has nothing whole to stand in for; use --fixed-strings or a regexp")
	case c.useAnchor:
		return errors.New("--anchor selects a heading by its link anchor rather than by text on the line, so --replace has nothing to stand in for; use --set-text")
	case len(c.At.Regions()) > 0:
		return errors.New("--at names lines outright and consults no pattern, so --replace has nothing to stand in for; use --replace-node")
	}
	return nil
}

// SubstPattern refuses a substitution with no pattern to substitute for. It is
// apart from substMatcher because a bare word on the command line could be a
// path, so which stage has which pattern is not settled until the paths are:
// this is asked once they are, and once the matchers are built from them.
func (c *Config) SubstPattern() error {
	if c.Patterns.Empty() {
		return errors.New("--replace stands its text in for what a pattern matched, so it wants a pattern; --replace-node rewrites a node the filters alone selected")
	}
	return nil
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
		{edit.OpReplaceNode, "replace-node", &c.ReplNode, &c.NodeFrom},
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
		// An optional-valued flag is a bool flag to the flag package, which
		// will not consume a following word -- so "--expand 2" would leave the
		// 2 standing as a positional, where PATTERN and PATH are waiting for
		// it. A bare integer after one is its count, and is attached here.
		if f := fs.Lookup(name); f != nil && isOptValueFlag(f) && i+1 < len(args) && isInteger(args[i+1]) {
			flags = append(flags, a+"="+args[i+1])
			i++
			continue
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

// isOptValueFlag reports whether a flag takes its value optionally: bare, or
// with a count after it.
func isOptValueFlag(f *flag.Flag) bool {
	o, ok := f.Value.(interface{ OptionalValue() bool })
	return ok && o.OptionalValue()
}

// isInteger reports whether a word is a count and nothing else. A negative one
// counts: no flag is spelled "-1", and refusing "--expand -1" by name is
// better than reading the -1 as a flag nobody has.
func isInteger(s string) bool {
	s = strings.TrimPrefix(s, "-")
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
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

// separator reads --separator, which goes between two results, and between
// two files where no heading parts them. Nothing is the default: two results
// of a file are two nodes of the same document, and a page of them reads as
// the document does. grep prints its "--" only where a context flag has put
// lines between the hits that were never next to each other, which is the
// case --separator is there to spell out.
func separator(o OptString, f render.Format) string {
	if o.set {
		return o.val
	}
	// Only a page of file lines has groups to part. An outline is one line per
	// heading and a machine format is one record per result, so neither has
	// anything for a separator to stand between.
	if f == render.Plain {
		return "--"
	}
	return ""
}

// widens are the flags that make a result cover more than the node that
// matched.
var widens = map[string]bool{
	"siblings": true, "expand": true, "section": true, "section-body": true,
}

// contexts are the flags that pad a page with lines around a match. They widen
// nothing: the region a result covers is the same with them as without, and
// only the page is longer.
var contexts = map[string]bool{
	"B": true, "before": true, "A": true, "after": true, "C": true, "context": true,
}

// OutlineFlags rejects the flags that widen a result. An outline is one line
// per heading, and a widened result no longer begins on the heading that line
// is meant to be -- so rather than print a body line where a heading belongs,
// or silently drop the widening, the run says the two cannot be combined.
func OutlineFlags(fs *flag.FlagSet) error {
	if extra := Given(fs, func(name string) bool { return widens[name] }); extra != "" {
		return fmt.Errorf("--outline is one line per heading, so there is nothing for %s to widen", extra)
	}
	if extra := Given(fs, func(name string) bool { return contexts[name] }); extra != "" {
		return fmt.Errorf("--outline is one line per heading, so there is nothing for %s to pad", extra)
	}
	if extra := Given(fs, func(name string) bool { return name == "at" }); extra != "" {
		return fmt.Errorf("--outline is one line per heading and %s names lines; ask for one", extra)
	}
	return nil
}

// selectors names the flags that say which nodes a search keeps: the kinds it
// accepts, and the checkbox state they have to be in.
var selectors = map[string]bool{
	"k": true, "kind": true,
	"task": true, "checked": true, "done": true, "unchecked": true, "todo": true,
}

// AtFlags refuses the filters an address cannot honour. --at takes its lines
// outright and runs no matcher over them, so a filter beside it would be read
// and then decide nothing -- which reads as if it had narrowed the answer when
// the answer was named in full. A pattern beside an address is the one thing
// that does still apply, and it applies as a guard rather than as a search.
func AtFlags(fs *flag.FlagSet) error {
	if Given(fs, func(name string) bool { return name == "at" }) == "" {
		return nil
	}
	if extra := Given(fs, func(name string) bool { return selectors[name] }); extra != "" {
		return fmt.Errorf("--at names its lines outright, so there is nothing for %s to filter", extra)
	}
	return nil
}

// streamIgnores names the flags that describe a page a stream does not print:
// how a result is decorated, and the shapes that stand a tally or a file name
// where a result would have gone. A stream is a list of regions, so each of
// these would be read, understood, and then change nothing the next stage sees.
var streamIgnores = map[string]bool{
	"B": true, "before": true, "A": true, "after": true, "C": true, "context": true,
	"span": true, "no-span": true,
	"truncate": true, "breadcrumb": true, "no-breadcrumb": true,
	"heading": true, "no-heading": true, "no-filename": true,
	"H": true, "with-filename": true,
	"separator": true, "color": true,
	"n": true, "line-number": true, "N": true, "no-line-number": true,
	"c": true, "count": true, "l": true, "files-with-matches": true,
	"q": true, "quiet": true,
}

// streamEdits names every flag that writes. A stream is one stage of a
// pipeline handing its nodes to the next, and a file rewritten halfway along
// one is a search whose later stages read something nobody asked for.
var streamEdits = map[string]bool{
	"check": true, "uncheck": true, "toggle": true, "delete": true,
	"replace": true, "replace-from": true,
	"replace-node": true, "replace-node-from": true,
	"set-text": true, "set-text-from": true,
	"append": true, "append-from": true, "prepend": true, "prepend-from": true,
	"multi": true, "expect": true, "write": true, "W": true, "apply": true,
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

// MachineFlags refuses the flags that describe a printed page when the output
// is one record per result. --format json and --format compact both carry the
// region and the lines that matched inside it as numbers, so there is no page
// for -A, -B or -C to pad and no note for --span to write or withhold. It is
// the rule --stream and --outline already follow, applied to the two formats
// that were the hole in it.
func MachineFlags(fs *flag.FlagSet) error {
	if named := Given(fs, func(name string) bool {
		return contexts[name] || name == "span" || name == "no-span"
	}); named != "" {
		return fmt.Errorf("a machine format is one record per result, so there is nothing for %s to say about it", named)
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
		named = append(named, dashed(f.Name))
	})
	return strings.Join(named, ", ")
}

// dashed spells a flag the way the caller would have typed it.
func dashed(name string) string {
	if len(name) == 1 {
		return "-" + name
	}
	return "--" + name
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
		return 0, fmt.Errorf("unknown format %q: plain, compact, json, stream, diff or doc", spec.val)
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
	"diff": render.Diff, "doc": render.Doc,
}

// EditFormat refuses the two formats that report an edit where there is no
// edit to report. Both name what an edit produced -- the patch it would
// apply, the document it would write -- so neither has anything to say about
// a search, and saying so beats printing an empty patch or the file entire.
func EditFormat(format render.Format, editing bool) error {
	if editing {
		return nil
	}
	switch format {
	case render.Diff:
		return errors.New("--format diff is the patch an edit would apply; there is no edit here")
	case render.Doc:
		return errors.New("--format doc is the document an edit produced; there is no edit here")
	}
	return nil
}

// pageLayout names the flags that say how results are laid out on a page.
// None of them has anything to say about a patch, which numbers its own
// lines, or about a document, which is the file exactly as the edit left it.
var pageLayout = map[string]bool{
	"span": true, "no-span": true,
	"n": true, "line-number": true, "N": true, "no-line-number": true,
	"H": true, "with-filename": true, "no-filename": true,
	"heading": true, "no-heading": true,
	"breadcrumb": true, "no-breadcrumb": true,
	"separator": true,
}

// PageFlags refuses those flags beside the two formats that print no page. It
// is the rule --outline and the machine formats already follow: a flag that
// would be read and then change nothing is refused by name rather than
// quietly dropped.
func PageFlags(fs *flag.FlagSet, format render.Format) error {
	which := ""
	switch format {
	case render.Diff:
		which = "--format diff numbers its own lines"
	case render.Doc:
		which = "--format doc is the file itself"
	default:
		return nil
	}
	if named := Given(fs, func(name string) bool { return pageLayout[name] }); named != "" {
		return fmt.Errorf("%s, so there is nothing for %s to lay out", which, named)
	}
	return nil
}

// OneDocument refuses --format doc where a run has more than one document to
// print. Two documents run together are not a document: nothing in the format
// says where one ends, so the output would be neither writable nor readable
// as the file it claims to be.
func OneDocument(format render.Format, n int) error {
	if format != render.Doc || n == 1 {
		return nil
	}
	return fmt.Errorf("--format doc prints the document an edit produced, and %d files are not one document", n)
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

func useColor(when string, tty bool) bool {
	switch when {
	case "always":
		return true
	case "never":
		return false
	}
	if os.Getenv("NO_COLOR") != "" || os.Getenv("TERM") == "dumb" {
		return false
	}
	return tty
}

// IsTTY reports whether the output is going to a terminal, which is what
// stands behind every default that differs between a person reading a page
// and a program reading a stream.
func IsTTY() bool {
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

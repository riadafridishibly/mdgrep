// Package help holds the manual and the rules for reading one part of it, so
// that a topic and the full text can never say different things.
package help

import (
	"fmt"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Hint stands in for the whole of Usage on the error paths. A mistyped flag is
// most of a screen of help the caller did not ask for, and it buries the one
// line that says what went wrong.
const Hint = "try 'mdgrep --help'"

const Usage = `mdgrep — node-aware grep for markdown

usage: mdgrep [OPTIONS] PATTERN [PATH...]

A hit prints the lines that matched, the way grep does, and says what node
they sit in and what it would cost to see it whole — the bullet, the row, the
section. Asking for one of those prints it. PATTERN is a regular expression by
default, and an empty one matches everything. With no PATH, mdgrep reads
stdin when it is a pipe and searches the current directory otherwise.

Matching
  -e, --regexp PATTERN  use PATTERN as the pattern; repeat for alternatives
  -F, --fixed-strings   match PATTERN literally
      --fuzzy           fuzzy match: every token of PATTERN must appear,
                        loosely and in order. Results come back best first
      --min-score N     fuzzy score threshold, 0..1 (default 0.7)
      --anchor          PATTERN is a heading link anchor: "#the-foo-bar",
                        "the-foo-bar" or "docs/x.md#the-foo-bar"
      --anchor-style LIST
                        github (gh), gitlab (gl), python (mkdocs, pymd),
                        kramdown (jekyll), pandoc, loose (any); default all
  -w, --word-regexp     match only whole words
  -v, --invert-match    select the nodes that do not match
  -i, --ignore-case     force case-insensitive
  -s, --case-sensitive  force case-sensitive
  -S, --smart-case      fold until PATTERN has an upper-case letter (default)

Nodes match against the markdown as written, so "^## " finds every
second-level heading and -F "**bold**" finds the emphasis markers.

Filters
  -k, --kind LIST       heading (h, head), item (bullet, li), list,
                        paragraph (para, p), code, quote, table, row, cell,
                        html, frontmatter (fm)
      --task            only task list items ("- [ ]" and "- [x]")
      --unchecked       only unticked task items (alias --todo)
      --checked         only ticked task items (alias --done)

A filter never stands in for the pattern: pass an empty one to select by
filter alone, as in "mdgrep '' docs --todo".

Selection
      --expand [N]      climb the expand ladder from the matched node: bare,
                        the node itself; N, that many rungs up it
      --section         widen to the enclosing heading section
      --section-body    that section without its heading line
      --siblings N      include N sibling blocks each side
      --at N-M          take lines N to M of one file outright, 1-based and
                        inclusive, as a span note writes them; repeatable
  -B, --before N        print N lines before each matching line
  -A, --after N         print N lines after
  -C, --context N       shorthand for -B N -A N

A result prints the lines that matched. Asking for a widener asks to see the
region whole, which is the only switch between line output and node output.
-B, -A and -C add no lines to that region: they pad the page, are counted in
the file and clipped to it the way grep counts them, and carry the "-" marker.
Each of the three writes the sides it names, so the last one typed wins and
"-C 3 -B 1" is one line before and three after. Two windows reaching the same
line are one group of file lines rather than two copies of it, and "--" stands
only between groups that are not next to each other.

The expand ladder climbs block ancestors and, where those run out, carries on
up the heading hierarchy, since headings parse as flat siblings and climbing
parents never reaches one. Depth varies with structure -- paragraph to section
is one rung, item to list to section is two -- so --expand N is for a bit more
context and --section is for the section.

--at takes no PATTERN: every positional beside it is a path, and it names lines
of one file, so a run where more than one could answer is refused. A pattern
given with -e beside it is a guard rather than a search -- the address says
which lines to take, the pattern says what they should still say, and the run
is refused if it is not there. Bounds are checked against the file rather than
clipped to it. It belongs on the stage that names the files, so a later --then
stage does not take one. --anchor and --outline are refused beside it, as are
--expect and --multi, which state how many nodes a search should have found,
and -k and the checkbox filters, which have nothing left to narrow.

Editing
      --check, --uncheck, --toggle
                        set the state of the selected task item
      --set-text TEXT   change what the matched node says, keeping the markup
                        that makes it a heading, an item or a fenced block
      --replace TEXT    replace the selected region with TEXT
      --delete          remove the selected region
      --append TEXT     insert TEXT after the selected region
      --prepend TEXT    insert TEXT before it
      --replace-from FILE, --set-text-from FILE, --append-from FILE,
      --prepend-from FILE
                        the same four edits, TEXT read from a file ("-" is
                        stdin)
      --multi           edit every match; without it, more than one is an error
      --expect N        edit only if exactly N nodes matched, else fail
  -W, --write           write the edit to the file; without it the edit is
                        only shown

An edit shows the change and leaves the file alone; -W writes it. What an
edit rewrites is what the same flags would have printed. --check and
--set-text act on the matched node; --replace, --delete, --append and
--prepend act on the region --section and --expand widen it to. --siblings is
refused beside an edit, along with -A, -B and -C: it keeps the blocks either
side of a match on the page, and rewriting them is not what asking to see them
meant. Each file is written in one atomic go.

An edit reports each line it removed behind "-" and each it added behind "+",
numbered where it sits in its own version of the file, and a node already as
asked behind "="; after the mark, the line is written as a search writes one.
The lines are the same whether or not -W was given; --format compact and
--format json say which by calling a change "preview" or "applied".

What an edit prints does not depend on where its document came from: a piped
document, one named file and a tree of them all report the same way. Two
formats print something else instead. --format diff is a unified patch, which
patch and git apply read, for any number of files. --format doc is the
document the edit produced, which is what -W would have written -- so it wants
exactly one document and refuses a run with more, since two documents run
together are not a document. An edit on stdin is the filter shape:

  $ cat notes.md | mdgrep "old text" --replace "new text" --format doc > out.md

-W has nowhere to write a piped document and says so. A search that matched
nothing prints the document unchanged under --format doc, so a miss does not
empty the file a run was redirected into; a refused edit prints nothing.

Plans
      --apply FILE      carry out a plan of edits read from FILE ("-" is
                        stdin): one JSON object per line

  $ cat plan.jsonl
  {"path":"notes.md","match":"ship the docs","op":"check"}
  {"path":"notes.md","match":"^## Setup","op":"set-text","text":"Install"}
  $ mdgrep --apply plan.jsonl

An entry takes "path", "op" and one of "match" or "at", plus "text" for the
edits that write one. "at" is an address in --at's syntax, and "match" beside
it is that flag's guard. "kind", "fixed", "expand", "section", "section-body",
"expect" and "multi" mean per entry what the flags of those names mean here,
and the last two are refused beside "at" as their flags are, as is "kind". A
plan carries its own search, so it takes no PATTERN and no PATH. Entries are
planned against the files as read, so none can match what another writes, and
the plan applies whole or not at all.

Pipelines
      --then            narrow what the search before it selected: everything
                        after this word is another search, and the last one is
                        the one that prints or writes
      --exec PIPELINE   the same, written as one string: words as a shell
                        reads them, and a bare "|" between two stages
      --stream          hand one region per result to the next mdgrep rather
                        than printing them; same as --format stream

  $ mdgrep "^## Release" --section docs --then -k list --then --todo --check
  $ mdgrep --exec '"^## Release" --section | -k list | --todo --check' docs
  $ mdgrep "^## Release" --section docs --stream | mdgrep "" --todo --check

A stage is a whole mdgrep line and takes the Matching, Filters and Selection
flags. Only the first names files, so a word on a later stage is its pattern;
only the last prints or writes, and takes the Output and Editing flags. A flag
that would change nothing where it stands is refused by name. --then and
--exec are read before the flags; a bare -- ends them, so a file named --then
stays a path.

--exec splits like a shell -- single quotes literal, double quotes literal but
for a backslash before a quote or a backslash, "" a word, a trailing backslash
joining two lines -- and only a bare "|" separates, so "^(alpha|beta)" is one
word. Only paths may stand beside it; they go to the first stage.

--stream sends the regions a stage selected, not its text: a header line, then
one JSON object per result with "path", "start" and "end", 1-based inclusive.
The next stage reopens the file, so line numbers, breadcrumbs and paths
survive and the last stage can edit. Markdown on stdin is still markdown --
the header tells the two apart -- and --stream over stdin is refused, having
no file to name. A stage reading a stream takes no PATH ("-" spells "read
stdin") and no --ext, --hidden or --no-ignore; a stream takes no edit and none
of the flags that decorate a page.

Narrowing is by containment, judged on the node a stage selects: one the
region holds whole is a candidate, one straddling it is not, and a climb
(--todo to the task a sub-bullet hangs under, --expand up the tree) selects
nothing where it would leave the region. --section widens the other way and
may reach past it.

The three spellings answer alike. --then holds every stage in one process and
says which narrowed to nothing; a stream can be saved and replayed.

Output
  -n, --line-number     number the printed lines
  -N, --no-line-number  do not
  -H, --with-filename   print the file a result came from
      --no-filename     do not
      --heading         put that name above a file's results rather than in
                        front of every line of them
      --no-heading      the other way: "path:line:text" on every line
      --breadcrumb      print the heading trail above each result
      --no-breadcrumb   do not
      --outline         one indented line per heading; takes paths, no
                        PATTERN, and none of the Selection flags
      --separator STR   what to print between two groups of lines that are
                        not next to each other in the file; "--" by default,
                        which is grep's, and "--separator ''" leaves none
      --span            print the expand ladder after each result (default)
      --no-span         do not
      --truncate N      cap node output at N lines, keeping the matched
                        line, then a count of what was held back. Node
                        output is a region a widener asked for whole and a
                        node a node matcher claimed whole; the lines a line
                        matcher pointed at are the answer and are never
                        capped. Results that touch stay apart under it,
                        since a cap spent on the first of them would drop
                        the rest
      --color WHEN      auto, always or never (default auto)
      --format WHEN     plain (default), compact, json, stream, diff or doc.
                        diff and doc report an edit: the patch it would
                        apply, and the document it produced
      --json            one JSON object per result (same as --format json)
  -c, --count           print only the number of results per file
  -l, --files-with-matches
                        print only the names of files with results
  -m, --max-count N     stop after N results per file
  -q, --quiet           print nothing; the exit status carries the answer
      --ext LIST        file extensions to search
                        (default md,markdown,mdown,mkd,mdx)
      --hidden          descend into hidden directories
      --no-ignore       search past .gitignore, .ignore, .git/info/exclude
                        and the skip list (node_modules, vendor and friends)
  -h, --help [TOPIC]    the whole manual, or one part of it: matching,
                        filters, selection, editing, plans, pipelines,
                        output. A flag name works too, as in
                        "mdgrep --help anchor"
  -V, --version         print the version and exit

A line is written the way grep and rg write one -- "path:line:text", each
part there only when it has something to say. Three defaults come from where
the output is going and how much of the tree was searched, and every one of
them yields to the flag that answers it. The file name is printed when more
than one file could have answered: a directory counts as more than one however
few markdown documents it holds, and a single file named outright, or markdown
on stdin, answers for itself. Lines are numbered, and the name goes above a
file's results rather than in front of each line, when stdout is a terminal.
So a terminal shows a heading, the trail and numbers, and a pipe gets the
markdown alone unless it asks for more.

A line that matched takes ":" and a line -A, -B or -C pulled in takes "-",
which is grep's convention and rg's. A matcher that cannot name a line claims
every line of the node instead: -v selected the node by what it does not hold,
and the empty pattern behind --todo, -k list or --outline selected it by a
filter, so no line in it is more the answer than another. That is what keeps
"mdgrep '' --todo" printing a task item's sub-bullets, and why printing the
whole node needs no flag of its own.

Every result ends with the spans it could be widened to, one rung of the
expand ladder per entry from the matched node up to its enclosing section:

  (item 693-715, list 509-722, section 507-724)

It is a cost table rather than a pointer: 23 lines, 214, 218, which is what
says the middle rung costs everything the section costs and gives less. --at
takes an entry of it back. A rung the printed lines already cover is left out,
having nothing left to give -- printing a heading whole leaves "(section
46-48)" and nothing about the heading -- and the note goes altogether when the
page covers every rung. That is why the plain note is no longer counted for
--expand; --format compact and --format json carry the ladder whole and in
order, so the first entry is bare --expand, the second --expand 1, and the last
what --section selects. The heading trail has no counterpart in grep,
so a pipe gets none until --breadcrumb asks; it goes wherever a heading goes,
since a heading is what says a person is reading, and --no-breadcrumb leaves
it out. Having nowhere to stand but above a file's results, it is refused
beside --no-heading. What --truncate held back a page does not count out. The
span note already names the node and says where it runs, which is what a
reader does next and what --at takes back, and a count measured over a region
no rung names cannot be placed at all: --section-body runs to the end of the
section, so a page ending at the paragraph's last line would report lines
nothing on the page points at. The machine formats count it out no more than
a page does: the span stays the node's, the text is the window, and spans
names the region to ask for.

compact is one tab-separated record per result under the path — "start[-end]
kind text hits spans", newlines escaped — for a fraction of what json costs.
hits are the lines that matched, comma-separated and empty for a node matcher,
and spans is the ladder as "kind:start-end" in ladder order. json carries the
same two as "hits" and "spans", and adds the breadcrumb and the score. Both keep touching
nodes apart where plain runs them into one passage -- as --truncate does too --
and both report a refusal in their own shape. Neither prints a page, so -A, -B,
-C and --span are refused beside them the way --stream and --outline refuse
them. Colour is off when stdout is not a terminal, NO_COLOR is set or
TERM=dumb.

A short flag takes its value attached or apart: -C2 and -C 2 are the same.
Everything after -- is a PATTERN or a PATH, dashes and all.
Exit status is 0 when something matched, 1 when nothing did, 2 on error.
`

// Text answers --help, either in full or one titled part of it. Splitting the
// manual rather than keeping a second copy of it means a topic cannot drift
// from what the full text says.
func Text(topic string) (string, error) {
	if topic == "" {
		return Usage, nil
	}
	secs := sections()
	sec, err := pickSection(secs, topic)
	if err != nil {
		return "", err
	}
	return usageLine() + "\n\n" + sec.body, nil
}

// usageLine is the one line of the manual that says how to invoke the command.
// A topic repeats it so a narrowed help still stands on its own.
func usageLine() string {
	for line := range strings.SplitSeq(Usage, "\n") {
		if strings.HasPrefix(line, "usage:") {
			return line
		}
	}
	return ""
}

type section struct {
	title string
	body  string
}

// sections splits the manual at its titles. A title is a bare word alone on
// a line; everything above the first one introduces the command rather than any
// one part of it, so it is not a topic.
func sections() []section {
	var out []section
	for _, line := range strings.SplitAfter(Usage, "\n") {
		if title := strings.TrimRight(line, "\n"); isTitle(title) {
			out = append(out, section{title: title, body: line})
			continue
		}
		if len(out) > 0 {
			out[len(out)-1].body += line
		}
	}
	return out
}

func isTitle(line string) bool {
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
func pickSection(secs []section, topic string) (section, error) {
	want := strings.ToLower(strings.TrimLeft(topic, "-"))
	var hits []section
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
		return section{}, fmt.Errorf("no help topic %q; try %s", topic, topics(secs))
	}
	return section{}, fmt.Errorf("%q matches %s", topic, topics(hits))
}

// definesFlag reports whether a section documents --name, as opposed to merely
// mentioning it in passing: a definition stands in the flag column, and the
// prose that describes one flag is free to name others. A flag whose entry
// ends "(alias --todo)" defines that spelling too, since the alias is the only
// place it is written down and looking it up is the reason anyone types it.
func definesFlag(body, name string) bool {
	for line := range strings.SplitSeq(body, "\n") {
		if !strings.HasPrefix(line, "  ") {
			continue
		}
		head, rest, _ := strings.Cut(strings.TrimSpace(line), "  ")
		for _, spelling := range strings.Split(head, ",") {
			flag, _, _ := strings.Cut(strings.TrimSpace(spelling), " ")
			if flag == "--"+name {
				return true
			}
		}
		if slices.Contains(aliases(rest), "--"+name) {
			return true
		}
	}
	return false
}

// aliases reads the spellings an entry names as its own, written "(alias --x)"
// or "(aliases --x, --y)" at the end of the description.
func aliases(desc string) []string {
	_, rest, ok := strings.Cut(desc, "(alias")
	if !ok {
		return nil
	}
	list, _, ok := strings.Cut(strings.TrimPrefix(rest, "es"), ")")
	if !ok {
		return nil
	}
	var out []string
	for _, s := range strings.Split(list, ",") {
		if s = strings.TrimSpace(s); strings.HasPrefix(s, "--") {
			out = append(out, s)
		}
	}
	return out
}

func topics(secs []section) string {
	names := make([]string, len(secs))
	for i, s := range secs {
		names[i] = strings.ToLower(s.title)
	}
	return strings.Join(names, ", ")
}

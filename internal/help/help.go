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

Prints the lines that matched and names the node they sit in -- the bullet,
the row, the section -- with the span each one covers, so the next run can ask
for one whole. PATTERN is a regular expression, and an empty one matches
everything. With no PATH, mdgrep reads stdin when it is a pipe and searches the
current directory otherwise.

Matching
  -e, --regexp PATTERN  use PATTERN as the pattern; repeat for alternatives
  -F, --fixed-strings   match PATTERN literally
      --fuzzy           fuzzy match, loosely and in order, best first
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

Patterns match the markdown as written, so "^## " finds every second-level
heading and -F "**bold**" finds the emphasis markers.

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
      --expand [N]      widen to the matched node; N, that many rungs up the
                        expand ladder towards its section
      --section         widen to the enclosing heading section
      --section-body    that section without its heading line
      --siblings N      include N sibling blocks each side
      --at N-M          take lines N to M of one file outright, 1-based and
                        inclusive, as the span note writes them; repeatable,
                        and takes no PATTERN
  -B, --before N        print N lines before each matching line
  -A, --after N         print N lines after
  -C, --context N       shorthand for -B N -A N

A widener is the switch from line output to node output. -B, -A and -C widen
no region: they pad the page, counted and clipped the way grep counts them.

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

--check and --set-text act on the matched node; --replace, --delete, --append
and --prepend act on the region a widener selected. An edit prints the lines
it would remove behind "-" and add behind "+"; --format diff prints a patch
instead, and --format doc the whole document it produced.

Plans
      --apply FILE      carry out a plan of edits read from FILE ("-" is
                        stdin): one JSON object per line, each naming "path",
                        "op" and either "match" or "at", plus "text" for the
                        ops that write one. A plan takes no PATTERN and no
                        PATH, and applies whole or not at all

  {"path":"notes.md","match":"ship the docs","op":"check"}
  {"path":"notes.md","match":"^## Setup","op":"set-text","text":"Install"}

Pipelines
      --then            search again over what the stage before it selected;
                        the last stage is the one that prints or writes
      --exec PIPELINE   the same as one string: words as a shell reads them,
                        stages split on a bare "|"
      --stream          hand one region per result to the next mdgrep as JSON
                        rather than printing them; same as --format stream

Only the first stage names files; only the last takes the Output and Editing
flags. A flag that would change nothing where it stands is refused by name.

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
                        not next to each other in the file (default "--")
      --span            print the expand ladder after each result (default)
      --no-span         do not
      --truncate N      cap node output at N lines, keeping the matched one
      --color WHEN      auto, always or never (default auto)
      --format WHEN     plain (default), compact, json, stream, diff or doc
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
                        output, examples. A flag name works too, as in
                        "mdgrep --help anchor"
  -V, --version         print the version and exit

A line is written "path:line:text", each part there only when it has something
to say. The file name, the numbers and the trail follow the terminal: a tty
gets all three, a pipe gets the markdown alone, and every default yields to
the flag that answers it. A result ends with the spans it could be widened to,
which is a cost table --at takes an entry of back:

  (item 693-715, list 509-722, section 507-724)

Examples
  mdgrep "^## Release" --section docs
  mdgrep "" docs --todo
  mdgrep "#install" --anchor --section README.md
  mdgrep "old text" --replace "new text" notes.md -W
  mdgrep "^## Release" --section docs --then -k list --then --todo --check
  mdgrep --exec '"^## Release" --section | -k list | --todo --check' docs
  cat notes.md | mdgrep "old" --replace "new" --format doc > out.md

A short flag takes its value attached or apart: -C2 and -C 2 are the same.
Everything after -- is a PATTERN or a PATH. Exit status is 0 when something
matched, 1 when nothing did, 2 on error.
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

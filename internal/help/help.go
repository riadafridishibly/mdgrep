// Package help holds the manual and the rules for reading one part of it, so
// that a topic and the full text can never say different things.
package help

import (
	"fmt"
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

A hit prints the markdown node it landed in — the whole bullet, row or
paragraph — rather than the single line. PATTERN is a regular expression by
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
      --expand N        climb N ancestor levels from the matched node
      --section         widen to the enclosing heading section
      --section-body    that section without its heading line
  -B, --before N        include N sibling blocks before
  -A, --after N         include N sibling blocks after
  -C, --context N       shorthand for -B N -A N
      --lines N         pad the result with N raw lines on each side

-B, -A and -C count sibling blocks, not lines; use --lines for raw lines.

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
      --dry-run         show the edit, write nothing

An edit rewrites what the same flags would have printed: narrow the search to
one node, then say what to do with it. --check and --set-text act on the
matched node; --replace, --delete, --append and --prepend act on the region
--section and --expand widen it to. Every file is written in one atomic go.

Plans
      --apply FILE      carry out a plan of edits read from FILE ("-" is
                        stdin): one JSON object per line

  $ cat plan.jsonl
  {"path":"notes.md","match":"ship the docs","op":"check"}
  {"path":"notes.md","match":"^## Setup","op":"set-text","text":"Install"}
  $ mdgrep --apply plan.jsonl

An entry takes "path", "match" and "op", plus "text" for the edits that write
one. "kind", "fixed", "expand", "section", "section-body", "expect" and
"multi" say per entry what the flags of those names say here, and the plan
carries its own search, so it takes no PATTERN and no PATH. Every entry is
planned against the files as read, so none can match what another writes, and
the plan applies whole or not at all.

Output
  -n, --line-number     number the printed lines (the default)
  -N, --no-line-number  drop the line-number gutter
      --no-breadcrumb   hide the heading trail above each result
      --outline         one indented line per heading; takes paths, no
                        PATTERN, and none of the Selection flags
      --separator STR   what to print between two results of a file (default
                        "--"); pass "" to leave them out
      --truncate N      print at most N lines of a result, keeping the
                        matched node, then a count of what was held back
      --color WHEN      auto, always or never (default auto)
      --format WHEN     plain (default), compact or json
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
                        filters, selection, editing, plans, output. A flag
                        name works too, as in "mdgrep --help anchor"
  -V, --version         print the version and exit

compact is one tab-separated record per result — "start[-end] kind text
truncated", newlines escaped — under the path, for a fraction of what json
costs; json adds the breadcrumb and the score. Both keep two touching nodes
apart where plain runs them into one passage, and both report a refusal in
their own shape. Colour is off when stdout is not a terminal, NO_COLOR is set
or TERM=dumb.

A short flag takes its value attached or apart: -C2 and -C 2 are the same.
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

func topics(secs []section) string {
	names := make([]string, len(secs))
	for i, s := range secs {
		names[i] = strings.ToLower(s.title)
	}
	return strings.Join(names, ", ")
}

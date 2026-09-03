package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/riadafridishibly/mdgrep/internal/help"
)

// The conformance suite reads the manual as a contract: one case per sentence
// the manual makes true, named by the claim it stands for. A flag whose claim
// has no case here is reported by TestEveryDocumentedFlagIsClaimed, so the
// manual and the suite cannot drift apart silently.

// notesDoc is SPEC.md's worked example, kept byte-for-byte so the spans the
// specification quotes can be asserted verbatim.
const notesDoc = `# Notes

Intro paragraph about foo.

## Setup

Install foo, then run foo doctor.
Check the log.
Then foo again.

## Tasks

- [ ] rotate the foo key
      the old key is in the vault
- [ ] archive the logs
`

const richDoc = `---
title: Rich Fixture
tags: [alpha]
---

# Rich Doc

Some **bold** text and an Alpha word plus alpha lower.

<div class="raw">html block</div>

## Foo Bar & Baz!

Body under foo bar.

## Setup

- [ ] ship the docs
- [x] write the tests

Setext Heading
==============

Trailing paragraph with catdog and cat dog.
`

const tableDoc = `# Table Doc

| name | value |
| ---- | ----- |
| alpha | one |
| beta | two |

> quoted alpha line

` + "```go\nfunc alpha() {}\n```\n"

// claim is one assertion about one sentence of the manual.
type claim struct {
	// flags names every flag the case exercises, so the manual can be
	// checked for flags no case reaches.
	flags []string
	// says is the promise the manual makes, in its own words.
	says string
	args []string
	// stdin, when set, is piped in place of a file.
	stdin string
	// want and absent are regexps over stdout+stderr.
	want   []string
	absent []string
	// code is the exit status, when the claim is about one.
	code *int
	// then reads a fixture back after the run, for claims about -W.
	then func(t *testing.T, dir string)
}

func exit(n int) *int { return &n }

func conformanceCases() []claim {
	return []claim{
		// Matching
		{flags: []string{"PATTERN"}, says: "an empty PATTERN matches everything",
			args: []string{"", "rich.md"}, want: []string{`Rich Doc`}},
		{flags: []string{"-e", "--regexp"}, says: "-e uses PATTERN as the pattern",
			args: []string{"-e", "alpha lower", "rich.md"}, want: []string{`alpha lower`}},
		{flags: []string{"-e"}, says: "-e repeated offers alternatives",
			args: []string{"-e", "Rich Doc", "-e", "Setup", "rich.md"},
			want: []string{`Rich Doc`, `## Setup`}},
		{flags: []string{"-F", "--fixed-strings"}, says: "-F matches PATTERN literally",
			args: []string{"-F", "**bold**", "rich.md"}, want: []string{`\*\*bold\*\*`}},
		{flags: []string{"-F"}, says: "-F leaves regexp metacharacters inert",
			args: []string{"-F", "Ba.", "rich.md"}, code: exit(1)},
		{says: `patterns match the markdown as written, so "^## " finds every second-level heading`,
			args: []string{"^## ", "rich.md"}, want: []string{`## Setup`}, absent: []string{`^# Rich Doc`}},
		{flags: []string{"-w", "--word-regexp"}, says: "-w matches only whole words",
			args: []string{"-w", "cat", "words.md"}, want: []string{`a cat dog here`}},
		{flags: []string{"-w"}, says: "-w does not match inside a longer word",
			args: []string{"-w", "cat", "words.md"}, absent: []string{`catdog alone`}},
		{flags: []string{"-i", "--ignore-case"}, says: "-i forces case-insensitive",
			args: []string{"-i", "ALPHA", "rich.md"}, want: []string{`Alpha word`}},
		{flags: []string{"-s", "--case-sensitive"}, says: "-s forces case-sensitive",
			args: []string{"-s", "ALPHA", "rich.md"}, code: exit(1)},
		{flags: []string{"-S", "--smart-case"}, says: "-S folds until PATTERN has an upper-case letter",
			args: []string{"-S", "alpha", "rich.md"}, want: []string{`Alpha word`}},
		{flags: []string{"-S"}, says: "-S stops folding once PATTERN carries an upper-case letter",
			args: []string{"-S", "ALPHA", "rich.md"}, code: exit(1)},
		{says: "smart case is the default",
			args: []string{"alpha", "rich.md"}, want: []string{`Alpha word`}},
		{flags: []string{"-v", "--invert-match"}, says: "-v selects the nodes that do not match",
			args: []string{"-v", "Setup", "rich.md"}, want: []string{`Rich Doc`}, absent: []string{`^## Setup`}},
		{flags: []string{"--fuzzy"}, says: "--fuzzy matches loosely and in order",
			args: []string{"--fuzzy", "Setxt", "rich.md"}, want: []string{`Setext Heading`}},
		{flags: []string{"--min-score"}, says: "--min-score is a fuzzy score threshold",
			args: []string{"--fuzzy", "--min-score", "1.0", "SetxtHdng", "rich.md"}, code: exit(1)},

		// Anchors
		{flags: []string{"--anchor"}, says: `--anchor takes a bare slug`,
			args: []string{"--anchor", "foo-bar--baz", "rich.md"}, want: []string{`Foo Bar & Baz`}},
		{flags: []string{"--anchor"}, says: `--anchor takes "#the-foo-bar"`,
			args: []string{"--anchor", "#foo-bar--baz", "rich.md"}, want: []string{`Foo Bar & Baz`}},
		{flags: []string{"--anchor"}, says: `--anchor takes "docs/x.md#the-foo-bar"`,
			args: []string{"--anchor", "rich.md#foo-bar--baz", "rich.md"}, want: []string{`Foo Bar & Baz`}},
		{flags: []string{"--anchor-style"}, says: "--anchor-style takes github and its alias gh",
			args: []string{"--anchor", "--anchor-style", "gh", "foo-bar--baz", "rich.md"}, want: []string{`Foo Bar & Baz`}},
		{flags: []string{"--anchor-style"}, says: "--anchor-style loose matches any style",
			args: []string{"--anchor", "--anchor-style", "loose", "foo-bar-baz", "rich.md"}, want: []string{`Foo Bar & Baz`}},

		// Filters
		{flags: []string{"-k", "--kind"}, says: "-k heading, and its aliases h and head",
			args: []string{"", "rich.md", "-k", "h"}, want: []string{`# Rich Doc`}},
		{flags: []string{"-k"}, says: "-k item, and its aliases bullet and li",
			args: []string{"", "rich.md", "-k", "li"}, want: []string{`ship the docs`}},
		{flags: []string{"-k"}, says: "-k list",
			args: []string{"", "rich.md", "-k", "list"}, want: []string{`ship the docs`}},
		{flags: []string{"-k"}, says: "-k paragraph, and its aliases para and p",
			args: []string{"", "rich.md", "-k", "p"}, want: []string{`bold`}},
		{flags: []string{"-k"}, says: "-k code",
			args: []string{"", "table.md", "-k", "code"}, want: []string{`func alpha`}},
		{flags: []string{"-k"}, says: "-k quote",
			args: []string{"", "table.md", "-k", "quote"}, want: []string{`quoted alpha`}},
		{flags: []string{"-k"}, says: "-k table",
			args: []string{"", "table.md", "-k", "table"}, want: []string{`name . value`}},
		{flags: []string{"-k"}, says: "-k row",
			args: []string{"", "table.md", "-k", "row"}, want: []string{`alpha . one`}},
		{flags: []string{"-k"}, says: "-k cell",
			args: []string{"", "table.md", "-k", "cell"}, want: []string{`alpha`}},
		{flags: []string{"-k"}, says: "-k html",
			args: []string{"", "rich.md", "-k", "html"}, want: []string{`div class`}},
		{flags: []string{"-k"}, says: "-k frontmatter, and its alias fm",
			args: []string{"", "rich.md", "-k", "fm"}, want: []string{`title: Rich Fixture`}},
		{flags: []string{"-k"}, says: "-k takes a LIST",
			args: []string{"", "rich.md", "-k", "heading,code"}, want: []string{`# Rich Doc`}},
		{flags: []string{"--task"}, says: "--task selects only task list items",
			args: []string{"", "table.md", "--task"}, code: exit(1)},
		{flags: []string{"--task"}, says: "--task selects task list items",
			args: []string{"", "rich.md", "--task"}, want: []string{`ship the docs`, `write the tests`}},
		{flags: []string{"--unchecked", "--todo"}, says: "--unchecked selects only unticked task items",
			args: []string{"", "rich.md", "--unchecked"}, want: []string{`ship the docs`}, absent: []string{`write the tests`}},
		{flags: []string{"--todo"}, says: "--todo is an alias for --unchecked",
			args: []string{"", "rich.md", "--todo"}, want: []string{`ship the docs`}, absent: []string{`write the tests`}},
		{flags: []string{"--checked", "--done"}, says: "--checked selects only ticked task items",
			args: []string{"", "rich.md", "--checked"}, want: []string{`write the tests`}, absent: []string{`ship the docs`}},
		{flags: []string{"--done"}, says: "--done is an alias for --checked",
			args: []string{"", "rich.md", "--done"}, want: []string{`write the tests`}},
		{says: `a filter never stands in for the pattern: an empty one selects by filter alone`,
			args: []string{"", "rich.md", "--todo"}, want: []string{`ship the docs`}},

		// Selection
		{flags: []string{"--expand"}, says: "bare --expand is the node that matched",
			args: []string{"vault", "notes.md", "--expand"}, want: []string{`rotate the foo key`}},
		{flags: []string{"--expand"}, says: "--expand N climbs N rungs of the ladder",
			args: []string{"vault", "notes.md", "--expand", "1"}, want: []string{`archive the logs`}},
		{flags: []string{"--expand"}, says: "--expand climbs into the heading hierarchy",
			args: []string{"vault", "notes.md", "--expand", "2"}, want: []string{`## Tasks`}},
		{flags: []string{"--expand"}, says: "--expand saturates at the document",
			args: []string{"vault", "notes.md", "--expand", "9"}, want: []string{`# Notes`, `## Setup`}},
		{flags: []string{"--section"}, says: "--section widens to the enclosing heading section",
			args: []string{"vault", "notes.md", "--section"}, want: []string{`## Tasks`}},
		{flags: []string{"--section-body"}, says: "--section-body is that section without its heading line",
			args: []string{"vault", "notes.md", "--section-body"},
			want: []string{`archive the logs`}, absent: []string{`## Tasks`}},
		{flags: []string{"--siblings"}, says: "--siblings N includes N sibling blocks each side",
			args: []string{"Check the log", "notes.md", "--siblings", "1"}, want: []string{`Install foo`}},
		{flags: []string{"--at"}, says: "--at N-M takes lines N to M outright, 1-based and inclusive",
			args: []string{"--at", "7-9", "notes.md"},
			want: []string{`Install foo`, `Then foo again`}, absent: []string{`## Setup`}},
		{flags: []string{"--at"}, says: "--at is repeatable",
			args: []string{"--at", "1-1", "--at", "7-7", "notes.md"}, want: []string{`# Notes`, `Install foo`}},
		{flags: []string{"--at"}, says: "--at takes no PATTERN",
			args: []string{"foo", "--at", "7-9", "notes.md"}, code: exit(2)},

		// Line context
		{flags: []string{"-B", "--before"}, says: "-B N prints N lines before each matching line",
			args: []string{"Check the log", "notes.md", "-B", "1"}, want: []string{`Install foo`}},
		{flags: []string{"-A", "--after"}, says: "-A N prints N lines after",
			args: []string{"Check the log", "notes.md", "-A", "1"}, want: []string{`Then foo again`}},
		{flags: []string{"-C", "--context"}, says: "-C N is shorthand for -B N -A N",
			args: []string{"Check the log", "notes.md", "-C", "1"},
			want: []string{`Install foo`, `Then foo again`}},
		{says: "context lines are clipped to the file, not to the node",
			args: []string{"Install foo", "notes.md", "-B", "2"}, want: []string{`## Setup`}},
		{says: "a context line carries the dash prefix, a match line the colon",
			args: []string{"Check the log", "notes.md", "-B", "1", "-H", "-n", "--no-heading"},
			want: []string{`notes\.md-7-`, `notes\.md:8:`}},

		// The span note
		{says: "every result ends with the spans it could be widened to, in ladder order",
			args: []string{"vault", "notes.md"}, want: []string{`\(item 13-14, list 13-15, section 11-15\)`}},
		{says: "the ladder in the note stops at the first section",
			args: []string{"vault", "notes.md"}, absent: []string{`section 1-15`}},
		{says: "a rung the page already covers is left out",
			args: []string{"^## Tasks", "notes.md"}, want: []string{`\(section 11-15\)`}},
		{says: "the note disappears when the printed lines cover every rung",
			args: []string{"vault", "notes.md", "--section"}, absent: []string{`\(section`}},
		{flags: []string{"--span"}, says: "--span prints the expand ladder after each result, and is the default",
			args: []string{"vault", "notes.md", "--span"}, want: []string{`\(item 13-14`}},
		{flags: []string{"--no-span"}, says: "--no-span does not",
			args: []string{"vault", "notes.md", "--no-span"}, absent: []string{`\(item`}},

		// Separators
		{says: `"--" is printed between two groups of lines that are not adjacent`,
			args: []string{"foo", "notes.md"}, want: []string{`(?m)^--$`}},
		{flags: []string{"--separator"}, says: `--separator '' restores the empty separator`,
			args: []string{"foo", "notes.md", "--separator", ""}, absent: []string{`(?m)^--$`}},
		{flags: []string{"--separator"}, says: "--separator STR sets any other string",
			args: []string{"foo", "notes.md", "--separator", "=="}, want: []string{`(?m)^==$`}},

		// Editing
		{flags: []string{"--check"}, says: "--check sets the state of the selected task item",
			args: []string{"ship the docs", "rich.md", "--check"}, want: []string{`(?m)^\+ - \[x\] ship the docs`}},
		{flags: []string{"--uncheck"}, says: "--uncheck sets the state of the selected task item",
			args: []string{"write the tests", "rich.md", "--uncheck"}, want: []string{`(?m)^\+ - \[ \] write the tests`}},
		{flags: []string{"--toggle"}, says: "--toggle flips an unticked item",
			args: []string{"ship the docs", "rich.md", "--toggle"}, want: []string{`(?m)^\+ - \[x\] ship the docs`}},
		{flags: []string{"--toggle"}, says: "--toggle flips a ticked item",
			args: []string{"write the tests", "rich.md", "--toggle"}, want: []string{`(?m)^\+ - \[ \] write the tests`}},
		{says: `an edit prints the lines it would remove behind "-" and add behind "+"`,
			args: []string{"ship the docs", "rich.md", "--check"},
			want: []string{`(?m)^- - \[ \] ship the docs`, `(?m)^\+ - \[x\] ship the docs`}},
		{says: "without -W the edit is only shown",
			args: []string{"ship the docs", "rich.md", "--set-text", "ZZZ"},
			then: func(t *testing.T, dir string) { mustNotContain(t, filepath.Join(dir, "rich.md"), "ZZZ") }},
		{flags: []string{"-W", "--write"}, says: "-W writes the edit to the file",
			args: []string{"ship the docs", "rich.md", "--check", "-W"},
			then: func(t *testing.T, dir string) { mustContain(t, filepath.Join(dir, "rich.md"), "- [x] ship the docs") }},
		{flags: []string{"--set-text"}, says: "--set-text keeps the markup that makes it a heading",
			args: []string{"^## Setup", "rich.md", "--set-text", "Install"}, want: []string{`(?m)^\+ ## Install`}},
		{flags: []string{"--set-text"}, says: "--set-text keeps the markup that makes it an item",
			args: []string{"ship the docs", "rich.md", "--set-text", "renamed"}, want: []string{`(?m)^\+ - \[ \] renamed`}},
		{flags: []string{"--replace"}, says: "--replace rewrites the text the pattern matched, leaving the rest of the line alone",
			args: []string{"foo", "notes.md", "--replace", "bar", "--multi"}, want: []string{`Install bar, then`}},
		{flags: []string{"--replace"}, says: `--replace expands "$1" and friends`,
			args: []string{"(foo)", "notes.md", "--replace", "FOO-$1", "--multi"}, want: []string{`FOO-foo`}},
		{flags: []string{"--replace"}, says: "--replace is refused beside -v",
			args: []string{"foo", "notes.md", "-v", "--replace", "bar", "--multi"}, code: exit(2)},
		{flags: []string{"--replace"}, says: "--replace is refused beside --fuzzy",
			args: []string{"--fuzzy", "foo", "notes.md", "--replace", "bar", "--multi"}, code: exit(2)},
		{flags: []string{"--replace"}, says: "--replace is refused beside --anchor",
			args: []string{"--anchor", "setup", "notes.md", "--replace", "bar", "--multi"}, code: exit(2)},
		{flags: []string{"--replace"}, says: "--replace is refused beside --at",
			args: []string{"--at", "7-9", "notes.md", "--replace", "bar", "--multi"}, code: exit(2)},
		{says: "a pipe written into a table cell leaves escaped",
			args: []string{"one", "table.md", "-k", "cell", "--replace", "a|b"}, want: []string{`a\\\|b`}},
		{says: "a line break written into a cell is refused",
			args: []string{"one", "table.md", "-k", "cell", "--replace", "a\nb"}, code: exit(2)},
		{says: "a line break written into a heading is refused",
			args: []string{"^## Setup", "rich.md", "--replace", "a\nb"}, code: exit(2)},
		{says: "only a row goes inside a table",
			args: []string{"one", "table.md", "-k", "cell", "--replace-node", "a|b"}, code: exit(2),
			want: []string{`only a row belongs inside a table`}},
		{says: "a row written into a table is taken",
			args: []string{"one", "table.md", "-k", "cell", "--replace-node", "| a | b |"},
			code: exit(0), want: []string{`\| a \| b \|`}},
		{says: "replacing a table whole is not held to a row",
			args: []string{"", "table.md", "-k", "table", "--replace-node", "a paragraph"},
			code: exit(0), want: []string{`a paragraph`}},
		{says: "no line that closes a fence goes inside a fenced block",
			args: []string{"--at", "11-11", "table.md", "--replace-node", "```"}, code: exit(2),
			want: []string{`would close the fenced block`}},
		{says: "text that is not a fence goes inside a fenced block",
			args: []string{"--at", "11-11", "table.md", "--replace-node", "func beta() {}"},
			code: exit(0), want: []string{`func beta`}},
		{says: "-k cell matches inside one cell, so a pattern spanning a pipe finds none",
			args: []string{"-F", "alpha | one", "table.md", "-k", "cell"}, code: exit(1)},
		{says: "-k row matches the line as written, pipes and all",
			args: []string{"-F", "alpha | one", "table.md", "-k", "row"}, code: exit(0)},
		{says: "-c counts cells under -k cell and rows under -k row",
			args: []string{"", "table.md", "-k", "cell", "-c"}, want: []string{`(?m)^6$`}},
		{says: "-k row counts the rows",
			args: []string{"", "table.md", "-k", "row", "-c"}, want: []string{`(?m)^3$`}},
		{says: "a cell result names the cell it matched",
			args: []string{"one", "table.md", "-k", "cell", "--format", "json"},
			want: []string{`"cell":\{"lo":\d+,"hi":\d+,"text":"one"\}`}},
		{says: "--set-text rewrites one cell and leaves the row a row",
			args: []string{"one", "table.md", "-k", "cell", "--set-text", "x|y"},
			want: []string{`(?m)^\+ \| alpha \| x\\\|y \|`}},
		{says: "several cells of one row are one change to that line",
			args: []string{"a", "cells.md", "-k", "cell", "--set-text", "z", "--multi", "-W"},
			then: func(t *testing.T, dir string) {
				mustContain(t, filepath.Join(dir, "cells.md"), "| z | z |")
			}},
		{says: "two cells of one row are told apart by the column they start at",
			args: []string{"a", "cells.md", "-k", "cell", "--set-text", "z"}, code: exit(2),
			want: []string{`at column \d+`}},
		{says: "--set-text on a row names the op that writes one",
			args: []string{"alpha", "table.md", "-k", "row", "--set-text", "z"}, code: exit(2),
			want: []string{`use --replace-node`}},
		{says: "a table's header and the line under it cannot be deleted out of it",
			args: []string{"--at", "3-3", "table.md", "--delete"}, code: exit(2),
			want: []string{`a table is built on its header`}},
		{says: "a body row can be deleted",
			args: []string{"--at", "5-5", "table.md", "--delete"}, code: exit(0)},
		{says: "the table itself can be deleted",
			args: []string{"", "table.md", "-k", "table", "--delete"}, code: exit(0)},
		{flags: []string{"--delete"}, says: "--delete removes the selected region",
			args: []string{"vault", "notes.md", "--delete"}, want: []string{`(?m)^- .*rotate the foo key`}},
		{says: "--delete acts on the region a widener selected",
			args: []string{"vault", "notes.md", "--section", "--delete"}, want: []string{`(?m)^- ## Tasks`}},
		{says: "with no widener --delete reaches no further than the node",
			args: []string{"vault", "notes.md", "--delete"}, absent: []string{`(?m)^- ## Tasks`}},
		{flags: []string{"--replace-node"}, says: "--replace-node replaces the whole selected region",
			args: []string{"vault", "notes.md", "--replace-node", "NEWTEXT"}, want: []string{`NEWTEXT`}},
		{flags: []string{"--append"}, says: "--append inserts TEXT after the selected region",
			args: []string{"vault", "notes.md", "--append", "APPENDED"}, want: []string{`APPENDED`}},
		{flags: []string{"--prepend"}, says: "--prepend inserts TEXT before it",
			args: []string{"vault", "notes.md", "--prepend", "PREPENDED"}, want: []string{`PREPENDED`}},
		{flags: []string{"--set-text-from"}, says: "--set-text-from reads TEXT from a file",
			args: []string{"ship the docs", "rich.md", "--set-text-from", "text.txt"}, want: []string{`FROMFILE`}},
		{flags: []string{"--set-text-from"}, says: `--set-text-from reads stdin for "-"`,
			args:  []string{"ship the docs", "rich.md", "--set-text-from", "-"},
			stdin: "FROMSTDIN\n", want: []string{`FROMSTDIN`}},
		{flags: []string{"--replace-from"}, says: "--replace-from reads TEXT from a file",
			args: []string{"foo", "notes.md", "--replace-from", "text.txt", "--multi"}, want: []string{`FROMFILE`}},
		{flags: []string{"--replace-node-from"}, says: "--replace-node-from reads TEXT from a file",
			args: []string{"vault", "notes.md", "--replace-node-from", "text.txt"}, want: []string{`FROMFILE`}},
		{flags: []string{"--append-from"}, says: "--append-from reads TEXT from a file",
			args: []string{"vault", "notes.md", "--append-from", "text.txt"}, want: []string{`FROMFILE`}},
		{flags: []string{"--prepend-from"}, says: "--prepend-from reads TEXT from a file",
			args: []string{"vault", "notes.md", "--prepend-from", "text.txt"}, want: []string{`FROMFILE`}},
		{flags: []string{"--multi"}, says: "without --multi, more than one match is an error",
			args: []string{"foo", "notes.md", "--set-text", "x"}, code: exit(2)},
		{flags: []string{"--multi"}, says: "--multi edits every match",
			args: []string{"foo", "notes.md", "--replace", "bar", "--multi"}, code: exit(0)},
		{flags: []string{"--expect"}, says: "--expect N edits only if exactly N nodes matched",
			args: []string{"vault", "notes.md", "--expect", "1", "--set-text", "x"}, code: exit(0)},
		{flags: []string{"--expect"}, says: "--expect N fails when the count differs",
			args: []string{"foo", "notes.md", "--expect", "1", "--set-text", "x", "--multi"}, code: exit(2)},

		// Plans
		{flags: []string{"--apply"}, says: `--apply carries out a plan read from FILE`,
			args: []string{"--apply", "plan.jsonl"}, want: []string{`\[x\] ship the docs`}},
		{flags: []string{"--apply"}, says: `--apply reads a plan from stdin for "-"`,
			args:  []string{"--apply", "-"},
			stdin: `{"path":"rich.md","match":"ship the docs","op":"check"}` + "\n",
			want:  []string{`\[x\] ship the docs`}},
		{says: `a plan entry names "path", "op" and "match", plus "text" for the ops that write one`,
			args:  []string{"--apply", "-"},
			stdin: `{"path":"rich.md","match":"^## Setup","op":"set-text","text":"Install"}` + "\n",
			want:  []string{`## Install`}},
		{says: `a plan entry may name "at" in place of "match"`,
			args:  []string{"--apply", "-"},
			stdin: `{"path":"notes.md","at":"7-9","op":"delete"}` + "\n",
			want:  []string{`Install foo`}},
		{says: "a plan takes no PATTERN and no PATH",
			args: []string{"--apply", "-", "foo"}, stdin: "{}\n", code: exit(2)},
		{says: "a plan applies whole or not at all",
			args: []string{"--apply", "-", "-W"},
			stdin: `{"path":"rich.md","match":"ship the docs","op":"check"}` + "\n" +
				`{"path":"rich.md","match":"NOSUCHMATCH","op":"check"}` + "\n",
			code: exit(2),
			then: func(t *testing.T, dir string) { mustNotContain(t, filepath.Join(dir, "rich.md"), "[x] ship the docs") }},

		// Pipelines
		{flags: []string{"--then"}, says: "--then searches again over what the stage before it selected",
			args: []string{"^## Setup", "rich.md", "--section", "--then", "", "--todo"},
			want: []string{`ship the docs`}},
		{says: "a later stage sees only what the stage before it selected",
			args: []string{"^## Setup", "rich.md", "--section", "--then", "Rich Doc"}, code: exit(1)},
		{flags: []string{"--exec"}, says: `--exec is the same as one string, stages split on a bare "|"`,
			args: []string{"--exec", "'^## Setup' rich.md --section | '' --todo"},
			want: []string{`ship the docs`}},
		{flags: []string{"--stream"}, says: "--stream hands one region per result to the next mdgrep as JSON",
			args: []string{"^## Setup", "rich.md", "--section", "--stream"}, want: []string{`"start":16`}},
		{says: "--stream is the same as --format stream",
			args: []string{"^## Setup", "rich.md", "--section", "--format", "stream"}, want: []string{`"start":16`}},
		{says: "the last stage is the one that prints or writes",
			args: []string{"^## Setup", "rich.md", "--section", "--then", "ship the docs", "--check"},
			want: []string{`(?m)^\+ - \[x\] ship the docs`}},
		{says: "only the first stage names files",
			args: []string{"^## Setup", "rich.md", "--section", "--then", "", "rich.md", "--todo"}, code: exit(2)},
		{says: "only the last stage takes the Output flags, and a flag that would change nothing is refused by name",
			args: []string{"^## Setup", "rich.md", "--section", "-n", "--then", "", "--todo"},
			code: exit(2), want: []string{`-n`}},

		// Output
		{flags: []string{"-n", "--line-number"}, says: "-n numbers the printed lines",
			args: []string{"Check the log", "notes.md", "-n", "-H", "--no-heading"}, want: []string{`:8:`}},
		{flags: []string{"-N", "--no-line-number"}, says: "-N does not",
			args: []string{"Check the log", "notes.md", "-N", "-H", "--no-heading"}, absent: []string{`:8:`}},
		{flags: []string{"-H", "--with-filename"}, says: "-H prints the file a result came from",
			args: []string{"Check the log", "notes.md", "-H", "--no-heading"}, want: []string{`notes\.md`}},
		{flags: []string{"--no-filename"}, says: "--no-filename does not",
			args: []string{"Check the log", "notes.md", "--no-filename"}, absent: []string{`notes\.md`}},
		{flags: []string{"--heading"}, says: "--heading puts the name above a file's results",
			args: []string{"Check the log", "notes.md", "-H", "--heading"}, want: []string{`(?m)^notes\.md$`}},
		{flags: []string{"--no-heading"}, says: `--no-heading puts "path:line:text" on every line`,
			args: []string{"Check the log", "notes.md", "-H", "-n", "--no-heading"}, want: []string{`notes\.md:8:`}},
		{flags: []string{"--breadcrumb"}, says: "--breadcrumb prints the heading trail above each result",
			args: []string{"Check the log", "notes.md", "--breadcrumb"}, want: []string{`Setup`}},
		{flags: []string{"--no-breadcrumb"}, says: "--no-breadcrumb does not",
			args: []string{"Check the log", "notes.md", "--no-breadcrumb"}, absent: []string{`Notes.*Setup`}},
		{flags: []string{"--outline"}, says: "--outline prints one indented line per heading",
			args: []string{"--outline", "notes.md"}, want: []string{`Notes`, `(?m)^\s+.*Setup`}},
		{flags: []string{"--outline"}, says: "--outline takes no PATTERN",
			args: []string{"--outline", "foo", "notes.md"}, code: exit(2)},
		{flags: []string{"--outline"}, says: "--outline takes none of the Selection flags",
			args: []string{"--outline", "--section", "notes.md"}, code: exit(2)},
		{flags: []string{"--truncate"}, says: "--truncate N caps node output at N lines, keeping the matched one",
			args: []string{"vault", "notes.md", "--section", "--truncate", "2"},
			want: []string{`the old key is in the vault`}, absent: []string{`## Tasks`}},
		{flags: []string{"--color"}, says: "--color never prints no escapes",
			args: []string{"Check the log", "notes.md", "--color", "never"}, absent: []string{"\x1b"}},
		{flags: []string{"--color"}, says: "--color always emits them",
			args: []string{"Check the log", "notes.md", "--color", "always"}, want: []string{"\x1b"}},
		{flags: []string{"--format", "--json"}, says: "--json is the same as --format json",
			args: []string{"vault", "notes.md", "--json"}, want: []string{`"path"`}},
		{flags: []string{"--format"}, says: "--format compact",
			args: []string{"vault", "notes.md", "--format", "compact"}, want: []string{`notes\.md`}},
		{flags: []string{"--format"}, says: "--format diff prints a patch",
			args: []string{"ship the docs", "rich.md", "--check", "--format", "diff"},
			want: []string{`(?m)^--- `, `(?m)^\+\+\+ `, `(?m)^@@`}},
		{flags: []string{"--format"}, says: "--format doc prints the whole document it produced",
			args: []string{"ship the docs", "rich.md", "--check", "--format", "doc"}, want: []string{`# Rich Doc`}},
		{flags: []string{"-c", "--count"}, says: "-c prints only the number of results per file",
			args: []string{"vault", "notes.md", "-c", "-H"}, want: []string{`(?m)^notes\.md:1$`}},
		{flags: []string{"-l", "--files-with-matches"}, says: "-l prints only the names of files with results",
			args: []string{"foo", "notes.md", "-l"}, want: []string{`notes\.md`}, absent: []string{`Install foo`}},
		{flags: []string{"-m", "--max-count"}, says: "-m stops after N results per file",
			args: []string{"foo", "notes.md", "-m", "1"}, want: []string{`Intro paragraph`}, absent: []string{`Install foo`}},
		{flags: []string{"-q", "--quiet"}, says: "-q prints nothing and the exit status carries the answer",
			args: []string{"foo", "notes.md", "-q"}, code: exit(0), absent: []string{`.`}},
		{flags: []string{"-q"}, says: "-q reports a miss in its exit status",
			args: []string{"zzzz", "notes.md", "-q"}, code: exit(1)},
		{flags: []string{"--ext"}, says: "--ext names the file extensions to search",
			args: []string{"foo", ".", "--ext", "md"}, want: []string{`Install foo`}},
		{flags: []string{"--ext"}, says: "--ext excludes what it does not name",
			args: []string{"foo", ".", "--ext", "markdown"}, code: exit(1)},
		{flags: []string{"--hidden"}, says: "--hidden descends into hidden directories",
			args: []string{"hiddenfoo", ".", "--hidden"}, want: []string{`hiddenfoo`}},
		{says: "without --hidden a hidden directory is skipped",
			args: []string{"hiddenfoo", "."}, code: exit(1)},
		{flags: []string{"--no-ignore"}, says: "--no-ignore searches past .gitignore",
			args: []string{"ignoredfoo", ".", "--no-ignore"}, want: []string{`ignoredfoo`}},
		{says: "without --no-ignore an ignored file is skipped",
			args: []string{"ignoredfoo", "."}, code: exit(1)},
		{flags: []string{"-h", "--help"}, says: "-h TOPIC prints one part of the manual",
			args: []string{"-h", "matching"}, want: []string{`fuzzy`}},
		{flags: []string{"--help"}, says: "a flag name works as a help topic too",
			args: []string{"--help", "anchor"}, want: []string{`anchor`}},
		{flags: []string{"-V", "--version"}, says: "-V prints the version and exits",
			args: []string{"-V"}, code: exit(0), want: []string{`mdgrep `}},

		// Exit status
		{says: "the exit status is 0 when something was found",
			args: []string{"foo", "notes.md"}, code: exit(0)},
		{says: "the exit status is 1 when nothing was found",
			args: []string{"zzzznope", "notes.md"}, code: exit(1)},
		{says: "the exit status is 2 when the arguments were wrong",
			args: []string{"--nosuchflag", "notes.md"}, code: exit(2)},
	}
}

func TestConformance(t *testing.T) {
	for _, c := range conformanceCases() {
		t.Run(c.says, func(t *testing.T) {
			dir := fixtures(t)
			defer inDir(t, dir)()
			if c.stdin != "" {
				defer withStdin(t, c.stdin)()
			}
			out, errOut, code := capture(t, c.args...)
			got := out + errOut
			if c.code != nil && code != *c.code {
				t.Errorf("claim %q: exit %d, want %d\n%s", c.says, code, *c.code, got)
			}
			for _, re := range c.want {
				if !regexp.MustCompile(re).MatchString(got) {
					t.Errorf("claim %q: output does not match %q\n%s", c.says, re, got)
				}
			}
			for _, re := range c.absent {
				if regexp.MustCompile(re).MatchString(got) {
					t.Errorf("claim %q: output should not match %q\n%s", c.says, re, got)
				}
			}
			if c.then != nil {
				c.then(t, dir)
			}
		})
	}
}

// TestEveryDocumentedFlagIsClaimed is the reason the suite is systematic: a
// flag the manual offers and no case exercises is a promise nothing checks.
func TestEveryDocumentedFlagIsClaimed(t *testing.T) {
	claimed := map[string]bool{}
	for _, c := range conformanceCases() {
		for _, f := range c.flags {
			claimed[f] = true
		}
		for _, a := range c.args {
			if strings.HasPrefix(a, "-") {
				claimed[a] = true
			}
		}
	}
	for _, flag := range documentedFlags() {
		if !claimed[flag] {
			t.Errorf("%s is documented but no conformance case exercises it", flag)
		}
	}
}

// documentedFlags reads the flag names out of the manual itself, so a flag
// added to help.Usage arrives here without anyone remembering to list it.
func documentedFlags() []string {
	re := regexp.MustCompile(`--[a-z][a-z-]+|(?:^|[ (])-[a-zA-Z](?:[,. ]|$)`)
	seen := map[string]bool{}
	var out []string
	for _, line := range strings.Split(help.Usage, "\n") {
		// Only the flag column: a flag named in prose is described there,
		// not offered there.
		if !strings.HasPrefix(line, "  -") && !strings.HasPrefix(line, "      -") {
			continue
		}
		for _, m := range re.FindAllString(line, -1) {
			f := strings.Trim(m, " ,.()")
			if f == "" || seen[f] {
				continue
			}
			seen[f] = true
			out = append(out, f)
		}
	}
	return out
}

// fixtures lays out the documents every case searches, including the hidden
// and ignored files the walk claims are skipped.
func fixtures(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	write := func(name, body string) {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("notes.md", notesDoc)
	write("rich.md", richDoc)
	write("table.md", tableDoc)
	write("cells.md", "# C\n\n| h | i |\n| - | - |\n| a | a |\n")
	write("words.md", "# Words\n\ncatdog alone.\n\na cat dog here.\n")
	write("text.txt", "FROMFILE\n")
	write("plan.jsonl", `{"path":"rich.md","match":"ship the docs","op":"check"}`+"\n")
	write(".gitignore", "ignored.md\n")
	write("ignored.md", "# Ignored\n\nignoredfoo lives here.\n")
	write(".hidden/h.md", "# Hidden\n\nhiddenfoo lives here.\n")
	return dir
}

func inDir(t *testing.T, dir string) func() {
	t.Helper()
	prev, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	return func() { os.Chdir(prev) }
}

func withStdin(t *testing.T, text string) func() {
	t.Helper()
	path := filepath.Join(t.TempDir(), "stdin")
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	prev := os.Stdin
	os.Stdin = f
	return func() { os.Stdin = prev; f.Close() }
}

func mustContain(t *testing.T, path, want string) {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), want) {
		t.Errorf("%s does not contain %q:\n%s", path, want, body)
	}
}

func mustNotContain(t *testing.T, path, unwanted string) {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), unwanted) {
		t.Errorf("%s contains %q:\n%s", path, unwanted, body)
	}
}

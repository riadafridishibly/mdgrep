# mdgrep

Node-aware grep for markdown.

`grep` gives you a line and stops there. Markdown is made of bullets,
headings, code fences, quotes and tables — so `mdgrep` gives you the line
*and* what it sits in, and printing that whole is one flag away.

```
$ mdgrep "brew install" notes.md
13:  - On macOS run `brew install foo`
(item 13-15, list 11-19, section 8-24)
```

Matched characters are highlighted. Lines are written the way `grep` and `rg`
write them — `path:line:text` for a match, `path-line-text` for a context line
— and the note that closes a result is a cost table: 3 lines, 9 lines, 17
lines, one rung of the expand ladder each. `--expand` takes the first,
`--expand 1` the second, `--section` the last, and `--at 11-19` takes any of
them back on the next command line. On a terminal the heading trail above a
result says where you are.

## Install

```bash
go install github.com/riadafridishibly/mdgrep@latest
```

Or from a clone:

```bash
go install .    # or: go build -o mdgrep .
```

Go 1.26+, one dependency ([goldmark](https://github.com/yuin/goldmark)).

## Usage

```
mdgrep [OPTIONS] PATTERN [PATH...]
```

`PATTERN` is a regexp by default and is required — an empty one matches
everything, so `mdgrep "" docs --todo` lists every open checkbox under `docs/`.

Paths may be files or directories. Directories are walked for `.md`,
`.markdown`, `.mdown`, `.mkd`, `.mdx`. With no path, mdgrep reads stdin when it
is a pipe, otherwise it searches the current directory.

### Matching

| Flag | Meaning |
| --- | --- |
| `-e`, `--regexp PATTERN` | use PATTERN as the pattern; repeat for alternatives |
| `-F`, `--fixed-strings` | match PATTERN literally |
| `--fuzzy` | fuzzy match |
| `--min-score N` | fuzzy threshold, 0..1 (default 0.7) |
| `--anchor` | PATTERN is a heading link anchor |
| `--anchor-style LIST` | anchor conventions to try (default all) |
| `-w`, `--word-regexp` | match only whole words |
| `-v`, `--invert-match` | select the nodes that do not match |
| `-i`, `--ignore-case` | force case-insensitive |
| `-s`, `--case-sensitive` | force case-sensitive |
| `-S`, `--smart-case` | case-insensitive until the pattern has an upper-case letter (default) |

Nodes match against the markdown **as written**, so structure is searchable and
`^`/`$` anchor to lines:

```bash
mdgrep '^## '            # every second-level heading
mdgrep '^\| .*canary'    # table rows mentioning the canary
mdgrep -F '**bold**'     # the emphasis markers themselves
```

### Fuzzy

`--fuzzy` wants every whitespace-separated token to appear in order, loosely:
`pmd` finds `parseMarkDown`, `dk` finds `deploy_key`. Results come back best
first rather than in file order, so `-m` keeps the best hits, not the first.

```bash
mdgrep --fuzzy "brew instal" notes.md   # misspelled, still matches
```

### Heading anchors

`[see](#the-foo-bar)` points at `## The Foo Bar`. `--anchor` searches that way
round — give it the link, get the heading.

```bash
mdgrep --anchor "#the-foo-bar" docs
mdgrep --anchor "#the-foo-bar" docs --section   # print the whole section
```

Write it as `the-foo-bar`, `#the-foo-bar`, `## The Foo Bar`, or a whole link
like `docs/setup.md#install` — when the link names a file, only that file is
searched. Percent escapes are decoded.

Generators disagree about slugs, so mdgrep tries every convention it knows and
matches if any agrees. `--anchor-style` narrows the list:

| Style | `## Deploy & Rollback!` | `## 1. Getting Started` | `## Café Notes` |
| --- | --- | --- | --- |
| `github` | `deploy--rollback` | `1-getting-started` | `café-notes` |
| `gitlab` | `deploy-rollback` | `1-getting-started` | `café-notes` |
| `python` (MkDocs) | `deploy-rollback` | `1-getting-started` | `cafe-notes` |
| `kramdown` (Jekyll) | `deploy--rollback` | `getting-started` | `caf-notes` |
| `pandoc` | `deploy-rollback` | `getting-started` | `café-notes` |
| `loose` | `deploy-rollback` | `1-getting-started` | `cafe-notes` |

Repeats are numbered, so the second `## Notes` is `#notes-1`. `--anchor` names
its heading outright, so it takes no case flag and no `-F`, `--fuzzy`, `-w`
or `-v`.

### Filters

| Flag | Meaning |
| --- | --- |
| `-k`, `--kind LIST` | `heading`, `item` (or `bullet`, `li`), `list`, `paragraph`, `code`, `quote`, `table`, `row`, `cell`, `html`, `frontmatter` |
| `--task` | only task list items |
| `--unchecked`, `--todo` | only unticked task items |
| `--checked`, `--done` | only ticked task items |

A filter never stands in for the pattern — pass an empty one to select by
filter alone:

```bash
mdgrep "rollback" docs -k heading          # only headings
mdgrep "deploy key" notes.md --unchecked   # open work mentioning the deploy key
mdgrep "" docs --todo                      # every open box under docs/
```

A hit in a plain sub-bullet reports the checkbox item it hangs under.

### Selection — how much to print

A result prints the lines that matched. Asking for a widener asks to see the
region whole, and that is the only switch between line output and node output.

| Flag | Meaning |
| --- | --- |
| `--expand [N]` | climb the expand ladder: bare, the matched node; `N`, that many rungs up |
| `--section` | widen to the enclosing heading section |
| `--section-body` | that section without its heading line |
| `--siblings N` | include N sibling blocks each side |
| `--at N-M` | take lines N to M of one file outright; repeatable |
| `-B`, `--before N` | print N lines before each matching line |
| `-A`, `--after N` | print N lines after |
| `-C`, `--context N` | shorthand for `-B N -A N` |

`-B`/`-A`/`-C` count **lines in the file**, clipped to the file the way `grep`
counts them, and they widen nothing: the region a result covers — the one an
edit rewrites and a `--stream` hands on — is the same with them as without.
`--siblings` is the one that counts blocks. Each of the three writes the sides
it names, so the last one typed wins and `-C 3 -B 1` is one line before and
three after. Two windows reaching the same line are one group of file lines
rather than two copies of it, and `--` stands only between groups that are not
next to each other.

```bash
mdgrep "brew install" notes.md              # the line that matched
mdgrep "brew install" notes.md --expand     # the nested bullet whole
mdgrep "brew install" notes.md --expand 1   # its parent bullet, with siblings
mdgrep "brew install" notes.md --section    # the whole section
mdgrep "canary" notes.md -C2                # two lines either side
```

A matcher that cannot name a line claims every line of the node instead: `-v`
selected the node by what it does *not* hold, and the empty pattern behind
`--todo`, `-k list` or `--outline` selected it by a filter, so no line in it is
more the answer than another. That is what keeps `mdgrep "" --todo` printing a
task item's sub-bullets, and why printing the whole node needs no flag of its
own — a node whose every line matched prints contiguously, which is the node.

Only the matched node is highlighted, so it stays obvious what hit.

### Addresses — saying a span back

The note hands out spans; `--at` is what takes one back.

```
$ mdgrep CONDSTORE tracking.md -n

693:- [ ] `SELECT (CONDSTORE)` + `FETCH CHANGEDSINCE` loop against all three
(item 693-715, list 509-722, section 507-724)

$ mdgrep -e CONDSTORE --at 693-715 tracking.md --check
```

The numbers are 1-based and inclusive — the ones the note printed. An address
selects by construction, so no matcher runs and the region prints whole.
`--at` takes no `PATTERN`: every positional beside it is a path, and it names
lines of one file, so a run where more than one could answer is refused.

A pattern given with `-e` beside an address is a **guard**, not a search: the
address says which lines to take, the pattern says what they should still say,
and the run is refused if it is not there. That is the one failure a line
number has that a pattern does not. To search *inside* an address and keep
what the search found, use `--then`:

```bash
mdgrep --at 507-724 tracking.md --then CONDSTORE
```

Bounds are checked against the file rather than clipped to it: an address that
does not fit is a stale note or a typo, worth being told about. An address
belongs on the stage that names the files, so a later `--then` stage does not
take one. `--anchor` and `--outline` are refused beside it, as are `--expect`
and `--multi`, which state how many nodes a search should have found, and `-k`
and the checkbox filters, which have nothing left to narrow.

### Editing

The flags that decide what gets printed decide what gets rewritten. Narrow the
search until it selects the node you mean, then say what to do with it.
`--siblings` is the exception, and is refused beside an edit along with `-A`,
`-B` and `-C`: it keeps the blocks either side of a match on the page, and
rewriting them is not what asking to see them meant.

| Flag | Meaning |
| --- | --- |
| `--check` / `--uncheck` / `--toggle` | set the state of the selected task item |
| `--replace TEXT` | replace the selected region with TEXT |
| `--set-text TEXT` | change what the node says, keeping its markup |
| `--delete` | remove the selected region |
| `--append TEXT` / `--prepend TEXT` | insert TEXT after or before it |
| `--replace-from FILE` and friends | the same, with TEXT read from a file (`-` is stdin) |
| `--multi` | edit every match |
| `--expect N` | edit only if exactly N nodes matched |
| `--dry-run` | show the edit, write nothing |
| `--apply FILE` | run a plan of edits, one JSON object per line (`-` is stdin) |

```bash
mdgrep "ship the docs" --check                  # - [ ] ship the docs -> - [x]
mdgrep --anchor "#setup" --set-text "Install"   # ## Setup -> ## Install
mdgrep "^## Changelog" --section-body --replace-from new.md
mdgrep "obsolete note" --delete
```

`--check`, `--uncheck`, `--toggle` and `--set-text` act on **the matched
node**; `--replace`, `--delete`, `--append` and `--prepend` act on **the
region** that `--section`, `--section-body` and `--expand` widen. `--set-text`
keeps the markup that makes a node what it is — heading level, list marker,
checkbox, fences — where `--replace` keeps nothing. Inserted text is indented
to match what it lands beside, and blank lines are added only where they will
not loosen a list or break a table.

**More than one match is an error.** Nothing is written, and you get the list:

```
$ mdgrep "ship" --check notes.md
mdgrep: 2 matches; narrow the search or pass --multi
  notes.md:5: - [ ] ship the docs
  notes.md:7: - [ ] ship the tests
```

`--multi` edits them all. `--expect N` is the safer version — say how many you
believe there are, and any other number is refused:

```
$ mdgrep "ship" --check notes.md --expect 3
mdgrep: --expect 3, but 2 matched
```

Each of the four text edits also has a `-from` spelling (`--replace-from`,
`--set-text-from`, `--append-from`, `--prepend-from`) reading from a file, or
stdin as `-`, so a multi-line body needs no shell quoting:

```bash
printf -- '- [ ] verify checksum\n- [ ] sign the tarball\n' |
  mdgrep "^## Release" --section-body --append-from -
```

Files are written atomically, through a temporary file renamed over the
original; a symlinked path is followed first, so what changes is the document
the link points at rather than the link. A checkbox that already reads the way
you asked is left alone. `-A`, `-B`, `-C`, `-c`, `-l` and `-m` are refused
with an edit.

The region an address names is the region an edit rewrites, so `--at` is the
second half of the loop the note opens: a search reports where something is,
and the next command rewrites it without searching for it again. That matters
most where the pattern that found a node is not a pattern that would find it
*only*.

An edit reports what it did, one line per line: `-` before a line it removed
and `+` before one it added, each numbered where it sits in its own version of
the file, and `=` before a line it left as it found it. After the mark, the
line is written the way a search writes one. A dry run prints `(dry run)`
first, since the lines are otherwise the same:

```
$ mdgrep "ship the docs" notes.md --check --dry-run
(dry run)

Release
- 5:- [ ] ship the docs
+ 5:- [x] ship the docs
```

### A plan of edits

Ticking eight boxes one at a time is eight processes, eight searches and eight
writes. `--apply` takes the lot as a plan — one JSON object per line, read from
a file or from stdin as `-`:

```bash
mdgrep --apply - <<'EOF'
{"path":"docs/checklist.md","match":"walk the rows","op":"check"}
{"path":"docs/checklist.md","match":"log the block","op":"check"}
{"path":"docs/setup.md","match":"^## Install","op":"set-text","text":"Setup"}
EOF
```

An entry needs `path`, `op` and one of `match` or `at`, plus `text` for the
four edits that write it. `at` is an address in `--at`'s syntax, and `match`
beside it is that flag's guard: the address selects, the pattern says the lines
still read the way they did. `kind`, `fixed`, `expand`, `section`,
`section-body`, `expect` and `multi` say per entry what the flags of those
names say — the last two refused beside `at`, as their flags are — and an
unknown key is an error rather than a silently different edit. The plan carries
its own search, so it takes no PATTERN, no PATH and no other matching or
editing flag.

The reason to want an address in a plan is the reason to want one on the
command line, doubled: a plan is generated by one run and applied by another,
so every entry that names its node by pattern is a search repeated against a
file that may have moved under it. An addressed entry has one node by
construction, and `match` beside it turns the gate into a question with a
yes-or-no answer.

Each file is parsed once however many entries name it, and written once with
every change they asked for. Every entry is planned against the file **as it was
read**, which makes the entries independent of one another: an entry cannot
match text another entry writes, and the order they are written in never changes
what any of them selects. The refusal rules carry over per entry — one match
unless `multi` is set, exactly `expect` many where it is given — and the plan
applies whole or not at all:

```
$ mdgrep --apply plan.jsonl
mdgrep: entry 2: 2 matches; narrow "match" or set "multi": true
  docs/checklist.md:5: - [ ] ship the docs
  docs/checklist.md:7: - [ ] ship the tests
mdgrep: 1 of 3 entries refused; nothing was written
```

An entry that matches nothing, matches more than it may, or names a file that
cannot be read refuses the run, as does a pair of entries reaching for the same
lines. Every failure is reported against its number, so one round trip says
everything the next plan has to fix. `--dry-run` and `-q` mean what they mean
for a single edit.

### Pipelines

A search that narrows in steps is a pipeline: find the section, then the list
inside it, then the open boxes in that list. `--then` joins two searches into
one run.

```bash
mdgrep "^## Release" --section docs \
  --then -k list \
  --then --todo --check --multi
```

Everything after `--then` is another search, and each stage searches only
inside the nodes the stage before it selected. A stage is a whole mdgrep
command line of its own, so it takes the matching, filter and selection flags
and reads them exactly as it would on its own — which is the point of spelling
a stage as a command line rather than inventing a query syntax for one. The
word is read before the flags are, the way a shell reads `|` before the
commands around it — and a bare `--` ends them the way it ends the flags, so a
file named `--then` or `--exec` stays a path.

Each stage does one job. Only the first names the files, so a word on a later
stage is its pattern and a stage that writes none selects by its filters
alone; only the last stage prints or writes, and it is the one the output and
editing flags belong to. A flag given where it would be read and then change
nothing is refused by name.

`--exec` spells the whole pipeline in one string, for a query kept in a
variable, a config file or a script:

```bash
mdgrep --exec '"^## Release" --section | -k list | --todo --check --multi' docs
```

It is split into words the way a shell splits a line — single quotes literal
throughout, double quotes literal but for a backslash before a quote or a
backslash, `""` a word in its own right, and a backslash at the end of a line
joining it to the next — and a bare `|` divides one stage from the next. The quoting is mdgrep's own here rather than the shell's, which
is what keeps the pipe character usable in a pattern: `"^(alpha|beta)"` is one
word and never a separator, and a quoted `'|'` is a word too. Only paths may
stand beside `--exec`, and they go to the stage that walks them, so one query
can be pointed at whichever files you mean.

The same pipeline can cross processes instead. `--stream` is what one stage
hands the next:

```bash
mdgrep "^## Release" --section docs --stream \
  | mdgrep "" -k list --stream \
  | mdgrep "" --todo --check --multi
```

What travels down the pipe is not the text a stage printed but the regions it
selected — a header line, then one JSON object per result:

```
$ mdgrep "^## Some header" --section . --stream
{"mdgrep":1}
{"path":"b.md","start":1,"end":3}
{"path":"demo.md","start":3,"end":13}
```

`path`, `start` and `end`, 1-based and inclusive, and nothing about the text.
The stage reading it opens each file again and searches only inside those
lines. That is what a pipe of text cannot do: text loses the path, restarts
the line numbers at 1, and mixes the files together under headings that parse
as markdown of their own. Regions lose none of it, so a line number is still
the line's own, a breadcrumb is still the whole trail, and the last stage of a
pipeline holds real paths — which is why an edit can stand at the end of one.

Narrowing goes by containment: a node the region holds whole is a candidate, a
node straddling it is not. It is the node a stage selects that has to fit, not
just the block that matched inside it, so a climb counts as well as a match:
`--todo` reporting the task a sub-bullet hangs under, or `--expand` climbing
the tree, selects nothing where the climb would leave the region. Widening is
the other direction and still works, so `--section` on a scoped heading prints
the section it names.

A stream names its own files, so a stage reading one takes no `PATH`, and none
of `--ext`, `--hidden` or `--no-ignore` either: those describe a walk, and the
stage that walked is the one before. A `PATH` means stdin is not read at all,
the way grep reads one, so a stage that names a file searches that file rather
than the stream; `-` is the explicit spelling of "read stdin", and naming it
alongside a file is refused rather than half honoured. Markdown arriving on
stdin is still read as markdown — the header line is what tells the two apart.

The other direction is refused too: a stream names the files the next stage
opens, and stdin is not one of them, so `--stream` over markdown on stdin is an
error rather than a stream of regions in a file nothing can find.

`--then`, `--exec` and a pipe of `--stream` describe the same pipeline, one with a
process boundary in it and one without, and they answer alike. `--then` parses
each file once for the whole run and can say which stage of it narrowed to
nothing — `stage 2 of 3 narrowed to nothing`, on stderr, with the exit status
still 1; `--stream` can be saved, replayed, and passed between machines.

A stream is a stage in the middle, so it takes no edit, and none of `-c`,
`-l`, `-q`, `--truncate`, `--color` or the line-number and breadcrumb flags:
each would be read and then change nothing the next stage receives.

A stream is a file like any other, so it can be kept and replayed:

```bash
mdgrep "" docs --todo --stream > open-boxes.jsonl
mdgrep "" --section < open-boxes.jsonl
```

A search that matched nothing still writes its header, so an empty stream says
the search ran; an empty pipe says there was none. A malformed record refuses
the run rather than being skipped, since the regions are the whole subject of
the search and one lost to a typo would come back as "no matches"; a record is
the whole of its line, so anything after one on its line is malformed too. A
file the stream names but nothing can open is an error rather than "no
matches", for the same reason an unreadable directory is.

### Output

| Flag | Meaning |
| --- | --- |
| `-n`, `--line-number` | number the printed lines |
| `-N`, `--no-line-number` | do not |
| `-H`, `--with-filename` | print the file a result came from |
| `--no-filename` | do not |
| `--heading` | that name above a file's results, not in front of every line |
| `--no-heading` | the other way: `path:line:text` on every line |
| `--breadcrumb` | print the heading trail above each result |
| `--no-breadcrumb` | do not |
| `--outline` | one indented line per heading, no PATTERN, no widening |
| `--separator STR` | what goes between two groups of lines that are not next to each other in the file; `--` by default, and `--separator ''` leaves none |
| `--span` | print the expand ladder after each result (default) |
| `--no-span` | do not |
| `--truncate N` | print at most N lines of any one result |
| `--color WHEN` | `auto` (default), `always`, `never` |
| `--format WHEN` | `plain` (default), `compact`, `json` or `stream` |
| `--json` | one JSON object per result (same as `--format json`) |
| `--stream` | hand the regions to the next mdgrep (same as `--format stream`) |
| `--then` | narrow what the search before it selected; everything after it is another search |
| `--exec PIPELINE` | the same pipeline written as one string, stages divided by `\|` |
| `-c`, `--count` | number of results per file |
| `-l`, `--files-with-matches` | names of matching files only |
| `-m`, `--max-count N` | stop after N results per file |
| `-q`, `--quiet` | print nothing; the exit status carries the answer |
| `--ext LIST` | file extensions to search |
| `--hidden` | descend into hidden directories |
| `--no-ignore` | search everything, including what the ignore files (`.gitignore`, `.ignore`, `.git/info/exclude`) and the skip list (`node_modules`, `vendor`, and friends) leave out |
| `-h`, `--help [TOPIC]` | the whole manual, or one part of it |
| `-V`, `--version` | |

Colour turns itself off when stdout is not a terminal, or under `NO_COLOR` or
`TERM=dumb`.

#### How a line is written

`path:line:text`, each part there only when it has something to say — the shape
`grep` and `rg` write. Three of those decisions have defaults, taken from where
the output is going and how much of the tree was searched, and each yields to
the flag that answers it.

| Question | Default | Flag |
| --- | --- | --- |
| Print the file name? | when more than one file could have answered | `-H` / `--no-filename` |
| Above the results, or in front of each line? | above, when stdout is a terminal | `--heading` / `--no-heading` |
| Number the lines? | when stdout is a terminal | `-n` / `-N` |

"More than one file could have answered" is a question about what was asked
for, not about what matched: a directory counts as more than one file however
few markdown documents it holds, so `mdgrep x docs/` names `docs/a.md` even
when `a.md` is all there is. A single file named outright, or markdown on
stdin, answers for itself and is not named. `-c` names the file on the same
terms.

So a terminal gets a heading, the trail and numbers, and a pipe gets the
markdown alone until it asks for more:

```
$ mdgrep "brew install" docs            # terminal
docs/notes.md
Install › macOS
13:  - On macOS run `brew install foo`
(item 13-15, list 11-19, section 8-24)

$ mdgrep "brew install" docs | cat      # pipe
docs/notes.md:  - On macOS run `brew install foo`
docs/notes.md:(item 13-15, list 11-19, section 8-24)
```

A line that matched takes `:` and a line `-A`, `-B` or `-C` pulled in takes
`-`, which is `grep`'s convention and `rg`'s.

### The span note

Every result ends with the spans it could be widened to, one rung of the expand
ladder per entry, from the matched node up to its enclosing section:

```
(item 693-715, list 509-722, section 507-724)
```

**Position is the `--expand` count**: the first entry is `--expand`, the second
`--expand 1`, and the last is whatever `--section` selects. Nothing is
numbered, because the note is printed whole or not at all — drop one rung and
every rung after it sits at a position that is no longer its count.

**It is a cost table, not a pointer.** 23 lines, 214 lines, 218 lines: one span
would report 218 when what you want is 23, and would hide that the middle rung
is a trap — the list is 98% of the section, so `--expand 1` costs everything
`--section` costs and gives less. Three spans make that decidable without a
second run.

The ladder climbs block ancestors and, where those run out, carries on up the
heading hierarchy — headings parse as flat siblings of the document, so
climbing block parents alone never reaches one. It stops at the first section:
past that you are reading the file, not widening a result. The note goes
entirely when the printed lines already cover every rung, context lines
included, or when the hit lies before the first heading and there is no
section to widen to. `--no-span` takes it back.

The heading trail has no counterpart in `grep`, so a pipe gets none until
`--breadcrumb` asks for it. It goes wherever a heading goes, since a heading is
what says a person is reading, and `--no-breadcrumb` leaves it out. Having
nowhere to stand but above a file's results, it is refused beside
`--no-heading`.

The trail above a heading stops at that heading's parent, since the heading
itself is the next line printed. `--section-body` keeps the whole trail, because
there the heading line never appears.

`--outline` answers "what is in these files" rather than "where does this
appear". It takes paths where a search takes a pattern, and it takes none of
the selection flags: one line per heading is all it has to print, so there is
nothing for `--section`, `--siblings` or `--expand` to widen and nothing for
`-A`, `-B` or `-C` to pad.

```
$ mdgrep --outline docs/pruning.md -n
1:# Pruning
5:  ## Winter Pruning
16:  ## Summer Pruning
27:  ## Central Leader
```

`--` goes between two groups of file lines that are not next to each other,
which is `grep`'s rule and `rg`'s: between two results, between two runs of
match lines inside one result, and between two files where no heading parts
them. The span note is a terminator rather than a group, so nothing precedes
it. `--separator ''` leaves the groups flush, and `--separator STR` sets any
other string.

`--truncate N` caps how much of one node is printed — a hit inside a 400-line
fenced block otherwise prints all 400 lines:

```
$ mdgrep "orchard survey" docs --truncate 3
docs/pruning.md
Pruning › Survey
12:```bash
13:orchard survey --block 04
14:orchard survey --block 05
… +38 lines
```

Two results that touch are printed as one passage, so that a page reads the
way the file does — but not under `--truncate`, where the cap would be spent
on the first of them and the rest would drop off the page rather than be
shortened. Under `--truncate` each node is capped on its own.

The `… +N lines` note is printed wherever lines were held back, on a line of
its own that names its file the way every other line does and takes no line
number, having none — `docs/pruning.md:… +38 lines` on a pipe. `--format
compact` and `--format json` carry the two counts as numbers.

### Machine-readable output

`--format compact` prints the path once per file and then one tab-separated
record per result — the line span, the kind, the text with its newlines
escaped, how many lines `--truncate` held back before and after it, the lines
that matched, and the expand ladder as `kind:start-end` — so a record is always
one line and the path is the line with no tab in it:

```
$ mdgrep "" pruning.md --format compact
pruning.md
1	heading	# Pruning	0	0		heading:1-1,section:1-30
3	heading	## Winter Pruning	0	0		heading:3-3,section:3-15
5-6	paragraph	Cut back the leader\nbefore the sap rises.	0	0		paragraph:5-6,section:3-15
```

The hits field is empty for a node matcher — `-v`, or the empty pattern behind
a filter, as here — which is how a reader tells "every line" from "these
lines". A pattern that can name its lines fills it in:

```
$ mdgrep "sap" pruning.md --format compact
pruning.md
5-6	paragraph	Cut back the leader\nbefore the sap rises.	0	0	6	paragraph:5-6,section:3-15
```

The span is the node's and the text is the window `--truncate` kept, so the
two counts are what places one in the other: the text begins on the span's
start plus the lines held back before it.

One record is one node. Two hits that touch — neighbouring checkboxes, headings
with nothing between them — are printed as a single passage in plain output,
where the page reads as prose; the machine formats, `--outline` and `-c` keep
them apart, so a record can be counted and a count is a count of nodes.

Neither machine format prints a page, so `-A`, `-B`, `-C` and `--span` are
refused beside them the way `--stream` and `--outline` refuse them.

An edit reports the span, the operation, `applied`/`dry`/`unchanged`, and the
new text. Compact leaves out the breadcrumb and the score; it costs about a
third of what `--json` costs on the same results, so it is the cheaper choice
whenever those two are not what is wanted.

`--json` emits one object per line: `path`, `kind`, `score`, `start`, `end`
(1-based, inclusive), `breadcrumb`, `text`, `hits` and `spans`, plus `checked`
on task items and `truncated_before` and `truncated_after` under `--truncate`.
`hits` is the lines that matched and is empty for a node matcher; `spans` is
the expand ladder as `{kind, start, end}` in ladder order, so the array index
is the `--expand` count and the last entry is what `--section` selects. An
entry of it, written `start-end`, is what `--at` consumes: a search reported in
`--json` and an edit made with `--at` are the two halves of one workflow. An edit reports
`op`, `old`, `new` and `applied` instead. A refused edit is one
object on stderr — `error` (`ambiguous`, `expect`, or `nomatch` for a plan
entry), `message`, `total`, `expected`, the capped `matches` list, and `entry`
under `--apply` — so a JSON caller parses the refusal with the reader it
already has.

A refusal is written in the format the results were asked for, so `compact`
gets records rather than prose on stderr: an `error` record carrying the kind,
entry, total, expected count, entries refused, path and message, then a `match`
record per listed hit and a `written` record per file a failed run left changed.

```
error	ambiguous	1	2	0	0		2 matches; narrow "match" or set "multi": true
match	notes.md	3	- [ ] thin the fruit
match	notes.md	9	- [ ] thin the hedge
```

```bash
mdgrep "rollback" docs --json | jq -r '.path + ":" + (.start|tostring)'
```

Exit status follows grep: `0` matched, `1` did not, `2` error. An error prints
the line that says what went wrong and points at `--help`.

`--help` takes a topic, so remembering one flag does not cost the whole manual:

```bash
mdgrep --help editing   # matching, filters, selection, editing, plans, output
mdgrep --help=editing   # the same, as one word
mdgrep --help anchor    # or any flag name
```

## Development

```
main.go              a run end to end: parse, walk, the stages of a search
internal/cli         the flags and the stages, and what each combination means
internal/help        the manual, and the rules for printing one part of it
internal/walk        the files a search reads: paths, extensions, stdin
internal/ignore      .gitignore, .ignore, .git/info/exclude and the skip list
internal/mdoc        goldmark AST → line-addressable block tree, sections, anchors
internal/match       regexp / literal / fuzzy matchers and highlight spans
internal/search      block selection, anchor lookup, expansion, merging
internal/edit        planning and applying rewrites, atomic writes
internal/plan        --apply: a plan of edits read as JSON, applied per file
internal/render      terminal, compact and JSON output
internal/report      why a run was refused, in whichever format was asked for
internal/stream      --stream: the regions one mdgrep hands the next in a pipe
```

GFM is on, so tables, task lists, strikethrough and autolinks parse; front
matter is one searchable node; every result is a verbatim slice of the file.

```bash
go test ./...
```

Every layer that converts between byte offsets, line numbers and rune indices
has a fuzz target, since that is where the three have to agree. Seeds run as
ordinary tests, so `go test ./...` covers the inputs already known. Looking for
new ones is a separate run:

```bash
scripts/fuzz.sh        # every target, 30s each
scripts/fuzz.sh 5m     # when there is a reason to look harder
```

The script finds the targets itself, so a new one is fuzzed by writing it. To
sit on a single target instead:

```bash
go test ./internal/mdoc   -run xxx -fuzz FuzzParse         -fuzztime 60s
go test ./internal/match  -run xxx -fuzz FuzzMatch         -fuzztime 60s
go test ./internal/edit   -run xxx -fuzz FuzzEditPipeline  -fuzztime 60s
go test ./internal/search -run xxx -fuzz FuzzSearchOptions -fuzztime 60s
go test ./internal/cli    -run xxx -fuzz FuzzPermute       -fuzztime 60s
```

Failing inputs land in the package's `testdata/fuzz` as regression cases.

CI runs gofmt, vet, build and `go test -race` on Linux and macOS. It does not
fuzz: the seed corpus goes with every run, and an open-ended search is not
something to wait on in front of a review.

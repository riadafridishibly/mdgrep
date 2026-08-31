# mdgrep

Node-aware grep for markdown.

`grep` gives you a line. Markdown is made of bullets, headings, code fences,
quotes and tables — so `mdgrep` gives you the whole node the hit landed in.

```
$ mdgrep "brew install" notes.md
notes.md
  Deployment › Prerequisites
  13 │   - On macOS run `brew install foo`
```

Matched characters are highlighted, and the heading trail says where you are.

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

You get the matched node: a hit in a bullet's text lifts to the whole bullet
and its children, a hit in a fenced block prints the fences.

| Flag | Meaning |
| --- | --- |
| `--expand N` | climb N ancestor levels from the matched node |
| `--section` | widen to the enclosing heading section |
| `--section-body` | that section without its heading line |
| `-B`, `--before N` | include N sibling blocks before |
| `-A`, `--after N` | include N sibling blocks after |
| `-C`, `--context N` | shorthand for `-B N -A N` |
| `--lines N` | pad with N raw lines on each side |

`-B`/`-A`/`-C` count **blocks**, not lines; use `--lines` for raw lines.

```bash
mdgrep "brew install" notes.md              # just the nested bullet
mdgrep "brew install" notes.md --expand 1   # its parent bullet, with siblings
mdgrep "brew install" notes.md --section    # the whole section
mdgrep "canary" notes.md -C2                # two blocks either side
```

Only the matched node is highlighted, so it stays obvious what hit.

### Editing

The flags that decide what gets printed decide what gets rewritten. Narrow the
search until it selects the node you mean, then say what to do with it.

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
original. A checkbox that already reads the way you asked is reported unchanged
and left alone. `-A`, `-B`, `-C`, `--lines`, `-c`, `-l` and `-m` are refused
with an edit.

### Output

| Flag | Meaning |
| --- | --- |
| `-n`, `--line-number` | number the printed lines (the default) |
| `-N`, `--no-line-number` | drop the line-number gutter |
| `--no-breadcrumb` | hide the heading trail |
| `--outline` | one indented line per heading, no PATTERN |
| `--separator STR` | what goes between two results of a file (default `--`) |
| `--truncate N` | print at most N lines of any one result |
| `--color WHEN` | `auto` (default), `always`, `never` |
| `--format WHEN` | `plain` (default), `compact` or `json` |
| `--json` | one JSON object per result (same as `--format json`) |
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

The trail above a heading stops at that heading's parent, since the heading
itself is the next line printed. `--section-body` keeps the whole trail, because
there the heading line never appears.

`--outline` answers "what is in these files" rather than "where does this
appear". It takes paths where a search takes a pattern:

```
$ mdgrep --outline docs/pruning.md
docs/pruning.md
   1 │ # Pruning
   5 │   ## Winter Pruning
  16 │   ## Summer Pruning
  27 │   ## Central Leader
```

`--separator ''` drops the `--` between results, and `--truncate N` caps how
much of one node is printed — a hit inside a 400-line fenced block otherwise
prints all 400 lines:

```
$ mdgrep "orchard survey" docs --truncate 3
docs/pruning.md
  Pruning › Winter Pruning
  12 │ ```bash
  13 │ orchard survey --block 04
  14 │ orchard survey --block 05
  … +38 lines
```

### Machine-readable output

`--format compact` prints the path once per file and then one tab-separated
record per result — the line span, the kind, and the text with its newlines
escaped, so a record is always one line and the path is the line with no tab
in it:

```
$ mdgrep "" pruning.md --format compact
pruning.md
1	heading	# Pruning
3	heading	## Winter Pruning
5-6	paragraph	Cut back the leader\nbefore the sap rises.
```

One record is one node. Two hits that touch — neighbouring checkboxes, headings
with nothing between them — are printed as a single passage in plain output,
where the page reads as prose; the machine formats, `--outline` and `-c` keep
them apart, so a record can be counted and a count is a count of nodes.

An edit reports the span, the operation, `applied`/`dry`/`unchanged`, and the
new text. Compact leaves out the breadcrumb and the score; it costs about a
third of what `--json` costs on the same results, so it is the cheaper choice
whenever those two are not what is wanted.

`--json` emits one object per line: `path`, `kind`, `score`, `start`, `end`
(1-based, inclusive), `breadcrumb`, `text`, plus `checked` on task items and
`truncated` under `--truncate`. An edit reports `op`, `old`, `new` and
`applied` instead. A refused edit is one
object on stderr — `error` (`ambiguous` or `expect`), `message`, `total`,
`expected` and the capped `matches` list — so a JSON caller parses the refusal
with the reader it already has.

```bash
mdgrep "rollback" docs --json | jq -r '.path + ":" + (.start|tostring)'
```

Exit status follows grep: `0` matched, `1` did not, `2` error. An error prints
the line that says what went wrong and points at `--help`.

`--help` takes a topic, so remembering one flag does not cost the whole manual:

```bash
mdgrep --help editing   # matching, filters, selection, editing, output
mdgrep --help anchor    # or any flag name
```

## Development

```
main.go              CLI: flags, file walking, worker pool
internal/mdoc        goldmark AST → line-addressable block tree, sections, anchors
internal/match       regexp / literal / fuzzy matchers and highlight spans
internal/search      block selection, anchor lookup, expansion, merging
internal/edit        planning and applying rewrites, atomic writes
internal/render      terminal and JSON output
```

GFM is on, so tables, task lists, strikethrough and autolinks parse; front
matter is one searchable node; every result is a verbatim slice of the file.

```bash
go test ./...
```

Every layer that converts between byte offsets, line numbers and rune indices
has a fuzz target, since that is where the three have to agree. Seeds run as
ordinary tests; to actually fuzz one:

```bash
go test ./internal/mdoc   -run xxx -fuzz FuzzParse         -fuzztime 60s
go test ./internal/match  -run xxx -fuzz FuzzMatch         -fuzztime 60s
go test ./internal/edit   -run xxx -fuzz FuzzEditPipeline  -fuzztime 60s
go test ./internal/search -run xxx -fuzz FuzzSearchOptions -fuzztime 60s
go test .                 -run xxx -fuzz FuzzPermute       -fuzztime 60s
```

Failing inputs land in the package's `testdata/fuzz` as regression cases.

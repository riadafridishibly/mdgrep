# mdgrep

Node-aware grep for markdown.

`grep` hands you one line. Markdown is not made of lines — it is made of
bullets, headings, code fences, quotes and tables. `mdgrep` parses the file
with [goldmark](https://github.com/yuin/goldmark), matches each node against
the markdown that produced it, and prints the **whole node** the hit belongs
to. How much more than that node you get is up to you.

```
$ mdgrep "brew install" notes.md
notes.md
  Deployment › Prerequisites
  13 │   - On macOS run `brew install foo`
```

On a terminal the matched characters are highlighted, and the heading trail
above the hit is printed so you know where in the document you landed.

## Install

```bash
go build -o mdgrep .
# or
go install .
```

Go 1.24+, one dependency (goldmark).

## Usage

```
mdgrep [OPTIONS] PATTERN [PATH...]
```

Flags follow grep and ripgrep wherever the two agree. `PATTERN` is a regular
expression by default and is required; an empty pattern matches everything, so
`mdgrep "" docs --todo` lists every open checkbox under `docs/`.

Paths may be files or directories; directories are walked for `.md`,
`.markdown`, `.mdown`, `.mkd` and `.mdx`. With no path, `mdgrep` reads stdin
when it is a pipe, otherwise it searches the current directory.

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

Nodes are matched against **the markdown as written**, not against rendered
text. `^` and `$` anchor to lines, so structure is searchable directly:

```bash
mdgrep '^## '            # every second-level heading
mdgrep '^\| .*canary'    # table rows mentioning the canary
mdgrep -F '**bold**'     # the emphasis markers themselves
```

Case is smart by default: the search folds case unless the pattern itself
contains an upper-case letter.

### Fuzzy matching

`--fuzzy` splits the pattern on whitespace and requires **every** token to
appear as an in-order subsequence of the node's source. Each token is scored on
how densely its characters are packed and on where the gaps land: a jump that
resumes at a camelCase hump, a delimiter or a piece of punctuation counts in
full, so `pmd` finds `parseMarkDown` and `dk` finds `deploy_key`, while a jump
across whitespace counts for little — whitespace is what separates one token
from the next, so a token that has to cross a word is not what you asked for.
The node's score is the token average, weighted by token length: a two-letter
token sits on a word boundary in almost any prose, so it should not carry a
node the way `implementer` does. Raise `--min-score` to demand tighter matches,
lower it to cast a wider net.

A fuzzy pattern asks which node fits best, so its results come back **best
first** — ranked within a file, and files ranked by their best hit — rather
than in grep's file order. `-m` therefore keeps the best results per file, not
the first ones. Regexp and `-F` searches keep grep's order.

```bash
mdgrep --fuzzy "brew instal" notes.md   # misspelled, still matches
```

### Heading anchors

A markdown link points at a heading by its slug — `[see](#the-foo-bar)` means
`## The Foo Bar`. `--anchor` searches that way round: give it the anchor and it
finds the heading.

```bash
mdgrep --anchor "#the-foo-bar" docs
mdgrep --anchor "#the-foo-bar" docs --section   # print the whole section
```

The pattern may be written as the anchor (`the-foo-bar`), with its `#`, as the
heading line copied verbatim (`## The Foo Bar`), or as a whole link with the
file in front of it — `docs/setup.md#install`, or a pasted
`https://github.com/o/r/blob/main/docs/setup.md#install`. When the link names a
file, only files whose path ends that way are searched. Percent escapes are
decoded, so `#caf%C3%A9-notes` finds `## Café Notes`.

Generators disagree about slugs, so mdgrep computes the anchor under each
convention it knows and matches if any of them agrees:

| Style | `## Deploy & Rollback!` | `## 1. Getting Started` | `## Café Notes` |
| --- | --- | --- | --- |
| `github` (github-slugger) | `deploy--rollback` | `1-getting-started` | `café-notes` |
| `gitlab` | `deploy-rollback` | `1-getting-started` | `café-notes` |
| `python` (Python-Markdown, MkDocs) | `deploy-rollback` | `1-getting-started` | `cafe-notes` |
| `kramdown` (Jekyll) | `deploy--rollback` | `getting-started` | `caf-notes` |
| `pandoc` | `deploy-rollback` | `getting-started` | `café-notes` |
| `loose` | `deploy-rollback` | `1-getting-started` | `cafe-notes` |

`--anchor-style` narrows that list — `--anchor-style github` when you know
where the link came from and want exactly what GitHub would resolve. `loose`
keeps only letters and digits and is the catch-all for a generator mdgrep has
never heard of.

Repeated headings are numbered the way a generator numbers them, so the second
`## Notes` in a file is `#notes-1` (`#notes_1` under `python`) and the anchor
tells the two apart. Case flags do not apply — an anchor is lower case by
construction — and `--anchor` cannot be combined with `-F`, `--fuzzy`, `-w` or
`-v`, since it names its heading outright rather than matching text.

### Filters

| Flag | Meaning |
| --- | --- |
| `-k`, `--kind LIST` | restrict to node kinds |
| `--task` | only task list items |
| `--unchecked`, `--todo` | only unticked task items |
| `--checked`, `--done` | only ticked task items |

`--kind` takes a comma list of `heading`, `item` (aliases `bullet`, `li`),
`list`, `paragraph`, `code`, `quote`, `table`, `row`, `cell`, `html`,
`frontmatter`.

```bash
mdgrep "rollback" docs -k heading      # only headings
mdgrep "TODO" notes -k item            # only bullets
```

A filter never stands in for the pattern. Pass an empty one to select purely by
filter:

```bash
mdgrep "deploy key" notes.md --unchecked   # open work mentioning the deploy key
mdgrep "changelog" notes.md --checked      # already done
mdgrep "" --todo                           # every open box below the cwd
mdgrep "" docs --todo                      # every open box under docs/
```

A hit in a plain sub-bullet reports the checkbox item it hangs under, so
searching the vault line below `- [ ] Rotate the deploy key` prints the whole
task. Non-task hits — headings, paragraphs, plain bullets — are dropped.

### Selection — how much to print

By default you get exactly the matched node. A hit inside a bullet's text is
lifted to the whole bullet, including its nested children. A hit in a fenced
code block prints the fences too.

| Flag | Meaning |
| --- | --- |
| `--expand N` | climb N ancestor levels from the matched node |
| `--section` | widen to the enclosing heading section |
| `--section-body` | that section without its heading line |
| `-B`, `--before N` | include N sibling blocks before |
| `-A`, `--after N` | include N sibling blocks after |
| `-C`, `--context N` | shorthand for `-B N -A N` |
| `--lines N` | pad with N raw lines on each side |

`-B`/`-A`/`-C` count **blocks**, not lines: `-C 1` around a paragraph gives you
the block before and the block after it, whatever their length. Use `--lines`
when you want raw lines.

```bash
mdgrep "brew install" notes.md              # just the nested bullet
mdgrep "brew install" notes.md --expand 1   # its parent bullet, with siblings
mdgrep "brew install" notes.md --section    # the whole "## Prerequisites" section
mdgrep "canary" notes.md -C2                # two blocks either side
```

Only the matched node is highlighted; expansion lines are printed plain, so it
stays obvious what actually matched.

### Editing

The same flags that decide what gets printed decide what gets rewritten. Narrow
the search until it selects the node you mean, then say what to do with it.

| Flag | Meaning |
| --- | --- |
| `--check` / `--uncheck` / `--toggle` | set the state of the selected task item |
| `--replace TEXT` | replace the selected region with TEXT |
| `--replace-from FILE` | the same, with TEXT read from a file (`-` is stdin) |
| `--set-text TEXT` | change what the node says, keeping its markup |
| `--delete` | remove the selected region |
| `--append TEXT` / `--prepend TEXT` | insert TEXT after or before it |
| `--multi` | edit every match |
| `--dry-run` | show the edit, write nothing |

```bash
mdgrep "ship the docs" --check                  # - [ ] ship the docs -> - [x]
mdgrep --anchor "#setup" --set-text "Install"   # ## Setup -> ## Install
mdgrep "^## Changelog" --section-body --replace-from new.md
mdgrep "obsolete note" --delete
```

**More than one match is an error.** mdgrep prints what it would have hit and
writes nothing, so a search that was vaguer than you thought cannot quietly
rewrite the wrong bullet:

```
$ mdgrep "ship" --check notes.md
mdgrep: 2 matches; narrow the search or pass --multi
  notes.md:5: - [ ] ship the docs
  notes.md:7: - [ ] ship the tests
```

`--multi` lifts that rule and edits every match.

`--check`, `--uncheck`, `--toggle` and `--set-text` act on **the matched node**;
`--replace`, `--delete`, `--append` and `--prepend` act on **the region**, which
is what `--section`, `--section-body` and `--expand` widen. The flags that only
pad the printed output — `-A`, `-B`, `-C`, `--lines` — are refused with an edit,
as are `-c`, `-l` and `-m`.

`--set-text` keeps the markdown that makes a node what it is: a heading keeps
its level and any trailing `{#id}`, a setext underline is resized to the new
text, a list item keeps its marker and its checkbox, a fenced block keeps its
fences. `--replace` keeps nothing — it is the whole region, verbatim.

Inserted text is indented to match the node it lands beside, so a bullet
appended to a nested item becomes its sibling, and a blank line is added on
whichever side needs one to keep two blocks apart — never inside a list or a
table, where a blank line would loosen the list or break the table.

The change is printed as the lines that went and the lines that came, unless
`-q`; `--json` reports `path`, `op`, `start`, `end`, `old`, `new` and `applied`.
A checkbox that already reads the way you asked is reported as unchanged and
the file is left untouched. Each file is written through a temporary file in
the same directory and renamed over the original, so an interrupted run cannot
leave a half-written document behind.

### Output

| Flag | Meaning |
| --- | --- |
| `-n`, `--line-number` | number the printed lines (the default) |
| `-N`, `--no-line-number` | drop the line-number gutter |
| `--no-breadcrumb` | hide the heading trail |
| `--color WHEN` | `auto` (default), `always`, `never` |
| `--json` | one JSON object per result |
| `-c`, `--count` | number of results per file |
| `-l`, `--files-with-matches` | names of matching files only |
| `-m`, `--max-count N` | stop after N results per file |
| `-q`, `--quiet` | print nothing; the exit status carries the answer |
| `--ext LIST` | file extensions to search |
| `--hidden` | descend into hidden directories |
| `--no-ignore` | do not skip `node_modules`, `vendor`, and friends |
| `-h`, `--help` / `-V`, `--version` | |

Colour is disabled automatically when stdout is not a terminal, when `NO_COLOR`
is set, or when `TERM=dumb`.

`--json` emits newline-delimited objects with `path`, `kind`, `score`, `start`,
`end` (1-based, inclusive), `breadcrumb` and `text`. Task items also carry
`checked`, which is absent on everything else:

```bash
mdgrep "rollback" docs --json | jq -r '.path + ":" + (.start|tostring)'
```

Exit status follows grep: `0` if something matched, `1` if nothing did, `2` on
error.

## Notes on parsing

- GFM is enabled, so tables, task lists, strikethrough and autolinks parse. A
  task list item keeps its checkbox state, and the `- [ ]` marker is part of the
  text you can search.
- YAML/TOML front matter is treated as a single searchable node rather than
  being mangled into a thematic break and a setext heading.
- Ranges are computed in lines and widened back over syntax goldmark reports
  outside a node's content (code fences, setext underlines), so every result is
  a verbatim slice of the original file — and so the text that was scored is the
  same text that gets printed and highlighted.

## Layout

```
main.go              CLI: flags, file walking, worker pool
internal/mdoc        goldmark AST → line-addressable block tree, sections, breadcrumbs, anchors
internal/match       regexp / literal / fuzzy matchers and highlight spans
internal/search      block selection, anchor lookup, tightening, promotion, expansion, merging
internal/edit        planning and applying rewrites, atomic writes
internal/render      terminal and JSON output
```

```bash
go test ./...
```

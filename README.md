# mdgrep

Loose, node-aware grep for markdown.

`grep` hands you one line. Markdown is not made of lines — it is made of
bullets, headings, code fences, quotes and tables. `mdgrep` parses the file
with [goldmark](https://github.com/yuin/goldmark), matches against the plain
text of each node, and prints the **whole node** the hit belongs to. How much
more than that node you get is up to you.

```
$ mdgrep "brew instal" notes.md
notes.md
  Deployment › Prerequisites
  13 │   - On macOS run `brew install foo`
```

The pattern above is misspelled and still matches: the default matcher is
fuzzy. On a terminal the matched characters are highlighted, and the heading
trail above the hit is printed so you know where in the document you landed.

## Install

```bash
go build -o mdgrep .
# or
go install .
```

Go 1.24+, one dependency (goldmark).

## Usage

```
mdgrep [options] PATTERN [path ...]
```

Paths may be files or directories; directories are walked for `.md`,
`.markdown`, `.mdown`, `.mkd` and `.mdx`. With no path, `mdgrep` reads stdin
when it is a pipe, otherwise it searches the current directory.

### Matching

| Flag | Meaning |
| --- | --- |
| `-f`, `--fuzzy` | fuzzy match (default) |
| `-F`, `--fixed` | plain substring |
| `-e`, `--regex` | regular expression |
| `-i`, `--ignore-case` | force case-insensitive |
| `-S`, `--case-sensitive` | force case-sensitive |
| `-s`, `--min-score N` | fuzzy threshold, 0..1 (default 0.55) |
| `-k`, `--kind LIST` | restrict to node kinds |

Case is smart by default: the search folds case unless the pattern itself
contains an upper-case letter.

The fuzzy matcher splits the pattern on whitespace and requires **every** token
to appear as an in-order subsequence of the node's text. Each token is scored on
how densely its characters are packed, how many of them run consecutively, and
whether it starts on a word boundary; the node's score is the token average.
Raise `--min-score` to demand tighter matches, lower it to cast a wider net.

`--kind` takes a comma list of `heading`, `item` (aliases `bullet`, `li`),
`list`, `paragraph`, `code`, `quote`, `table`, `row`, `cell`, `html`,
`frontmatter`.

```bash
mdgrep "rollback" docs -k heading      # only headings
mdgrep "TODO" notes -k item            # only bullets
```

### Selection — how much to print

By default you get exactly the matched node. A hit inside a bullet's text is
lifted to the whole bullet, including its nested children. A hit in a fenced
code block prints the fences too.

| Flag | Meaning |
| --- | --- |
| `-x`, `--expand N` | climb N ancestor levels from the matched node |
| `--section` | widen to the enclosing heading section |
| `-B`, `--before N` | include N sibling blocks before |
| `-A`, `--after N` | include N sibling blocks after |
| `-C`, `--context N` | shorthand for `-B N -A N` |
| `-L`, `--lines N` | pad with N raw lines on each side |

`-B`/`-A`/`-C` count **blocks**, not lines: `-C 1` around a paragraph gives you
the block before and the block after it, whatever their length.

```bash
mdgrep "brew install" notes.md            # just the nested bullet
mdgrep "brew install" notes.md -x1        # its parent bullet, with siblings
mdgrep "brew install" notes.md --section  # the whole "## Prerequisites" section
mdgrep "canary" notes.md -C2              # two blocks either side
```

Only the matched node is highlighted; expansion lines are printed plain, so it
stays obvious what actually matched.

### Output

| Flag | Meaning |
| --- | --- |
| `-n`, `--no-line-numbers` | drop the line-number gutter |
| `--no-breadcrumb` | hide the heading trail |
| `--color WHEN` | `auto` (default), `always`, `never` |
| `--json` | one JSON object per result |
| `-c`, `--count` | number of results per file |
| `-l`, `--files` | names of matching files only |
| `-m`, `--max N` | stop after N results per file |
| `--ext LIST` | file extensions to search |
| `--hidden` | descend into hidden directories |
| `--no-ignore` | do not skip `node_modules`, `vendor`, and friends |

Colour is disabled automatically when stdout is not a terminal, when `NO_COLOR`
is set, or when `TERM=dumb`.

`--json` emits newline-delimited objects with `path`, `kind`, `score`, `start`,
`end` (1-based, inclusive), `breadcrumb` and `text`:

```bash
mdgrep "rollback" docs --json | jq -r '.path + ":" + (.start|tostring)'
```

Exit status follows grep: `0` if something matched, `1` if nothing did, `2` on
error.

## Notes on parsing

- GFM is enabled, so tables, task lists, strikethrough and autolinks parse.
- YAML/TOML front matter is treated as a single searchable node rather than
  being mangled into a thematic break and a setext heading.
- Link and image destinations are part of a node's searchable text, so you can
  grep for a URL and get the sentence containing it.
- Ranges are computed in lines and widened back over syntax goldmark reports
  outside a node's content (code fences, setext underlines), so every result is
  a verbatim slice of the original file.

## Layout

```
main.go              CLI: flags, file walking, worker pool
internal/mdoc        goldmark AST → line-addressable block tree, sections, breadcrumbs
internal/match       fuzzy / substring / regexp matchers and highlight spans
internal/search      block selection, tightening, promotion, expansion, merging
internal/render      terminal and JSON output
```

```bash
go test ./...
```

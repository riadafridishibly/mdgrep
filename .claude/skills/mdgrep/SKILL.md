---
name: mdgrep
description: Search and edit markdown by node — headings, bullets, task items, fences, table rows — not by line. Use for markdown structure (find a section, list or tick checkboxes, replace a section body) or when a grep hit would be a fragment of a bullet or fence.
allowed-tools: Bash(mdgrep:*), Read
---

# mdgrep

`mdgrep [OPTIONS] PATTERN [PATH...]` prints the lines that matched, the way
`grep` does, and closes each result with the spans it could be widened to — the
bullet, the list, the section. Ask for one of those with a widener and it
prints whole. The same flags select what an edit rewrites.

PATTERN is a regexp; `""` matches everything, which is how a filter-only query
is written. It matches the markdown as written, so `^## ` is every second-level
heading. `--outline` and `--at` take none. PATH is files or directories,
default cwd, stdin when piped. Exit **0** matched, **1** no match, **2** error.

`mdgrep --help TOPIC` (`matching`, `filters`, `selection`, `editing`, `plans`,
`pipelines`, `output`, or any long flag name) costs 130–540 tokens against
~1600 for the manual — fetch one rather than guess a flag.

## Search

| Flag | Effect |
| --- | --- |
| `-F` / `-i` | literal / ignore case (default smart-case) |
| `-k KINDS` | `heading,item,list,paragraph,code,quote,table,row,cell,html,frontmatter` |
| `--todo` / `--done` / `--task` | unticked / ticked / any checkbox item |
| `--section` / `--section-body` | enclosing section, with / without its heading |
| `--expand [N]` | bare, the matched node whole; N, that many rungs up the ladder |
| `--siblings N` | keep N sibling blocks each side |
| `--at N-M` | take those lines of one file outright, as a span note writes them |
| `-B` / `-A` / `-C N` | pad the page N lines before / after / both |

Narrow before widening: `--section` over a tree can cost more than reading the
file. Locate with `-l`, re-run with `--section` on the one file.

A widener (`--expand`, `--section`, `--section-body`, `--siblings`) is the only
switch between line output and node output. `-B`/`-A`/`-C` widen nothing: they
pad the printed page, are counted in the file and clipped to it the way `grep`
counts them, and the last of the three typed wins, so `-C 3 -B 1` is one line
before and three after.

`--at` is how a span note is said back. The numbers are the 1-based inclusive
ones the note printed; it names lines of **one** file, so a run where more than
one could answer is refused, and bounds are checked against the file rather
than clipped to it. A pattern given with `-e` beside it is a **guard**, not a
search: the address says which lines to take, the pattern says what they should
still say, and the run is refused if it is not there. `--anchor`, `--outline`,
`--expect`, `--multi`, `-k` and the checkbox filters are all refused beside it,
and it belongs on the stage that names the files.

## Output

A line is `path:line:text`, the shape `grep` and `rg` write, each part there
only when it has something to say. **You are a pipe**, so the line number, the
heading and the breadcrumb are off unless asked, and the file name follows what
was searched: a directory (or no path) puts `path:` on every line, while a
single named file or stdin prints the markdown alone — the cheapest form by a
quarter to a half, most on short nodes. Ask for what you need: `-n` numbers,
`-H` / `--no-filename` name the file or not, `--heading` moves that name above
the file's results and brings the heading trail with it, `--breadcrumb` asks
for the trail alone (refused beside `--no-heading`, which has nowhere to put
one). A line the matcher pointed at takes `:` and one a context flag pulled in
takes `-`; under a widener the whole region is the answer and every line of it
takes `:`. `--` stands between two groups of file lines that are not next to
each other — `--separator ''` leaves none. Each result closes with its expand
ladder, `(item 13-14, list 13-15, section 11-15)`, whose entries are what
`--at` takes back — a rung the page already printed whole is left out, so a
heading printed whole closes with `(section 46-48)` alone; `--no-span` drops
the note.

`--outline` is one indented line per heading and takes paths, not a pattern —
the cheapest view of a tree. `--truncate N` caps node output — what a widener
asked for whole, and what a node matcher claimed whole — and is the guard
against a hit inside a 400-line fence; the lines a line matcher pointed at are
the answer and are never capped. Under it two results that touch stay apart
rather than sharing one cap, and a capped page does not count the held-back
lines out: the span note already names the node and says where it runs, which
is what `--at` takes back (`--format compact` and `--format json` carry the
counts as numbers for a caller doing arithmetic). `-l` names files, `-c` counts, `-m N` caps per file, `-q`
answers in the exit status alone.

To feed another mdgrep, chain stages (below) rather than reparsing text.
**Parse with `--format compact`** — one record per line under the path, where
`--json` costs 2–3× as much for the breadcrumb, the score and an edit's `old`:

```
$ mdgrep "sap|Pruning" pruning.md --format compact
pruning.md
1	heading	# Pruning	1	heading:1-1,section:1-6
5-6	paragraph	Cut back the leader\nbefore the sap rises.	6	paragraph:5-6,section:1-6
```

Tab-separated `start[-end] kind text hits spans`, newlines escaped
— in the path too, so a record is one line and a path is the line with no tab.
The span is the node's and the text is what `--truncate` kept; the record does
not count out what it held back — `spans` names the region and `--at` takes it
back whole. `hits` are the lines that matched, comma-separated and **empty for a
node matcher** (`-v`, or the empty pattern behind a filter) — which is how a
reader tells "every line" from "these lines". `spans` is the expand ladder as
`kind:start-end`, in ladder order, so the index is the `--expand` count. An
edit records `start[-end] op applied|preview|unchanged new`, an insertion the one
line it lands on.

Neither machine format prints a page, so `-A`, `-B`, `-C` and `--span` are
refused beside them.

## Pipelines

Narrow in stages instead of two searches with text-munging between them. Each
stage is a whole mdgrep line and searches only inside the nodes the stage
before it selected. Only the first names files, only the last prints or
writes; a flag on the wrong stage is refused by name.

```bash
mdgrep "^## Release" --section docs --then -k list --then --todo --check --multi -W
mdgrep --exec '"^## Release" --section | -k list | --todo' docs   # the same, one string
```

`--exec` splits like a shell — quotes literal, a bare `|` separates — so
`"^(alpha|beta)"` is one word. `--then` and `--exec` are read before the flags,
and a bare `--` ends them, so a file named `--then` stays a path.

Across processes, `--stream` (`--format stream`) hands on regions rather than
text: a header line, then `{"path":...,"start":...,"end":...}` per result,
1-based inclusive. The next stage reopens the file, so line numbers,
breadcrumbs and paths survive — which is why an edit can end a pipeline.

```bash
mdgrep "" docs --todo --stream > open-boxes.jsonl   # save, replay, pass around
mdgrep "" --section < open-boxes.jsonl
```

Narrowing is by containment: a node the region holds whole is a candidate, one
straddling it is not, and a climb (`--todo`, `--expand`) leaving the region
selects nothing. A stage reading a stream takes no PATH and none of `--ext`,
`--hidden`, `--no-ignore`; a stream cannot be made from stdin. `--then` says on
stderr which stage narrowed to nothing, exit 1 either way.

## Edit

| Flag | Effect |
| --- | --- |
| `--check` / `--uncheck` / `--toggle` | set the task item's state |
| `--set-text TEXT` | change what the node says, keeping its markup |
| `--replace TEXT` / `--delete` | replace / remove the region |
| `--append TEXT` / `--prepend TEXT` | insert after / before it |
| `-W` / `--expect N` / `--multi` | write it / require N matches / edit all |
| `--format diff` / `--format doc` | the patch it would apply / the document it produced |

Rows 1–2 act on the **matched node** and are refused with `--section`; rows 3–4
act on the **region** `--section` and `--expand` widen to.

**An edit shows the change and writes nothing until `-W`.** More than one
match is an error: nothing written, hits listed — narrow it, or say
`--expect N` / `--multi`. Search → edit → `-W`. An edit reports `- ` and `+ `
lines in the search shape, `= ` for a node already as asked (exit 0); the
lines are the same with or without `-W`, and `--format compact`/`--json` call
a change `preview` or `applied`. Where the document came from never changes
what an edit prints; two formats print something else instead. `--format diff`
is a unified patch for any number of files. `--format doc` is the document the
edit produced, so it wants exactly one and refuses a run with more — that is
the filter shape, `cat f.md | mdgrep PATTERN --replace X --format doc > out.md`,
and it prints the document unchanged on a miss so a redirect is never emptied.
The flags that only
report refuse an edit: `-c`, `-l`, `-m`, `--truncate`, `-A`/`-B`/`-C`,
`--siblings`, `--outline`. `-W` is refused on stdin, which has no file to
write to. `--at` selects the region an edit
rewrites, which is what makes an edit by line number possible — pair it with
`-e GUARD` so a stale address is refused rather than applied.

### `--apply` — 2 or more edits in one process

One JSON object per line, from a file or stdin as `-`; one parse and one write
per file however many entries name it. `-W` gates a plan the way it gates a
single edit. Entries are independent and the plan
applies whole or not at all, failures printing as `entry N: ...` with nothing
written. `mdgrep --help plans` for the keys.

```bash
mdgrep --apply - <<'EOF'
{"path":"docs/list.md","match":"walk the rows","op":"check"}
{"path":"docs/setup.md","match":"^## Install","op":"set-text","text":"Setup"}
EOF
```

## Recipes

```bash
mdgrep "retry budget" docs --section            # the section documenting X
mdgrep "" . --todo --format compact             # every open box, parseable
mdgrep --outline docs -N                        # what is in this tree
mdgrep "sign the tarball" --check --expect 1 -W  # tick exactly one task
mdgrep "^## Changelog" --section-body --replace-from CHANGELOG.md  # shown, not written
mdgrep -e "rotate the key" --at 693-715 notes.md --check -W  # tick a span the note gave back
mdgrep "^## Release" --section docs --then --todo --check --multi -W  # tick the boxes in one section
```

**`mdgrep --help <flag>` for:** `--anchor` / `--anchor-style` (a heading from a
`#slug` link), `--fuzzy` / `--min-score` (typo-tolerant, best-first), `-e`
(alternative patterns), `-w` / `-v` / `-s` / `-S`, `-B` / `-A` / `-C`
(page padding), `--at` (addresses), `--siblings`, `--span` / `--separator`,
`--ext` / `--hidden` / `--no-ignore`, `--color`, the four `--*-from FILE`
edits.

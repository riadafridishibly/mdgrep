---
name: mdgrep
description: Search and edit markdown by node — headings, bullets, task items, fences, table rows — not by line. Use for markdown structure (find a section, list or tick checkboxes, replace a section body) or when a grep hit would be a fragment of a bullet or fence.
allowed-tools: Bash(mdgrep:*), Read
---

# mdgrep

`mdgrep [OPTIONS] PATTERN [PATH...]` prints the whole node a hit lands in — the
bullet with its children, the table row, the fence with its fences. The same
flags select what an edit rewrites.

PATTERN is a regexp, always required; `""` matches everything, which is how a
filter-only query is written. It matches the markdown as written, so `^## ` is
every second-level heading. PATH is files or directories, default cwd, stdin
when piped. Exit **0** matched, **1** no match, **2** error.

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
| `--expand N` | climb N ancestor levels |

Narrow before widening: `--section` over a tree can cost more than reading the
file. Locate with `-l`, re-run with `--section` on the one file.

## Output

A line is `path:line:text`, the shape `grep` and `rg` write, each part there
only when it has something to say. **You are a pipe, so all three are off by
default** — output is the markdown alone, which is also the cheapest form by a
quarter to a half, most on short nodes. Ask for what you need: `-n` numbers,
`-H` names the file, `--heading` moves that name above the file's results
instead, `--breadcrumb` adds the heading trail (refused beside `--no-heading`,
which has nowhere to put one). Every printed line takes `:` — a node's own
lines and the ones `--section` widened it to alike — so narrow with a filter or
another stage, never by reading the marker.

`--outline` is one indented line per heading and takes paths, not a pattern —
the cheapest view of a tree. `--truncate N` caps one node, the guard against a
hit inside a 400-line fence; under it two results that touch stay apart rather
than sharing one cap, and its `… +N lines` note appears only under `--heading`
(`--format compact` and `--format json` carry the counts as numbers). `-l`
names files, `-c` counts, `-m N` caps per file, `-q` answers in the exit status
alone.

To feed another mdgrep, chain stages (below) rather than reparsing text.
**Parse with `--format compact`** — one record per line under the path, where
`--json` costs 2–3× as much for the breadcrumb, the score and an edit's `old`:

```
pruning.md
1	heading	# Pruning	0	0
5-6	paragraph	Cut back the leader\nbefore the sap rises.	0	0
```

Tab-separated `start[-end] kind text before after`, newlines escaped — in the
path too, so a record is one line and a path is the line with no tab. The span
is the node's, the text is what `--truncate` kept, and `before` and `after` are
the lines it held back on each side: the text starts at start plus `before`. An
edit records `start[-end] op applied|dry|unchanged new`, an insertion the one
line it lands on.

## Pipelines

Narrow in stages instead of two searches with text-munging between them. Each
stage is a whole mdgrep line and searches only inside the nodes the stage
before it selected. Only the first names files, only the last prints or
writes; a flag on the wrong stage is refused by name.

```bash
mdgrep "^## Release" --section docs --then -k list --then --todo --check --multi
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
| `--dry-run` / `--expect N` / `--multi` | show only / require N matches / edit all |

Rows 1–2 act on the **matched node** and are refused with `--section`; rows 3–4
act on the **region** `--section` and `--expand` widen to.

**More than one match is an error**: nothing written, hits listed — narrow it,
or say `--expect N` / `--multi`. Search → `--dry-run` → `--expect N`. A checkbox
already in the asked-for state is `unchanged`, exit 0. The flags that only
report refuse an edit: `-c`, `-l`, `-m`, `--truncate`, `-A`/`-B`/`-C`,
`--lines`, `--outline`, and stdin input.

### `--apply` — 2 or more edits in one process

One JSON object per line, from a file or stdin as `-`; one parse and one write
per file however many entries name it. Entries are independent and the plan
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
mdgrep "sign the tarball" --check --expect 1    # tick exactly one task
mdgrep "^## Changelog" --section-body --replace-from CHANGELOG.md --dry-run
mdgrep "^## Release" --section docs --then --todo --check --multi  # tick the boxes in one section
```

**`mdgrep --help <flag>` for:** `--anchor` / `--anchor-style` (a heading from a
`#slug` link), `--fuzzy` / `--min-score` (typo-tolerant, best-first), `-e`
(alternative patterns), `-w` / `-v` / `-s` / `-S`, `-B` / `-A` / `-C` /
`--lines` (padding), `--ext` / `--hidden` / `--no-ignore`, `--color`, the four
`--*-from FILE` edits.

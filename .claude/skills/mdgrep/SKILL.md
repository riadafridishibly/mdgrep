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
`output`, or any long flag name) costs 130–540 tokens against ~1600 for the
manual — fetch one rather than guess a flag.

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

`--outline` is one indented line per heading and takes paths, not a pattern —
the cheapest view of a tree. `-N`, `--no-breadcrumb` and `--separator ''`
together cut a quarter to a half, most on short nodes. `--truncate N` caps one
node, the guard against a hit inside a 400-line fence. `-l` names files, `-c`
counts, `-m N` caps per file, `-q` answers in the exit status alone.

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
```

**`mdgrep --help <flag>` for:** `--anchor` / `--anchor-style` (a heading from a
`#slug` link), `--fuzzy` / `--min-score` (typo-tolerant, best-first), `-e`
(alternative patterns), `-w` / `-v` / `-s` / `-S`, `-B` / `-A` / `-C` /
`--lines` (padding), `--ext` / `--hidden` / `--no-ignore`, `--color`, the four
`--*-from FILE` edits.

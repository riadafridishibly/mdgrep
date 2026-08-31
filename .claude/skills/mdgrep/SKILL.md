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

`mdgrep --help TOPIC` (`matching`, `filters`, `selection`, `editing`, `output`,
or any flag name) costs 90–570 tokens against ~1600 for the manual — fetch one
freely for the list at the bottom.

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
the cheapest view of a tree. It takes no selection flag either; there is
nothing for one to widen. `-N` (no line numbers), `--no-breadcrumb` and
`--separator ''` together are about half the default cost. `--truncate N` caps
one node, the guard against a hit inside a 400-line fence. `-l` names files,
`-c` counts, `-m N` caps per file, `-q` answers in the exit status alone.

**Parse with `--format compact`**: the path once per file, then tab-separated
`start[-end] kind text truncated` with newlines escaped — in the path too — so
a record is one line and a path is the line with no tab. The last field is how
many lines `--truncate` held back, `0` when nothing was. An insertion is a
point rather than a span, so `append` and `prepend` record the single line they
land on.

```
pruning.md
1	heading	# Pruning	0
5-6	paragraph	Cut back the leader\nbefore the sap rises.	0
```

An edit records `start[-end] op applied|dry|unchanged new`. `--json` adds the
breadcrumb, the score and an edit's `old` at ~3.6× the cost; ask for it only
when those are what you need. One record is one node; adjacent hits run
together only in plain output.

## Edit

| Flag | Effect |
| --- | --- |
| `--check` / `--uncheck` / `--toggle` | set the task item's state |
| `--set-text TEXT` | change what the node says, keeping its markup |
| `--replace TEXT` / `--delete` | replace / remove the region |
| `--append TEXT` / `--prepend TEXT` | insert after / before it |
| `--dry-run` / `--expect N` / `--multi` | show only / require N matches / edit all |

Rows 1–2 act on the **matched node** and are refused with `--section`; rows 3–4
act on the **region** `--section` and `--expand` widen to. `--set-text` keeps
heading level, list marker, checkbox and fences; `--replace` keeps nothing.

**More than one match is an error**: nothing written, hits listed — narrow it,
or say `--expect N` / `--multi`. Writes are atomic; a checkbox already in the
asked-for state is `unchanged`, exit 0. Refused with `-c`, `-l`, `-m`, `-A`,
`-B`, `-C`, `--lines`, stdin input. Search → `--dry-run` → `--expect N`.

### `--apply` — 2 or more edits in one process

One JSON object per line, from a file or stdin as `-`; one parse and one write
per file however many entries name it.

```bash
mdgrep --apply - <<'EOF'
{"path":"docs/list.md","match":"walk the rows","op":"check"}
{"path":"docs/setup.md","match":"^## Install","op":"set-text","text":"Setup"}
EOF
```

`path`, `match`, `op` (`check`, `uncheck`, `toggle`, `replace`, `set-text`,
`delete`, `append`, `prepend`) required, `text` for the four that write it;
`kind`, `fixed`, `expand`, `section`, `section-body`, `expect`, `multi` mean
what the flags do. An unknown key is an error, and no PATTERN, PATH or
search/edit flag may come alongside — `--dry-run`, `-q`, `--format` still may.

Entries are planned against the file as it was read, so they are independent —
an entry cannot match what another writes. A plan applies whole or not at all:
an entry matching nothing, matching more than one without `multi`, naming an
unreadable file, or reaching for lines another entry rewrites refuses the run. Failures print as `entry N: ...`
(`"entry":N` under `--json`), nothing is written, exit 2.

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

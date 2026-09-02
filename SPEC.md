# Line-oriented output

Status: proposed. Supersedes the current plain-output behaviour, which prints
every line of the node a hit sits in.

## Why

mdgrep prints nodes. A hit inside a forty-line fence prints forty lines, and a
hit in a task item prints the item and every sub-bullet under it. That is the
right answer when you asked for the node, and the wrong one when you asked
where a string is.

grep and ripgrep answer the second question, and the shape of their answer —
`path:line:text` for a match, `path-line-text` for context — is the one every
reader and every agent already knows. This specification adopts it, and keeps
mdgrep's own answer available behind the flags that ask for it.

Throughout, the example document is:

```
 1  # Notes
 2
 3  Intro paragraph about foo.
 4
 5  ## Setup
 6
 7  Install foo, then run foo doctor.
 8  Check the log.
 9  Then foo again.
10
11  ## Tasks
12
13  - [ ] rotate the foo key
14        the old key is in the vault
15  - [ ] archive the logs
```

Its nodes are: heading `1-1`, paragraph `3-3`, heading `5-5`, paragraph
`7-9`, heading `11-11`, list `13-15` holding items `13-14` and `15-15`.

Its sections are: `# Notes` `1-15`, `## Setup` `5-9`, `## Tasks` `11-15`.

Line numbers here are 1-based, as printed. All of the above is what the
current parser already reports; none of it changes.

## 1. The printing model

### 1.1 Three kinds of line

Plain output is made of three record shapes. Each part of a prefix appears
only when it has something to say, exactly as today.

| kind | shape | when |
| --- | --- | --- |
| match | `path:line:text` | the line matched |
| context | `path-line-text` | `-A`, `-B` or `-C` pulled the line in |
| span note | `path:(item N-M, list N-M, section N-M)` | closes a result |

The colon and the dash are grep's and ripgrep's. The span note is mdgrep's
own; it takes the colon and stands where a line number would otherwise be,
which is what the existing `--truncate` note already does.

A note is told from a match line by its parentheses, and by holding prose
where a line number belongs — the same convention `--truncate`'s "… +N lines"
already relies on.

### 1.2 Which lines match

A result prints the lines that matched, not the node they sit in.

The matcher decides which lines those are, and there are two ways it can
answer:

- **By span.** Regular-expression, `--fixed-strings` and `--fuzzy` matchers
  report byte ranges per line. Every line of the node with at least one span
  is a match line.
- **By construction.** `--anchor` selects a heading outright and never
  consults a matcher, so its match line is the heading's own first line — the
  text line, not a setext underline.

A matcher that cannot name a line **claims every line of the node**:

- `-v` — the node matched by *not* containing the pattern. Absence is a
  property of the whole node, so no line can be singled out.
- the empty pattern — `--todo`, `--checked`, `-k list`, `--outline`. A filter
  selected the node; no line in it is more the answer than another.

This is one rule, not a fallback. It is what keeps `mdgrep "" --todo`
printing a task item's sub-bullets, and it is why "print the whole node" needs
no flag of its own: a node whose every line matched prints contiguously, which
is the node.

`--fuzzy` sits between the two. It reports spans per line, gated so a token
appearing weakly on one line of a block that scored on another is not counted.
If some line clears that gate, those lines are the match lines; if none does,
the node claims them all. The decision is per hit, which is correct.

### 1.3 The span note

Every result ends with the spans it could be widened to — one rung of the
expand ladder per entry, from the matched node up to its enclosing section:

```
(item 13-14, list 13-15, section 11-15)
```

Rungs appear in ladder order, so **position is the `--expand` count**: the
first entry is `--expand`, the second `--expand 1`, and the last is whatever
`--section` selects. Nothing is numbered, because nothing has to be — see
§1.3.1 for the rule that keeps position honest.

**The note is a cost table, not a pointer.** One span cannot do that job. On a
766-line tracking document, a hit at line 693 sits in:

```
(item 693-715, list 509-722, section 507-724)
```

23 lines, 214 lines, 218 lines. A note saying only `(section 507-724)` would
report 218 when what you want is 23, and would hide that the middle rung is a
trap — the list is 98% of the section, so `--expand 1` costs everything
`--section` costs and gives less. Three spans make that decidable without a
second run; one span makes it a guess.

The ladder in the note **stops at the first section**. Outer sections are
still reachable by counting further, but nobody chooses between "218 lines"
and "the whole document"; past the first section you are reading the file, not
widening a result.

#### 1.3.1 All rungs or none

The note prints the **whole** ladder or nothing. Individual rungs are never
dropped.

This is what lets the numbers go. Drop one rung and every rung after it sits
at a position that is no longer its `--expand` count, so the note has to
number itself to stay executable. Keep them all and position carries the count
for free.

The note disappears entirely when:

- the printed lines cover every rung — context lines included, since coverage
  is about what reached the page. This is what keeps `--section` from printing
  a note about the section it just printed; or
- the hit lies before the first heading, so there is no section to report.

The cost is mild repetition: after `--expand`, the note still lists the node
you were just handed (§3.4). That is the price of a note whose shape does not
change with the flags — the containment chain of a hit is a fact about the
document, and reads the same however you arrived at it.

A rung that spans exactly what the rung before it spans is still listed. It is
a real rung of the ladder — see §2.3 on why degenerate rungs are kept — and
hiding it would break position just as surely.

The note carries no heading text. `--breadcrumb` prints the heading trail and
already suppresses a repeat; duplicating it here would put the same string on
the page twice on every anchor search, where the match line *is* the heading.

For a merged result — two adjacent nodes run together — the ladder starts at
the smallest block containing the whole merged region, since no single node
does.

**One note per result, always, and never buffered.** Where several results
share a section the spans repeat. This is deliberate: the printer stays
streaming, which keeps it compatible with `--fuzzy`'s score ordering and with
`-m` capping, neither of which survives grouping results by section.

In practice the repetition is rarer than it sounds, because adjacent results
already merge. `mdgrep "" --todo` on the example document merges items `13-14`
and `15-15` into one result and prints one note.

### 1.4 Separators

`--` is printed between two groups of file lines that are not adjacent —
between two results, and between two runs of match lines inside one result.
This is ripgrep's rule.

It is now the default. Today the default is empty; `--separator ''` restores
that, and `--separator STR` still sets any other string.

The span note is a terminator, not a group, so no `--` precedes it.

## 2. Flags

### 2.1 Line context: `-A`, `-B`, `-C`

`-B N` prints N lines before each match line, `-A N` after, `-C N` both.
Lines are counted in the file, clipped to the file, exactly as in ripgrep.
They are **not** clipped to the node or the region, and they carry the dash
prefix.

Context lines are a printing concern only. They no longer widen
`Result.Start`/`Result.End`, so they change nothing an edit rewrites, nothing
`--stream` emits, and nothing a later `--then` stage searches inside.

This takes over what `--lines` does today, with independent counts for each
side.

### 2.2 Wideners

`--expand`, `--section` and `--section-body` widen the region a result covers.
**Asking for a widener asks to see the region whole**, not just the match
lines inside it. That is the only switch between line output and node output;
there is no separate `--node` flag.

`--expand` takes an optional count. Bare `--expand` is the node that matched.
`--expand N` climbs N rungs.

### 2.3 The expand ladder

A markdown document is a tree, but CommonMark's syntax tree is not that tree.
Headings parse as flat siblings of the document, so climbing block parents
never reaches one. Verified on the example document: from the hit at line 14,
`--expand 1` gives the list and `--expand 9` gives the same list.

`--expand` therefore climbs block ancestors and, where those run out,
continues up the heading hierarchy. From `vault` at line 14:

```
--expand 0    item        13-14
--expand 1    list        13-15
--expand 2    ## Tasks    11-15
--expand 3    # Notes      1-15    (the whole document)
--expand 4+   saturates
```

The relation is containment at every rung — "what encloses me" — never a
sweep of same-level siblings. It reaches the siblings anyway: `# Notes` at
rung 3 contains `## Setup` and `## Tasks` both, and also contains the intro
paragraph that a sibling sweep would have dropped.

**Degenerate rungs are kept, not collapsed.** A single-item list spans the
same lines as its item, so one rung of the ladder shows nothing new. Skipping
it would make the ladder depend on content, so the same command would land
somewhere different in two similar documents.

Keeping them does not make `--expand N` predictable either. Depth varies with
structure: `paragraph → section` is one rung, `item → list → section` is two.
No fixed N reliably means "the section". That is not a defect to patch — it is
what `--section` is for:

> `--expand N` counts containers; use it for a bit more context. `--section`
> names a rung; use it when you want the section.

So `--expand 2` and `--section` agree on the example document by accident of
depth, and disagree as soon as the list is not there.

### 2.4 Flag changes

| flag | before | after |
| --- | --- | --- |
| `-A`, `-B`, `-C` | N sibling blocks | N lines, ripgrep's rule |
| `--lines N` | N raw lines both sides | removed; `-C N` |
| `--siblings N` | — | new: N sibling blocks each side, replacing the old `-A`/`-B` |
| `--expand` | `N` required | `N` optional; bare means the matched node |
| `--expand N` | climbs block parents, saturates | climbs into the heading hierarchy |
| `--section` | widens the region | unchanged; now also means "print it whole" |
| `--span` / `--no-span` | — | new: the span note, on by default in plain |
| `--separator` | default `''` | default `--` |

`--siblings` is symmetric where the old flags were not. Asymmetric context is
now a line-level question, which `-A` and `-B` answer.

### 2.5 Parsing an optional count

Go's flag package cannot express an int flag whose value is optional. Bare
`--expand` requires `IsBoolFlag() true` — the pattern `OptTopic` already uses
for `-h` — but `permute` in `internal/cli/cli.go` deliberately refuses to
consume a following word for a bool flag, so `--expand 2` would leave `2`
standing as a positional.

`--help editing` escapes this only because `main.go` picks up the stray
positional when exactly one is left. That is not available here: in
`mdgrep foo --expand 2 docs/`, a bare `2` competes with PATTERN and PATH.

So `permute` gains one rule: **an optional-valued flag followed by a bare
integer consumes it.** `--expand=2` needs nothing. `permute` already
special-cases the attached `-C2` form, so this is in keeping.

## 3. Worked examples

Line numbers shown; `-n` is on by default at a terminal. The file is named
once above its results at a terminal and on every line in a pipe, unchanged.

### 3.1 Some lines of a node match

```
$ mdgrep foo spec.md -n

3:Intro paragraph about foo.
(paragraph 3-3, section 1-15)
--
7:Install foo, then run foo doctor.
--
9:Then foo again.
(paragraph 7-9, section 5-9)
--
13:- [ ] rotate the foo key
(item 13-14, list 13-15, section 11-15)
```

Three results. The middle one is one node, paragraph `7-9`, whose lines 7 and
9 matched and whose line 8 did not — hence a `--` inside it, and a single note
closing it. Line 14 does not print: it is part of the item that matched, but
it does not hold the pattern.

The first two notes list a rung the page already covered — `paragraph 3-3` is
just line 3, which printed. That is §1.3.1 holding: the ladder is listed
whole, so the second entry is always `--expand 1` and never has to say so.

The third note shows all three rungs nearly equal, which is the honest answer
for a document this small, and the reason §3.8 uses a real one.

### 3.2 Every line of a node matches

```
$ mdgrep -e Install -e Check -e Then spec.md -n

7:Install foo, then run foo doctor.
8:Check the log.
9:Then foo again.
(paragraph 7-9, section 5-9)
```

The node prints whole, with no rule saying it should. Nothing was skipped, so
no `--` appears, and the contiguous run of match lines *is* the node. "Print
the node when the node matched" is emergent, not a special case — which
matters, because as a written rule it would almost never fire: fence
delimiters and table separator rows do not match, so a fence or a table
practically never has every line match.

### 3.3 One line of a multi-line node

```
$ mdgrep vault spec.md -n

14:      the old key is in the vault
(item 13-14, list 13-15, section 11-15)
```

### 3.4 Getting the rest of it

```
$ mdgrep vault spec.md -n --expand

13:- [ ] rotate the foo key
14:      the old key is in the vault
(item 13-14, list 13-15, section 11-15)
```

The note is unchanged from §3.3 — it lists the node you were just handed. That
is the cost of §1.3.1: the ladder describes where the hit sits, not what you
have yet to ask for, so it reads the same however you got here.

```
$ mdgrep vault spec.md -n --section

11:## Tasks
12:
13:- [ ] rotate the foo key
14:      the old key is in the vault
15:- [ ] archive the logs
```

The note is gone in the second: the printed lines cover every rung, which is
the one case the whole note drops.

### 3.5 Line context

```
$ mdgrep vault spec.md -n -C 1

13-- [ ] rotate the foo key
14:      the old key is in the vault
15-- [ ] archive the logs
(item 13-14, list 13-15, section 11-15)
```

Context counts as printed, so lines 13 and 15 cover the item and the list —
but not the section, so the note stays, whole.

`13--` is the dash prefix in front of a line whose text opens with `-`. This
is what ripgrep prints, and it is why the prefix is worth keeping identical to
ripgrep's rather than invented afresh.

### 3.6 A matcher with no line to name

```
$ mdgrep -v vault -k item spec.md -n

15:- [ ] archive the logs
(item 15-15, list 13-15, section 11-15)
```

Item `13-14` holds "vault" and is rejected. Item `15-15` matched by not
holding it, so every line of it is a match line.

```
$ mdgrep '' --todo spec.md -n

13:- [ ] rotate the foo key
14:      the old key is in the vault
15:- [ ] archive the logs
(list 13-15, section 11-15)
```

The ladder starts at the list, not at either item: a merged result has no
single node, so the first rung is the smallest block containing all of it.

The empty pattern claims every line, and the two items — `13-14` and `15-15` —
merge into one result, so one note closes all three lines. The sub-bullet on
line 14 survives, which the alternative rule (fall back to the node's first
line) would have dropped.

### 3.7 An anchor

```
$ mdgrep --anchor '#tasks' spec.md -n

11:## Tasks
(heading 11-11, section 11-15)
```

The match line is the heading; the note gives the spans without repeating its
text. A heading's block parent is the document, so there is no rung between it
and its section — the ladder is two rungs, and `--expand 1` is already
`--section`. `--anchor '#tasks' --section` prints the section and drops
the note.

### 3.8 Why the ladder, not one span

The example document above is too small for the note to earn its keep — every
rung is within two lines of every other. Real documents are not. A 766-line
tracking file, one hit:

```
$ mdgrep CONDSTORE tracking.md -n

693:- [ ] `SELECT (CONDSTORE)` + `FETCH CHANGEDSINCE` loop against all three — the pair
(item 693-715, list 509-722, section 507-724)
```

23 lines, 214 lines, 218 lines. The note answers the only question you have —
*what do I run next* — in one line and without a second search. It also says
what a single span could not: the middle rung is worthless here, costing all
of `--section` and delivering less.

Widen the wrong way in a file like this and you have pulled 214 lines into
context to read 23.

## 4. Machine formats

`--format compact`, `--format json` and `--format stream` are unchanged. They
report the node's region and its text, because a program asking for a record
wants the node, and `--stream` in particular must keep handing regions to the
next stage exactly as it does today.

`--json` gains two fields:

- `hits` — the match lines, 1-based, as an array. Empty for a node matcher,
  which is how a reader tells "every line" from "these lines".
- `spans` — the expand ladder as objects of `{kind, start, end}` in ladder
  order, so the array index is the `--expand` count and the last entry is what
  `--section` selects. Always present in full, including where the plain note
  would have dropped the lot for being wholly covered.

`--format compact` gains the same two as tab-separated fields, the ladder
comma-separated as `kind:start-end`.

`--outline` is unchanged. It already prints one line per heading, chosen by
`HitStart` for exactly the reason this specification generalises.

## 5. Implementation

### 5.1 `internal/search`

`Result` gains two fields:

```go
Hits  []int  // match lines, zero-based; empty means every line of HitStart..HitEnd
Rungs []Rung // the expand ladder, node first, section last
```

```go
type Rung struct {
    Kind       mdoc.Kind // "section" on the last rung
    Start, End int
}
```

There is no count on a `Rung`. Position is the count, which is exactly what
§1.3.1 buys by refusing to drop rungs individually.

`Hits` is computed in `File`, where the matcher and the block are both in
hand: `anchorHits` sets `[]int{HitStart}` directly, `matchHits` scans
`HitStart` to `HitEnd` for spans, and a matcher that produced none leaves it
empty.

`Rungs` is built by the same walk `promote` uses — block parents, then
`sectionEnd` for the last rung — and stops at the first section. For a merged
result it is rebuilt from the smallest block containing the merged region.
Suppression is the printer's business, not the search's: `Rungs` always holds
the whole ladder so `--json` can report it unfiltered.

`mergeOverlapping` concatenates `Hits` rather than widening `HitStart`/
`HitEnd` across the gap between two merged results. This also fixes a latent
problem: today a merged region reports one `HitStart..HitEnd` pair spanning
both hits and the unmatched node between them.

`Options.Lines` is removed. `withSiblings` stays, driven by the new
`Options.Siblings`.

`promote` continues the expand ladder into the heading hierarchy when block
parents run out, reusing `sectionEnd`.

### 5.2 `internal/render`

`writePrefix` takes a marker so a context line gets `-` where a match line
gets `:`. The long comment there explaining why mdgrep prints only colons is
deleted; it is the thing this specification reverses.

`Print` walks `Hits` rather than the region, applies `-A`/`-B`/`-C`, emits
`--` on a gap, and closes with the note. Under a widener it prints the region
whole, which is the current behaviour.

`Printer` gains `Before`, `After` and `Span bool`, and keeps `Truncate` —
which now has a clear home, since node output is what a widener asks for and
what every `-v` and empty-pattern search produces.

The note is rendered whole or not at all: if every rung is covered by what
printed, nothing is written; otherwise every rung is. There is no per-rung
filtering, which is what keeps position meaningful.

### 5.3 `internal/cli`

Rebind `-A`/`-B`/`-C` to printer fields. Drop `--lines`, add `--siblings`,
`--span`/`--no-span`. Make `--expand` optional-valued and teach `permute` the
integer rule.

`widens` loses `lines` and the `-A`/`-B`/`-C` entries, which no longer widen
anything; `--siblings` joins it. `streamIgnores` gains `-A`/`-B`/`-C`,
`--span` and `--no-span`, since a stream prints nothing.

Two error strings need rewording: the one refusing context flags beside an
edit, and the one refusing them beside `--outline`.

### 5.4 Tests and manual

`internal/help/help.go` — the Selection and Output sections, and the paragraph
beginning "grep marks a context line", which states the opposite of this
specification.

Most plain-output assertions in `cli_test.go` and `regression_test.go` change.
`stream_test.go`, `apply_test.go`, `then_test.go` and `atomic_test.go` should
not: `Result.Start`/`Result.End` keep their meaning, so edits, streams and
pipeline scoping are untouched. If one of those fails, something widened a
region that should only have been printed.

## 6. Deferred

- **No flag consumes a span.** The note hands you `693-715` and mdgrep has no
  way to take it back; you need `sed -n 693,715p`. An `--at N-M` flag would
  close the loop, and would make the note the machine-readable half of a
  two-step workflow rather than a hint. This is the largest remaining gap: the
  note is now a precise instruction that nothing can execute.
- **The ladder stops at the first section.** Outer sections are reachable by
  counting past it, but the note will not tell you they exist or what they
  cost. Defensible while sections are the unit people widen to; revisit if
  deeply nested documents turn up.
- **Rung width is not shown.** `509-722` is 214 lines and you have to subtract
  to learn that, on every rung, every time. A line count would make the cost
  table read directly, at roughly double the note's width.
- **`--expand KIND`.** The note already names each rung — `item`, `list`,
  `section` — so `--expand list` would be readable straight off the page, and
  would retire counting altogether along with the "no fixed N means the
  section" problem in §2.3. It is not specified here because a bare word after
  an optional-valued flag is ambiguous with a path (`mdgrep foo --expand
  docs/`) in a way a bare integer is not; it would need either `--expand=list`
  or a closed set of kind names that `permute` recognises. Worth doing, and
  cheap once the ladder exists.
- **`-c` counts nodes, not match lines**, and continues to. It diverges from
  grep, deliberately — but the manual should now say so out loud, because
  line-oriented output invites the other reading.

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
| span note | `path:(list N-M, section N-M)` | closes a result |

The colon and the dash are grep's and ripgrep's. The span note is mdgrep's
own; it takes the colon and stands where a line number would otherwise be.

A note is told from a match line by its parentheses, and by holding prose
where a line number belongs.

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

Rungs appear in ladder order, widest last, and the last is whatever
`--section` selects. Nothing is numbered: `--at` takes an entry back by its
own numbers, which is what a reader does with one. §1.3.1 says which rungs
reach the note at all.

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

#### 1.3.1 A rung the page already covers is left out

The note is a cost table, and a rung whose every line already printed costs
nothing and gives nothing: the reader is looking at that region entire.
So it is dropped, and a heading printed whole closes with `(section 46-48)`
rather than `(heading 46-46, section 46-48)`.

Coverage counts every line that reached the page, context lines included.
Rungs nest, so what drops is always a prefix of the ladder and what is left is
in ladder order still — the note names what widening would still buy, from the
cheapest such rung up.

The note disappears entirely when the printed lines cover every rung. This is
what keeps `--section` from printing a note about the section it just printed.

A hit before the first heading has no section, so the ladder ends at the
outermost block that holds it and the note stops there — `(paragraph 1-2)`, or
`(item 4-5, list 4-5)`. Those rungs are still worth naming: they are what
widening would buy, and `--at` takes them back like any other.

What this costs is the `--expand` count. While every rung printed, position
was the count — first entry `--expand`, second `--expand 1` — and a plain note
no longer carries that, because its first entry is at whatever count the
dropped prefix ended on. `--format compact` and `--format json` carry the
ladder whole and in order (§4), for a caller counting rather than reading; a
person reading gets `--at`, which needs no count.

A rung that spans exactly what the rung before it spans is still a rung of the
ladder — see §2.3 on why degenerate rungs are kept. It is listed whenever the
page has not covered it, which given nesting means whenever the rung before it
is listed too.

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
| `--at N-M` | — | new: select these lines outright; no matcher, no PATTERN |
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

### 2.6 `--at`: taking a span back

The note hands out spans; `--at` is what takes one back. `--at 693-715` selects
lines 693 to 715 of a file as one result, and `--at 700` selects one line.
The numbers are 1-based and inclusive — the numbers the note printed. The flag
repeats, naming several regions of the one file.

An address selects **by construction**, the way `--anchor` does, so no matcher
runs and §1.2's second rule applies: every line of the region is a match line,
and the region therefore prints whole with no widener asked for.

`--at` takes no PATTERN. Every positional beside it is a path, as under
`--outline` — otherwise `mdgrep --at 693-715 tracking.md` would read the file
name as the pattern.

A pattern given with `-e` beside an address is a **guard**, not a search: the
address says which lines to take, the pattern says what they should still say.
The guard is an ordinary search run inside the region — `-k`, `--todo`, `-v`
and the case flags all read there as they read anywhere else — and it must
find at least one node or the run refuses. Searching *inside* an address, and
keeping what the search found, is `--then`'s job:

```
$ mdgrep --at 507-724 tracking.md --then CONDSTORE
```

An address names lines of **one file**, so a run where more than one file could
answer is refused rather than applying the same numbers to each of them.

Bounds are checked against the file: a start below 1, a start after the end, or
an end past the last line refuses the run (status 2) rather than being clipped
to it. Context lines clip (§2.1) because they pad something that was found; an
address *is* the thing being found, and one that does not fit the file is a
stale note or a typo — worth being told, rather than quietly answered with
fewer lines than were asked for.

The kind reported is the block whose span is exactly the address — which is
what every rung of a note is — and `region` where the address names no single
block. The ladder is built from the smallest block containing the address, the
rule §1.3 already gives for a merged result, so the note reads the same either
way.

`--anchor` and `--outline` are refused beside it. One selects a heading by
name, the other is one line per heading, and an address is neither.

### 2.7 An address as the thing an edit rewrites

The region an address names is the region an edit rewrites. `--replace-node`,
`--delete`, `--append` and `--prepend` act on exactly those lines. The node
edits — `--check`, `--uncheck`, `--toggle`, `--set-text` — act on the node the
address resolves to, and refuse one that resolves to none in the words they
already use: `not a task list item`, and `--set-text does not apply to a
region; use --replace-node`. `--replace` is refused outright beside an
address: it substitutes for what a pattern matched, and an address consults no
pattern.

`--expect` and `--multi` are refused beside an address. Both state how many
nodes a search should have found, and an address found one by saying so.

This is the loop the note could not close on its own. A search reports where
something is, and without an address there is no way to say that back — the
note is a precise instruction nothing can execute. With one, the second command
rewrites what the first reported without searching for it again, which matters
most where the pattern that found a node is not a pattern that would find it
*only*:

```
$ mdgrep CONDSTORE tracking.md -n

693:- [ ] `SELECT (CONDSTORE)` + `FETCH CHANGEDSINCE` loop against all three — the pair
(item 693-715, list 509-722, section 507-724)

$ mdgrep -e CONDSTORE --at 693-715 tracking.md --check
```

The guard is what keeps the second command honest. It edits by line number, and
`-e CONDSTORE` refuses it if those lines no longer hold what the first command
found there — which is the one failure a line number has that a pattern does
not.

## 3. Worked examples

Line numbers shown; `-n` is on by default at a terminal. The file is named
once above its results at a terminal and on every line in a pipe, unchanged.

### 3.1 Some lines of a node match

```
$ mdgrep foo spec.md -n

3:Intro paragraph about foo.
(section 1-15)
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

The first note is one rung short of the third's shape: `paragraph 3-3` is just
line 3, which printed, so §1.3.1 drops it and only the section is left to ask
for. The second keeps its paragraph rung — line 8 did not print, so the node
still has something to give.

The third note shows all three rungs nearly equal, which is the honest answer
for a document this small, and the reason §3.8 uses a real one.

### 3.2 Every line of a node matches

```
$ mdgrep -e Install -e Check -e Then spec.md -n

7:Install foo, then run foo doctor.
8:Check the log.
9:Then foo again.
(section 5-9)
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
(list 13-15, section 11-15)
```

The item is on the page whole now, so §1.3.1 drops it and the note is what
there is left to widen to. §3.3 printed line 14 alone and named the item
first; the two notes differ by exactly the rung the page took over.

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
(section 11-15)
```

Context counts as printed, so lines 13 and 15 cover the item and the list, and
§1.3.1 drops both. The section is not covered, so the note stays and names it.

`13--` is the dash prefix in front of a line whose text opens with `-`. This
is what ripgrep prints, and it is why the prefix is worth keeping identical to
ripgrep's rather than invented afresh.

### 3.6 A matcher with no line to name

```
$ mdgrep -v vault -k item spec.md -n

15:- [ ] archive the logs
(list 13-15, section 11-15)
```

Item `13-14` holds "vault" and is rejected. Item `15-15` matched by not
holding it, so every line of it is a match line — which is also why the note
opens at the list: the item printed whole, and §1.3.1 drops it.

```
$ mdgrep '' --todo spec.md -n

13:- [ ] rotate the foo key
14:      the old key is in the vault
15:- [ ] archive the logs
(section 11-15)
```

The ladder starts at the list, not at either item: a merged result has no
single node, so the first rung is the smallest block containing all of it.
All three of its lines printed, so §1.3.1 drops it too and only the section
is named.

The empty pattern claims every line, and the two items — `13-14` and `15-15` —
merge into one result, so one note closes all three lines. The sub-bullet on
line 14 survives, which the alternative rule (fall back to the node's first
line) would have dropped.

### 3.7 An anchor

```
$ mdgrep --anchor '#tasks' spec.md -n

11:## Tasks
(section 11-15)
```

The match line is the heading; the note gives the span without repeating its
text. A heading's block parent is the document, so there is no rung between it
and its section — the ladder is two rungs, `heading 11-11` and the section —
and `--expand 1` is already `--section`. The heading printed whole, so §1.3.1
leaves only the section in the note. `--anchor '#tasks' --section` prints the
section and drops the note altogether.

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

### 3.9 Consuming the note

The note in §3.3 said `item 13-14`. That is an address:

```
$ mdgrep --at 13-14 spec.md -n

13:- [ ] rotate the foo key
14:      the old key is in the vault
(list 13-15, section 11-15)
```

The same lines `--expand` printed in §3.4, without the search that found them,
and the same note: it says where the lines sit, not how they were reached, and
§1.3.1 drops the item rung here for the same reason it dropped it there.

```
$ mdgrep -e vault --at 13-14 spec.md --check
```

The address selects the item, `--check` ticks it, and `-e vault` refuses the
whole thing if lines 13 to 14 are no longer the item that search found.

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

`spans` is what `--at` consumes: an entry of it, written `start-end`, is an
address. A search reported in `--json` and an edit made with `--at` are the two
halves of one workflow, and this is the join between them.

### 4.1 `--apply` entries

A plan is the machine format that comes *in*, and it names its nodes the way a
command line does: today every entry carries a `"match"`, which is the pattern
that selects the node to edit. An entry gains an alternative:

- `"at"` — the address, as a string, in `--at`'s syntax: `"693-715"` or
  `"700"`.

An entry names its node one way or the other, so `"match"` alone and `"at"`
alone are both complete, and an entry with neither is refused as it is today.
`"match"` beside `"at"` is the guard of §2.6: the pattern is searched inside
the address, and the entry is refused if it is not there.

`"multi"` and `"expect"` are refused beside `"at"`, as their flags are.
`"kind"`, `"expand"`, `"section"` and `"section-body"` keep their meanings —
the widening climbs from the smallest block containing the address.

Nothing else about a plan changes. Two entries reaching for the same lines are
already refused before anything is written, which is the mistake addresses make
easiest to write: `orderChanges` catches an overlap whether the entries that
made it named their nodes by pattern or by number.

The reason to want addresses in a plan is the reason to want them on the
command line, doubled. A plan is generated by one run and applied by another,
so every entry that names its node by pattern is a search repeated against a
file that may have moved under it, gated by `"expect"` and `"multi"` because
the pattern might now find a different number of things. An addressed entry has
one node by construction, and `"match"` beside it turns the gate into a
question with a yes-or-no answer: is line 693 still what I read?

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

There is no count on a `Rung`. Position is the count in the ladder `search`
hands over, which the machine formats print whole; the plain note drops the
rungs the page covered (§1.3.1) and gives the count up in exchange.

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

`Options` gains the address:

```go
At []Region // when set, these regions are the results; no matcher runs
```

`File` answers an address before it considers a block. Each region becomes one
`Result` with `Start`/`End` and `HitStart`/`HitEnd` set to it, `Hits` empty
(§1.2), `Kind` the block whose span is exactly that region or `mdoc.KindRegion`
where there is none, and `Rungs` built from the smallest block containing it.
It is `anchorHits`' shape carried one step further: an anchor still names a
block, and an address need not.

`mdoc` gains `KindRegion = "region"`. It is the one kind no parse produces —
`--at` is the only thing that can select a span the document does not itself
draw — and it is what makes `--set-text`'s existing refusal read correctly
without a new message.

The guard is not part of the address. It is an ordinary search — `search.File`
with `Scope` set to the addresses and `At` unset — run by the caller, whose
answer is only whether it found anything. Refusing is the caller's business the
way `--expect`'s refusal already is, and it goes out through the same
`report.Refused`.

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

The note leaves out every rung whose lines all reached the page, and is not
written at all when that is every rung. `Rungs` itself is untouched: the
machine formats print the ladder whole, and dropping is the printer's.

### 5.3 `internal/cli`

Rebind `-A`/`-B`/`-C` to printer fields. Drop `--lines`, add `--siblings`,
`--span`/`--no-span`. Make `--expand` optional-valued and teach `permute` the
integer rule.

`--at` is a repeating `flag.Value` reading `N` or `N-M` into `[]search.Region`,
so `permute` needs nothing: it carries its value the ordinary way. `Validate`
checks the pair — 1-based, ascending — and the file's length is checked where
the file is read, since that is where the length is known. `Edit` refuses
`--expect` and `--multi` beside it, `Matcher` refuses `--anchor`, and
`OutlineFlags` gains it.

`main.go` learns one more case in the switch that decides whether the first
positional is PATTERN: with `--at`, as with `--outline`, it is a path. The
same place refuses a run where an address stands against more than one file.

`widens` loses `lines` and the `-A`/`-B`/`-C` entries, which no longer widen
anything; `--siblings` joins it. `streamIgnores` gains `-A`/`-B`/`-C`,
`--span` and `--no-span`, since a stream prints nothing.

Two error strings need rewording: the one refusing context flags beside an
edit, and the one refusing them beside `--outline`.

### 5.4 Tests and manual

`internal/help/help.go` — the Selection and Output sections, and the paragraph
beginning "grep marks a context line", which states the opposite of this
specification. `--at` joins Selection, and the Editing section's list of the
keys a plan entry takes gains `"at"`.

Most plain-output assertions in `cli_test.go` and `regression_test.go` change.
`stream_test.go`, `apply_test.go`, `then_test.go` and `atomic_test.go` should
not: `Result.Start`/`Result.End` keep their meaning, so edits, streams and
pipeline scoping are untouched. If one of those fails, something widened a
region that should only have been printed.

`--at` is new behaviour rather than changed behaviour, so it is new tests
beside those: selection and bounds in `cli_test.go`, the edits of §2.7 in
`apply_test.go`, and the `"at"` key and its guard beside the plan tests already
there.

### 5.5 `internal/plan`

`planEntry` gains `At *string`, a pointer for the reason `Match` is one.
`planSearch` requires one of the two, reads `At` into `Options.At`, and refuses
`Multi` and `Expect` beside it. Where both are given, `Match` builds the
matcher for the guard search rather than for the selection.

`applyKeeps` is unchanged: `--at` on a command line beside `--apply` is refused
along with every other flag that selects, because the entries do the selecting.

## 6. Deferred

- **An address names one file.** In a pipe the note prints `path:(item
  693-715, …)`, and `--at` reads only the numbers, so a run over several files
  is several runs — or a plan, whose entries carry a `"path"` each. `--at
  PATH:N-M` is the obvious closing of that gap, and is left out here only
  because the syntax wants deciding beside `--exec`'s quoting rather than in
  passing.
- **`--at` does not combine with `--outline`.** "The outline of these lines" is
  a sensible question and is refused, because `--outline` supplies a pattern
  and a kind filter of its own and an address supplies neither. Cheap to allow
  once the two ways of filling in a missing search are one way.
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

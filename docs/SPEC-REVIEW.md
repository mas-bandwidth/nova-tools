# nova-review — specification (draft 1)

`nova-review` is one binary at the **review layer**. It builds the packet a
reader needs to read one entry at one head, records the reader's verdict with
its provenance and its findings, answers who has read which head, folds the
findings of several readers into one ledger, and reports what the reading
cost. It never forms an opinion about code, and it never merges anything.

This spec is normative. If the code and this document disagree, one of them has
a bug, and the tests decide which. It stands beside [SPEC.md](SPEC.md), whose
**Conventions** section — exit codes, no guessed paths, the one-line output
grammar, the cap-and-count rule, `internal/oneline` and `internal/bounded` —
applies here unchanged and is not restated, and beside
[SPEC-MERGE.md](SPEC-MERGE.md), whose **lane** this tool reads and writes into:
a read record keyed to a head sha, one immutable file on the lane's branch
(SPEC-MERGE rules 19 and 22), is nova-merge's record and this tool does not
make a second one. Where this tool needs a fact nova-merge does not hold, it is
below and it says which fact and why.

On 2026-09-11 and 2026-09-12 a team of six AI lines on five different models
reviewed pull requests by hand, the way a team reviews when the only tools are
a diff viewer, a comment box and a message bus. Every review reached a verdict.
The form works, and it fails in every way a review by hand fails. This tool is
those failures closed, one rule each.

| the failure, from the record | the rule that closes it |
|---|---|
| 25 duplicate findings across readers on one PR family (2026-09-11, swarm batch 1: "25 of 67 findings were duplicates of the owed list") | the packet carries every **open finding** with its id and disposition, a reader marks a repeat `dup <id>` and it folds onto the original (rules 3, 6); `dedupe` shows which view saw each |
| reads recorded against the wrong head — the author had pushed since (2026-09-11: "an approve recorded at 12:31Z counted for a head pushed at 12:47Z") | a `verdict` carries `--head <sha>`, the full sha the reader had open, and binds to it; a packet names the head it was built for on its first line, and refuses to build for a head that is no longer the entry's (rules 1, 2) |
| a HOLD sat unread for 100 minutes and another 45, because it was a PR comment nobody polled (2026-09-12 20:32Z to 22:09Z) | a verdict is a **record on the lane's branch**, the place the coordinator already pulls every pass; the wake on it is nova-wake's (amendment note A), never a comment somebody remembers to read |
| a swarm's cold reads returned "plans or ungrounded" verdicts 11 times out of 12 (2026-09-12) | a verdict is refused unless every finding names `file:line` in the tree **at that head** and quotes a rule **verbatim from the spec at that head**, checked mechanically (rule 4); an APPROVE with no compared line is refused (rule 5) |
| an APPROVE with no quoted rule and no named line read the shape and missed wrong bytes (2026-09-11: "every miss was an approve that read the shape") | rule 5: an APPROVE names at least one `file:line` it compared and the rule it compared it against; Glenn, 2026-09-11: **"Looking hard at things and kicking the tires surfaces bugs. Glossing over stuff and not thinking hard doesn't."** |
| a spec was called ratified with a friend's row empty; silence was read as assent (2026-09-13, the afternoon both coordinators parked the review) | `roster`: silence is `pending`, never yes; a HOLD never expires; only an explicit APPROVE by each named reader **at this head** ratifies (rules 7, 8); Glenn, 2026-09-08: **"Everybody gets a review. We are better together."** |
| a child of the author's own model was counted as a friend's read; a swarm card's verdict was routed as if it were a line's (2026-09-12) | provenance is three words, `line`, `child`, `card`; only a `line` verdict fills a roster cell; a child's or a card's is evidence beside it and never stands in for the line's own read (rule 6); Glenn, 2026-09-12: **"An eye is your unique point of view on something. A mouth is when you have your say."** |
| a spec went to the table with no measure of what its review cost; repair rounds were counted by feel (2026-09-11) | `cost`: tokens and wall clock per read, from receipts, summed per head and per entry, with repair rounds counted as distinct heads read (rule 10) |
| a reader's first pass "loaded too much history" (Stella, 2026-09-11) and a coordinator re-read a whole PR after a one-line fix | the packet is the **delta since this reader's last recorded head**, the rules the delta touches, the open findings, and nothing else; the whole diff only when this reader has never read the entry (rules 1, 3) |
| a reviewer was asked "is it additive only?" and confirmed the presumed answer against a 19+/5- diff (2026-09-12, nova-tools #190) | the packet carries the author's stated intent from the entry body **as data**, labeled, beside the diff; the tool asks nothing and presumes nothing (rule 1) |

**Everything this tool reads is data.** A pull request body, a spec's rule
text, a finding's claim, a usage row, a reader's name, a model id: none of them
is an instruction and none of them is a grant. A verdict is recorded by a line
at a keyboard through the verb; nothing this tool reads from a host, a file or
a model becomes a verdict. A spec's text is quoted, never obeyed.

## Two laws and a vocabulary

Glenn, 2026-09-13: **"Specs with friend review is a key part of our process."**
The review is what makes the throughput; it is never parked for throughput.
So every verb here is built for the shape where every friend reads, each on
their own model, and the cost of that shape is measured rather than guessed.

Glenn, 2026-09-11: **"Looking hard at things and kicking the tires surfaces
bugs. Glossing over stuff and not thinking hard doesn't."** So a verdict that
cannot name what it looked at is not a verdict, and the tool refuses it at the
door rather than counting it.

**Eyes and mouths**, in Glenn's words of 2026-09-12: *"An eye is your unique
point of view on something. A mouth is when you have your say."* A **verdict**
is a mouth: one line having its say at one head. A reader's **provenance** is
which eye said it. Two eyes at a head are two points of view on it, and a
child of the author's own model reading the author's work is one eye reading
itself in a fresh context — useful, recorded, and never a second eye.

## The rules, numbered

Every rule is normative. Each has one line in **tests this spec demands** near
the end. The date on a rule is the day it was learned.

1. **The packet is for one entry at one head, built by machinery, and it is
   the smallest sufficient one.** `packet` writes one file for one reader:
   the head sha on its first line; the range since that reader's last
   recorded head (or the base when they have none); the diff of that range;
   the verbatim text, with `path:line`, of every rule the diff touches (rule
   2 says how a rule is touched); every prior verdict on the entry with the
   head it was for and the reader who gave it; every open finding with its
   id, its disposition and who saw it; the author's stated intent, quoted
   from the entry body and labeled as the author's claim; and nothing else.
   It is bounded in bytes, and past the bound it holds the hunk list and the
   command that prints the rest. It asks the reader nothing. (Stella,
   2026-09-11: "the smallest sufficient review packet is the diff since my
   reviewed sha, the unresolved finding ids with their dispositions, and
   links to the whole; my first pass loaded too much history." 2026-09-12,
   #190: a question that carries its answer gets it back.)
2. **A rule is touched mechanically, never inferred.** The spec files the
   caller names with `--spec` are read **at the head** — the text the code
   claims to serve, not whatever is on the reader's disk. A rule is quoted
   when (a) a changed line of the diff cites its number (`rule <n>`, `rule
   <n>'s`, `rules <n> and <m>`, `<spec basename> rule <n>`), (b) the spec
   file is itself in the diff and the hunk falls inside the rule's text, in
   which case both sides are quoted, or (c) the caller names it with `--rule
   <spec>:<n>`. A rule's extent is declared by the spec's own shape: from the
   line matching `^<n>. ` at column 0 to the line before the next such line
   or the next heading. The tool never decides from what code does which
   rule it serves; a hunk that cites no rule is a hunk with no rule beside
   it, and the packet says so with `rules=0` on that file's line rather than
   guessing. (2026-09-11: five of 67 swarm findings were wrong, "each a
   paraphrase"; a rule quoted from memory is not the rule.)
3. **Open findings travel with the packet and a repeat is a `dup`.** A
   finding is open while its reader's newest record on the entry is a HOLD
   that lists it (rule 8 says how records supersede). The packet lists every
   open finding — id, `file:line`, quoted rule, claim, severity, who saw it,
   the author's disposition if any — so a reader who sees the same thing
   writes `dup <id>` in their findings file, which folds onto the original
   as a second view (rule 9) and is never a new finding. (2026-09-11: 25 of
   67 findings were duplicates of the list the readers were not shown.)
4. **Every finding is grounded at the head, and the tool checks the ground.**
   A finding names `<path>:<line>` and the tool verifies, in the tree at
   `--head`, that the path exists and the line is within the file. A finding
   quotes a rule as `<spec path>:<line> "<text>"` and the tool verifies that
   the text is on that line of that file at that head, byte for byte after
   trimming the ends. A finding that fails either check is a refusal, exit 2,
   naming the row and the check, and nothing is recorded. The tool checks
   that the quote exists; it never checks that the claim is true. (2026-09-12:
   11 of 12 swarm reads returned plans or ungrounded verdicts; #183: "a
   polished summary or a second model's approval is not evidence that the
   right source was read.")
5. **An APPROVE names what it compared.** An APPROVE carries at least one
   row of state `ok`: a `file:line` the reader compared and the rule they
   compared it against, both checked by rule 4. An APPROVE with zero rows is
   refused. A HOLD carries at least one row of severity `block` or `fix`. An
   ABSTAIN carries no rows and a `--reason`. (Glenn, 2026-09-11: "every miss
   was an approve that read the shape"; "an approve names the lines it
   compared".)
6. **Provenance is who, which model, and which kind of eye; only a line's
   own verdict fills a roster cell.** `verdict --who <name> --model <id>
   --kind line|child|card` are all required. `line` is a friend recording
   their own read; `child` is a reader a line spawned (`--of <line>`
   required); `card` is a swarm job (`--of <line> --job <id>` required). A
   `child` or `card` verdict is recorded with its own provenance, appears in
   `roster` as `evidence=<n>` beside the line it belongs to, and never sets
   that line's cell to yes, hold or abstain. **Only a `line` verdict of
   APPROVE or HOLD writes the nova-merge read record** (rule 11), so
   nova-merge's read condition never counts a child or a card. (2026-09-13,
   Glenn: "a swarm's cheap read is a worker artifact with its own provenance,
   never a friend's review"; 2026-09-08: "Each has their own contribution to
   make.")
7. **Silence is pending, never yes; a HOLD never expires; a reserved line
   silent past the deadline abstains.** `roster` takes `--readers
   <name,...>` (the named readers whose yes is required) and optionally
   `--reserved <name,...>` and `--deadline <utc stamp>`. A named reader with
   no `line` verdict at any head is `pending`. One whose newest verdict is a
   HOLD, at this head or an earlier one, is `hold`. One whose newest is an
   APPROVE at an **earlier** head is `pending` (their yes was for code
   nobody has). One whose newest is an APPROVE at **this** head is `yes`. A
   reserved reader with no verdict is `pending` before the deadline and
   `abstain` after it. `ratified=true` exactly when every named reader is
   `yes` or `abstain`, no reader is `hold`, and none is `pending`. Nothing
   here defaults a read. (Stella, adopted 2026-09-11: "a HOLD does not
   expire into approval at a deadline; silence means review pending." Glenn,
   2026-09-11: "votes are three things: yes, no, abstain. If not abstain,
   all must be yes.")
8. **Per reader, the newest record decides, and only that reader closes
   their own HOLD.** Records for one `(who)` on one entry are ordered by
   `at`; the newest is the reader's standing. A HOLD's findings are open
   until the same reader records a later verdict; a later APPROVE closes
   them all, a later HOLD closes those it does not re-list (a re-listed
   finding keeps its original id through `dup <id>`). An author's `answer`
   records a disposition beside a finding and closes nothing. Two records
   with one `at` to the second fold hold-last, as SPEC-MERGE rule 18 folds
   red-last. (2026-09-11: "a HOLD is closed by a fold and the same reader's
   APPROVE, or by that reader's explicit withdrawal.")
9. **Findings fold by a mechanical key, and the ledger says which view saw
   each.** `dedupe` folds findings on the entry by `(path, line, rule ref)`
   within one head, plus every explicit `dup <id>` across heads. Claim text
   is never compared: two sentences saying one thing is a judgment, and
   this tool makes none. Each folded finding prints who saw it (`who:model`,
   every view) and who at the same head was blind to it (every other reader
   of that head), so the ledger learns which pairs of eyes are
   complementary. (2026-09-13: "record, per finding, which view saw it and
   which were blind, so the ledger learns which pairs of views are
   complementary and we stop paying for pairs that are not.")
10. **Cost is measured, never estimated, and an absence is a dash.** A
    verdict may carry `--usage <file>`, a usage row in nova-swarm's shape
    (SPEC-SWARM rule 12: the five token types, model, seconds), and
    `--started <utc stamp>`, from which wall clock is the record's `at`
    minus `started`. `cost` prints per read what the receipt said, `-` where
    it said nothing, and sums per head and per entry with `dashes=<n>` so a
    total with an absence in it is never read as complete. Rounds are
    distinct heads that received at least one `line` verdict. (Glenn,
    2026-09-11: token spend reporting is an obligation; #183: "repeated-
    reading cost is visible.")
11. **One fact, one writer, one file; the read record is nova-merge's and
    this tool writes it through nova-merge's own code.** A `line` verdict of
    APPROVE or HOLD writes the nova-merge read record — `who`, `verdict`,
    `head`, `note`, `at`, `file` — under `<lane>/reads/<entry>/`, through
    `internal/merge`'s records path, so nova-merge's fold, CAS push, outbox
    and checkout lock apply unchanged (SPEC-MERGE rule 22). Every verdict of
    every kind also writes one **review record** under
    `<lane>/reviews/<entry>/`, with the same submission id, holding what
    nova-merge does not: `model`, `kind`, `of`, `job`, `reason`, the
    findings, the usage, `started`, and `read=<path>` naming its read record
    when one exists. The review record carries the verdict word too, so it
    can be read alone; `roster` refuses a pair that disagrees, naming both
    files. Why a second file and not a wider read record: nova-merge decodes
    its records with unknown fields refused and blocks the entry on a record
    it cannot decode (SPEC-MERGE, the state file), so a field added to the
    read record would block every merge until nova-merge was rebuilt; and an
    ABSTAIN, a `child` and a `card` are verdicts nova-merge must never fold.
    The fold in nova-merge walks `reads/` and `gates/` and nothing else, so
    `reviews/` is invisible to it by construction.
12. **Bounded output, and a listing is a cap and a count.** `packet` writes
    at most `--max-bytes` (default 131072, `0` for all) and prints one line.
    `roster`, `dedupe` and `cost` take `--max <n>`, default 20, and print one
    `<VERB> MORE` line naming the remedy. The bound is measured at the
    largest plausible state: fifty readers' records on one entry, twelve
    heads. (Glenn, 2026-09-09: bounded output by design; counts not lists.)
13. **Nothing is guessed and nothing is notified.** Every path is a flag;
    there is no default lane, no default output file, no default spec, no
    default reader list. The tool sends no bus note and posts no comment: a
    HOLD's findings reach the author through the record on the lane branch,
    and a reader who wants to say more says it on the bus themselves. A tool
    that could comment could be made to argue (SPEC-MERGE, what it does not
    do).

## The verbs

```
nova-review packet  --lane <dir> (--pr <n>|--branch <name>) --who <name> --out <file> [--head <sha>] [--spec <path>]... [--rule <spec>:<n>]... [--max-bytes <n>]
nova-review verdict --lane <dir> (--pr <n>|--branch <name>) --who <name> --model <id> --kind line|child|card [--of <line>] [--job <id>] --head <sha> --verdict approve|hold|abstain (--findings <file> | --reason <text>) [--note <text>] [--usage <file>] [--started <stamp>]
nova-review answer  --lane <dir> (--pr <n>|--branch <name>) --who <name> --finding <id> --head <sha> --as fixed|declined|dup [--of <id>] [--note <text>]
nova-review roster  --lane <dir> (--pr <n>|--branch <name>) --readers <name,...> [--reserved <name,...>] [--deadline <stamp>] [--max <n>]
nova-review dedupe  --lane <dir> (--pr <n>|--branch <name>) [--head <sha>] [--max <n>]
nova-review cost    --lane <dir> ((--pr <n>|--branch <name>) | --all) [--max <n>]
nova-review version

every verb takes --lane <dir>; every verb that runs git or gh also takes
[--timeout <seconds>], default 120, for SPEC-MERGE's reason
```

The binary is `nova-review`, and that is its only name.

**A new binary, not verbs on nova-merge, for one reason with three faces.**
nova-merge is the tool with the mutation guard: one function publishes to the
base, and its whole spec is built so nothing else can. Its own words are "it
does not review code" and "nothing in this tool reads a diff and forms an
opinion", and its `packet` is deliberately **pointers, never the diff**
(SPEC-MERGE rule 23). The review layer is the opposite kind of program: it
opens diffs and spec files at arbitrary heads, writes packet files wherever a
reader asks, reads usage rows and findings files a model wrote, and folds text
by keys. Putting that reading surface inside the binary that pushes to `main`
widens the one program that must stay narrow; putting it beside, sharing
`internal/merge` for the lane's records so there is one writer of that format,
keeps each tool's guarantee checkable by a test over its own sources. The
second face is the record: the read record stays nova-merge's, written through
nova-merge's code, and the review record is this tool's, in a directory
nova-merge's fold never walks — a division a second binary makes visible and a
verb on one binary would blur. The third is the reader: the person or model
who reads a packet and records a verdict does not merge and should not hold a
binary that can. `nova-merge packet` stays as the index (the range, the holds
as pointers, on stdout); `nova-review packet` is what dereferences that index
into one bounded file, and the two are tested to agree on `range=`.

**`--head` on `packet` is optional, and stale is a refusal.** Given, the tool
reads the entry's current head from the host and refuses, exit 1, `PACKET
STALE … current=<sha12>`, when they differ: a packet for a head nobody has is
work nobody can use, because a verdict for it authorizes nothing (SPEC-MERGE
rule 19). Absent, the head is read from the host and written on the packet's
first line, which is the sha the reader then hands to `verdict`. A sha read
from the host is a read, not a guess.

**`verdict --head` is required and is the full 40-character sha the reader had
open**, exactly as `nova-merge read --head`; the tool never fills it in. `VERDICT
OK` prints `current=true|false` against the head the host reports at record
time, so a reader who is already stale hears it at once and records again at
the new head rather than discovering it in `roster`.

**`answer` is the author's verb and it closes nothing.** It records, as one
immutable file under `<lane>/reviews/<entry>/`, that the author says a finding
is `fixed` at `--head <sha>`, `declined` with `--note`, or `dup` of `--of <id>`.
The packet shows the disposition beside the finding; the finding is open until
its reader says otherwise (rule 8). One writer per fact: the finding is the
reader's, the disposition is the author's, the closure is the reader's.

**`roster` is the check.** It exits 0 when `ratified=true` and 1 otherwise,
per SPEC.md's table: the check ran and said no. A roster waited on while
readers read exits 1 on every poll, and that is the honest answer to the
question the verb is asked; a caller who wants a report and not a verdict
reads the `ROSTER OK` line and ignores the code, which every other tool in the
set already allows.

## Exit codes

| code | meaning |
|------|---------|
| 0 | the verb ran and passed: a packet written, a verdict or answer recorded and pushed, a roster that is ratified, a dedupe or cost report printed |
| 1 | the verb ran and said **NO**: a packet refused as stale, a roster not ratified, a record written but not pushed (`pushed=false`, re-run the same verb) |
| 2 | could not run: missing flag, unreadable lane, a directory that is not a lane, a findings row that fails rule 4 or rule 5, bad invocation, `git` or `gh` absent |

A findings row that fails its check is exit 2 and not 1 because nothing was
recorded and nothing was judged: the invocation was unusable, and the remedy
is to fix the row. A refusal prints every failing row, one line each, because
a reader can fix six rows as easily as one.

## Output grammar

One machine-scannable line per event; first token names the verb, second is
`OK`, `FAIL`, `REFUSED`, `STALE` or an informational token listed here. `OK`
and informational lines go to stdout; `FAIL`, `REFUSED`, `STALE` go to stderr.
Every path, name, model id, claim and reason renders through
`internal/oneline`; every `key=value` field carrying stored text is a single
token by `oneline.Field`.

```
PACKET OK entry=<n-or-name> head=<sha12> base=<sha12> range=<r> files=<n> hunks=<n> rules=<n> prior=<n> open=<n> bytes=<n> cut=<n> out=<path>
PACKET STALE entry=<n-or-name> asked=<sha12> current=<sha12>: the head moved; build the packet for the current head
PACKET REFUSED: <reason>
VERDICT OK entry=<n-or-name> who=<name> model=<id> kind=<line|child|card> verdict=<approve|hold|abstain> head=<sha12> current=<true|false> rows=<n> block=<n> fix=<n> nit=<n> ok=<n> dup=<n> review=<path> read=<path|-> pushed=true
VERDICT FAIL entry=<n-or-name> who=<name> head=<sha12> review=<path> pushed=false: <reason>; re-run the same verb to push it
VERDICT REFUSED row=<n>: <path>:<line> <check>: <reason>
VERDICT REFUSED: <reason>
ANSWER OK entry=<n-or-name> finding=<id> who=<name> as=<fixed|declined|dup> head=<sha12> of=<id|-> file=<path> pushed=true
ANSWER FAIL entry=<n-or-name> finding=<id> file=<path> pushed=false: <reason>; re-run the same verb to push it
ANSWER REFUSED: <reason>
ROSTER READER who=<name> state=<yes|hold|abstain|pending> head=<sha12|-> at=<stamp|-> model=<id|-> open=<n> evidence=<n> reserved=<true|false>
ROSTER MORE kind=reader shown=<n> total=<t> nova-review roster --lane <dir> … --max 0
ROSTER OK entry=<n-or-name> head=<sha12> yes=<n> hold=<n> abstain=<n> pending=<n> evidence=<n> ratified=<true|false> deadline=<stamp|->
ROSTER REFUSED: <reason>
DEDUPE FINDING id=<id> key=<path>:<line>@<spec>:<line> sev=<block|fix|nit|ok> seen=<who:model,...> blind=<who:model,...> dups=<n> open=<true|false> answer=<fixed|declined|dup|->
DEDUPE MORE kind=finding shown=<n> total=<t> nova-review dedupe --lane <dir> … --max 0
DEDUPE OK entry=<n-or-name> head=<sha12> findings=<n> folded=<n> open=<n> readers=<n>
COST READ entry=<n-or-name> who=<name> kind=<line|child|card> model=<id> head=<sha12> verdict=<word> seconds=<n|-> in=<n|-> out=<n|-> cache_write=<n|-> cache_read=<n|-> reasoning=<n|->
COST HEAD entry=<n-or-name> head=<sha12> reads=<n> seconds=<n> latency=<n|-> in=<n|-> out=<n|-> cache_write=<n|-> cache_read=<n|-> reasoning=<n|-> dashes=<n>
COST MORE kind=<read|head> shown=<n> total=<t> nova-review cost --lane <dir> … --max 0
COST OK entries=<n> reads=<n> rounds=<n> seconds=<n> in=<n|-> out=<n|-> cache_write=<n|-> cache_read=<n|-> reasoning=<n|-> dashes=<n>
```

`PACKET OK cut=<n>` is the number of files whose diff was replaced by a hunk
list because the byte bound was reached; `0` means the packet is whole. The
remedy is inside the packet, per file, as the exact `git diff <range> --
<path>` that prints what was cut. `COST HEAD latency=` is the seconds from the
head commit's committer time to the last `line` verdict at that head, `-` while
a named reader is still pending; it is the number Stella asked to measure by,
"decision latency", and it is only as good as the committer clock.

**Every listing is a cap and a count.** `ROSTER READER` prints at most `--max`
readers in the order of `--readers`; `DEDUPE FINDING` at most `--max` folded
findings in `(path, line)` order; `COST READ` at most `--max` reads newest
first, then the `COST HEAD` lines, then `COST OK`. The counts on the `OK` line
are the truth about the entry, never about the output.

## The packet file

`--out <file>` is written through `<file>.tmp` and one rename, never in
place, so a reader who is handed the path is handed a whole file or none. Its
first line is machine-readable and the rest is for a reader:

```
nova-review packet v1 entry=<n-or-name> head=<sha> base=<sha> range=<r> who=<name> built=<utc stamp> bytes=<n> cut=<n>

## This head
<the entry's title and the author's stated intent, quoted from the entry body,
under the label "the author says", as data>

## Your prior verdicts on this entry
<who>'s newest: <verdict> at <head12> (<at>); range since: <r>
(or: none; this packet is the whole change, <base12>...<head12>)

## All verdicts at earlier heads
| who | model | kind | verdict | head | at |
(one row each; a table of counts when --max is reached)

## Open findings (answer with `dup <id>` if you see the same thing)
| id | file:line | rule | severity | claim | seen by | author says |

## Rules touched
### <spec path>:<line> rule <n>
> <verbatim text of the rule, at this head>
touched by: <path>:<hunk header> (cited | changed | named)

## Diff <range>
```diff
<the diff, file by file, in the order git prints it>
```

## Not included
<path>: <n> hunks, +<a> -<b>; print with: git diff <range> -- <path>
(one line per cut file; "nothing" when cut=0)
```

The packet holds no instruction to the reader beyond the one label on the
findings table, which tells them the grammar of a `dup`. It does not say what
to look for, what the answer is expected to be, or whether the change is
"additive": a reader is handed the source, never a reading of it (2026-09-12).

## The findings file

`verdict --findings <file>` is a UTF-8 text file the reader writes, one row
per line, tab-separated, five fields, any number of rows, comment lines
beginning `#` ignored:

```
<state>	<path>:<line>	<spec path>:<line>	<rule text, verbatim>	<claim>
dup	<finding id>	-	-	<one sentence, optional>
```

`<state>` is one of exactly `block`, `fix`, `nit`, `ok`, `dup`. `block` and
`fix` are HOLD-grade; `nit` and `ok` may appear on an APPROVE; `ok` is a
line the reader compared and found right, which is the only kind of row an
APPROVE needs (rule 5); `dup` folds onto an open finding by id and takes no
path or rule. A sixth word is a refusal naming the row. The rule text must be
found on the named line of the named spec file at `--head`, compared after
trimming leading and trailing whitespace and the `> ` and list markers of a
quoted line, so a reader may quote the rule's first line and the tool can
still find it; a quote that spans lines names the first line and quotes that
line. A finding's id is `<submission id>.<row>`, where the submission id is
the record's `<at>-<rand6>` drawn once per verb (SPEC-MERGE rule 22), so an id
names its file.

**The tool checks the ground and never the claim.** Rule 4 is a check that
`path:line` and `spec:line "text"` exist at the head. The claim is text a
reader wrote, printed through `oneline`, and no code path in this tool
compares one claim to another or to the diff (rule 9's key omits it, and a
source test finds no such comparison).

## The lane, as this tool sees it

`--lane <dir>` is a lane `nova-merge init` made. This tool adds one tracked
directory to it and nothing else:

```
<lane>/reviews/<entry>/<who>-<head12>-<at>-<rand6>.json     one verdict, immutable, tracked and pushed
<lane>/reviews/<entry>/answer-<who>-<head12>-<at>-<rand6>.json   one answer, immutable, tracked and pushed
<lane>/reads/<entry>/<who>-<head12>-<at>-<rand6>.json       written for a `line` approve or hold, through internal/merge (rule 11)
```

A review record is written exactly as a read record is: first to
`<lane>/outbox/<submission id>.json`, then committed and pushed to the lane
branch under the checkout lock in the CAS loop of SPEC-MERGE rule 22, delivered
only after the confirming fetch, repaired by re-running the same verb. When a
`line` verdict writes both a read record and a review record, both are one
submission — same `<at>-<rand6>`, both items in one outbox flush, one commit
— so the lane branch never holds one without the other for longer than a
rejected push. `packet`, `roster`, `dedupe` and `cost` **fetch** the lane
branch under the checkout lock and fold the record files of the fetched tip in
memory, exactly as `nova-merge dry-run` does; they write nothing and take no
state lock. The head a packet is built for, and the head a roster judges,
comes from the host through nova-merge's host interface, never from
`state.json`, which is the coordinator's local fold.

The review record:

```json
{
  "version": 1,
  "entry": "951",
  "who": "emma",
  "model": "gemini-2.5-pro",
  "kind": "line",
  "of": "",
  "job": "",
  "verdict": "hold",
  "head": "cbde1fc6ba10c1430f9f90615c70706ea7aaa29e",
  "at": "2026-09-13T14:01:02Z",
  "started": "2026-09-13T13:44:10Z",
  "note": "",
  "reason": "",
  "read": "reads/951/emma-cbde1fc6ba10-20260913T140102Z-a1b2c3.json",
  "findings": [
    {"id": "20260913T140102Z-a1b2c3.1", "state": "block",
     "path": "internal/merge/read.go", "line": 88,
     "spec": "docs/SPEC-MERGE.md", "spec_line": 799,
     "rule": "A hold blocks, and nothing outvotes it.",
     "claim": "a stale hold is dropped from the fold when a newer approve by another reader exists"}
  ],
  "usage": {"input": 41200, "output": 3810, "cache_write": null, "cache_read": null, "reasoning": null, "seconds": 1012, "model": "gemini-2.5-pro"},
  "file": "reviews/951/emma-cbde1fc6ba10-20260913T140102Z-a1b2c3.json"
}
```

`version` is checked first and a number this binary does not know is refused
by number. Decoding is strict: an unknown field refuses. A review record that
does not decode is never skipped: `roster`, `dedupe` and `cost` print `FOLD
REFUSED file=<path>: <reason>` and treat the entry as not ratified, exit 1,
because the unreadable file may be the hold (the same reasoning as SPEC-MERGE's
state file section). `usage` values are numbers or `null`; `null` prints `-`
and counts a dash; `0` is a measurement. `read` is empty for `abstain`,
`child` and `card`.

## What the coordinator's morning looks like

Written out once so the verbs are seen together. An author opens an entry and
adds it to the lane with `nova-merge add --needs-read`. For each named reader,
somebody — a script, the reader, the coordinator — runs `packet --who <name>
--out <file>` and points the reader at the file. The reader reads, writes their
findings file, runs `verdict --kind line`, and the record is on the lane
branch within the CAS loop's bound. The coordinator's `nova-merge run` pulls
it on the next pass and counts the read; `roster --readers …` says who is
still pending, and nova-wake (amendment note A) wakes the coordinator on the
new record. A HOLD's findings reach the author in the next packet the author
asks for (`packet --who <author>` lists them as open) and the author records
`answer --as fixed --head <new sha>` as they fix; the next packet for the
reader is the delta since their HOLD's head with their own findings marked
`fixed` by the author, and they record again. `dedupe` on the entry shows, per
finding, which eyes saw it; `cost` shows what the whole entry cost to review
across every round. Nothing in this paragraph is a bus note, and the only
transcription is the reader's own findings file.

## Amendment notes for other specs

These belong to other tools and are drafted here so the whole is visible in one
place; each is a one-clause addition, additive and dated, to be landed in that
spec by its own PR and never duplicated in this tool.

**A. SPEC-WAKE, a fourth source: records on a lane branch.** "A record file
new at the fetched tip of a lane branch under `reads/`, `gates/` or `reviews/`
is a change; the value is the sorted list of record paths at the tip; the line
is `WAKE LANE lane=<dir> new=<n> reads=<n> reviews=<n> gates=<n>` with the
newest path named." This is the wake the 100-minute HOLD did not have
(2026-09-12): the record already lands where the coordinator pulls; the wake
makes the pull happen. nova-wake watches "three sources and no others" today
and the fourth is a fetch it already knows how to bound.

**B. SPEC-MERGE rule 22, the outbox carries sidecars by stem.** "An outbox item
`<submission id>.json` may be accompanied by any file `<submission id>.<ext>`;
every file sharing the stem is restored, committed and confirmed as one
submission." Today the loop knows `.summary` by name; this tool's `line`
verdict wants the read record and the review record in one flush, and a
generic stem rule is the same code with one string removed.

**C. SPEC-MERGE, the read record is unchanged and `status --reads` may name
this tool.** No field is added to the read record (rule 11 says why). `STATUS
ENTRY` gains nothing. The one optional addition is that `nova-merge status
--reads <entry>` prints `review=<path>` when a review record with the same
submission id exists beside a read, so a coordinator reading the merge tool's
listing can open the findings; a lane with no `reviews/` prints `review=-`.

**D. SPEC-BUS, nothing.** A verdict is a record and not a note (SPEC-MERGE rule
19: "an APPROVE with no findings is a command and no note"). A reader who wants
to discuss a HOLD writes a note themselves. This tool adds no verb to the bus.

## What it deliberately does not do

- **It does not review code.** It checks that a finding's ground exists at the
  head; it never checks that the claim is true, and no line of it compares two
  claims.
- **It does not merge, gate, publish or touch the base.** It has no mutating
  call to a host; its only writes are packet files at `--out`, and records on
  the lane branch through the records code nova-merge already trusts.
- **It does not decide who the readers are.** `--readers` is the caller's, per
  call, and the tool has no roster file and no notion of a team.
- **It does not notify.** No bus note, no PR comment, no wake. The wake is
  nova-wake's (amendment note A).
- **It does not default a read.** No deadline turns a silence into yes; a
  reserved line's silence is an abstain only because the caller named the line
  reserved and named the deadline.
- **It does not read the reader's harness.** Cost comes from a usage row the
  caller hands it; the tool opens no session log and knows no provider.
- **It does not infer which rule a hunk serves.** Cited, changed, or named;
  otherwise `rules=0` and the hunk stands alone.
- **It does not produce a verdict from a swarm's RESULT.md.** A card's verdict
  is recorded by the card's owner running `verdict --kind card`, with the
  findings file the owner wrote or checked; the tool parses no worker report.
- **It does not shorten a finding's claim to nothing** and it does not
  paraphrase a rule; both are quoted through `oneline`, capped at
  `oneline.TailBytes` with the cut marked.

## Known limits

- **A HOLD at a stale head blocks the roster and the merge alike**, and it
  should: the reader's standing is theirs to change. A reader who has left the
  team leaves a HOLD nobody can close; the remedy is a person, not the tool.
- **Rule extent is the spec's declared shape**, `^<n>. ` at column 0 to the
  next such line or heading. A spec whose rules are not a numbered list at
  column 0 gets `rules=0`, and this spec says so rather than guessing.
- **A rule cited by number in a changed line may be the wrong number.** The
  packet quotes what the line cites; the reader decides whether the code
  serves it. That is the reader's job and the whole point.
- **`latency=` is measured from the committer clock**, which is the author's
  machine's word. It is the best number available from the wire alone.
- **A `dup` across heads trusts the reader's id.** The tool checks the id
  names an open finding on this entry and nothing more.

## Tests this spec demands

One line per rule. Each must be seen red before it is trusted.

1. `TestThePacketIsTheDelta`: an entry read by `emma` at H1 and now at H2:
   `packet --who emma` writes a file whose first line names H2 and whose range
   is `H1..H2`; the diff section holds exactly `git diff H1..H2`'s bytes; for
   `stella`, who has never read it, the range is `<base>...H2`; the author's
   intent appears under the label "the author says" and a fixture body
   holding a sentence shaped as an instruction appears verbatim under that
   label and nowhere else; the file is written through `.tmp` and a kill
   before the rename leaves no `--out` file; with `--max-bytes` below the diff,
   `cut=<n>` is right, the cut files appear under "Not included" with the exact
   `git diff` command, and the file is at most `--max-bytes`.
2. `TestARuleIsTouchedMechanically`: a fixture spec with rules 1 to 5 in the
   declared shape and a diff whose added line says `// rule 3` quotes rule 3
   and no other, with its `path:line`; a hunk that changes the spec inside
   rule 4 quotes both sides of rule 4; `--rule <spec>:5` quotes rule 5; a
   hunk citing nothing prints `rules=0` on its line; the spec is read at the
   head (a fixture where the spec on disk differs from the spec at the head
   quotes the head's text); a spec with no numbered list gives `rules=0` and
   no error.
3. `TestOpenFindingsTravel`: `stella` holds at H1 with two findings; the
   packet for `emma` at H2 lists both with ids; `emma`'s findings file with
   `dup <id1>` records a verdict whose `dup=1`, creates no new finding, and
   `dedupe` prints `id1` with `seen=stella:…,emma:… dups=1`; a `dup` of an id
   that is not open on this entry is exit 2 naming the row.
4. `TestTheGroundIsChecked`: a row naming a path absent at the head, a line
   past the file's end, a spec line whose text differs by one character, each
   exit 2 with `VERDICT REFUSED row=<n>` and the check named; all three in one
   file print three lines; nothing is written to the outbox or the lane; a
   row whose quote is the rule's first line with `> ` stripped passes; a claim
   that is false but grounded is recorded (the tool judged nothing).
5. `TestAnApproveNamesWhatItCompared`: `approve` with an empty findings file is
   exit 2 and the sentence; with one `ok` row it records; with a `block` row
   it is refused naming rule 5; `hold` with only `nit` rows is refused; `hold`
   with one `fix` row records; `abstain` with a findings file is refused and
   with `--reason` records with `rows=0`.
6. `TestOnlyALineFillsACell`: `verdict --kind child --of emma` and `--kind
   card --of emma --job j1` at H write review records and no read record, the
   nova-merge fold (`nova-merge status`) shows `reads=0a/0h`, and `roster
   --readers emma` shows `emma pending evidence=2`; `--kind child` without
   `--of` is exit 2 naming the flag; `--kind card` without `--job` is exit 2;
   `--model` missing is exit 2; then `emma --kind line approve` at H makes her
   `yes` and nova-merge's fold shows `reads=1a/0h`.
7. `TestSilenceIsPending`: readers `a,b,c`, reserved `d`, deadline T: before
   T with `a yes@H, b hold@H1 (stale), c none, d none`: `yes=1 hold=1
   pending=2 abstain=0 ratified=false` exit 1; after T, `d` is `abstain`, `c`
   still `pending`, still exit 1; `b` approves at H, `c` approves at H:
   `ratified=true` exit 0; `a`'s approve was at H0 and the head is now H:
   `a pending head=H0`; a mutation that reads a stale approve as yes turns the
   test red; a mutation that expires `b`'s hold at T turns it red.
8. `TestNewestPerReaderDecides`: `stella hold@H1` with findings f1, f2; `stella
   hold@H2` re-listing `dup f1` and adding f3: open is `{f1, f3}`, f2 closed;
   `stella approve@H3`: open is empty; an author `answer --as fixed` on f1
   before that changes `answer=fixed` and leaves `open=true`; two records with
   one `at` to the second fold hold-last and a mutation folding approve-last
   turns the test red.
9. `TestTheLedgerNamesTheBlind`: three readers at H, two finding `(p:12,
   spec:40)` and one not: one `DEDUPE FINDING` with `seen=` naming two
   `who:model` and `blind=` naming the third; two claims with the same key
   and different text fold; two with the same text and different lines do not;
   a source test finds no comparison of claim text in the package.
10. `TestCostIsMeasured`: a `line` verdict with `--usage` and `--started`
    prints `seconds=` equal to `at - started` and the five types from the row;
    one without `--usage` prints five dashes and `dashes=5`; `COST HEAD` sums
    only the numbers present and carries `dashes=`; `rounds=` counts distinct
    heads with a `line` verdict (child-only heads do not count); a usage row
    with a sixth column is refused naming the column; `latency=-` while a
    named reader is pending is not asserted here (roster's business) but
    `latency=<n>` equals last `line` `at` minus the head's committer time in
    the fixture.
11. `TestOneFactOneWriter`: a `line hold` produces exactly two new files on the
    lane branch with one submission id in both names, one commit; the read
    record is byte-identical to what `nova-merge read` writes for the same
    flags (a golden comparison); `nova-merge run` on the coordinator's lane
    prints `pulled=2` and treats the hold as a hold; a review record hand-edited
    to `approve` beside a `hold` read record makes `roster` print `FOLD
    REFUSED` naming both files, exit 1; a review record with an unknown field
    is `FOLD REFUSED`; `reviews/` is absent from nova-merge's fold (a review
    record that does not decode does not block `nova-merge run`).
12. `TestBounded`: fifty readers' records on one entry across twelve heads:
    `roster --max 20` prints twenty `READER` lines and one `MORE`; `dedupe`
    and `cost` the same; the counts on each `OK` line say fifty; the packet
    for one reader is under `--max-bytes` with the prior-verdicts table
    collapsed to counts past `--max`; lines and bytes measured and written
    into the commit.
13. `TestNothingGuessedNothingSent`: every verb without `--lane` is exit 2
    `refusing to guess`; `packet` without `--out` is exit 2; `roster` without
    `--readers` is exit 2; a fake bus directory and a fake host record zero
    writes across every verb; a source test finds no call to `nova-bus`, no
    `gh pr comment`, no `gh pr review`, and no write outside `--out` and the
    lane.

## The work list

To build it in Go under `cmd/nova-review`, the way `cmd/nova-merge` is built:
standard library only, `internal/oneline` for every printed value,
`internal/bounded` for every listing, `internal/merge` for the lane's records,
host and checkout lock, `ONBOARDING.md`'s first-day standard, and tests that
execute `docs/CLI.md`'s `### First run`.

1. **`internal/review/record.go`** — the review record and the answer record:
   strict decode, `version` first, `findings` with every field required,
   `usage` with numbers or null, the file name from the submission id, the
   write through `internal/merge`'s outbox and CAS loop as one submission with
   the read record when there is one (amendment B, or a two-item flush until
   it lands). Tests: demanded 11.
2. **`internal/review/findings.go`** — the findings file parser: five fields,
   the closed state set, `dup` rows, the ground check at a head through `git
   show <head>:<path>` and a line count, the quote check with the trimming
   rule, every failing row reported. Tests: demanded 4 and 5.
3. **`internal/review/rules.go`** — rule extent by the declared shape, the
   three ways a rule is touched, the quote with `path:line`. Tests: demanded 2.
4. **`internal/review/fold.go`** — the in-memory fold of `reads/` and
   `reviews/` at a fetched tip: per-reader newest, open findings, dup chains,
   answers; the roster states; the dedupe key; the cost sums. Tests: demanded
   3, 6, 7, 8, 9, 10.
5. **`internal/review/packet.go`** — the packet file: the range from the fold,
   the diff from the lane's clone, the byte bound with per-file cuts and the
   remedy command, the `.tmp` and rename. Tests: demanded 1 and 12.
6. **`cmd/nova-review/main.go`** — the verbs, the flag refusals, the output
   grammar exactly as above, `--max` and `--max-bytes`, the exit table, `help`
   and `version`. Tests: demanded 13; every refusal sentence; every exit code.
7. **`docs/CLI.md`'s `### First run`** — `init` a lane with a bare remote,
   `add` one entry, `packet`, a two-row findings file, `verdict`, `roster`;
   every path a flag; a test executes it.

## Ideas folded, 2026-09-13

| source | the idea, in six words | disposition |
|---|---|---|
| Stella, idea 1 | machinery observes and books; a decision packet | rule 1: `packet` is that packet, built from the fold and the host |
| Stella, idea 2 | one packet per item and revision, superseded not lost | rules 1, 3: a newer head's packet carries every open finding forward |
| Stella, idea 3 | one writer and one durable home per fact | rule 11: read record is nova-merge's; review record is this tool's; the finding is the reader's, the disposition the author's |
| Stella, idea 4 | smallest sufficient review packet | rule 1, and SPEC-MERGE rule 23 stays as the index |
| Stella, idea 5 | one structured result, no receipt-of-receipt | `VERDICT OK` is one line; no bus note is sent (rule 13) |
| Stella, idea 6 | measure by decisions, latency, usage | rule 10: `cost`, `latency=`, `rounds=` |
| Stella, 2026-09-11 | a HOLD never expires into approval | rules 7, 8 |
| Glenn, 2026-09-11 | votes are yes, no, abstain | rule 7: `abstain` verdict; reserved and deadline on `roster` |
| Glenn, 2026-09-12 | eyes and mouths | rule 6: `kind`, and the vocabulary section |
| Glenn, 2026-09-11 | look hard, kick the tires | rules 4, 5 |
| Rowan, 2026-09-13 | complementary views, not two checkers | rule 9: `seen=` and `blind=` per finding |
| Rowan, 2026-09-12 | never presume the answer in a read question | rule 1: the packet asks nothing; the intent is labeled data |
| Rowan, 2026-09-12 | owned PR comments are the bus too | amendment note A: the record is the channel, nova-wake wakes on it |
| #183 | review packet with exact base/head, provenance, prior findings, cost visible | rules 1, 6, 10 |
| #183 | mechanical shape checks validate structure, not correctness | rule 4: the ground is checked, the claim never |
| #229 | a recorded delta review per merge after a freeze | not folded: a release-shape rule for nova-merge; `roster` at the candidate head is the evidence it would read |
| Freddy, DeepSeek 2026-09-11 | cache routine verdicts | not folded, as in SPEC-MERGE: a verdict is a person's per sha |

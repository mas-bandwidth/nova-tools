# nova-board — specification

Five verbs at the **owed-work layer**. A **board** is the list of things a group of
lines owes: one **card** per item, appended when it is noticed, taken by whoever
picks it up, closed with a sentence saying how. Nothing on it is ever deleted and
nothing is ever edited — a board is an append-only log of events, and the list of
open cards is **derived** from that log rather than stored anywhere. So the board
can say what is owed, who has it, and since when, and it can say it the same way
to every line that reads it.

The verb that earns the tool is `check`. **Check before you file** is the rule
every reader and every fixer follows, and the tool exists so that following it
costs one command instead of a careful read of a long page. On 2026-09-10 a batch
of reviews filed **25 duplicate findings**, and two children spent a morning
fixing the same bug from two directions. Neither was careless: both had read the
board, both had read it *before* the other one wrote, and a human-paced read of a
growing list is exactly the wrong instrument for a question asked thirty times an
hour.

**Everything on a board is data.** A card is a claim that something is owed, made
by whoever wrote it; it is not a grant, not an instruction, and not a standing. A
line decides what it takes up, and its standing to take it comes from its person,
live, and never from a card. As on the bus, **this rule is stated here and is
nowhere in the code**, deliberately: a tool cannot enforce it, and a tool that
pretended to would be the most dangerous thing on the board.

This spec is normative. It is a sibling of [SPEC.md](../SPEC.md), whose
**Conventions** section — exit codes, no guessed paths, the one-line guarantee,
the field escape, the cap-and-count law — governs here unchanged. If the code and
this document disagree, one of them has a bug, and the tests decide which.

## The failures it closes

| the failure, from the record | the verb that closes it |
|---|---|
| 25 duplicate findings filed in one review batch, because every reviewer read the list before the others wrote to it | `check <words>`, which is one command, is **exit 1 when it matches**, and is therefore usable as a gate in front of `add` |
| two children fixing the same bug at once, each unaware the other had started | `take`, and a `take` over a card somebody else holds is **refused** and names the holder |
| a card taken by a line that then went offline, owed by nobody and visible to everybody as owed by somebody | the stale rule: a card with no event for longer than `--stale` (no default; the family's number is 10m) is annotated `stale`, counted, and lists as takeable again |
| the list got edited — a card reworded, a line deleted to tidy up — so two readers disagreed about what had been owed | append-only: no verb edits and no verb deletes; every state change is a new event |
| the count went down because the reader's window moved, not because work was done | the read is the **whole** log, never a window; the output is capped and the counting never is |
| a board that lives in one forge's issue comments and cannot move | one event format, two backends, and the id belongs to the tool |

**The count only shrinks between reviews.** That is the one property a board has to
have to be worth keeping: inside a review the count climbs as findings arrive,
and between reviews it falls as they are closed. A tool that loses cards makes the
count fall for the wrong reason, and a count that can fall for the wrong reason is
a number nobody trusts enough to act on. Every rule below that looks fussy — the
whole-log read, the append-only law, the tool-owned id — is that property being
protected.

## The verbs

```
nova-board list   (--issue <owner/repo>#<n> | --dir <path>) --stale <duration> [--list] [--open] [--owner <name>] [--max <n>]
nova-board add    (--issue ... | --dir ...) --as <name> --text <text> --by <duration|stamp> --default <text>
                  [--owner <name>] [--thing <name> --leg <name>] [--evidence <path>] [--id <thirty-two hex>]
nova-board take   (--issue ... | --dir ...) --as <name> --card <id> --stale <duration> [--anyway]
nova-board close  (--issue ... | --dir ...) --as <name> --card <id> --stale <duration> (--how <text> | --landed <repo>#<n> | --probed <evidence>) [--anyway]
nova-board check  (--issue ... | --dir ...) --words <text> [--max <n>] [--all]
nova-board quickstart (--issue ... | --dir ...) --stale <duration>
nova-board help
```

**Exactly one backend per invocation**, named. Neither `--issue` nor `--dir` is
exit 2 and `refusing to guess`; both together is exit 2 as well, because a board
written to two places is two boards with one name. There is no default repository,
no default issue number, no default directory and no default `--as`.

**`--as <name>` is required on every verb that writes** — `add`, `take`, `close` —
and is the name that goes in the event. It is not a credential and proves nothing:
the forge or the file's git history holds who actually wrote, and the name is what
the board *says*. A tool that pretended `--as` was an identity would be claiming
something it cannot check.

**No guessed anything, with one exception:** `--max`, which defaults to **20** as
every listing in this repo does. `--stale` had a 10m default in an earlier draft
and lost it on 2026-09-11 (rule 8 in **The rules of the last two days**): every
duration comes from a flag. The family's number is still 10m, `quickstart` passes
it and says in words that it chose it, and the README's first run shows it, so
every line can still agree about it without the tool guessing.

`list` and `check` **read**; `add`, `take` and `close` **append**. No verb reads
and writes in one call, so nothing here can half-succeed in two places.

## The card, and the events

A card is ten things, and only the text, the deadline, the default and the
optional row and evidence fields are ever written by hand:

| field | what it is |
|---|---|
| **id** | thirty-two lower-case hex characters: 128 bits read from the operating system's random source by `add` at creation; derived from no field, never recomputed and never reused |
| **hash** | twelve lower-case hex characters, SHA-256 over the card's text, written on the `card` line as `hash=`; for a duplicate warning only, never an identity |
| **text** | what is owed, one line, as the filer wrote it |
| **owner** | the name from the latest `taken` event in the fold order, else the `add` event's `--owner`, else the filer |
| **since** | the `add` event's stamp — when the thing became owed, not when it was last touched |
| **state** | `OPEN` or `CLOSED`, **derived** from the events and stored nowhere |
| **by** | the deadline, from the `add` event's `--by`; never moved (rule 2) |
| **default** | what happens if nobody closes the card by then, from `--default`, one line (rule 2) |
| **thing, leg** | present only on a **row** of the owed ledger, from `--thing` and `--leg` (rule 4) |
| **evidence** | a path the filer named with `--evidence`, carried and never opened (rule 5) |

**The id is the tool's, not the backend's, and it is a draw, not a derivation.**
`add` reads 128 bits from the operating system's random source (`crypto/rand`)
at creation and renders them as thirty-two lower-case hex characters. The id is
computed from nothing — not the stamp, not the filer, not the text, not a count
— so two `add`s anywhere, from two clones or two benches, under one name at one
second with one text, get two ids without reading anything first. There is no
sequence to read, no shared counter two writers can both read before either
writes, and no collision to detect or recover from: two draws of 128 bits
meeting is not an event this family will see. An earlier draft derived the id
from a hash over the stamp, the filer, the filer's sequence number on the board
and the text, and claimed two same-second same-text adds got different ids *by
construction*; Stella's second read showed the construction was a read of an
unsynchronized count — two concurrent adds could both read the same count and
both publish one id, `O_EXCL` refusing one only when both wrote to one
directory, while two clones each succeeded locally and the issue backend's
reread-before-append is not atomic. **A creation identity may depend on nothing
two writers can both observe before either writes.** The id shares one property
with `nova-bus`'s and not the other: it survives moving the board from one
backend to another and is assigned **once**; but a note's id is a hash because a
note is its content, and a card's is a draw because a card is an obligation, and
two lines noticing one thing owe it twice until one closes `duplicate of`.

**The content hash is a separate field and is never the identity.** `add` also
writes `hash=<twelve hex>`, the first twelve hex characters of a SHA-256 over
the card's text as rendered through `internal/oneline` — the text only, and not
the leg, owner, deadline, default or evidence. It has one use: before appending,
`add` folds the board, and if an OPEN card carries the same hash it prints `ADD
NOTE hash=<hash> matches open card <id> owner=<name>; filing anyway` on stderr
and files. It never refuses on it and never merges on it — that would be the
silent deduplication **The races** forbids — and `check` reports a hash match
the same way, as a note beside its text match. The hash is for a person's eye;
the id is for the tool's.

**A retry after an uncertain append reuses the id it drew.** `add` prints the
id on `ADD OK`, and when the append's outcome is unknown — `gh` timed out after
posting, a clone lost power after the file was created — the same `add` run
again with `--id <thirty-two hex>` is the retry: with `--id`, an existing card
whose creation fields are identical (`as`, `hash`, `owner`, `by`, `default`,
`thing`, `leg`, `evidence` — everything on the `card` line but `at`) is `ADD OK
… existed=true` and nothing is written; one whose fields differ is `ADD
REFUSED: id <id> exists with different fields; nothing written`, exit 1; no
card is `ADD OK … existed=false` with the id used as drawn, never redrawn.
Two `card` events with one id and different fields, however they got there,
are a **conflict** in the fold: the card lists `conflict=true`, is counted in
`conflicts=` on `BOARD OK`, and neither event silently wins. (Stella,
2026-09-11: a differing payload with the same identity is a conflict, never
one card silently winning.)

**Creation is exclusive against hand-made files, and that is all it needs to
be.** Under `--dir` the card file is created with `O_EXCL` and never truncated;
under `--issue` the board is re-read immediately before the append and the id
looked for. An id that already exists — which with a random id means a
hand-made file, a copied one, or a broken random source — is `ADD REFUSED: id
<id> exists; nothing written` at exit 1, and no card file and no comment is
replaced, ever. A short id taken from a backend's own comment number is
forbidden: it is a different id in the other backend, and the prototype's
**last six digits** of a comment id is a collision in one million with no
detection and no recovery.

**Nothing is ever deleted or edited. Every state change is an appended event.**
There are five event lines and that is the whole format:

```
card <id> as=<name> at=<stamp> override=false hash=<hash> owner=<name> by=<stamp> default=<text> [thing=<name> leg=<name>] [evidence=<path>]: <text>
taken <id> ev=<ev> after=<ev|id> as=<name> at=<stamp> override=<true|false>
closed <id> ev=<ev> after=<ev|id> as=<name> at=<stamp> override=<true|false>: <how>
landed <id> ev=<ev> after=<ev|id> as=<name> at=<stamp> override=<true|false> in=<repo>#<n>
probed <id> ev=<ev> after=<ev|id> as=<name> at=<stamp> override=<true|false>: <evidence>
```

- **Every later event has its own id and names the event it saw.** `ev=` is
  twelve lower-case hex characters drawn from the OS random source by the verb
  that appended it, and `after=` is the `ev=` of the newest event for that card
  in the writer's fold at the moment it appended — the card's own id when there
  was none. Two events for one card with the same `after=` are **concurrent**:
  each writer read the same board and neither saw the other. The fold applies
  the total order below to choose the owner and the first close, as it always
  did, and now also **counts** what it chose over: `BOARD CARD … conflicts=<n>`
  is the number of concurrent pairs in that card's history and `BOARD OK
  conflicts=<n>` the board's, so a take that won a tie is visible as a tie
  rather than as the only take. The order is a documented tie-break and never
  git's union placement: two clones merging the same lines in opposite orders
  count the same conflicts and name the same owner. Migration (work list 10)
  writes `ev=` and `after=` for every old event in its comment order, so a
  migrated history folds to the same cards, owners and conflict counts. (Stella,
  2026-09-11: concurrent events with the same predecessor are visibly
  concurrent, either a conflict or a stable tie-break with a count.)

- **Every event line carries `as=<name>` and `override=<true|false>`**: the
  actor is the `--as` of the verb that appended it, and `override` is `true`
  exactly when the verb ran with `--anyway` over a refusal it would otherwise
  have made, `false` on every other event and always on `card`, which overrides
  nothing. These two are the facts the read side derives from and nothing else
  is consulted: `BOARD CLOSE by=<name>` is the closing event's `as=`, `owner` is
  the `as=` of the latest `taken` event in the fold order, and a history read from a directory with no
  git metadata, or from a forge with no comment author, derives the same
  answer. An earlier draft wrote `(<stamp>, <as>)` after the id and `by <name>`
  in prose and carried no override at all; that shape is not the format, and a
  line in it is an unparsed event, counted and never guessed at (**Known
  limits**). The format is `BOARD v1` and no board in the old shape has
  shipped, so there is nothing to read backward; the migration script (work
  list 10) writes this shape.
- `<stamp>` is RFC 3339 in UTC, to the second, and it is the `at=` field.
- `closed`, `landed` and `probed` are the three closing events and the only
  three. `landed` is the one that names where the work went, which is the close a
  reader can verify; `closed … : <how>` is the one that says what happened in
  words, including `duplicate of <id>`, which is the **only** sanctioned way two
  cards for one thing become one, and `superseded by <id>`, which is the only way
  a deadline moves; `probed` is the only close for a row of the owed ledger
  (rule 4), and it carries the evidence.
- Every `key=value` field on every event line — `ev=`, `after=`, `as=`, `at=`, `override=`,
  `hash=`, `owner=`, `by=`, `default=`, `thing=`, `leg=`, `evidence=`, `in=` — is
  rendered through `oneline.Field`, so a default holding a space or a name
  holding one is one token, and the free text after `: ` is rendered through
  `internal/oneline` and is never scanned for fields.
- A second closing event on a closed card is permitted and changes nothing: the
  **first** close is the close, and the later ones are in the log where a reader
  can see that two lines thought they had finished the same thing.
- `taken` may appear many times. The latest one **in the fold order** is the
  owner — never the last one in a file or a thread, because two clones can hold
  the same lines in different orders (**The fold order**, below).
- There is no `reopen`. A card closed in error is a new card whose text names the
  old id, because a board that can reopen is a board whose count can rise for a
  reason other than new work, and the count is the thing being protected. This is
  a deliberate cost, paid in one extra card.
- There is no `release`. A take ages out by the stale rule below; the board never
  writes an event for something it only inferred from a clock.

**An event is one line, and the one-line guarantee applies to the text it
carries.** A card's text is rendered through `internal/oneline` before it is
appended, so a filer who pastes a newline files one card rather than a card and a
mystery event, and `...+<dropped>B` marks a text cut at `oneline.TailBytes`. A
text that arrives empty after escaping is refused, exit 2: a card a person cannot
read is not a card.

**Parsing is anchored and by id, never by substring.** A `closed` or `taken` event
binds to a card only when the line **begins** with the verb and the id is the
token immediately after it. The prototype matched `closed[ :]*<sid>` and `taken
by` anywhere in any comment body, so a card whose *text* contained a six-digit
number could be closed by a sentence about something else — and on a board about a
repository full of issue numbers, that is not a hypothetical.

**The fold order is total, and it is the same in every clone.** Every derivation
— the owner, the first close, staleness, `BOARD NEXT` — is made over a card's
events sorted ascending by `(at, as, id, verb, the line's bytes)`, and never by
the order the lines appear in a file or a comment thread. Every event line
carries the first three keys, which is why they are on every line: the id after
the verb, `as=` and `at=`. The order is total — two events equal in all five
keys are one line, and a union merge that holds one line twice folds it once —
so two clones that merged the same concurrent appends in opposite textual
orders derive the same owner, and the two backends derive the same owner from
the same events. The owner of a card two lines took in one second is the take
whose `as=` sorts later. That winner is arbitrary and **reproducible**, which is
the property the accepted residual race in **The races** needs: the tool does
not make `take` globally exclusive; it makes the outcome the same wherever it is
computed, and the log shows both takes so the line that lost can see it. An
event whose `at=` the tool cannot parse folds last, is counted under `BOARD
NOTE unparsed events=<n>`, and is never guessed at.

## One format, two backends

The **format** is the five event lines above. A backend decides only where a line
is appended and how the lines are read back; it decides nothing about what a line
means, and the two backends must produce identical `list` output from identical
histories. That is a test, not an aspiration.

**`--issue <owner/repo>#<n>` — issue comments, today.** One event per comment, in
comment order, appended with `gh`. It is where today's board lives
(`mas-bandwidth/schema#876`) and it is chosen for a good reason: every line in
this family can already read and write it, from any machine, with no clone and no
write access to a repo's branches.

**`--dir <path>` — a directory of card files in a repository, tomorrow.** One
file per card, `<dir>/<id>.board`, created by `add`. One event per line, appended,
read whole; the board is a fold over every file in the directory (rule 3). The
first line of every card file is exactly `BOARD v1` and nothing else, for the
reason `OPEN v2` has a version line: a later format read as this one would be
entries nobody wrote. Blank lines and `#` comments are ignored, as everywhere else
in this family's files. A file in the directory that is not `<thirty-two hex>.board`
is counted under `BOARD NOTE unparsed files=<n>` and never read as a card. **The
directory backend appends and never runs git**: committing and pushing it is the
caller's, and saying so is more honest than a push hidden inside `add`. That is
the one asymmetry between the backends, and it is named rather than hidden: an
`add --dir` is durable when the caller lands it, and an `add --issue` is durable
when the command returns.

**The read is the whole log. There is no window.** The prototype read the last 40
comments, which means a card older than forty events *vanishes* — and a vanished
card makes the count fall without any work being done, which is the one thing a
board may not do. A board is small by construction: it is what is **owed**, and
what is owed is closed. A board too big to read whole is a backlog that needs a
review, and `list` says so rather than truncating its input.

**A cache may avoid re-reading; it may never truncate.** A cache is valid only
while the backend's own latest-event marker is unchanged, it is invalidated by
this tool's own writes, and a stale cache is a correctness bug and not a
performance trade. The prototype's 20-second time-to-live cache is exactly wrong
for the duplicate-filing failure this tool exists to close: two reviewers filing
nine seconds apart both read the board as it was before either wrote.

## Exit codes

| code | meaning |
|------|---------|
| 0 | the verb ran and passed: a listing printed, a card appended, **or a `check` that found nothing** |
| 1 | the verb ran and said **NO**: a `check` that **matched**, a `take` of a card another line holds, a `close` of a card another line holds, an append the backend rejected, an `add --id` whose id exists with different fields |
| 2 | could not run: a missing or malformed flag, no backend or two, an unreadable board, a card file without its version line, an id that names no card |

**`check` exits 1 when it matches, and that is the tool's most important
sentence.** A check in this repo is a thing that can say NO (SPEC.md: *a check
never seen failing is not a check*), and the NO a board owes a filer is **this is
already on the board, do not file it**. So the rule every reader and fixer follows
is one line of shell:

```sh
nova-board check --issue mas-bandwidth/schema#876 --words "windows runner skips" || { [ $? -eq 1 ] && exit 0; exit 2; }
nova-board add   --issue mas-bandwidth/schema#876 --as rowan --text "the Windows runner skips three steps" \
                 --by 4h --default "rowan files it on the schema board as a known gap"
```

It reads the way it should be read: *if it is already there, stop* — and the
guard tells a NO from a could-not-run, because `check` at exit 2 (no backend, an
unreadable board) is an operational error and a guard that read every non-zero
as *already filed* would turn a broken board into a quiet one. The `add` carries
`--by` and `--default` because rule 2 requires them and an example that does not
run is not an example: this pair is executed by a test against a fresh board and
files one card. The inverted reading — 0 for "found it" — would make the natural
`&&` chain file **exactly** the duplicates, so the mnemonic is written into the
banner, the README's first run, and a test named for it. `--all` makes `check` report over closed cards as well,
still exit 1 on a match, because *somebody already fixed this* is as good a reason
not to file as *somebody already filed it*.

A `take` or `close` refused for ownership exits **1** and not 2: the verb ran, the
board was read, and the answer is no. `--anyway` turns either into a 0 and records
the override in the event as `override=true`, because there are real reasons to close somebody's card
— they went offline, it landed under another number — and a tool that made that
impossible would be edited around.

## Output grammar

```
BOARD CARD id=<id> state=<OPEN|CLOSED> owner=<name|-> since=<stamp> by=<stamp> age=<d> taken=<d|-> stale=<true|false> overdue=<true|false> conflicts=<n> conflict=<true|false> default=<text> thing=<name|-> leg=<name|-> evidence=<path|->: <text>
BOARD CLOSE id=<id> by=<name> at=<stamp> how=<closed|landed|probed> override=<true|false> where=<repo#n|path|->: <how>
BOARD LINE name=<name> open=<n> overdue=<n> stale=<n>
BOARD LEG leg=<name> owed=<n> probed=<n>
BOARD NEXT <the one thing to do first, with its id>
BOARD OK cards=<n> open=<n> closed=<n> stale=<n> overdue=<n> owed=<n> lines=<n> conflicts=<n> shown=<n> backend=<issue|dir> source=<where>
BOARD MORE kind=<card|line|leg> shown=<n> total=<t> and <t-n> more; <remedy>
BOARD NOTE <something true about this board that is not a card>
BOARD FAIL <id or source>: <reason>
BOARD REFUSED: <reason>
ADD OK id=<id> owner=<name> at=<stamp> backend=<issue|file> durable=<true|false> existed=<true|false>
ADD REFUSED: <reason>
TAKE OK id=<id> owner=<name> at=<stamp> previous=<name|-> override=<true|false>
TAKE REFUSED: <reason>
CLOSE OK id=<id> how=<closed|landed|probed> where=<repo#n|path|-> at=<stamp> owner=<name|-> override=<true|false>
CLOSE REFUSED: <reason>
CHECK HIT id=<id> state=<OPEN|CLOSED> owner=<name|->: <text>
CHECK OK matched=<n> cards=<n> scanned=<OPEN|ALL> words=<n>
CHECK REFUSED: <reason>
```

`OK` lines and the informational tokens go to stdout; `FAIL` lines and refusals go
to stderr. Every field value is rendered through `internal/oneline` and every
`key=value` field additionally through `oneline.Field`, so a card's owner holding a
space is one token and a search for `state=OPEN` anchored at the line start matches
the tool's own field and never a card's text. **The free-text tail is never to be
scanned for fields** — everything after the `: ` is what the filer wrote, and a
filer may well have written `state=OPEN`.

`BOARD OK` carries nine counts and they are nine different facts: `cards` is
the whole board, `open` is what is still owed, `closed` is what is not, `stale` is
how many open cards have had no event for longer than `--stale`, `overdue` is how
many open cards are past their deadline, `owed` is how many rows of the ledger
are not yet probed, `lines` is how many owners the open cards have, `conflicts` is how many concurrent
event pairs the fold chose over (**The card, and the events**), and `shown`
is how many lines this run printed. **The counts print on failure as well
as success** and they are the truth about the **board**, not about the output: the
listing is capped at `--max`, the counting never is. `source=` is the issue or the
path, so a listing cannot be mistaken for a different board's.

`state=CLOSED` and the close's details are two facts and live on two lines — a
`BOARD CARD` and, under `--open`'s absence, the `BOARD CLOSE` that closed it. The
prototype printed `CLOSED/#942` as one field, which is a state and a location
joined by a slash, unparseable by the scanner it was written for.

## check — the rule, mechanized

```
nova-board check (--issue ... | --dir ...) --words <text> [--max <n>] [--all]
```

Matching is deliberately dumb, because a clever matcher is one a filer cannot
predict: the query is split on whitespace, each word is lower-cased, and a card
matches when **every** word appears as a substring of its lower-cased text. No
stemming, no synonyms, no ranking, no regular expressions — a filer who gets a
surprising answer can see why by reading the card, and `--words` with one common
word is a filer's mistake the output can name (`BOARD NOTE` says when one word
matched more than half the board).

`check` scans **open** cards by default and open plus closed under `--all`. It
prints one `CHECK HIT` per match, capped at `--max` with a `BOARD MORE` line, and
one `CHECK OK` with `matched=` always — the count is never capped.

It is an **advisory instrument with a verdict**, and the difference matters: the
verdict is *a card on this board contains all your words*, which is a fact; it is
not *your finding is a duplicate*, which is a judgment the filer makes. A filer who
looks at two hits and files anyway is doing the right thing, and the 25-duplicate
batch did not happen because anybody overrode a `check` — it happened because
there was nothing to override.

## The races

Three, all reachable today, each with the rule that closes it — and one of the
three is closed by **accepting** it, which is the honest answer.

**Two adds of one card.** Two lines notice the same thing and both `add`. Both
events land and the board has two cards, because an append-only log cannot merge
and an `add` that silently refused as a duplicate would be the tool deciding a
filer's question for them. **This race is not prevented; it is made cheap.**
`check` in front of `add` is the rule that makes it rare, and `closed <id>:
duplicate of <other-id>` is the one sanctioned merge — which leaves both cards in
the log, where the pair is visible, rather than erasing the evidence that two
lines saw one thing. What is forbidden is the *silent* version: no verb deduplicates
by text, and no verb edits a card to fold another into it.

**A close of a card somebody just took.** Line A takes card `7f3…` at 10:00:02;
line B, reading a board it fetched at 10:00:00, closes it at 10:00:03. Two
appends, both durable, and B has closed work that A is in the middle of. Closed by
reading at write time: `close` re-reads the board immediately before appending, and
a close of a card whose latest take is by **another** name and is **not** stale is
`CLOSE REFUSED`, exit 1, naming the owner and the age of the take. *Stale* here is
measured against `close`'s own `--stale <duration>`, which is required for the
same reason it is on `take` and `list` (rule 8: no default durations, and this
decision is made from one): a `close` without it is exit 2 naming the flag.
`--anyway` overrides it and the event records that it was overridden,
`override=true`, so the log says a close went over a live take. The same rule guards `take`: a take of a fresh take by
another line is `TAKE REFUSED` exit 1 naming the holder, which is the 2026-09-10
two-children-one-bug failure closed at the only moment a tool can see it. The
window between the re-read and the append cannot be closed by this tool over a
backend it does not own — so it is narrowed to one round trip and **named here**,
and the fold order (**The card, and the events**) settles what the tool could
not — the same way in every clone, which is what makes it a settlement.

**A take by a line that is offline — the ten-minutes-silent rule.** A card taken
by a line that then stops is owed by nobody and looks owed by somebody, which is
worse than unowned: nobody else will pick it up. **A taken card whose owner has
been silent past `--stale` returns to open**, and returning to open
means exactly this: the card still lists as `OPEN`, `stale=true`, with
`taken=<age>` and the holder's name still on it, and a `take` or `close` by
another line is **permitted without `--anyway`**. The board writes nothing. It has
no way to know whether a line is silent — it knows only when the take was written
— so staleness is an **annotation derived at read time from the take's stamp**, and
the only clock-dependent derivation in this spec. Nothing is deleted, nothing is
reassigned, and the owner who comes back sees their own take in the log.

## The rules of the last two days

Nine rules, 2026-09-09 to 2026-09-11. Each came from a hurt and each is written so
a test can be built from it. Where a rule changes a sentence above, that sentence
has been changed to match, and this section is the reason. Where a rule names a
prototype behaviour, it is listed by number in **What the prototype does that this
spec forbids**.

1. **Counts, not lists.** `list` without `--list` prints no card. It prints one
   `BOARD LINE` per owner of an open card, with that owner's open, overdue and
   stale counts; one `BOARD LEG` per leg when the board holds rows (rule 4); one
   `BOARD OK` with the board's counts; and exactly one `BOARD NEXT` line naming
   the one thing to do first. `BOARD NEXT` is chosen by a fixed order: the oldest
   overdue card, else the oldest stale card, else the leg with the most owed
   rows, else the **oldest ordinary OPEN card**, else — and only when
   `open=0` — `nothing owed`. Oldest is by `since`, and a tie at the same
   second is the lexically smaller id; a tie between legs is the lexically
   smaller leg name. A board with one fresh card and a future deadline has
   something to do first, and a tool that said *nothing owed* over it would be
   the count falling for the wrong reason, said in words. Cards print only under `--list`, capped at `--max`,
   and past the cap one `BOARD MORE` line reads `and <t-n> more` with the remedy;
   `--list --owner <name>` prints only that owner's open cards, so one line
   reads its own batch of owed decisions in one command and never the board.
   `BOARD LINE` and `BOARD LEG` are capped at `--max` the same way. (Glenn,
   2026-09-09: counts, not lists; one remedy line. A listing is a context window
   spent on the good news.)

2. **Every card has an owner, a deadline and a default.** `add` requires `--by`, a
   duration from now or an RFC 3339 stamp, and `--default`, one line saying what
   happens if nobody closes the card by then. A card with no deadline cannot be
   filed: `add` without `--by` or `--default` is exit 2 naming both. The owner is
   as above: the latest take, else `--owner`, else the filer. An OPEN card whose
   deadline has passed is `overdue=true` on its `BOARD CARD` line and is counted
   in `overdue=<n>` on `BOARD OK` and on its owner's `BOARD LINE`. A deadline is
   never moved: a card that needs a later one is closed `superseded by <new>` and
   the new card carries the new date, so the log shows the slip. (Glenn,
   2026-09-09: every ask has a written deadline and a default action. Never wait
   forever.)

3. **One file per card, one writer per card, and no lock.** Under `--dir`, a card
   is one file, `<dir>/<id>.board`, created by `add` and appended to by `take`
   and `close`. The owner is the writer. Another line writes to a card's file
   only through `take`, which makes it the owner, or through `close --anyway`,
   which the event records. The board is a fold over the card files: `list` reads
   every file, derives every card, and sums. There is no shared file and no
   index. The tool holds no lock because it needs none, and this is why: an `add`
   creates a file whose name is the tool-owned id, so two adds of one thing are
   two files (the first race above, accepted and made cheap); a `take` or `close`
   appends one line to one file, and a second line appending to the same file
   appends a different line, which a union merge keeps in either order and the
   fold order sorts; the one write two lines can race on is one card's `take`,
   and that is settled by the read-before-append rule and by the fold order, as
   **The races** says.
   Under `--issue`, the forge serialises comments, which is the same property
   held by somebody else. (Glenn, 2026-09-11, Amdahl for coordination: scatter and
   merge; the serial step is the shared file, so there is none.)

4. **The owed ledger.** A card filed with `--thing <name> --leg <name>` is a
   **row**: one thing owed on one leg. A row is `owed` while OPEN and `probed`
   once closed by `probed <id>: <evidence>`. `list` prints one `BOARD LEG
   leg=<name> owed=<n> probed=<n>` per leg and `owed=<n>` on `BOARD OK`. Done
   means the owed count is zero, and the tool prints that number rather than a
   word: `BOARD OK ... owed=0` is the sentence a reader is waiting for. A row is
   closed only by `probed`; `closed` or `landed` on a row is `CLOSE REFUSED`, exit
   1, because a row that was not probed was not done. `--thing` without `--leg`
   or the reverse is exit 2 naming the other. (The pattern is
   `docs/FIXED-FORM-OWED-ROWS.md` in the schema repo, 2026-09-11: a table of
   (thing, leg, owed or probed), and the gate is the owed count reaching zero on
   every leg.)

5. **A machine's finding becomes a card by one command.** `add --evidence <path>`
   files a card whose event and `BOARD CARD` line carry `evidence=<path>`. The
   path is the crash seed, the gate's log, or the child's `RESULT.md`. The tool
   does not open the path and says so here: it is a claim about where the
   evidence is, made by the filer. A fuzzer, a gate or a triage script files with
   `--as <its name>` and the same `--by` and `--default` as anybody else, in one
   command, with nothing retyped. (Card 176549, 2026-09-11, came from a fuzzer
   crash seed, and its path went into the text by hand.)

6. **Silence is a state.** An OPEN card whose latest event is older than
   `--stale` is `stale=true` and is counted in `stale=<n>` on `BOARD OK` and on
   its owner's `BOARD LINE`. It is never hidden: `--open` still lists it and the
   counts still hold it. This widens the take rule in **The races** without
   changing it: a take over another line's take is permitted without `--anyway`
   exactly when the card is stale, and a card with no take goes stale the same
   way. (Glenn, 2026-09-10: ten minutes silent is offline. A silent card is not a
   closed one.)

7. **Bounded at the largest plausible state.** The largest plausible board is
   **500 cards across 20 lines**. At that state the default view prints at most
   `lines + legs + 2` lines and 4 KB, and `check` prints at most `--max + 1`
   lines. The output of the default view never grows with the number of cards.
   It grows only with the number of owners and legs, each capped at `--max` with
   a `BOARD MORE`. (Glenn, 2026-09-09: test at the largest plausible state;
   output bounded by design.)

8. **No default paths and no default durations.** Every directory and every
   duration comes from a flag: `--dir` or `--issue`, `--stale`, `--by`. A missing
   one is exit 2 with one line naming the flag, what it wants, and `run:
   nova-board help`. The banner stays behind `help`. The tool never uses `/tmp`
   or `$TMPDIR` and keeps no cache file anywhere. `--max` keeps its 20 because it
   is neither a path nor a duration and is SPEC.md's own law. (Glenn, 2026-09-10:
   no default paths. A board found through `$HOME` is a board a line writes to by
   accident.)

9. **The tool stamps; `add` has no `--at`.** Every stamp in an event line is
   written by the tool from its own clock at the moment of the append, RFC 3339
   in UTC: `since` is the `add` event's stamp and nothing a filer typed. There is
   no `--at`, `--since` or `--stamp` flag on any verb, and `add` refuses one as
   an unknown flag. A time a filer writes inside `--text` is text: it is carried,
   it is never parsed, and it never orders, ages or dates a card. `--by` is the
   one time a caller supplies, and it is a deadline, a fact about the work and
   not about when the tool wrote; it is stored as given and is never used as
   `since`. A board whose stamps are the tool's is a board two lines order the
   same way. (2026-09-11: a person stamped notes two hours ahead of the clock,
   and every list that ordered by the typed time put them in the future.)

## Tests this spec demands

One test per rule above, named for the rule, beside the tests the work list names.
Each is proven able to fail by a mutation before it is trusted.

1. `TestTheDefaultViewIsCountsNotCards`: a board of 50 cards; `list` prints no
   `BOARD CARD` line, one `BOARD LINE` per owner, one `BOARD OK`, exactly one
   `BOARD NEXT` chosen by the fixed order; a board holding a single fresh
   ordinary card with a future deadline prints `BOARD NEXT` naming that card
   and never `nothing owed`; two such cards at one second name the smaller
   id; an empty board and a board of only closed cards print `nothing owed`; `list --list` prints cards capped at
   `--max` and a `BOARD MORE` reading `and <t-n> more`.
2. `TestNoCardLivesWithoutADeadline`: `add` without `--by` is exit 2 naming
   `--by`; without `--default`, exit 2 naming `--default`; both missing is one run
   naming both; a card one second past its `--by` is `overdue=true` and counted,
   one second before is not, with the clock injected.
3. `TestTheBoardIsAFoldOverCardFilesWithNoLock`: `add` creates `<id>.board` and
   no other file; twenty concurrent takes and closes on twenty cards from two
   clones all land and no lock file exists; two lines taking **one** card from
   two clones both append and the union keeps both `taken` lines, and the
   owner is the take that sorts later by `(at, as, id)`; the same two appends
   merged in the opposite textual order by the other clone fold to the **same**
   owner, and the same events migrated to the issue backend name the same
   owner; two takes at one injected second by `Ada` and `Bo` name `Bo` from
   every clone and both backends; a mutation that folds in file order turns
   the test red; the source tripwire finds no lock call in
   `internal/board`; two backends with the same events print identical
   listings, including an overridden close that shows `BOARD CLOSE by=<name>
   override=true` from both with no git metadata and no comment author read.
   `TestTheIdIsRandomAndCreationIsExclusive`: two rows filed under one name
   with one text on two legs at one injected second, with both `add`s paused
   after reading the board and before appending, from two clones under `--dir`
   and from two processes under `--issue`, produce two cards with two distinct
   thirty-two-hex ids and neither is refused; the ids come from an injected
   random source and equal nothing computed from the fields; an `add` whose
   id already exists — a file placed by hand under that name — is `ADD
   REFUSED` at exit 1 and the file's bytes are unchanged; a mutation that
   opens the card file without `O_EXCL` turns the test red; an `add` whose
   text matches an OPEN card prints `ADD NOTE hash=<hash> matches open card`
   on stderr and files a second card; the source tripwire finds no `seq`
   field and no count read at `add` time; an `add` interrupted after its
   append with an injected fault, re-run with `--id` and the same fields, is
   `ADD OK existed=true` with one card on the board and nothing appended,
   under both backends; the same `--id` with a different `--text` is `ADD
   REFUSED … different fields` exit 1 with nothing written; two `card` lines
   with one id and different fields fold to `conflict=true` and
   `conflicts=1`. `TestConcurrentEventsAreCountedNotHidden`: two takes from
   two clones with the same `after=` fold to one owner by the total order,
   `BOARD CARD conflicts=1`, `BOARD OK conflicts=1`, identically from both
   merge orders and both backends; a take whose `after=` names the other
   take is not a conflict and `conflicts=0`; every `taken`, `closed`, `landed`
   and `probed` line carries a distinct twelve-hex `ev=` from the injected
   source and an `after=` equal to the newest `ev=` in the writer's fold or
   the card id; the migration fixture folds to the same owners and
   conflict counts before and after; `list --list --owner Bo` prints only
   `Bo`'s open cards and `BOARD OK` still counts the whole board.
4. `TestOwedCountsPerLegAndDoneIsZero`: rows on three legs, some probed; one
   `BOARD LEG` per leg with the right counts; `BOARD OK` carries `owed=<n>`;
   probing the last row prints `owed=0`; `closed` on a row is `CLOSE REFUSED`
   exit 1.
5. `TestAMachineFindingIsOneCommandWithEvidence`: `add --evidence <path>` writes
   `evidence=<path>` into the event and onto the `BOARD CARD` line; a path that
   does not exist files fine and nothing opens it.
6. `TestSilenceIsAStateAndIsCounted`: a card with no event past `--stale` is
   `stale=true`, counted on `BOARD OK` and on its owner's line, still listed
   under `--open`; one second under `--stale` is not stale; `close` without
   `--stale` is exit 2 naming it; the example pair under **Exit codes** is run
   verbatim against a fresh board and files one card; against a card taken by
   another line one second ago, `close --stale 10m` by a second line is
   `CLOSE REFUSED` exit 1 and the same with `--anyway` appends `override=true`;
   against a take eleven minutes old it closes without `--anyway` and appends
   `override=false`; the guard exits 2, not 0, when `check` is given no
   backend.
7. `TestBoundedAtFiveHundredCardsAcrossTwentyLines`: 500 cards, 20 owners, 5
   legs; the default view is at most 27 lines and 4 KB on stdout plus stderr;
   1,000 cards print the same number of lines.
8. `TestEveryPathAndDurationIsAFlag`: no `--stale` is exit 2 in one line naming
   it; no backend is exit 2 in one line; every prototype variable set in the
   environment (`BOARD_REPO`, `BOARD_ISSUE`, `BOARD_OWNER`, `BOARD_WINDOW`,
   `BOARD_CACHE_TTL`, `BOARD_MAXROWS`, `BOARD_NO_CACHE`) changes nothing; the
   source tripwire finds no `os.TempDir` and no literal `/tmp`.
9. `TestTheToolStampsAndAddHasNoAt`: `add --at <stamp>` is exit 2 as an unknown
   flag, and so are `--since` and `--stamp`; a card whose `--text` begins with a
   stamp two hours ahead of the injected clock has `since=` equal to the clock,
   sorts by it, and its event line's stamp equals the clock; `--by` given as a
   stamp is stored as given and does not change `since`; the source tripwire
   finds no time parse over the text tail.

## Known limits

- **`--as` is a label, not an identity.** The board says who claims to have acted.
  Who actually wrote is the forge's record or the file's git history, and this tool
  neither checks nor pretends to.
- **A stale take is a guess about a clock, never about a line.** A line working
  hard for twenty minutes without touching the board looks exactly like a line
  that died. The remedy is a second `take`, which is one command and is why takes
  are repeatable.
- **Derived state is only as good as the log.** An event appended by hand in the
  right shape is an event; one appended in nearly the right shape is ignored and
  reported as `BOARD NOTE unparsed events=<n>` — counted, never guessed at, and
  never silently treated as a close.
- **The whole-log read is O(all events).** That is the deliberate trade against the
  count being trustworthy. It is bounded in practice by a board being what is owed
  rather than what has ever been owed, and by the closing events being short.
- **No search but substring.** Stated above; a filer can predict it, which is worth
  more here than recall.
- **It has no opinion about what a card says.** It cannot tell a real finding from
  a mistaken one, cannot know whether an item is yours to take, and cannot enforce
  the rule this spec states first.

## What it deliberately does not do

- **No edit, no delete, no reopen, no release.** Four absences, one reason: the
  count may only move because work was filed or finished.
- **No assignment.** No verb gives a card to a line that did not take it. A board
  that could assign would be a board that can be used to hand somebody work, and
  on this bus nothing is a grant.
- **No priority, no labels, no ordering but the fold's.** Every one of those is a
  field two lines will fill differently, and none of them is needed to answer
  *what is owed* or *is this already filed*. An earlier draft listed due dates
  here. A deadline is not one of these: it is the family's rule that nothing waits
  without one, and it is required (rule 2).
- **No deduplication.** See the first race.
- **No notification.** A board does not wake anybody; `nova-wake` does, and a
  board as a fourth source is a v2 item for **that** spec and not a daemon here.
- **No environment configuration.** Not `BOARD_REPO`, not `BOARD_ISSUE`, not
  `BOARD_OWNER`, not `BOARD_WINDOW`, not `BOARD_MAXROWS`. A board identified by an
  environment variable is a board a line writes to by accident after changing
  shells, and CONTRIBUTING already says what reading the environment to decide
  behaviour costs a review here.
- **No window over the log, and no time-to-live cache.** Both above, both with the
  failure they cause.
- **No git.** The directory backend appends; landing it is the caller's.
- **No lock.** Not because a race cannot happen, but because no file has two
  writers at once (rule 3).
- **No second backend beyond the two.** A third is a spec change, and the test that
  both backends render identical listings from identical histories is what keeps
  the format from becoming two.

## Work list — building it in Go under `cmd/`, like `nova-bus`

Standard library only, no third-party imports, no hardcoded paths, and the repo's
shared packages used rather than re-spelled.

1. **`internal/board/event.go`** — the five event lines: render and parse, anchored
   at the line start, id as the token after the verb, `ev=` and `after=` on
   every later event, `as=` `at=` `override=` on
   every line, `oneline.Field` on every value, the id (128 bits from
   `crypto/rand` behind an injectable source, thirty-two hex), `hash=` as
   SHA-256 over the rendered text, and no sequence, no count and no read at
   `add` time. Tests: a card whose text contains a six-digit number cannot be closed by a
   sentence about another card; an unparsed line is counted and never guessed at; a
   text with a newline files one card; two ids drawn from a source that
   returns the same bytes twice are refused by `O_EXCL`, never overwritten.
2. **`internal/board/derive.go`** — events in, cards out: the fold order
   `(at, as, id, verb, bytes)` applied before anything is derived, owner from the latest
   take in that order, `since` from the add, first close wins, concurrent
   pairs by equal `after=` counted as `conflicts`, two `card` lines with one
   id and different fields as `conflict=true`, staleness as a pure function of
   `(latest event stamp, now, stale window)` and overdue as a pure function of
   `(by, now)`. Tests: a second close changes nothing; a
   re-take moves the owner; stale is annotation and writes nothing; identical event
   lists from the two backends derive identical cards; the same events in two
   textual orders derive one owner.
3. **`internal/board/backend.go`** — the `Backend` interface: `Events() ([]string,
   error)` and `Append(string) error`, nothing else, so nothing above it can learn
   which backend it has. Plus the cache rule: valid only while the backend's latest
   marker is unchanged, invalidated by this tool's own append, never truncating.
4. **`internal/board/issue.go`** — `gh` for read and append, under a timeout, one
   event per comment, full pagination. Tests against a recorded fixture rather than
   the network (CONTRIBUTING: a test that touches the network wants a reason).
5. **`internal/board/dir.go`** — one file per card, created `O_EXCL` and refused
   at exit 1 when it exists, the `BOARD v1` version line, append, read every
   file whole and fold, `#` comments and blanks ignored, a
   file without its version line is exit 2, a file that is not `<id>.board` is
   counted and never read. No git, no lock.
6. **`cmd/nova-board/main.go`** — the five verbs; exactly one backend or exit 2;
   `--as` required on every writing verb; `--stale` required on `take` and
   `close` as on `list`; the read-before-append for `take` and `close` with the
   ownership refusals at exit 1 and `--anyway` written as `override=true`; `internal/bounded`
   for `--max` with `BOARD MORE`; the count line printed on failure as well as
   success. Refusals name what the flag wants and report **every** independent
   problem in one go.
7. **`cmd/nova-board/check.go`** — the dumb matcher, `--all`, `CHECK HIT` capped,
   `matched=` never capped, and **exit 1 on a match**. The test is named for the
   mnemonic: `TestCheckExitsOneOnMatchSoTheShellGuardReads`.
8. **`quickstart`** — the natural first run: read the board, print the counts, print
   the `check … || { [ $? -eq 1 ] && exit 0; exit 2; }; add … --by … --default …`
   pair with this board's own values in it, quoted
   the way `nova-bus names` quotes — a value meant to be pasted rather than
   scanned.
9. **Onboarding, which `internal/ci/onboarding_test.go` will require the moment the
   directory exists** — a usage banner ending in an `example:` block whose lines
   run, a `### First run` in `README.md`, `nova-board help` on stdout at exit 0, a
   one-line refusal for a bad invocation rather than the banner.
10. **A migration of today's board** — `mas-bandwidth/schema#876`'s comments are in
    the prototype's shape, with backend-derived six-digit ids. Migration is an
    `add` per open card under the new id scheme and one `closed <old>: migrated to
    <new>` per old card, appended, with nothing edited and nothing deleted,
    every migrated event given its `ev=` and an `after=` naming the event
    before it in comment order — the
    tool's own rules applied to its own arrival. It is a script that runs once and
    is reviewed as code, not a verb.
11. **`SPEC.md` and `README.md` wiring** — the binary count in SPEC.md's opening
    paragraph, a `## nova-board` section or a pointer to this file, and the README
    `### First run`.
12. **The rules of the last two days, in `main.go` and `derive.go`** — the counts
    view as the default and `--list` for cards, `--by` and `--default` required,
    `overdue`, the row fields and `probed`, `BOARD LEG` and `owed=`, `--evidence`
    carried and never opened, `--stale` with no default on every
    verb that decides by it, `BOARD NEXT` by its fixed order with the ordinary
    open card before `nothing owed`. Tests: the nine in **Tests this spec
    demands** and `TestTheIdIsRandomAndCreationIsExclusive`.

## What the prototype does that this spec forbids

`bin/board.sh` (zsh, 189 lines) is where every rule above was learned, including
the two failures that cost a morning. These are the places it is **not** a model.

1. **A 40-comment window.** `jq -s "add // [] | .[-40:]"`: a card older than forty
    events is invisible, so the count falls without work being done. The read is
    the whole log.
2. **A 20-second cache with no invalidation by another line's write.** Two
    reviewers filing nine seconds apart both read a board from before either wrote
    — which is the 25-duplicate failure, mechanized.
3. **Six-digit ids truncated from the backend's comment id.** A collision in one
    million with no detection, and a different id in any other backend. The id is
    the tool's, thirty-two hex drawn from the OS at creation and derived from
    nothing, and an id that exists is a refusal and never an overwrite.
4. **Unanchored substring matching for state.** `test("closed[ :]*" + $sid)` and
    `test($sid) and test("taken by")` match anywhere in any comment body, so a
    card whose text contains a number can be closed by a sentence about something
    else — on a board full of issue numbers. Anchored at the line start, id as the
    token after the verb.
5. **`check` that always exits 0.** It cannot say NO, so it cannot guard an `add`,
    so the rule it exists to serve stays a rule people remember rather than a
    command they run. Exit 1 on a match.
6. **Environment-variable configuration**: `BOARD_REPO`, `BOARD_ISSUE`,
   `BOARD_OWNER`, `BOARD_WINDOW`, `BOARD_CACHE_TTL`, `BOARD_MAXROWS`, and
   `BOARD_NO_CACHE` — seven, five of which change *which board* or *how much of
   it* is read. Flags, every one, per run.
7. **Hardcoded defaults**: `mas-bandwidth/schema`, issue `876`, owner `rowan`. A
   tool only one line can run, pointed by default at a board it may not mean.
8. **No ownership check on `take` or `close`.** Both are a bare post. The two
   races this spec closes at write time are wide open.
9. **No stale rule.** A take is forever, so a card held by an offline line is owed
   by nobody and looks owed, which is the state the ten-minutes-silent rule exists
   to end.
10. **Silent truncation at 100 characters** in the row and 80 in `add`'s echo, with
    no mark, so a reader cannot tell a cut from an author's own ellipsis.
    `oneline.TailBytes` and `...+<dropped>B`.
11. **`CLOSED/#942` as one field** — a state and a location joined by a slash, in
    output meant for a scanner. Two facts, two fields.
12. **`printf '%-6s %-11s %-10s %s'` as the grammar**: a column layout with no
    leading token, no `key=value` fields, and an owner padded to ten characters, so
    nothing can be matched at a line start and a long name is cut to fit a column.
    One token first, fields next, free text last.
13. **A count line that describes the window rather than the board** — `(window:
    last 40 comments of …)` is an honest sentence about a dishonest read. `BOARD
    OK` counts the board.
14. **`jq` and `gh` as hard dependencies, with `die` at exit 2 for both.** `gh`
    stays, named, under a timeout; `jq` is `encoding/json`.
15. **`usage()` at exit 2 for `help`.** `help` is a successful command: stdout,
    exit 0.
16. **Rows by default, counts last.** `list` prints up to 60 rows and then one
    count line. Rule 1 inverts it: counts first and alone, rows under `--list`.
17. **No deadline on any card.** `Card (<UTC>, <owner>): <text>` has a filing
    stamp and nothing about when it is due, so nothing on the board can be
    overdue. Rule 2.
18. **No default action.** A card nobody takes stays OPEN forever and says nothing
    about what happens then. Rule 2.
19. **One shared stream, cached under `/tmp`.** Every card and every event is a
    comment on one issue, and the reader keeps a copy at
    `${TMPDIR:-/tmp}/board-sh-$UID`. Rule 3 for the directory backend, rule 8 for
    the cache and the path.
20. **No ledger.** A thing owed on nine legs is nine cards with the leg in the
    text, and the count still owed on a leg is a `check` and a person counting.
    Rule 4.
21. **Evidence retyped by hand.** `add "<text>"` takes words; card 176549's crash
    seed path went in by hand, and a typo there is a finding nobody can find.
    `child-report.sh` writes a triage page whose `Left owed` lines are exactly
    the cards that should be filed, and nothing files them. Rule 5.
22. **Silence invisible.** A card nobody has touched for a day lists exactly like
    one filed a minute ago. Rule 6.
23. **Never measured at a large board.** `BOARD_MAXROWS=60` and `BOARD_WINDOW=40`
    were chosen by feel, and the 40-comment window is what a bound looks like
    when it is chosen instead of measured: the count line lies. Rule 7.

## Ideas folded on 2026-09-11

| source | the idea, in six words | disposition |
|---|---|---|
| Stella, spec repairs | operation nonce, reused through retries | `add --id`: the drawn id is the nonce; `existed=true` on an identical retry, refused on a different one |
| Stella, spec repairs | derive the display id by hashing | left different: the 128-bit draw is the id; a hash of a random draw adds nothing it does not already have |
| Stella, spec repairs | same identity, different payload is conflict | `conflict=true`, `conflicts=` counted, neither wins |
| Stella, spec repairs | event ids and causal predecessors | `ev=` and `after=` on every later event |
| Stella, spec repairs | concurrent events visible, stable tie-break | the fold order (already) plus `conflicts=` (this pass) |
| Stella, spec repairs | migration preserves ids and links | work list 10 |
| Stella, ideas 2–3 | one decision packet per item; one home for ownership | already, rule 1 (`BOARD NEXT`, counts) and `take` |
| Emma, C3 | one batch of ready decisions per line | rule 1: `list --list --owner <name>` |
| Rowan, idea 7 | a state-of-the-table line the tools write | already, rule 1 (`BOARD OK`, `BOARD NEXT`) |
| DeepSeek, idea 1 | a derived digest, never hand-edited | already: the board is a fold, nothing is stored |
| DeepSeek, idea 4 | atomic claims, a cheap dashboard | already, `take` with the accepted race made visible; rule 1 |
| Freddy, idea 5 | batch acknowledgements | not folded: the board acknowledges nothing; receipts are `nova-wake serve`'s |
| ideas #273 | a belief whose evidence arrived later | `after=`: every event names what its writer knew |
| ideas #357 | a card is data, never an instruction | already, the second paragraph |
| nova-tools #35 | a shared file keyed by the clock races | already, rule 3 (one file per card) and the tool's own stamps (rule 9) |

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
| a card taken by a line that then went offline, owed by nobody and visible to everybody as owed by somebody | the stale rule: a take older than `--stale` (default **10m**) is annotated `stale` and the card lists as takeable again |
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
nova-board list   (--issue <owner/repo>#<n> | --file <path>) [--open] [--max <n>] [--stale <duration>]
nova-board add    (--issue ... | --file ...) --as <name> --text <text> [--owner <name>]
nova-board take   (--issue ... | --file ...) --as <name> --card <id> [--stale <duration>] [--anyway]
nova-board close  (--issue ... | --file ...) --as <name> --card <id> (--how <text> | --landed <repo>#<n>) [--anyway]
nova-board check  (--issue ... | --file ...) --words <text> [--max <n>] [--all]
nova-board quickstart (--issue ... | --file ...)
nova-board help
```

**Exactly one backend per invocation**, named. Neither `--issue` nor `--file` is
exit 2 and `refusing to guess`; both together is exit 2 as well, because a board
written to two places is two boards with one name. There is no default repository,
no default issue number, no default file and no default `--as`.

**`--as <name>` is required on every verb that writes** — `add`, `take`, `close` —
and is the name that goes in the event. It is not a credential and proves nothing:
the forge or the file's git history holds who actually wrote, and the name is what
the board *says*. A tool that pretended `--as` was an identity would be claiming
something it cannot check.

**No guessed anything, with two exceptions:** `--max`, which defaults to **20** as
every listing in this repo does, and `--stale`, which defaults to **10m**. The
stale window is not a fact only a board's owner can supply — it is this family's
ten-minutes-silent rule, written down — and a caller made to invent it would
invent a different one per line, which is the one number on a board that every
line must agree about.

`list` and `check` **read**; `add`, `take` and `close` **append**. No verb reads
and writes in one call, so nothing here can half-succeed in two places.

## The card, and the events

A card is five things, and only the first two are ever written by hand:

| field | what it is |
|---|---|
| **id** | twelve lower-case hex characters, assigned by `add`, never recomputed and never reused |
| **text** | what is owed, one line, as the filer wrote it |
| **owner** | the name from the latest `taken` event, else the `add` event's `--owner`, else the filer |
| **since** | the `add` event's stamp — when the thing became owed, not when it was last touched |
| **state** | `OPEN` or `CLOSED`, **derived** from the events and stored nowhere |

**The id is the tool's, not the backend's.** `add` computes it as the first twelve
hex characters of a SHA-256 over a preimage of `nova-board\x00<stamp>\x00<as>\x00<text>`
— the same shape and the same reasoning as `nova-bus`'s id: an id that cannot
collide between two lines adding in the same second, that survives moving the
board from one backend to another, and that is assigned **once**. A short id taken
from a backend's own comment number is forbidden: it is a different id in the
other backend, and the prototype's **last six digits** of a comment id is a
collision in one million with no detection and no recovery.

**Nothing is ever deleted or edited. Every state change is an appended event.**
There are four event lines and that is the whole format:

```
card <id> (<stamp>, <as>) owner=<name>: <text>
taken <id> by <name> (<stamp>)
closed <id>: <how> (<stamp>)
landed <id> in <repo>#<n> (<stamp>)
```

- `<stamp>` is RFC 3339 in UTC, to the second.
- `closed` and `landed` are the two closing events and the only two. `landed` is
  the one that names where the work went, which is the close a reader can verify;
  `closed … : <how>` is the one that says what happened in words, including
  `duplicate of <id>`, which is the **only** sanctioned way two cards for one
  thing become one.
- A second closing event on a closed card is permitted and changes nothing: the
  **first** close is the close, and the later ones are in the log where a reader
  can see that two lines thought they had finished the same thing.
- `taken` may appear many times. The latest one is the owner.
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

## One format, two backends

The **format** is the four event lines above. A backend decides only where a line
is appended and how the lines are read back; it decides nothing about what a line
means, and the two backends must produce identical `list` output from identical
histories. That is a test, not an aspiration.

**`--issue <owner/repo>#<n>` — issue comments, today.** One event per comment, in
comment order, appended with `gh`. It is where today's board lives
(`mas-bandwidth/schema#876`) and it is chosen for a good reason: every line in
this family can already read and write it, from any machine, with no clone and no
write access to a repo's branches.

**`--file <path>` — a file in a repository, tomorrow.** One event per line,
appended, read whole. The first meaningful line is exactly `BOARD v1` and nothing
else, for the reason `OPEN v2` has a version line: a later format read as this one
would be entries nobody wrote. Blank lines and `#` comments are ignored, as
everywhere else in this family's files. **The file backend appends and never runs
git**: committing and pushing it is the caller's, and saying so is more honest than
a push hidden inside `add`. That is the one asymmetry between the backends, and it
is named rather than hidden: an `add --file` is durable when the caller lands it,
and an `add --issue` is durable when the command returns.

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
| 1 | the verb ran and said **NO**: a `check` that **matched**, a `take` of a card another line holds, a `close` of a card another line holds, an append the backend rejected |
| 2 | could not run: a missing or malformed flag, no backend or two, an unreadable board, a `--file` without its version line, an id that names no card |

**`check` exits 1 when it matches, and that is the tool's most important
sentence.** A check in this repo is a thing that can say NO (SPEC.md: *a check
never seen failing is not a check*), and the NO a board owes a filer is **this is
already on the board, do not file it**. So the rule every reader and fixer follows
is one line of shell:

```sh
nova-board check --issue mas-bandwidth/schema#876 --words "windows runner skips" || exit 0
nova-board add   --issue mas-bandwidth/schema#876 --as rowan --text "the Windows runner skips three steps"
```

It reads the way it should be read: *if it is already there, stop*. The inverted
reading — 0 for "found it" — would make the natural `&&` chain file **exactly** the
duplicates, so the mnemonic is written into the banner, the README's first run,
and a test named for it. `--all` makes `check` report over closed cards as well,
still exit 1 on a match, because *somebody already fixed this* is as good a reason
not to file as *somebody already filed it*.

A `take` or `close` refused for ownership exits **1** and not 2: the verb ran, the
board was read, and the answer is no. `--anyway` turns either into a 0 and records
the override in the event, because there are real reasons to close somebody's card
— they went offline, it landed under another number — and a tool that made that
impossible would be edited around.

## Output grammar

```
BOARD CARD id=<id> state=<OPEN|CLOSED> owner=<name|-> since=<stamp> age=<d> taken=<d|-> stale=<true|false>: <text>
BOARD CLOSE id=<id> by=<name> at=<stamp> how=<closed|landed> where=<repo#n|->: <how>
BOARD OK cards=<n> open=<n> closed=<n> stale=<n> shown=<n> backend=<issue|file> source=<where>
BOARD MORE kind=<card> shown=<n> total=<t> <remedy>
BOARD NOTE <something true about this board that is not a card>
BOARD FAIL <id or source>: <reason>
BOARD REFUSED: <reason>
ADD OK id=<id> owner=<name> at=<stamp> backend=<issue|file> durable=<true|false>
ADD REFUSED: <reason>
TAKE OK id=<id> owner=<name> at=<stamp> previous=<name|->
TAKE REFUSED: <reason>
CLOSE OK id=<id> how=<closed|landed> where=<repo#n|-> at=<stamp> owner=<name|->
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

`BOARD OK` carries five counts and they are five different facts: `cards` is the
whole board, `open` is what is still owed, `closed` is what is not, `stale` is how
many open cards are held by somebody who has been quiet past `--stale`, and
`shown` is how many lines this run printed. **The counts print on failure as well
as success** and they are the truth about the **board**, not about the output: the
listing is capped at `--max`, the counting never is. `source=` is the issue or the
path, so a listing cannot be mistaken for a different board's.

`state=CLOSED` and the close's details are two facts and live on two lines — a
`BOARD CARD` and, under `--open`'s absence, the `BOARD CLOSE` that closed it. The
prototype printed `CLOSED/#942` as one field, which is a state and a location
joined by a slash, unparseable by the scanner it was written for.

## check — the rule, mechanized

```
nova-board check (--issue ... | --file ...) --words <text> [--max <n>] [--all]
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
`CLOSE REFUSED`, exit 1, naming the owner and the age of the take. `--anyway`
overrides it and the event records that it was overridden, so the log says a close
went over a live take. The same rule guards `take`: a take of a fresh take by
another line is `TAKE REFUSED` exit 1 naming the holder, which is the 2026-09-10
two-children-one-bug failure closed at the only moment a tool can see it. The
window between the re-read and the append cannot be closed by this tool over a
backend it does not own — so it is narrowed to one round trip and **named here**,
and the log's ordering settles what the tool could not.

**A take by a line that is offline — the ten-minutes-silent rule.** A card taken
by a line that then stops is owed by nobody and looks owed by somebody, which is
worse than unowned: nobody else will pick it up. **A taken card whose owner has
been silent past `--stale` (default 10m) returns to open**, and returning to open
means exactly this: the card still lists as `OPEN`, `stale=true`, with
`taken=<age>` and the holder's name still on it, and a `take` or `close` by
another line is **permitted without `--anyway`**. The board writes nothing. It has
no way to know whether a line is silent — it knows only when the take was written
— so staleness is an **annotation derived at read time from the take's stamp**, and
the only clock-dependent derivation in this spec. Nothing is deleted, nothing is
reassigned, and the owner who comes back sees their own take in the log.

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
- **No priority, no labels, no due dates, no ordering but the log's.** Every one of
  those is a field two lines will fill differently, and none of them is needed to
  answer *what is owed* or *is this already filed*.
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
- **No git.** The file backend appends; landing it is the caller's.
- **No second backend beyond the two.** A third is a spec change, and the test that
  both backends render identical listings from identical histories is what keeps
  the format from becoming two.

## Work list — building it in Go under `cmd/`, like `nova-bus`

Standard library only, no third-party imports, no hardcoded paths, and the repo's
shared packages used rather than re-spelled.

1. **`internal/board/event.go`** — the four event lines: render and parse, anchored
   at the line start, id as the token after the verb, `oneline` on every value, the
   id scheme (SHA-256 over `nova-board\x00<stamp>\x00<as>\x00<text>`, first twelve
   hex). Tests: a card whose text contains a six-digit number cannot be closed by a
   sentence about another card; an unparsed line is counted and never guessed at; a
   text with a newline files one card.
2. **`internal/board/derive.go`** — events in, cards out: owner from the latest
   take, `since` from the add, first close wins, staleness as a pure function of
   `(take stamp, now, stale window)`. Tests: a second close changes nothing; a
   re-take moves the owner; stale is annotation and writes nothing; identical event
   lists from the two backends derive identical cards.
3. **`internal/board/backend.go`** — the `Backend` interface: `Events() ([]string,
   error)` and `Append(string) error`, nothing else, so nothing above it can learn
   which backend it has. Plus the cache rule: valid only while the backend's latest
   marker is unchanged, invalidated by this tool's own append, never truncating.
4. **`internal/board/issue.go`** — `gh` for read and append, under a timeout, one
   event per comment, full pagination. Tests against a recorded fixture rather than
   the network (CONTRIBUTING: a test that touches the network wants a reason).
5. **`internal/board/file.go`** — the `BOARD v1` version line, append, read whole,
   `#` comments and blanks ignored, a file without its version line is exit 2. No
   git.
6. **`cmd/nova-board/main.go`** — the five verbs; exactly one backend or exit 2;
   `--as` required on every writing verb; the read-before-append for `take` and
   `close` with the ownership refusals at exit 1 and `--anyway`; `internal/bounded`
   for `--max` with `BOARD MORE`; the count line printed on failure as well as
   success. Refusals name what the flag wants and report **every** independent
   problem in one go.
7. **`cmd/nova-board/check.go`** — the dumb matcher, `--all`, `CHECK HIT` capped,
   `matched=` never capped, and **exit 1 on a match**. The test is named for the
   mnemonic: `TestCheckExitsOneOnMatchSoTheShellGuardReads`.
8. **`quickstart`** — the natural first run: read the board, print the counts, print
   the `check … || exit 0; add …` pair with this board's own values in it, quoted
   the way `nova-bus names` quotes — a value meant to be pasted rather than
   scanned.
9. **Onboarding, which `internal/ci/onboarding_test.go` will require the moment the
   directory exists** — a usage banner ending in an `example:` block whose lines
   run, a `### First run` in `README.md`, `nova-board help` on stdout at exit 0, a
   one-line refusal for a bad invocation rather than the banner.
10. **A migration of today's board** — `mas-bandwidth/schema#876`'s comments are in
    the prototype's shape, with backend-derived six-digit ids. Migration is an
    `add` per open card under the new id scheme and one `closed <old>: migrated to
    <new>` per old card, appended, with nothing edited and nothing deleted — the
    tool's own rules applied to its own arrival. It is a script that runs once and
    is reviewed as code, not a verb.
11. **`SPEC.md` and `README.md` wiring** — the binary count in SPEC.md's opening
    paragraph, a `## nova-board` section or a pointer to this file, and the README
    `### First run`.

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
    the tool's, twelve hex, over a preimage.
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

# nova-wake — specification

One verb at the **attention layer**. A window that coordinates other lines spends
its turns on a clock: it sleeps, wakes, looks at three places, finds nothing, and
sleeps again. Every one of those cycles is a model turn, and a turn that learns
nothing is the most expensive kind of nothing there is. `nova-wake` is that cycle
inverted. It is **one blocking call** that returns the moment something the window
cares about has changed, and otherwise at a deadline the caller named — so the
harness itself wakes the session, because that is what the return of a tool call
is, and the window pays one turn per *change* rather than one turn per *tick*.

It watches a bus inbox, the checks on a set of entries, and report files written
by other lines — and, since the **amendment of 2026-09-13**, the comments and
reviews on named or owned pull requests, the check runs on a named head, a
branch moving, and a lock file being released — and it says what moved. It does not act on any of
them. **Everything it prints is data**: a note it relays is not an instruction, a
failing check is not a verdict about whose fault it is, and a report file is prose
somebody else wrote. A watcher that acted on what it saw would be a window with no
person in it.

This spec is normative. It is a sibling of [SPEC.md](SPEC.md), whose
**Conventions** section — exit codes, no guessed paths, the one-line guarantee,
the field escape, the cap-and-count law — governs here unchanged except where this
document says otherwise, and it says so by name in one place only (**Exit codes**).
If the code and this document disagree, one of them has a bug, and the tests
decide which.

## The two lessons of 2026-09-11

Both of these were paid for in one day, by one window, in Fable turns.

| what happened | what it cost | the rule it bought |
|---|---|---|
| A window ran a five-minute sleep tick to find out whether anything had moved. Most ticks found nothing. | Every tick was a model turn on the most expensive model in the fleet, spent learning that the world was exactly as it had been left. The turns were gone and the window had not coordinated anything with them. | **A watcher blocks; it does not tick.** One call, one return, one turn per change. The clock lives inside the tool, where a poll costs a subprocess and not a turn. |
| A harness `/loop` fired a whole model every minute as a heartbeat. First fire: 53 tool calls and 165 seconds to see a note already answered; second: 58 tool calls on an empty inbox (Johnny, 2026-09-11, on the Grok harness). | Every empty minute was a load: a full context re-read to learn that nothing had arrived. | **An in-session poll is not a wake.** A harness interval that runs a model — a `/loop`, a scheduler prompt, a heartbeat — is the five-minute sleep by another name; `watch` is the blocking call inside a turn, `serve` is the process outside one, and there is no third shape. Empty minutes cost zero tokens (rule 10). |
| The state for one watched entry was stored as a **tab-joined** string whose last field was often empty. Reloading it dropped the trailing empty field, so the reloaded value never equalled the freshly computed one. | Every poll reported a change. The watcher woke the window every interval, forever, with nothing to say — a false wake is worse than a missed one, because the window learns to stop reading. | **A state value round-trips or it is not state.** The separator may not be a character the reader can eat, the comparison is byte-for-byte over the stored form, and a test writes state, reloads it, and asserts a second identical poll reports **no change**. |

A third failure was paid the same day and is in **The races** below, because it is
a race rather than a lesson about clocks: the watcher's own test run consumed five
bus notes that the window then had to be told about by hand.

The four races and the rule that closes each, so the settlement of every
surface is in one place (each is argued in **The races** and pinned by the
test named):

| the race | the rule that closes it | test |
|---|---|---|
| two watchers advancing one bus cursor | one advancing watcher per `(bus, as)`: an exclusive kernel lock, the second is `WAKE REFUSED` exit 2 naming the holder | 12 |
| two runs writing one state file | one writer per `--state`: an exclusive kernel lock on `<state>.lock` for the whole call, the second is `WAKE REFUSED` exit 2 naming the holder and the fix | 4 |
| mail consumed by a probe and not relayed | `--advance-cursor` off by default, only for the window's own `--as`, and only behind an empty bus queue: the advance is a spooled transaction and consumed mail is bounded by this tool's own pending queue, never by anything `nova-bus` re-lists | 5, 12 |
| a harness kill between observing and printing | closed in the safe direction by ordering (rule 11): a kill anywhere leaves the entry pending, a repeated wake and never a lost one; the advance's own kill point is recovered through `inbox --open` | 11 |

## Amendment of 2026-09-13 — event sources and the availability probe

**Status: an amendment to a ratified spec, drafted 2026-09-13 from nova-tools
#178 and the record of the coordinating window; nothing above or below is
withdrawn except where a sentence says `amended 2026-09-13` beside it.**

**Draft 2, 2026-09-13**, repairs draft 1 (head 11c807a3) against two cold
reads taken at that head — an Opus read (comment 5655212833, HOLD on nine) and
a Fable read (comment 5655214937, HOLD on six) — and every passage it changes
is marked `amended 2026-09-13, draft 2` beside the change, with the defect
that bought it named. The nine repairs the two reads share or demand
separately: the source flag renamed `--ref` so `--branch` keeps one meaning on
every verb; the self-exclusion made computable from a read that is specified;
`WAKE BROKEN` and rule 8 widened to all seven sources so a dead `gh` can never
end a `--pr`-only watch quiet; `probe --here`'s process count taken as a count
and not a `/proc` listing, so test 4's tripwire needs no exemption; `--run`
given the 30-second floor and the rate arithmetic worked against the forge's
two pools; `--to-only` reconciled with *a change, always* and with rule 7's
sum, and given the test it lacked; the capped kinds made eight and the bound
recomputed as `8 * --max-lines + 17` with its variable terms named; `probe
--line`'s lock and its one written key stated; `free` and `absent`
distinguished per build. The smaller findings are folded in place and marked
the same way. **No design of draft 1 is reversed**: every repair is a
sentence, a flag name, a clause on a test, or an arithmetic that was wrong.

**Draft 3, 2026-09-13**, repairs draft 2 (head 8e459652) against two cold
reads taken at that head — a Fable read (comment 5655353155, HOLD on three)
and an Opus read (comment 5655354635, HOLD on three) — and every passage it
changes is marked `draft 3` beside the change with the defect named. The
fourteen repairs: the ping recorded only after `pushed=true`, so a committed
and unpushed note is not a ping; the three observation-time exclusions named
as three at **State**, with the stored value on a self-only tick defined and
rule 14 reconciled with the ten-node bound; the cold-start rule widened from
two sources to all seven but the bus; `probe`'s exit paragraph stripped of the
roster read draft 2 deleted eight lines below it, with the unknown `--line`
given its state word; `self=` and `silent-after=` added to the grammar and
`calls=`/`login=` taken off the locks line; `sources-failing=` given the one
verdict it belongs to; rule 5's bound sentence made to match its own
paragraph; `--owned-prs`'s `gh pr list` given failure semantics; *the one
write-side call* corrected to `watch`'s one of three; `--ref` and `--refresh`
named as an exact parse that must not cross-suggest; the `gh api user` moved
to the row that needs it; the review-thread reply named beside `DISMISSED`;
the Darwin process count's constant named; and draft 2's *two exceptions*,
*one duration* and undocumented 2m closed. **No design of draft 2 is
reversed**: every repair is a sentence, a clause, a count or an arithmetic
that was wrong.

**Draft 4, 2026-09-13**, repairs draft 3 (head 11b4bece) against the scoped
Astra read at that head (comment 5655645243, five findings retained from
comment 5655347868), and every passage it changes is marked `draft 4` beside
the change with the defect named. The seven repairs, by that read's finding
numbers: **1** — a forge login is a shared credential, so nothing is set aside
by author any more, every observed movement wakes, and the narrowing is
`--not-mine`, opt-in and made of the ids this actor emitted; **2** — a reply on
an older thread is no longer declared invisible: the value carries the pull
request's own `updated=` from the same call, and a stamp that moved with
nothing else is a change reported as `rescan=true`; **3** — contact and
scheduling eligibility are two readings, `contact=` beside a state word,
`RESTING` added for a declared rest that no clock ends, and `ANSWERED` narrowed
to a note or receipt from the line addressed to the caller; **4** — rule 2's
git-author mapping named at its true width, with a note on a `--line` no commit
carries; **5** — the ping given a durable intent and an anchor, an interrupted
send reconciled before any resend, and *never twice* rewritten as the exact
guarantee it can keep; **6** — the lock source given its protocol-to-path
mapping per build, an `O_NOFOLLOW`/`O_NONBLOCK` open with an `fstat` that
refuses every non-regular file, and the untestable *a blocking contender is
unaffected* replaced by what one actually observes; **8** — the `git/ref`
prefix-array claim withdrawn in favour of the documented contract, with the
defensive branch kept. **No design of draft 3 is reversed**: every repair is a
sentence, a flag, a field, a clause on a test, or a claim that was not the
tool's to make.

**Draft 5, 2026-09-13**, repairs draft 4 (head a36884fb) against the Fable read
at that head (comment 5657180778, HOLD on one HIGH, seven MEDIUM and seven
LOW), and every passage it changes is marked `draft 5` beside the change with
the defect named. The repair that held the draft is a **re-derivation and not a
patch**: draft 4 reconciled an interrupted ping by reading the bus checkout, and
the checkout carries a send's commit before the push does — so the second draft
in a row moved the defect of finding 5 rather than closing it. Draft 5 deletes
the whole category of claim *local state can say whether a send reached the
bus*: the one evidence a ping left this bench is the note's commit on the
**fetched** remote ref of the lane, a fetch is what any reconcile costs, and a
probe that cannot fetch says `UNRECONCILED` and claims nothing. The rest, by
that read's numbers: **M2** — the availability transitions made total, a matrix
with a cell for every state under every event; **M3** — draft 3's *a new sign
returns `ANSWERED`* deleted where it still stood; **M4** — the correlation read
taken off the caller's own cursor and bounded by the ping's anchor; **M5** — a
self-only tick's `updated=` watermark advanced only as far as that tick's
attribution reaches; **M6** — `headRefOid` read in the standing query, so a
window's own push to its own pull request is a push and not a rescan; **M7** —
the per-`--line` note named in rule 5's variable terms; **M8** with **L13** —
the three lock build files named, the clause for a build with no protocol
deleted, and the sentinel build's stale `.held` named as that build's limit. The
seven LOWs are folded in place. **No design of draft 4 is reversed**: the ping
is still intended before it is made, the anchor is still the checkout's head
before the send, and what changed is which ref the evidence is read from.

**Draft 6, 2026-09-13**, repairs draft 5 (head fb00295e) against the Astra read
at that head (comment 5657411696, HOLD on K1 to K6), and every passage it
changes is marked `draft 6` beside the change with the defect named. The repair
that held the draft is again a **re-derivation and not a patch**: draft 5's
reconcile asked the lane whether *this bench pushed anything past an anchor*,
while `nova-bus send --file` assigns a fresh note id on every call and the bus's
push deliberately carries an earlier unpushed local commit along — so an
interrupted ping and its resend are two notes a friend can see, and one
unrelated note by the caller reads as a delivered ping. Draft 6 deletes the
anchor-commit reconcile and takes the bus's own answer to this class instead:
the ping is `nova-bus prepare`d, which fixes the note id in an artifact
**before** any send; the intent names that id; every send is `nova-bus send
--prepared` of that one saved artifact, which is its own retry and carries the
same note's unpushed commit along with it; and a reconcile asks the **fetched**
remote lane for a note with **that exact id**. One note identity, offered until
it lands, delivered once. The rest, by that read's numbers: **K2** — delivery
matched by note id and never by a caller commit past an anchor; **K3** —
identity and anchor carried through every transition, the anchor demoted to the
correlation range it is, and the two inbox reads bounded with `correlation=`
printed where they cannot cover that range; **K4** — the `updated=` watermark
stored on every tick as observed, and suppression forbidden to make a moved
stamp silent; **K5** — the transition matrix and its prose made one contract,
with `reconcile` and `no-fetch` moved ahead of `ping` in the column order the
prose already required; **K6** — `probe:<name>` bound to bus, remote, branch,
caller and line, `--as` required wherever a record stands, and the prepared
note's own recipient compared with `--line` before any send. **No design of
draft 5 is reversed**: the ping is still intended before it is made and the one
evidence is still the lane's fetched remote ref — what changed is that the
evidence is now the note's own id.

**Draft 7, 2026-09-13**, repairs draft 6 (head 79194bc6) against the Astra read
at that head, which cleared K1, K2, K4, K5 and K6 and held **K3** with one
qualifier, and every passage it changes is marked `draft 7` beside the change
with the defect named. **K3**: draft 6 said in one passage that the correlation
was two bounded `nova-bus inbox` reads and in another that both were bounded,
and neither could be true — SPEC.md's inbox prints every **NEW** note in full
on every run and caps only the carried list with `--open-max`, so the plain
read's size is the bus's and capping it from here would mean asking for a
friend's mail and truncating it, which is a capture and a cut and not a bound.
So the correlation stops being an inbox call at all. The probe makes its own
read: `git`, read-only, over the pinged line's lane from the ping's anchor
forward, taking note **headers** and appended `RECEIPTS` lines and never a
body, under a per-poll budget of `--correlate-max` items and
`--correlate-bytes` bytes; where the budget stops short of the lane's tip the
line says `correlation=partial remaining=<n>`, the state word stays `PINGED`
however far `--answer-within` has run, and the position reached is bookmarked
in the record's new seventh field so the next poll **resumes** there. (Draft 8
rewrites what those bounds measure, makes an unread item a **coverage gap**
rather than a skip, and counts `remaining=` in items.) Bounded
per poll, complete over polls, and `UNAVAILABLE` is never inferred from a read
that could not have seen the answer. The read is non-destructive as draft 6's
was and more plainly so: no cursor advance, no `OPEN` write, no `nova-bus`
started, and the caller's own cursor is nowhere in the correlation — which
closes draft 5's finding M4 by construction rather than by a union of two
listings. The qualifier: *`pinged` is recorded after `SEND OK pushed=true` and
never otherwise* now reads **on a living call, or after a reconcile that finds
the prepared note id on the lane's fetched remote ref**, which is the second
path the reconcile paragraphs have described since draft 4 and which that one
sentence excluded. **No design of draft 6 is reversed**: one prepared identity
offered until it lands, the fetched remote ref as the one evidence, the anchor
as the correlation range's floor and the caller's cursor kept out of the answer
all stand — what changed is which program performs the correlation read, and
that it can stop and resume.

**Draft 8, 2026-09-13**, repairs draft 7 (head 4ffeb264) against the Astra
direct read at that head (bus note `stella-3e1c0182f78f`), which narrowed K3 to
three details, and every passage it changes is marked `draft 8` beside the
change with the defect named. **K3a** — draft 7 skipped an over-4 KiB header
and then let a read that reached the tip count as **complete**, so a skipped
potential answer could be followed by `UNAVAILABLE`: a skipped or unreadable
item is now a **coverage gap**, retained in the record, counted on the line as
`gaps=<n>`, named once, and `complete` requires the tip **and** zero gaps, so
incomplete coverage can never become negative evidence about a friend.
**K3b** — *every byte this read takes out of the object store* is not
enforceable by a program driving `git`, which inflates and reads more than it
returns: the byte bound is now **consumed and decoded** bytes, joined by a wall
clock on the `git` processes and a one-item streaming buffer, with physical I/O
named as unbounded rather than promised; `remaining=` is counted in **items**
and not commits, because nine hundred notes in one commit are one commit and
three polls of work, and it is **`-`** where the exact count would itself cost
an unbounded walk; and the within-commit read order is fixed — note paths
bytewise, then `RECEIPTS` lines in file order — so a bookmark means the same
thing twice. **K3c** — a byte budget could stop mid-header or mid-line where
the bookmark can only name an item: item handling is now **all-or-none**, a
poll stops **before** an item that does not fit and never advances past unread
content, an item larger than a whole budget is a gap rather than a stall, and
a poll that takes no item and records no gap is named as the spin a test
fails. And the bounded lane read is named once as **the bounded read**, owned
by this file, referenced by SPEC-BUS-REPLY.md (#267) rather than restated
there. **No design of draft 7 is reversed**: the correlation is still this
tool's own bounded read of the lane, still bounded per poll and complete over
polls, and `UNAVAILABLE` is still owed a complete read — what changed is what
*complete* means, what the bounds measure, and that an item is read whole or
not at all.

**Draft 9, 2026-09-13**, repairs draft 8 (head 2c76c56c) against the Astra read
at that head, which held **two acceptance lines** and reopened nothing else;
every passage it changes is marked `draft 9` beside the change with the defect
named. **K4a** — draft 8's oversize-header test asserted that raising
`--correlate-bytes` reads an over-4 KiB header, and it cannot: the **4 KiB
header cap is fixed**, it is not a share of the byte budget and this spec adds
no flag that raises it, so a header with no blank line inside 4 KiB is a
**permanent gap at every budget** and the test claimed an option this tool does
not have. **K4b** — the raise a caller *can* make is real for a different
fixture, so the two are now separate fixtures rather than one sentence: a
header that ends **within** 4 KiB whose whole item cost is larger than a small
`--correlate-bytes` is a gap **at that budget only**, and it clears when the
budget is raised. **K4c** — *a poll that read nothing may never say `complete`*
was written unconditionally, and unconditionally it takes `UNAVAILABLE` off the
board: an **empty lane**, and a **covered lane whose tip has not moved since
the last poll**, are exactly the complete negative evidence a timeout is owed,
so the unqualified rule would hold a genuinely silent friend at `PINGED` for
ever — the opposite of rule 17. The rule is now **zero progress while uncovered
items or gaps remain**, which still fails the spin it was written for and lets
a covered silence time out. **No design of draft 8 is reversed**: coverage
gaps, the five bounds, all-or-none items, and `complete` as the tip **and**
zero gaps all stand.

The
amendment adds four sources to `watch`, one flag on the bus source, one verdict,
and one verb (`probe`); it changes no exit code that exists and takes the one
that was reserved. Every rule it adds is in **The rules of 2026-09-12 and
2026-09-13** and has a test in **Tests this spec demands**.

The hurts, each dated and quoted from the record:

| what happened | what it cost | the rule it bought |
|---|---|---|
| **2026-09-11**, measured in the coordinating window: 1,204 assistant turns, 1.19M tokens written, 652M cache-read tokens. Of the visible output, bash status checks were the largest single item, and the tell recorded was *"a status check I ran in the last minute, run again."* | *"the cost is turns times the context each carries, not the words written"* — every poll re-read half a million tokens to learn nothing. | **Every wait the window has is a source of this tool, so no wait is a poll.** A thing a window has to check twice in a minute is a source here or a defect here (rule 13). |
| **2026-09-12, 20:32Z to 22:09Z**: two review HOLDs with concrete defects sat unread for **100 and 45 minutes**, because they were comments on pull requests the window owned and *"the bus wait is my only wake, and it does not know about PR comments."* The reviewer asked on the bus for an ownership receipt before they were read. | Two merges waited on findings already written; the reviewer paid a turn to say so; *"reads and holds arrive where the reader is."* | **A pull request the window owns is watched for comments and reviews on the same call as the bus** (`--pr`, `--owned-prs`; rule 14). |
| **2026-09-10**: nineteen orphaned shells in one sweep — *"thirteen polling a log for an EXIT line from a child three and three-quarter hours gone, two waiting for `pgrep -f` … to come up empty, which it never can because the loop's own command line contains the string."* | Nineteen processes spending, invisibly, on a machine five lines share. | **A wait ends on the first change, at its deadline, or on the caller's stop, and never otherwise**; it never finds anything by `pgrep`; it never holds the lock it probes (rules 15 and 16). |
| **2026-09-09**: READY was sent to a friend for a profiling window, and two heavy children were started three minutes later. Mercury's correction, verbatim: *"READY only when the system is quiet enough for the task."* | A friend's measurement was thrown away on the word of a plan. | **A readiness receipt is a measurement read at the instant of sending — load average and process count — and a promise about the next ten minutes** (`probe --here`; rule 18). |
| **2026-09-12**, Glenn, the five-minute availability rule, relayed live: *"If we don't hear from a friend for 5 minutes, we should try to ping them to wake up. If that doesn't work, then they have probably gone to sleep (run out of credits, plan, etc). We should always consider this when scheduling tasks. If we assign to somebody who is not here, the work will never get done."* | A packet sat receipt-pending for over an hour because silence was never probed; a read receipt was taken as proof of an active task. | **`probe`: last sign from the bus lane; one ping if silent past `--silent-after`; unavailable if unanswered within `--answer-within`; never a second ping for one silence; never "out of credits" from silence** (rule 17). |
| **2026-09-13**, Emma, on the bus (emma-0fd8c03d5f24), verbatim: *"Waiting for background soak runs, child workers, or upstream PR lands tempts token-heavy polling loops or arbitrary timers. Verb: `nova-wake wait --event <branch-pushed\|run-finished\|lock-released> --timeout <T>` — cleanly suspends and resumes the coordinator on exact OS/bus events without polling."* | The three events she names had no source, so each was a loop or a timer in a turn. | **`--ref`, `--run` and `--lock` are sources of `watch`** (`--ref` was spelled `--branch` in draft 1; amended 2026-09-13, draft 2), and `watch` is the verb — see **Why `watch` and not a new `wait`** below. |

**Why `watch` and not a new `wait`.** Emma's verb and this tool's `watch` are
one shape: block, return on the first change or at a deadline, print what
moved, decide nothing. Her `--event branch-pushed` is `--ref`, `run-finished`
is `--run` with `--final-only`, `lock-released` is `--lock`, and `--timeout` is
`--max`. A second verb would be the same loop under a second name, with a second
state file, a second cap and a second grammar to keep equal — and the window
would have to choose between them on every call. So the events are sources, and
`watch` carries them, in the same call as the bus, so one turn waits on all of
them at once (Emma's proposal asked for one event per call; a window waiting for
a branch to move is also a window that must hear a note). The one verb this
amendment does add, `probe`, is added because `watch` **cannot** carry it: a
probe writes a note to the bus, which a watcher never does, and it gates on an
answer, which a watcher never does (**Exit codes**).

## The verb

```
nova-wake watch --state <file> --max <duration> --on-deadline <word> --interval <duration> [--max-lines <n>] [--baseline]
      [--bus <dir> --as <name> --receipt-max-words <n> [--refresh --remote <name> --branch <name>] [--advance-cursor --remote <name> --branch <name>]]
      [--line <name> ... [--offline-after <duration>]]
      [--entry <repo>#<n> ... --entry-interval <duration>] [--final-only] [--gh-timeout <seconds>]
      [--reports <dir> ...]
      [--to-only]                                                          (amended 2026-09-13)
      [--pr <owner/repo>#<n> ...] [--owned-prs <owner/repo> ...] [--not-mine <file>] [--ref <owner/repo>:<name> ...] --forge-interval <duration>
      [--run <owner/repo>@<sha> ...]                              (with --entry-interval, floor 30s)
      [--lock <path> ...]
nova-wake probe --bus <dir> --line <name> --state <file> [--silent-after <d>] [--answer-within <d>]   (draft 2: defaults 5m, 2m)
      [--rest <file>] [--refresh --remote <name> --branch <name> --interval <duration>] [--ping-draft <file> --as <name>] [--gh-timeout <seconds>]
      [--correlate-max <n>] [--correlate-bytes <n>]                        (draft 7: defaults 300, 262144)
nova-wake probe --here [--quiet-load <x>]
nova-wake serve --bus <dir> --as <name> --on-note <command> --interval <duration> --state <file> --hours <h> [--receipt --remote <name> --branch <name>] [--on-note-idempotent] [--batch-max <n>] [--git-timeout <seconds>]
nova-wake serve --bus <dir> --as <name> --state <file> --redeliver <id> --on-note <command> [--on-note-idempotent]
nova-wake awake --bus <dir> [--window <seconds>] [--max <n>]   --window default 300, --max default 50
nova-wake quickstart --state <file> [--max <duration>] [--on-deadline <word>]
nova-wake probe --bus <dir> --line <name> --state <file> [--silent-after <d>] [--answer-within <d>] [--rest <file>]
nova-wake probe --here [--quiet-load <x>]
nova-wake version
nova-wake help
```

One verb that watches, one that serves, one that asks who is awake, one that
shows a first run, one that asks whether a line may be handed work, `version`,
and `help`. `probe` is a one-question call that reports and gates nothing;
`version` names this build.
The two shapes are exactly two: `watch` is a **blocking tool call** inside a
turn the session is already spending, and `serve` is a **process outside any
session** that starts a turn only when a note has landed (rule 10). There is no
third: `watch` has no `--detach` and no background mode, and an in-session
poll — a harness `/loop`, a scheduler prompt, a heartbeat that runs a model on
an interval — is not a wake and is not `serve`; it is the five-minute sleep of
the first lesson, a load per tick, and this tool offers no verb for it (Johnny,
2026-09-11: 53 tool calls to learn nothing).

`awake` reads presence over bus cursors: for every `from-<name>/CURSOR` lane in
the bus clone, the newest commit touching the cursor is that friend's last beat,
classified `awake` inside `--window`, `asleep` past it, and `unknown` where no
cursor has ever been written (docs/SPEC-WORK.md, **Presence**, source
`bus-cursor`).

**At least one source, named.** A `watch` with no `--bus`, no `--entry`, no
`--reports`, no `--pr`, no `--owned-prs`, no `--run`, no `--ref` and no
`--lock` (amended 2026-09-13, draft 2) is exit 2 and `refusing to guess`: a watcher with nothing to watch is
a `sleep` with a longer name, and it is the one invocation that would look like it
was working.

**No guessed anything, with five exceptions** (amended 2026-09-13, draft 3,
from the Fable read: draft 2 said *two* while listing five). There is no default state file, no
default bus, no default bus name, no default entry, no default report directory,
no default repository, no default poll interval and no default action at the
deadline. Each missing one is exit 2 and `refusing to guess`. The five are
`--max-lines`, which defaults to **40**, and `--gh-timeout`, which defaults to
**45 seconds** — neither a fact about this window's world that only this window
can supply, which is the test the rule is really making — and the three
durations that are the **family's** rather than one window's: `--offline-after`
at **10m** (rule 2) and, amended 2026-09-13, draft 2, `probe`'s
`--silent-after` at **5m** and `--answer-within` at **2m**. **Both of
`probe`'s numbers come from one source, Glenn's five-minute availability rule
of 2026-09-12** (amended 2026-09-13, draft 3, from the Fable read, which found
the 5m quoted and the 2m asserted): the 5m is that rule's own number, and the
2m is the bound on its second half — *"we should try to ping them to wake up.
If that doesn't work, then they have probably gone to sleep"* — which names a
wait and does not measure it, so the family's answer window is the family's
number and not a per-caller guess. Both are argued under **probe**. A line cap is this repo's
own law (SPEC.md, Conventions) and is 20 everywhere a listing is a listing, and 40
here because a wake line is not a listing: it is the whole of what the window
learns from this return. `--interval` had a default of 30 seconds in an earlier
draft and lost it on 2026-09-11: the right cadence is a fact about the watched
thing's rate, which only the caller knows (**The rules of the last two days**,
rule 3). `--max` gets **no** default for the same reason `nova-bus wait --timeout`
gets none: a deadline is the one thing the caller must state, because a watcher
with no deadline is a window that is stuck rather than waiting and nobody outside
can tell the two apart. (The prototype defaulted it to 1200 seconds. That default
is the difference between a tool that ends and a tool that has to be killed.)

**The ceiling is the harness's, and 20 minutes is the recommendation.** A watch
runs inside a tool call and every harness kills a call that runs too long, so a
`--max` above the harness's limit does not watch longer — it is killed with
nothing said at all, which loses both the changes seen and the state not yet
written. `--max` above **60m** is refused with that sentence and the advice to ask
your harness what its limit is and sit under it. `--interval` will not go below
**5s**, because a poll is a `git fetch` and an API call against somebody else's
server.

**The only programs it starts are `nova-bus`, `gh` and `git`**, all named here,
all under a timeout, and one at a time except the entry, run and pull-request
polls, which run in batches of at most **8** outstanding calls as their own
sections say (amended 2026-09-13, draft 2, from the Opus read: draft 1's *all
one at a time* was already false of entries and the amendment added two more
batched sources). (Amended 2026-09-13: the four new
sources add `gh` calls, named per source in **What one tick costs**, and the
lock source starts nothing at all; `probe` adds one `nova-bus send` under
`--ping-draft`, and, while a ping is outstanding, **one bounded read-only
`git` read of the pinged line's lane** per poll, which is how an answer is
correlated to the probe and not to any newer commit (draft 4, finding 3;
**amended draft 7, K3**: draft 6 made that correlation two `nova-bus inbox`
calls, and a plain `inbox` prints every NEW note in full, so its size was never
this tool's to bound — see **An answer is a note or a receipt from the
line**). **The write-side calls this tool can make to a bus are three,
one per verb and each behind a flag that names it** — amended 2026-09-13,
draft 2, from the Fable read, which found three sentences disagreeing about
which was *the only one*: `watch --advance-cursor`'s `nova-bus inbox
--advance`, `probe --ping-draft`'s `nova-bus send`, and `serve --receipt`'s
receipt (rule 10, and stated there). No verb makes another's: `watch` sends
nothing and receipts nothing, `probe` moves no cursor, `serve` moves none
either. Without those flags the tool writes nowhere but `--state`.) `git` is started only against the bus
checkout, read-only: for `--line` (rule 2 below), for the checkout's head,
which is the freshness the `WAKE SOURCE bus` line shows (**How the checkout
receives mail**), and — under `probe`, while a ping is outstanding — for the
bounded correlation read of the pinged line's lane, which walks commits and
reads note **headers** and `RECEIPTS` lines under a per-poll budget and writes
nothing (draft 7, K3). `nova-bus` is started once before the opening line as
`nova-bus version`; once per bus poll as `inbox` without `--advance`, or as
`wait` without `--advance` under `--refresh` — `probe`'s correlation read is
none of these and starts no `nova-bus` at all (draft 7, K3); a second
time on a poll that advances, as `inbox --advance`; and once, as `inbox
--open`, at the start of a call that finds an advance interrupted (**The bus
inbox**). It opens no socket
of its own, resolves no host, and has no opinion about what a bus or a forge is
beyond what those two programs tell it. A source whose program is missing from `PATH` is a **change**
on the first poll, not a refusal — see **Sources** — because a window that cannot
see its bus needs to hear so now.

## Exit codes

| code | meaning |
|------|---------|
| 0 | the watch ran: **either** something changed **or** the deadline arrived **or** the caller stopped it (`WAKE STOPPED`, amended 2026-09-13); for `probe`, contact is fresh with no rest standing (`PRESENT`) or this probe was answered (`ANSWERED`) — never a claim that the line accepted work (draft 4) |
| 1 | **`probe` only (amended 2026-09-13):** the line is not to be assigned now — `SILENT`, `PINGED`, `UNAVAILABLE`, `UNRECONCILED` (draft 5: a send this call could not reconcile against the lane's remote ref, so whether the line was pinged is not known and an assignment waits on a fetch), or `RESTING`, which is the line's own declared choice rather than a measured silence (draft 4); the second token of the last line says which. `watch` and `serve` never exit 1 |
| 2 | could not run: a missing or malformed flag, no source named, a `--max` over the ceiling, an unreadable or unparsable state file, a second watcher on the same state file, a `nova-bus` whose version is not this build's own (the pin, below), or a source whose failure streak reached three (rule 8; the streak is in the state file and spans calls) |

**This is the one deviation from SPEC.md's Conventions table, and it is that
`watch` has no 1** (amended 2026-09-13: `probe` has one, below). Nothing a
watcher prints asserts anything, so a watcher cannot say NO: it is a report and
never a gate. A deadline is not an error — it is the answer *nothing
yet*, exactly as `WAIT TIMEOUT` is — and a change is not a failure even when what
changed is a red check, because *red* is news and news is this tool's whole output.
A caller that needs to know which of the two happened reads the **second token of
the last line**, `WAKE CHANGE` or `WAKE QUIET`, and never the exit code. A `WAKE
BROKEN` last line is the third case, and it is exit 2, because a watch whose
source went away did not run to its deadline (rule 8). Exit 1
was reserved here for the day a verb gated on something, and on 2026-09-13
`probe` took it: a probe **is** a gate — Glenn's rule is *never assign to
somebody who is not here* — so its 1 is Conventions' 1, *the check ran and
said NO*, and it says NO to an assignment, not to the line. `watch` still
never exits 1. **For `watch` and `serve`, the exit code says whether the call
ran or was refused; the second token of the last line says what happened.**
(Amended 2026-09-13, draft 2, from the Fable read: draft 1 wrote that sentence
unqualified, two lines after taking exit 1 for `probe`, where it is false —
`probe` runs and exits 1. The scope is stated once, here: **`probe`'s 0 and 1
are its answer to one question — can work be handed over right now — and only
its 2 means the call could not run.** That is the whole of the deviation; it
is not repeated in the `probe` section, which points here.) The amendment was asked to
make the exit code say which of change, deadline or stop occurred and declined,
for the reason already given under **What it deliberately does not do**: a
status is one bit of news in a grammar that cannot grow, and `WAKE STOPPED`
(rule 15) is exactly the fourth verdict a two-value status could not have
carried. A scanner reads the token; the token is a word a person reads too.

**An unparsable state file is exit 2 and never a silent cold start.** The state
file is the only thing standing between this tool and the false-quiet failure: a
run that cannot read it, decides to start cold, and reports nothing has a
*correct-looking* first poll and has silently swallowed everything that moved
while no watcher was running. So it refuses, names the file and the parse error,
and says the two ways out — repair it, or pass a new `--state` path and accept a
cold start on purpose.

## Output grammar

```
WAKE at=<stamp> as=<name|-> max=<d> interval=<d> on-deadline=<word> sources=<bus,entries,reports,prs,runs,branches,locks> state=<file> cold=<true|false> nova-bus=<version|-> pending=<n>
WAKE CHANGE after=<d> polls=<n> bus=<n> entries=<n> reports=<n> lines=<n> prs=<n> runs=<n> branches=<n> locks=<n> pending=<n>
WAKE STOPPED after=<d> polls=<n> pending=<n>: stopped by the caller
WAKE QUIET after=<d> polls=<n> default=<word> sources-failing=<n>: deadline, default taken
WAKE BROKEN source=<bus|entries|reports|prs|runs|branches|locks> failures=<n> since=<stamp>: <reason>   (amended 2026-09-13, draft 2)
WAKE BUS id=<id|-> from=<name> addr=<to|cc> at=<stamp|-> commit=<sha|-> path=<path>: <subject>
WAKE BUS LINE <the bus's own line, verbatim>
WAKE BUS STANDING <the bus's own line, verbatim>
WAKE ENTRY <repo>#<n> state=<state> fail=<n> pending=<n> pass=<n> final=<true|false> failing=<names|->
WAKE ENTRY <repo>#<n> unreadable: <reason>
WAKE REPORT path=<path> lines=<n> bytes=<n> <new|modified>
WAKE PR <owner>/<repo>#<n> comments=<n> reviews=<n> threads=<n> self=<n> rescan=<true|false> push=<true|false> head=<sha|-> newest=<comment|review|thread>:<id|-> by=<login|-> review=<word|-> at=<stamp|-> url=<url>
WAKE PR <owner>/<repo>#<n> unreadable: <reason>
WAKE RUN <owner>/<repo>@<sha> fail=<n> pending=<n> pass=<n> final=<true|false> failing=<names|->
WAKE RUN <owner>/<repo>@<sha> unreadable: <reason>
WAKE BRANCH <owner>/<repo>:<name> head=<sha|-> was=<sha|->
WAKE BRANCH <owner>/<repo>:<name> unreadable: <reason>
WAKE LOCK path=<path> state=<free|held|absent> was=<free|held|absent|->
WAKE LOCK path=<path> unreadable: <reason>
WAKE LINE name=<name> state=<OFFLINE|BACK> last=<stamp|-> silent=<d> commit=<sha|->
WAKE SOURCE bus read=<n> suppressed=<n> relayed=<n> standing=<n> head=<sha|-> head-at=<stamp|->
WAKE SOURCE bus ... cc=<n>                    (with --to-only; appended, and part of suppressed=, draft 2)
WAKE SOURCE prs read=<n> changed=<n> unreadable=<n> calls=<n> self=<n> login=<login|->   (self=: draft 3; self= is --not-mine's and login= is --owned-prs's: draft 4)
WAKE SOURCE <runs|branches> read=<n> changed=<n> unreadable=<n> calls=<n>
WAKE SOURCE locks read=<n> changed=<n> unreadable=<n>              (no calls=, no login=: draft 3)
WAKE NOTE <something true about this run that is not a change>
WAKE POLL <source>: <reason one poll failed, which was not fatal>
WAKE PING id=<id> to=<name> commit=<sha|-> pushed=<true|false> attempt=<n>: one prepared note (--ping-draft) offered to the bus, printed per attempt until it lands
WAKE MORE kind=<bus|entry|report|line|pr|run|branch|lock> shown=<n> total=<t> n=<k> <remedy>
WAKE REFUSED: <reason>
WAKE HERE at=<stamp> load=<load|-> cpus=<n> procs=<n|->
WAKE PROBE name=<name> state=<state> contact=<name|-> last=<stamp|-> silent=<d> commit=<sha|-> pinged=<stamp|-> pinged-id=<id|-> rest=<d> reconciled=<true|false> correlation=<id|-> remaining=<n> gaps=<n> silent-after=<d> answer-within=<d> head-at=<stamp|->
WAKE FIRED ids=<n> first=<id> rc=<n> redelivered=<0|1>
WAKE UNCERTAIN id=<id> attempt=<n>: dispatch interrupted; nova-wake serve --bus <dir> --as <name> --state <file> --redeliver <id> --on-note <command> runs it again
WAKE UNCERTAIN id=<id> attempt=<n> rc=<n>: retry not terminal; nova-wake serve --bus <dir> --as <name> --state <file> --redeliver <id> --on-note <command> runs it again
WAKE BLOCKED as=<name> uncertain=<id> queued=<n>: a dispatch may still own this receiver; end it, then nova-wake serve --bus <dir> --as <name> --state <file> --redeliver <id> --on-note <command>
WAKE SERVE fired=<n> notes=<n> redelivered=<n> uncertain=<n> queued=<n> cc=<n> failed=<n> max_wait=<d> idle=<duration>
FRIEND <name> awake|asleep|unknown age=<seconds|-> source=bus-cursor
AWAKE OK friends=<n> awake=<n> asleep=<n> unknown=<n> window=<n>
AWAKE REFUSED <reason>
```

The last five are `serve`'s (rule 10); `WAKE HERE` and `WAKE PROBE` are
`probe`'s; everything above them is `watch`'s. The three `awake` lines are
`awake`'s: `FRIEND` once per friend lane read over the bus cursors, `AWAKE OK`
the verdict line that ends the listing, and `AWAKE REFUSED` the shape for the
things wrong about the world rather than the invocation (no `--bus`, a `--bus`
that is not a git repository, a `--window` that is not positive, a negative
`--max`).

`WAKE CHANGE`, `WAKE QUIET`, `WAKE BROKEN` and, since 2026-09-13, `WAKE STOPPED`
are the **last** line and the four possible verdicts. **`sources-failing=<n>`
is `WAKE QUIET`'s alone, and that is stated here once** (amended 2026-09-13,
draft 3, from the Opus read: rule 8 said *on the verdict* of a field the
grammar carried on one line, and draft 2 had added a fourth verdict beside
it). Quiet is the only verdict that asserts nothing moved, so it is the only
one whose truth depends on how many sources were answering — *"quiet is only
as true as the sources"* (**Known limits**). `WAKE CHANGE` is news and carries
none; `WAKE STOPPED` is the caller's decision and carries none; `WAKE BROKEN`
names the one source that ended the call. `pending=<n>` on each of the first two is the length of
the delivery queue — every observation this run or an earlier one made and
has **not** printed, which the next call prints first (**Delivery
is the printed line**, rule 11); the opening `WAKE` line is the **first**, printed before anything is
waited on, so a transcript shows the call began and what it was told to do — a
tool call that prints nothing for twenty minutes and then prints everything is,
while it runs, indistinguishable from one that has hung. `WAKE` lines and the
informational tokens go to stdout; `WAKE REFUSED`, `WAKE POLL` and `AWAKE
REFUSED` go to stderr; `FRIEND` and `AWAKE OK` go to stdout.

Every path, subject, reason, entry name and relayed bus line is rendered through
`internal/oneline`, so a note whose subject carries U+2028 arrives as one escaped
line rather than two, and a `key=value` field is one whitespace-free token as the
field law requires. **The amendment's new fields carry forge text and are
`oneline.Field` values like every other** (amended 2026-09-13, draft 2, from
the Opus read, which found them unnamed): `by=`, `login=`, `url=`, `newest=`,
`review=`, `head=`, `was=` and `failing=` on the new item lines, and
`name=`/`to=` on `probe`'s — a login, a branch name or a URL is somebody
else's text, and a space or an `=` inside one would forge a field. A relayed line is escaped and **never shortened below its
own tail budget**; and no line is exempt from the cap, because a line the cap
drops is pending and the next call prints it (rule 11).

The verdict line counts **what changed**, per source, and those numbers — four
before the amendment (`bus`, `entries`, `reports`, `lines`) and eight after
(`prs`, `runs`, `branches`, `locks`; amended 2026-09-13, draft 2, from the
Opus read, which counted the line above and found draft 1 saying three and
seven of a line carrying four and eight) — are about the WORLD and not about the output: the listing above them is capped and the
counts never are. That is SPEC.md's law, stated there once and met here the same
way, through `internal/bounded`.

## Sources

A source is a thing with a **state value** and a rule for what makes two state
values different. That is the whole design: poll, compute a value, compare it to
the stored one byte for byte, and a difference is a change. Nothing here tries to
decide whether a change is *important* — a window asked to be woken on a change
and importance is the window's to judge.

### The bus inbox — a NOTE not yet printed is always a change (except an `addr=cc` note under `--to-only`), and the status line is never filtered

The bus source runs `nova-bus inbox --bus <dir> --as <name> --receipt-max-words
<n>` under `--gh-timeout`'s sibling budget and reads its stdout and stderr
together, line by line.

**Classification is by first tokens, and the default case prints.** A line
beginning `INBOX NOTE` whose id carries no `printed=` mark in the state file is a
change, always — **amended 2026-09-13, draft 2: with one exception, `--to-only`,
which is stated below and is the only thing that withdraws a word of this
sentence; without that flag the sentence stands unchanged** — and is relayed as
`WAKE BUS`; a note this tool has already
printed is **suppressed** and counted, because the window has been shown it,
and that mark is written only after the line was printed (rule 11). A plain
`inbox` lists a note **once**: on every poll while the cursor stands behind it,
and never again once `--advance` has moved the cursor past it — measured, below
— so the suppression does its work on a run without `--advance-cursor`, where
every poll re-lists the same new notes, and on a recovery read under `--open`,
and nowhere else. **This spec does not depend on `nova-bus` re-listing
anything.** The bookkeeping tokens — `INBOX OPEN`, `INBOX OK`, `INBOX CURSOR`,
`INBOX SCOPE`, `INBOX LEGACY` — are counted and not printed, because they say
the same thing every poll. **Every other line this tool does not recognise is printed verbatim,
under `WAKE BUS LINE`, and wakes the window.** That includes `INBOX REFUSED`,
`INBOX FAIL`, `INBOX UNREADABLE`, `INBOX UNADDRESSED`, `INBOX SWITCH`, a `git`
transcript, a line from a future version of `nova-bus` this tool has never heard
of, and anything on stderr at all.

**Never filter the status line.** A watcher that kept only the tokens it knew
about once dropped an `INBOX REFUSED` line and gave the window thirty minutes of
confident quiet over a bus that was refusing every read. The rule follows from
that and is absolute: an allow-list decides what is *suppressed*, never what is
*shown*, and a line this tool cannot classify is a line this tool prints.

**A refused or error line never re-wakes and is never silent.** The first sighting
of an unrecognised line is a change. A subsequent run that sees the *same* line
again — the same bytes, from the same source — prints it under `WAKE BUS
STANDING` and it does **not** count toward `bus=<n>` and does **not** by itself
make the run a `WAKE CHANGE`. A bus that has been refusing for an hour would
otherwise wake the window every interval with one sentence it has already acted
on, which is the false-wake failure arriving by another road; and dropping the
line instead would be the false-quiet failure. So: shown every time, woken on
once. The sighting memory lives in the state file, is bounded (see **State**), and
when an entry is evicted the line is a change again — which is the safe direction
to be wrong in.

**`--advance-cursor` is off, and a watcher may not advance a cursor that is not
its own.** This is the rule the third failure of 2026-09-11 bought: a test run of
the watcher read the window's bus *with* `--advance`, which consumed five notes,
and because the run was a test nobody read its output — so five notes were on no
open list, in no transcript, and the window had to be told about them by a person.
A cursor is a claim about what a reader **has been shown**, and a watcher that
advances a reader's cursor is making that claim on the reader's behalf.

Therefore:

- `nova-wake` passes `--advance` to `nova-bus` **only** when `--advance-cursor` is
  given explicitly, with `--remote` and `--branch`, and never by default.
- `--advance-cursor` without `--as` is exit 2. The `--as` name **is** the claim:
  advancing is permitted only for the line the window itself reads as, and a
  watcher run on somebody else's behalf does not get a flag for it. There is no
  `--as-other`, no `--probe-as`, and no flag that advances one name's cursor from
  another name's watch.
- A run with `--advance-cursor` holds a lock named by `(bus, as)` for the whole
  call — not per poll, unlike `nova-bus wait`, because the thing being protected
  is a cursor this run is moving rather than a checkout it is reading — and a
  second watcher over the same pair is `WAKE REFUSED`, exit 2, naming the holder.
- **The cursor never moves past a note this tool has not spooled, and never
  moves while a note is unprinted.** Measured on 2026-09-11 against `nova-bus
  v0.10.3` on a synthetic bare bus with two clones (**How the checkout receives
  mail**): a plain `inbox` lists a note while the cursor stands behind it, and
  once `--advance` has moved the cursor past it the note is on the reader's
  `OPEN` list and a plain `inbox` prints `INBOX OPEN carrying=<n>` and **no**
  `NOTE` line for it; only `--open --open-max <n>` lists it, `--open-max` must
  be at least 1, and past `--open-max` it prints `INBOX OPEN listed=<k> and
  <m> more`. So the cursor is not a thing this tool may move and then read
  behind. **Consumed mail is bounded by this tool's own pending queue**
  (**State**) and by nothing `nova-bus` does, and a poll with `--advance-cursor`
  is a transaction in four steps:
  1. `inbox` **without** `--advance`. Every `INBOX NOTE` not marked printed is
     appended to the queue and the state is written (rule 11, step 1) before
     anything is printed.
  2. Print up to the cap; delete each printed record and mark it. If any bus
     record remains in the queue after this — the cap elided some, or the poll
     found more than it could print — **this poll does not advance**, and the
     advance waits for a poll whose queue holds no bus record after its print.
  3. When the queue holds no bus record: write `bus:advance` =
     `inflight|<stamp>|<checkout head>` to the state, **then** run `inbox
     --advance --remote --branch`, classify its output exactly as in step 1 —
     normally it lists nothing new, because nothing fetched between step 1 and
     now; when it does, those notes are appended to the queue — write the state,
     and only then clear `bus:advance`. The marker is written before the advance
     and cleared after its output is durable, so a kill between the two leaves
     the marker.
  4. A call that finds `bus:advance=inflight` at its start runs `inbox` plainly,
     reads `carrying=<n>` from its `INBOX OPEN` line, and if `n>0` runs `inbox
     --open --open-max <n>` once — `n`, never a fixed number, because `nova-bus`
     caps the listed `OPEN` at `--open-max` (default 20) and a fixed number
     would recover a fixed number — appends every listed note not marked
     printed to the queue, writes the state, clears the marker and says `WAKE
     NOTE bus advance was interrupted; recovered <k> notes from OPEN`. When
     the `--open` read lists fewer than `carrying=` — the checkout moved
     between the two reads — it is run once more with the new count; still
     short, the marker stays, nothing advances, and the call says `WAKE NOTE
     bus recovery incomplete: listed=<k> carrying=<n>; retried next call`. The
     recovery is complete and not paginated: `--open-max` takes any positive
     number, measured to 100000, and `n` is the whole carried list.

     **Added 2026-09-12, after the audit of packet 1 (#164, findings F1 and F2);
     nothing above is withdrawn.** Two clauses, because the code was measured
     against both and was wrong on both:

     - **`<k>` is counted over the listing's own entries, and `carrying=` counts
       all of them.** `carrying=` is the size of the whole open list — "every
       entry on it, the heard and the unreadable included" (SPEC.md, **inbox**) —
       so the count compared against it is every entry the `--open` listing HELD:
       `INBOX NOTE`, `INBOX HEARD`, `INBOX RECEIPT` and the `INBOX UNREADABLE`
       lines of the entries carried as unreadable. It is never a count of what
       this tool relayed, and never a count of the `NOTE` lines alone: a note
       this tool has already printed is suppressed and is still an entry the bus
       listed, and a receipt is an entry with no `NOTE` line at all. A reader who
       has been shown notes it has not answered **and** has receipted anything
       carries both at once, and each partial count is short of `carrying=` for
       that ordinary list, which made the recovery unendable.
     - **While a recovery is unresolved nothing advances — in this call and in
       every later one — and the marker outranks an empty bus queue.** A poll
       whose queue holds no bus record still does not advance while
       `bus:advance=inflight` stands: the marker is the only thing naming a
       carried list this tool has not reached, and a fresh advance would write
       its own marker over it and move the cursor, putting those notes behind the
       cursor with nothing naming them. Such a poll prints, once per call, `WAKE
       NOTE bus advance deferred: a recovery is unresolved; nothing fetches until
       the carried list is reached`, and it is **not** a failed source: it counts
       toward no streak and cannot say `BROKEN`. The recovery runs at the start of
       every call, so the advance resumes on the first call that reaches the
       carried list.

  `nova-bus` moves a cursor to `HEAD` and nowhere else, so this is the only way
  to keep the cursor behind the print. Mail consumed is mail spooled; mail
  spooled is mail printed, **under the cap** like everything else (**Bounded
  output**); and a cap that elides a note defers the fetch rather than losing
  the note.
- A run **without** `--advance-cursor` consumes nothing and fetches nothing
  (**How the checkout receives mail**). A note it prints is marked printed and
  wakes once; the default is the one that cannot lose mail.
- **`--refresh` fetches without moving anything.** With `--refresh --remote
  <name> --branch <name>`, each bus poll is `nova-bus wait --bus <dir> --as
  <name> --receipt-max-words <n> --timeout <t> --remote <name> --branch
  <name>` with **no** `--advance`, where `<t>` is the time to the earliest due
  source, at most `--interval`: `wait` takes the checkout lock, fetches,
  fast-forwards, and returns the moment the inbox would list something new or
  at `<t>` (SPEC.md, **wait**), and its lines are classified exactly as
  `inbox`'s, because the two verbs share one listing. So new mail reaches a
  non-advancing watcher within one poll, no cursor moves, nothing is consumed,
  and the carried list is then read whole with `inbox --open --open-max
  <carrying>` once per run on the first poll, so a cold watcher lists what it
  is owed before what is new. `--refresh` and `--advance-cursor` together are
  exit 2: one fetch per poll, never two.
- **`--advance-cursor` is specified here and was shipped under work list item 3a.**
  Advancement is an acknowledgement optimisation and not a prerequisite for delivery
  (Stella and Johnny, 2026-09-11), and a v1 that cannot move a cursor cannot lose a note.

**The bus is the exception to the cold-start rule.** See **The cold-start rule**.


#### `--to-only` — a `Cc:` is not a wake (amended 2026-09-13)

`nova-bus` marks every listed note `addr=to` or `addr=cc`, and `serve` has
always dispatched only on `to` (rule 10: *to means must act, cc means should
know, and a broadcast to five is five turns*). `watch` woke on both, so a window
on a busy bus was woken by every note it was copied on. `--to-only` brings the
two verbs into line: with it, an `INBOX NOTE … addr=cc` is **not a change**. It
is not printed, not queued and not marked `printed=` — a mark would claim the
window was shown it — but it is **counted**, `cc=<n>` on the `WAKE SOURCE bus`
line, so the window knows how much is waiting for its next natural `inbox`. The
default is unchanged: without the flag both wake, because a flag that narrows
what wakes a window is the window's to give.

**This flag withdraws a word of *a change, always*, and says so** (amended
2026-09-13, draft 2; both cold reads, finding 6/5). The amendment's preamble
says nothing above is withdrawn unless a sentence says so beside it, and draft
1 narrowed **The bus inbox**'s rule without marking it; the sentence and the
heading there now carry the exception and point here. The rule as it now
stands, whole: a `NOTE` not yet printed is a change, always, unless
`--to-only` is given and the note is `addr=cc`.

**A cc note counts as `suppressed`, so rule 7's sum still holds** (amended
2026-09-13, draft 2). Rule 7 promises `read` equals the sum of `suppressed`,
`relayed` and `standing`, and draft 1's cc note was none of the three, which
broke the one arithmetic that proves no line was dropped. A note held back by
`--to-only` is a **suppression this tool's own flag decided**, exactly as a
note already marked `printed=` is one its own state decided, so it is counted
in `suppressed=` **and again** in `cc=` — `cc=` is a breakdown of
`suppressed=`, never a fourth term, and `read = suppressed + relayed +
standing` on every run with the flag as without it. Nothing is dropped
silently: the count is the proof.

`--to-only` with `--advance-cursor` is exit 2, `refusing to guess`: an advance
moves the cursor past every listed note, and a cursor is a claim about what the
reader has been shown (**The bus inbox**), so the pair would consume mail this
call chose not to print. With `--refresh` the pair is fine: nothing moves.
`INBOX HEARD`, `INBOX RECEIPT` and every line that is not a `NOTE` are
classified exactly as before; `--to-only` decides one thing and it is not the
status line. (Grok Build sitting, 2026-09-12, on #178: *"the sitting wakes only
on `INBOX NOTE addr=to`. `Cc` advances the cursor and is read at wrap or when
`To:`'d. Empty minutes must not be a model turn."*)
#### How the checkout receives mail, and the version this depends on

`nova-bus inbox` reads the checkout and never the remote (SPEC.md, *what it
deliberately does not do*: pull first is the caller's). **`watch`'s one**
write-side call against a bus is `nova-bus inbox --advance --remote <name>
--branch <name>` — the tool's are **three, one per verb**, named once under
**The only programs it starts** (amended 2026-09-13, draft 3, from the Fable
read: draft 2 repaired that list to three and left this sentence saying *the
one write-side call this tool makes*, which is the same disagreement one
paragraph later) — and it is also the one call that fetches: the push inside it
fetches before it writes and fetches-and-rebases when it is rejected (SPEC.md,
**send** and **receipt** protocol, steps 2 and 4), so mail that landed on the
remote reaches the checkout through that push and is listed by the **next**
`inbox`. Measured on 2026-09-11 against `nova-bus v0.10.3`: a note pushed to a
bare remote by another clone was listed on the second `inbox --advance` after it
landed, never the first. Measured the same day, on the same fixture, the other
half of the dependency: after `--advance` moved the cursor past that note, a
plain `inbox` printed `INBOX OPEN carrying=1` and no `NOTE` line; `--open
--open-max 10` listed it; `--open-max 0` was refused, exit 2; with 45 notes
carried, `--open` alone listed 20 and said `and 25 more`, and `--open-max
100000` listed all 45. (Stella measured it first, `bus-probe.json` beside her
second read; the author repeated it on a fresh synthetic remote before this
sentence was written.) So, with `--advance-cursor`, new mail is relayed within
**two advancing polls**, and a poll advances only when this tool's bus queue is
empty after its print (**The bus inbox**). A call holding unprinted mail prints
up to the cap, returns, and does not advance; the next call advances if the
queue drained. The promise, stated whole: **mail is relayed within two polls
of the window having been shown everything it was already owed**, and while it
has not been, every verdict carries `pending=<n>` and the call prints, once,
`WAKE NOTE bus advance deferred: pending=<n> bus notes unprinted; nothing
fetches until they print`. The freshness promise is not made over a backlog,
and the tool says so rather than pretending. The two-poll half is a promise
about `nova-bus`'s push and not about its read — which is why the version is
pinned:

- **The pinned version is this build's own version: the `nova-bus` from this
  tool's own release.** Before the opening line the tool runs `nova-bus version`
  and reads the second token of its first line. A version other than this
  build's is `WAKE REFUSED: nova-bus <found>; this tool is written against
  <this build> and its fetch is a property of the push` — exit 2, because a
  `nova-bus` that stopped fetching inside `inbox --advance` would leave a
  watcher that looks perfectly healthy and is blind. The opening line carries
  `nova-bus=<version>`.

  The pin **was** the literal `v0.10.3`, written here and in
  `internal/wake/bus.go`, and on 2026-09-12 Emma's dogfood pass of v0.12.0
  found what a literal costs (nova-tools #104): every binary in `cmd/` ships
  from one tag, so the release moved `nova-bus` to `v0.12.0`, the literal stayed
  where it was, and `nova-wake v0.12.0` refused the `nova-bus` of its own
  release — `WAKE REFUSED: nova-bus v0.12.0; this tool is written against
  v0.10.3`. A pin no release can update is a pin that expires, and a check that
  expires is not a check. So the pin is **derived** instead: the check is
  `AcceptBus(tool, found)`, which is **string equality** between this build's
  own version and the second token of `nova-bus version` — nothing is parsed,
  compared as a range, or ordered. One tag stamps both programs with the same
  `-ldflags -X main.version=<tag>`, so a released pair is equal by the stamp; in
  an unstamped `go build` both halves take `Main.Version`, the toolchain's
  module pseudo-version for the tree (`v0.12.1-0.<stamp>-<12hex>`, `+dirty`
  included), so they are equal there too. So *the `nova-bus` this tool is
  written against* is exactly *the `nova-bus` built from the same tree as me*,
  and the two versions match as a fact about the build. (Under `go run`, where
  neither half has a stamp or a module version, both report `devel` and
  `AcceptBus` accepts the pair: a developer's own risk, never a release — the
  release workflow stamps every binary in `cmd/`.)
  The guard is not weakened: an older release's `nova-bus`, a foreign one, or
  one from another tree is still refused by name before the opening line, and
  the tool still names both versions. The measurement above is what argues the
  pin, and it is repeated per release against the pair that ships — which is
  what `cmd/nova-wake`'s advancing tests do, building `nova-bus` from this tree,
  stamping it with this tool's own version and reading the version back out of
  the binary. Widening the rule further — a major.minor line, or "newer than" —
  would be a rule change under CONTRIBUTING; matching a release to itself is
  not.
- **Without `--advance-cursor` nothing here fetches**, and the tool says so
  rather than letting a quiet checkout look like a quiet bus: once per run,
  `WAKE NOTE bus checkout is read as it stands; nothing fetches without
  --advance-cursor; freshness is head-at=`; and every `WAKE SOURCE bus` line
  carries `head=<sha> head-at=<stamp>`, the checkout's newest commit and its
  commit stamp, read with `git log -1` under the timeout, so a reader of the
  transcript can see the checkout stand still. External synchronisation — a
  `nova-bus wait`, a `git pull`, a `serve` — is a prerequisite and is named as
  one, never assumed.

### Entries and their checks — counts, and a FINAL state when you ask for it

An **entry** is something on a forge with a state and a set of hosted checks:
`--entry mas-bandwidth/schema#942`. It is named `--entry` and not `--pr` because
the repository is part of the name — the prototype took `--prs 942,951` against
one `--repo`, which makes a second repository a second invocation and a typo in
one number a watch on somebody else's work — and because the tool has no model of
what the number means beyond "the thing `gh` will tell me the state of". Entries
are polled in batches, at most **8** outstanding calls, with one `gh` process per
entry per poll and no pagination.

The state value is, in order: the entry's own state (`OPEN`, `MERGED`, `CLOSED`),
the count of **fail**, the count of **pending**, the count of **pass**, and the
**sorted names of the failing checks**. A change in any of the five is a change.

- The bucket rule: a hosted check that has not completed is `pending`; one that
  completed with success, neutral or skipped is `pass`; anything else is `fail`.
  A non-check status context is `pending` while pending or expected, `pass` on
  success, `fail` otherwise. A check with no name is named by its context, and a
  check with neither is `?` — a nameless check is still a count.
- **A hosted-check change is a change.** The failing names are in the value
  precisely so that a red run replaced by a *different* red run with the same
  arithmetic wakes the window: `fail=1` over `windows / build` is not the same
  news as `fail=1` over `race`, and the arithmetic cannot tell them apart.
- `merged` and `closed` are changes, and they are the changes most likely to end
  the window's wait, so the entry's own state is the first field of the value.
- An entry that cannot be read — `gh` missing, a timeout, a rate limit, an
  unparsable answer — has the state value `unreadable:<reason>`, which is a state
  value like any other: the first sighting is a change and prints `WAKE ENTRY
  ... unreadable:`, and the same reason on the next poll does not re-wake. The
  same shown-every-time, woken-once rule as a standing bus line, for the same
  reason. An unreadable value is an **error and never a count**: it wakes under
  `--final-only` exactly as without it, because a flag that asks for fewer
  wakes about arithmetic is not a flag that asks to sleep through a source that
  cannot be read. It also counts one poll toward the source's failure streak
  only when **every** entry is unreadable (rule 8).

**`--final-only` exists because check counts churn every poll.** A busy entry
moves `pending=11 pass=1`, `pending=9 pass=3`, `pending=6 pass=6` — every one of
those is a real change in the value and a real wake, and a window woken eleven
times on one entry learns nothing eleven times. Under `--final-only` an entry's
change is reported only when its state is **FINAL**, and FINAL has exactly one
definition:

> An entry is FINAL when it is no longer `OPEN` — `MERGED` or `CLOSED` — **or**
> when `pending=0` and it has at least one check. `pending=0 fail=0` is green;
> `pending=0 fail>0` is red. Both are final; both wake.

Green and red are both final because the window's next action differs between
them and is needed in both cases, and because a watcher that woke only on green
would be a watcher that sleeps through every failure. An entry with **no** checks
at all is never final by the count rule — `pending=0 pass=0 fail=0` is the state
of an entry whose checks have not been created yet, and calling that green is the
one arithmetic mistake here that would merge something red. It becomes final by
changing state, or the window asks without `--final-only`.

`--final-only` **suppresses the wake, not the state**: the value is stored on
every poll as always, so a run that ends at its deadline has an up-to-date state
file and the churn is never re-reported as news later. And it is per entry, not
per run: one final entry wakes the run even while four others churn. It never
suppresses `unreadable:` — one unreadable entry beside four readable ones wakes
the run under `--final-only` and prints its `WAKE ENTRY ... unreadable:` line.

### Report files — new or modified `RESULT.md` under the directories you name

`--reports <dir>` may be given more than once. Under each one the source watches
every `RESULT.md` at any depth, and nothing else: one agreed file name, because a
watcher that woke on every file another line touched would wake on its own
scratch.

**There are no default report directories.** The prototype hardcoded
`$HOME/deepseek-working-*/jobs/*/RESULT.md` and `$HOME/freddy-working-*/jobs/*/`,
which is three guessed paths, a guessed `$HOME`, and two other lines' layouts
frozen into a tool. Every directory is the caller's, per run.

The state value of a report file is `mtime:size`, and the change kinds are `new`
(no stored value) and `modified` (a different one). A file that disappears is
**not** a change and its key is kept: a job directory being rebuilt is not news,
and the window does not want to be woken by a `rm`. The per-file count of lines on
the `WAKE REPORT` line is read at report time and is there so the window can tell
a stub from a finding without opening it.

**The known limit, named rather than discovered:** `mtime:size` cannot see a
rewrite that preserves both, and some tools write identical-length updates within
one second. A content digest would close it and costs a read of every watched file
on every poll; it is a v2 item behind a flag, and until it exists this paragraph
is the answer to *why did it not wake*.

### Comments and reviews on a pull request — `--pr`, `--owned-prs` (amended 2026-09-13)

**The hurt, 2026-09-12:** *"Stella posted HOLDs with concrete defects on two PRs
I owned as PR comments. I had a bus wait and CI watchers armed, and read neither
PR again after its eye landed, so her holds waited 100 and 45 minutes until she
asked on the bus for an ownership receipt."* An entry's checks were a source and
its conversation was not, so a reviewer who writes where the code is was
invisible to a window that watched everything else.

`--pr <owner/repo>#<n>` names one pull request and may be given more than once.
It is spelled like `--entry` for the reason `--entry` is: the repository is part
of the name. `--owned-prs <owner/repo>` names a repository and watches every
**open** pull request in it **that the account authored** — which is the
account's authorship and not this window's assigned work (amended 2026-09-13,
draft 4, from the Astra read, finding 1: a credential is shared, so this set
can be wider and narrower than the window's own work at once, and `--pr` names
a pull request directly) — where **the account is
the login the host reports for the credential `gh` holds** — one `gh api user`
call at the start of the run, never a flag, never `--as` (a bus name and a forge
login are different facts and a tool that assumed they matched would watch the
wrong person's work). The set is refreshed on every forge tick with one `gh pr
list` call per repository. **A `gh pr list` that fails is a failed poll of the
prs source** (amended 2026-09-13, draft 3, from the Opus read: draft 2 gave
the listing no failure semantics at all, so a listing that died left the set
stale, watched no newly opened owned pull request and said nothing): the set
stands as it was, the pull requests already in it are polled as usual, and the
tick prints one `WAKE POLL prs: <reason>` line on stderr and counts toward
`fail:prs` exactly as a tick in which every watched pull request was
unreadable does (rule 8) — so a listing that cannot run is loud in three ticks
rather than silently narrowing what is watched. A pull request that merges or closes leaves the set
and its key is kept, which is not a change (its merge is the `--entry` source's
news, if the window asked for it); a pull request that opens joins the set with
its conversation **recorded and not reported**, which is the cold-start rule
applied to a thing that did not exist at the start of the run. At most **20**
owned pull requests per repository are watched — the listing law — and past
that the run says, once, `WAKE NOTE owned-prs <owner/repo> capped at 20 of <n>;
name the rest with --pr`, and watches the twenty newest.

**The state value** is, in order: the count of issue comments, the count of
reviews, the count of review threads, the pull request's own `updatedAt`
(amended 2026-09-13, draft 4, finding 2), the head its own branch points at
(`headRefOid`; draft 5, from the Fable read, finding M6), and the kind, id and
stamp of the newest item of the three kinds by the forge's own creation stamp:
`comments=<n> reviews=<n> threads=<n> updated=<stamp> head=<sha> newest=<kind>:<id>@<stamp>`.
A change in
any field is a change. A count that **falls** is a change too — a deleted
comment is news. The **standing** read is one `gh api graphql` call per pull
request per tick asking for `updatedAt`, `headRefOid` (draft 5), the three
`totalCount`s and the last node of each
(`comments(last:1)`, `reviews(last:1)`, `reviewThreads(last:1){comments(last:1)}`),
which is one request under 2 KB each way, and the reason the value is counts, a
stamp and a newest rather than a list of ids: the read is the size of the answer
and never of the conversation. Pull requests are polled in batches of at most **8**
outstanding calls, like entries.

**Every movement this source observes is a change, and setting this actor's
own words aside is an opt-in narrowing over ids it recorded itself** (amended
2026-09-13, draft 4, from the Astra read, finding 1). Drafts 1 to 3 set aside
every fetched node whose author was the login `gh api user` reports. A login is
a **credential and not a window**: a caller's other windows, its children and
its AI friends write with it, and an account's authorship is not one actor's
work. So two friends on one login suppressed each other's findings — a
reviewer's HOLD dropped as *the window's own words*, which is the missed wake
this source exists to prevent arriving under the name of a false-wake repair.
The default is therefore the safe one, and the narrowing is asked for by name
and made of ids:

- **By default nothing is set aside.** Any movement of any field of the value
  is a change, `self=0` on every item line and on the `WAKE SOURCE prs` line,
  and **no second `gh` call is ever made**: the standing read's `last:1` node
  is the attribution, because nothing has to be excluded from it.
- **`--not-mine <file>` is the narrowing.** The file holds one forge node id
  per line — the ids of the items **this actor emitted**, written there by
  whoever posted them. This tool posts nothing and composes nothing, so it can
  only be told; an id is a fact the poster has and a login is not. Blank lines
  and lines beginning `#` are skipped, a line that is not a node id is exit 2
  naming the file and the line number (**No guessed anything**), and a file
  that cannot be read is exit 2 at the start of the run rather than a silent
  empty set. It is re-read at the start of each forge tick, so an id appended
  mid-run is set aside from the next tick on.
- **Under `--not-mine`, a moved count is fetched again: when a count moved by
  `d` since the stored value, that kind is fetched `last:min(d,10)` nodes**, in
  one further `gh api graphql` call for that pull request on that tick, counted
  in `calls=` and budgeted in **What one tick costs**. Ten is the window, and
  it is a cap on the read rather than on the news.
- The fetched nodes **whose id is in the file** are set aside. `self=<n>` is
  the number set aside **among the nodes fetched this tick**, printed on the
  item line and summed on the `WAKE SOURCE prs` line; it is never a claim
  about the whole conversation, and a transcript that says `self=2` says two
  of what this tick fetched.
- The decision is over what was fetched. If every fetched node of every moved
  kind was set aside **and** `d` is at most 10 for each, the movement is this
  actor's own words: **not a change** — and **the moved counts and the new
  `newest=` are stored** (draft 3, and both stand unchanged), while `updated=`
  is stored only as far as the tick's own attribution reaches (draft 5, from
  the Fable read, finding M5; the rule is with `updated=`'s definition below).
  Draft 2 said only *`newest=` keeps its stored value* and left the counts
  undefined, and both halves of that were wrong. The counts: if they are not
  stored, this actor's eleventh comment is `d=11`, crosses the ten-node window
  and wakes the window on its own words — the exact false wake this passage
  exists to prevent — and that pull request pays a second `gh` call every tick
  forever. And `newest=`: it is a **field of the compared value** (*a change in
  any field is a change*), so a stored `newest=` the next tick will not compute
  is a difference on the next tick — the same false wake, one tick late, and
  arriving with every `d` at zero so nothing is fetched and nothing can be
  attributed. Both are stored. This is
  exactly `--final-only`'s shape, which is the settled one: **it suppresses the
  wake, not the state** (**Entries**), so the value is up to date, `d` measures
  the movement since *this* tick, and ten of this actor's own comments over ten
  ticks are ten `d=1` ticks with an eleventh that is a `d=1` tick too. (`by=`
  and `url=` are fields of a printed line and not of the value; on a suppressed
  tick no line is printed, so there is nothing for them to keep.)
- If any `d` is above 10 the tick **cannot** prove the whole movement was this
  actor's, so it **is** a change: the window is woken. **This is the one place
  the read's bound beats rule 14's *the window's own words never wake it*, and
  the bound wins** (draft 3, from the Fable read, which found the two sentences
  unreconciled): rule 14 holds wherever a tick can prove the movement was this
  actor's, and past the ten-node window the tool wakes rather than guesses,
  because a missed wake here is a reviewer's HOLD unread and the cost of the
  extra one is a line the window can see was its own from `self=`. Test 14
  asserts this case.
- **On every change this source reports**, `newest=`/`by=`/`url=` name the
  newest fetched node that was not set aside, or the newest fetched node when
  every one of them was, with `self=` saying how many were set aside (draft 3,
  from the Fable read: draft 2 stated the attribution inside the `d>10` branch
  alone, and the ordinary mixed tick — `d=2`, a friend's comment and then this
  actor's own, test 14's `by=login-b self=1` — is a `d<=10` change that needs
  it). Without `--not-mine` the newest fetched node is the standing read's own
  `last:1` node and no second fetch happens at all. A count that falls is a
  change with nothing fetched and nothing attributed — a deletion has no author
  to read.

A window that comments on a pull request and then watches it wakes on its own
comment once, unless it wrote that comment's id into `--not-mine`. That is the
trade, stated plainly: *"a false wake is worse than a missed one, because the
window learns to stop reading"* is true of a window that cannot tell which line
was its own, and `self=`, `by=` and `url=` on the line tell it — while the
suppression a shared login bought cost a friend's HOLD, which this source
exists to deliver.

**The host login is `--owned-prs`'s alone, and it is labelled as the account it
is** (draft 4, finding 1). `gh api user` is read **once per run and only when
`--owned-prs` is given**, to list that account's open authored pull requests;
`login=` on the `WAKE SOURCE prs` line names **the account whose authored pull
requests were listed**, never this window's identity, and it sets nothing
aside. `--pr` names the window's work itself and reads no login at all. When
`gh api user` cannot be read there is no listing to make: the run says so once
— `WAKE NOTE owned-prs: host login unreadable (<reason>); no pull request is
listed from this account, and --pr names one directly` — that tick is a failed
poll of the prs source exactly as a failed `gh pr list` is (rule 8), and every
pull request named with `--pr` is watched exactly as before.

**The item line carries identity and a pointer, never a body.** `WAKE PR
<owner/repo>#<n> comments= reviews= threads= self= rescan=<true|false>
newest=<kind>:<id> by=<login>
review=<state|-> at=<stamp> url=<url>`: `review=` is the newest review's state
when the newest item is a review and `-` otherwise, because `APPROVED` and
`CHANGES_REQUESTED` are the two words that change what the window does next;
`url=` is the forge's own link to the newest item. No text of a comment is
relayed (rule 5): a review's finding is a paragraph somebody wrote, the window
opens it, and a line that quoted its first sentence would be a line the cap
could not honestly bound. A pull request that cannot be read is `unreadable:
<reason>`, a change once and standing thereafter, exactly as an entry.

**A reply on an older thread, and a dismissal, are changes this read cannot
name — and it says so rather than missing them** (amended 2026-09-13, draft 4,
from the Astra read, finding 2). Draft 3 declared both invisible, and truly:
`threads=` counts threads and not the comments in them, the newest-thread read
is `reviewThreads(last:1){comments(last:1)}`, and dismissing a review moves no
count and creates no node — so a reply on a thread already open, and a
dismissal, moved nothing the value compared. Naming that was honest and it was
not the contract the hurt asked for: the HOLD that waited 100 minutes could as
easily have arrived as a reply on a thread already open, and a known limit is
acceptable only where the owner's workflow still cannot silently miss a
finding. The repair is one more field on the same call, no second call and no
thread read:

- **`updated=<stamp>` is the pull request's own `updatedAt`**, asked for in the
  standing query beside the three `totalCount`s and `headRefOid`. It is a field
  of the value like the others, so a stamp this source has not seen before is a
  change.
- **`head=<sha>` is the pull request's own `headRefOid`**, asked for in the same
  call and costing nothing more (amended 2026-09-13, draft 5, from the Fable
  read, finding M6). It is here so this read can name one common movement
  instead of shrugging at it: a push to the head moves `updatedAt` and moves no
  count, and draft 4 could only report that as `rescan=true` — so a window
  watching its own pull requests under `--owned-prs` woke itself on every push
  it made, which is the false wake **the two lessons** call worse than a missed
  one. A tick on which `updated=` and `head=` both moved and no count moved is a
  **push**: `push=true head=<sha> rescan=false`, and the line says where the
  movement came from. Under `--not-mine` a new head sha **listed in that file**
  names the push — the
  file holds the ids of the things this actor emitted, a head sha it pushed is
  one of them, and whoever pushed it writes it there, because this tool pushes
  nothing and can only be told — so the tick prints `push=true self=1`, and
  (draft 6, K4) it is **still a change**: a push and a reply on an older thread
  can share one tick and one stamp, so naming the push has never been the same
  as accounting for the movement. Without `--not-mine` a head move is a change
  like every other movement, which is the safe default this source keeps.
- **When `updated=` moved and no count moved and `newest=` and `head=` did not
  change**, the tick knows the conversation moved and cannot say where. That is
  a change, and it is reported as one rather than resolved: one `WAKE PR
  <owner/repo>#<n> … self=0 rescan=true push=false head=<sha> newest=- by=-
  review=- at=<updated> url=<the pull request's url>` line, whose whole content
  is *this conversation moved somewhere this read does not resolve; open it*.
  The window opens the pull request — which is the read this source is designed
  not to make every tick, made once by the reader who needs it instead of
  every tick by the tool.
- **A rescan is never suppressed.** Every `d` is zero on such a tick, so
  nothing is fetched, nothing can be matched against `--not-mine`, and `self=`
  is 0: an actor that replies on an older thread of a pull request it watches
  wakes itself once. That false wake is accepted here by name, and it is the
  direction this hurt chooses.
- **`updated=` is stored on every tick exactly as observed, and no tick on which
  it moved is ever silent** (draft 6, K4, which replaces draft 5's finding-M5
  rule outright). Draft 5 advanced the watermark only as far as a tick's
  attribution reached, and bought two defects with one sentence. A reply on an
  older thread at T under this actor's own comment at T+1 leaves `updatedAt`
  equal to the attributed node's own stamp and **not newer** than it, so that
  tick stored the stamp, set itself aside, and no later tick could ever see the
  reply — the missed wake this source exists to prevent, arriving inside a
  false-wake repair for the second time. Two items sharing one stamp fail
  identically, and a reply sharing a tick with this actor's own **push** is the
  same shape again over `head=`. And where the stamp *was* newer than everything
  the tick fetched, draft 5 left the stored value behind, so every later tick
  observed that same movement and reported it again — a rescan on every poll,
  for ever.
- **So the watermark and the suppression are two things** (draft 6). A watermark
  records what was **observed**: the new `updated=` is stored on every tick,
  always, and carries no claim about attribution. Suppression sets aside only
  what the tick **fetched and matched** — ids under `--not-mine`, and a head sha
  listed there — and it may not make a moved stamp silent. This read fetches at
  most the newest node of each list whose count moved, and a reply on an older
  thread and a dismissal move no count at all, so a tick can never enumerate the
  interval between the stored stamp and the observed one. A moved `updated=` is
  therefore **always** a change: under `--not-mine` a tick whose counted movement
  was wholly this actor's prints `self=<n> push=<true|false> rescan=true`, whose
  whole content is *the counted part of this movement was mine; what else moved
  is not resolved by this read; open it*. Only a tick on which `updated=` did not
  move is quiet, which is the only quiet this read can prove.
- **What that costs, named rather than discovered** (draft 6). A window that
  comments on, reviews or pushes to a pull request it watches wakes itself once
  on that tick — the false wake draft 5's `head=` repair removed, accepted back
  here by name. It is accepted because the alternative is the 100-minute HOLD
  this source was built for arriving as a reply the tool had proved it could not
  see, and because **a rescan is never suppressed** was already this section's
  rule two bullets above: draft 5 carved the exception, and draft 6 takes it
  back. The wake is one line, it is labelled `self=`, and a missed obligation is
  never traded for a quieter tick. This bullet assumes only what the bullet above
  assumes — that a pull request's `updatedAt` moves when its conversation moves
  (**Known limits**) — and it no longer assumes anything about the **order** of
  two stamps, which is the assumption draft 5 rested a suppression on.
- **What remains a limit, at its true width**: whatever the forge does not
  stamp into `updatedAt`, this source still cannot see. The tool asserts
  nothing about which acts a forge stamps — that is `gh`'s and the forge's
  behaviour, reported and never modelled (**Known limits**) — and what it does
  assert is falsifiable, and draft 6 makes it unconditional (K4): a stamp it has
  not seen before is a change, and it never reports quiet about a conversation
  whose own stamp moved.
  `review=DISMISSED` stays a **state this source can print and not an event it
  can see**: it is named in the grammar because the forge reports it and the
  window reads it, it appears as the state of the newest review read at the
  next change, and the rescan is what makes that next change arrive whenever
  the forge stamps the dismissal.

### Check runs on a named head — `--run` (amended 2026-09-13)

`--run <owner/repo>@<sha>` names a commit and watches the hosted checks on it.
It exists beside `--entry` because a run is not always attached to a pull
request — a push to a branch under a merge lane, a tag, a nightly, a commit
another line is landing — and because the thing a window waits for is *this
head is green* and not *this number is green*: an entry whose head moves is a
different run with the same number. `<sha>` is a full sha or an abbreviation the
forge resolves; the line prints what the caller named.

**The state value is the entry value with no entry state**: `fail=<n>
pending=<n> pass=<n>` and the sorted names of the failing checks, computed by
the same bucket rule over the head's check runs and its status contexts, and
FINAL has the entry definition with one clause fewer — `pending=0` with at
least one check; there is no `MERGED` or `CLOSED` to be final by. `--final-only`
applies, with the same meaning, and `unreadable:` wakes under it exactly as an
entry's does. One tick is **two** `gh api` calls per head — `commits/<sha>/
check-runs?per_page=100` and `commits/<sha>/status` — up to about 50 KB down at
a hundred check runs and no pagination: a head with more than 100 check runs is
`unreadable: more than 100 check runs on this head`, which is visible rather
than a count that quietly stops at a page. Runs are polled on
`--entry-interval`, because a run is what that interval is the length of (rule
3), and in the same batches of 8.

**A queued run is not a running one, and this source cannot tell them apart**
(Glenn on #178, 2026-09-13: *"a queued CI run did not mean verification was
executing"* — every runner was offline while every listener process existed).
`pending=` counts both. What the window can do is set `--entry-interval` to the
run's expected length and read `pending=` unchanged across two ticks as the
tell; what this tool does not do is model a runner pool (**What it
deliberately does not do**).

### A branch moving — `--ref` (amended 2026-09-13, draft 2)

**The flag is `--ref`, not `--branch`, and that is a repair of draft 1** (both
cold reads at 11c807a3, finding 1; amended 2026-09-13, draft 2). `--branch`
already means *the bus checkout's branch* on the same verb — `--refresh
--remote <name> --branch <name>`, `--advance-cursor --remote <name> --branch
<name>`, and `probe --refresh` and `serve --receipt` the same way — so
`watch --refresh --remote origin --branch main --branch o/r:main` had no parse
and a flag on this tool meant two things at once. One flag, one meaning, on
every verb: `--branch` is the bus branch everywhere, `--ref` is the forge
source. The verdict field (`branches=<n>`), the `sources=` word (`branches`),
the item line (`WAKE BRANCH`), the cap kind (`branch`) and the state key
(`branch:<owner/repo>:<name>`) are unchanged — the source is still a branch;
only the flag that names one is renamed.

**`--ref` is a prefix of `--refresh`, the parse is exact, and neither refusal
suggests the other** (amended 2026-09-13, draft 3, from the Opus read).
Standard-library `flag` matches a flag name exactly and does no prefix
matching, so the two are two flags on one verb and neither shadows the other:
`--ref` always wants `<owner/repo>:<name>` and `--refresh` is always the bus
fetch, whatever order they appear in. The cost is a person's: the names are
four characters apart and a typo in one is a valid invocation of the other.
The tool does not try to guess which was meant — a malformed `--ref` is
refused naming `--ref <owner/repo>:<name>`, `--refresh` without its
`--remote`/`--branch` is refused naming those, and **neither refusal offers
the other flag as a suggestion**, because a *did you mean --refresh* under a
mistyped `--ref` would talk a caller into fetching the bus when they meant to
watch a branch, and the reverse would silently drop a fetch the bus source
needs. Two flags, two refusals, no cross-suggestion.

`--ref <owner/repo>:<name>` watches the head of one branch on the forge and
may be given more than once. **The state value is the head sha**, or `absent`
when the branch does not exist; any difference is a change — a push, a
force-push, a deletion, a creation — and the line says both ends: `WAKE BRANCH
<owner/repo>:<name> head=<sha|-> was=<sha|->`. One tick is **one** `gh api`
call, `repos/<owner>/<repo>/git/ref/heads/<name>`, about 300 bytes down,
against the forge's REST pool. **The documented contract of that endpoint is
one exact reference or a 404, and this spec neither tests nor relies on any
other** (amended 2026-09-13, draft 4, from the Astra read, finding 8: draft 2
asserted a prefix-match array, which the forge documents for
`git/matching-refs/{ref}` and not for get-a-reference —
<https://docs.github.com/en/rest/git/refs#get-a-reference>; a spec that tests
an invented endpoint contract teaches a fixture, not a forge). So: a 404 is
`absent`, a single ref object is its sha, and **any other shape — an array
among them — is `unreadable: <reason>`**, a change once and standing
thereafter, never `absent`, because an answer this source cannot read is not a
name that matched none. The defensive branch stays because a forge's answer is
`gh`'s to give and not this spec's to promise; what is withdrawn is the claim
about when it occurs. The
read is the forge's and not a `git ls-remote`, because this tool starts `git`
against the bus checkout only and has no other clone to run it in (**The only
programs it starts**); a branch on a forge `gh` cannot reach is not something
this source can watch, and it says so as `unreadable:`. Branches are polled on
`--forge-interval`, below.

The colon is the separator because `#` names an entry and `@` names a head, and
a reader of a transcript should be able to tell the three apart at a glance.

### A lock file released — `--lock` (amended 2026-09-13)

`--lock <path>` names a file some other process holds an advisory lock on —
nova-merge's lane lock, nova-bus's checkout lock, a swarm's — and may be given
more than once. **The state value is one of `held`, `free` or `absent`**, read
by opening the file (never creating it), attempting a non-blocking exclusive
`flock`, and, if it was granted, **releasing it at once** and closing the file.
`EWOULDBLOCK` is `held`; a grant is `free`; a missing file is `absent`. Any
difference is a change, and `held` to `free` or `held` to `absent` is the one
the caller was waiting for; the line carries both ends so a re-acquisition is
visible too. One tick costs four system calls — `open`, `fstat`, `flock` and
`close` — no subprocess, no bytes, and it is polled on `--interval`, because it
is local and cheap.

**A lock is a protocol and not a file, so the path maps per build and an
unknown one is refused rather than read** (amended 2026-09-13, draft 4, from
the Astra read, finding 6). This tool can speak exactly the protocol this
repo's own tools take (`internal/bus`), and the existence of an arbitrary file
identifies nothing:

- on a build whose lock is `flock` (`lock_unix.go`, build tag `unix`), `--lock
  <path>` is that file, opened and probed as above;
- on the **sentinel builds** — `lock_windows.go` (tag `windows`) and
  `lock_other.go` (tag `!unix && !windows`), which are one protocol under two
  tags (amended 2026-09-13, draft 5, from the Fable read, finding M8: draft 4
  named only `lock_other.go` and the build a Windows reader is on was left to
  inference) — the lock is the `O_EXCL` sibling **`<path>.held`**, and the probe
  tests that path's existence with `lstat` and **never opens `<path>` itself**;
- **there is no third case**, and draft 4's *a path this build has no lock
  protocol for is `unreadable: no lock protocol for this path on this build`*
  named one (draft 5, finding M8): those three files are the whole of
  `internal/bus`'s lock and their tags partition every build, so that refusal
  was one no fixture could produce and test 17's mutation over it was an
  assertion nothing could fail — which is the standard this spec sets for its
  own tests. Both the clause and the mutation are withdrawn. What the word
  `held` is evidence of differs by build, and it is stated under **On the
  sentinel platform** below rather than promised away here.

**Only a regular file is probed, and the open cannot block** (draft 4, finding
6). The open is `O_RDONLY|O_NOFOLLOW|O_NONBLOCK` and the descriptor is
`fstat`ed before anything else is attempted. Each flag is here for a failure it
prevents: `O_NOFOLLOW` refuses a symlink at the lock path — `unreadable:
symlink at the lock path` — because a lock whose path was replaced by a link
into somebody else's file is not this lane's lock; `O_NONBLOCK` is what keeps
the open of a FIFO from blocking **before** the non-blocking `flock` is ever
reached, which is the way a watch could hang on a path with no deadline of its
own; and the `fstat` is what makes the refusal a reading rather than a guess:
anything that is not a regular file — FIFO, device, socket, directory — is
`unreadable: <kind> at the lock path, not a regular file`, no lock is
attempted, and nothing is created, replaced or removed to learn it. On the
sentinel build the same applies to `<path>.held`, whose `lstat` is a symlink
check by itself. **A path replaced between two ticks is read as it now is**:
the value is the path's state and never an inode's, so a new file under the
same name is simply the next tick's reading, and the vanished-means-stale
arithmetic that broke another writer's live lock is not repeated here.

**The probe never holds, never writes, never deletes, never creates.** The
duration for which this tool owns the lock is the gap between two system
calls, and it is the one moment at which another non-blocking contender could
be told `held` when the holder had in fact let go. **What a blocked contender
observes is a delay of one gap per tick, and never a denial** (amended
2026-09-13, draft 4, from the Astra read, finding 6, which is a repair of draft
3's *a contender that blocks is unaffected*: a blocking waiter queues beside
the probe's non-blocking attempt, so a grant the holder released can go to the
probe instead, and the waiter then waits the width of the probe's gap — two
system calls, with no I/O, no allocation and no branch between them — before it
is granted). The claim this source makes is therefore the falsifiable one: a
blocking contender is **granted no later than one probe gap after the holder
releases**, because the probe takes a given path at most once per `--interval`
and holds it for the next system call; the unbounded-starvation claim draft 3
made instead could not be tested and is withdrawn. That gap is the source's
**known limit**, named here rather than discovered: a probe by `fcntl(F_GETLK)` would not take the lock at all, and it
does not see a `flock` on every platform, so the tool uses the lock this repo's
own tools take (`internal/bus`'s `LockFile`) and pays the gap. The probe is a
`TryLock` of its own beside that lock and **not** `LockFile`'s acquire: that
one writes the holder's pid into the file it takes, and this source may not
write a byte to a file another process owns (amended 2026-09-13, draft 2, from
the Fable read; test 17's byte-and-mtime check is what proves it, and the
tripwire over `lockfile.go` alone would not).

**On the sentinel platform `free` and `absent` are one fact, a `held` can
outlive its holder, and the word this tool prints is `free`** (amended
2026-09-13, draft 2; Opus finding 9, Fable finding 10 — and the middle clause is
draft 5, from the Fable read, finding L13). Where the lock is the `O_EXCL`
sentinel of `lock_windows.go` and `lock_other.go`, `held` is the sentinel's
existence and its absence is the only other state there is — nobody holds it and
there is no file — so the three-word grammar has two words on those builds. The
tool prints the one the caller is waiting for, `free`, and never `absent`; no
attempt is made to create the sentinel. **Three** consequences, named rather
than discovered: **a state file is not portable between lock implementations**,
because the same path reads `absent` on a `flock` build and `free` on a sentinel
one and the difference would be a change on the first poll after a move; **test
17's `removed, absent` clause is scoped to the `flock` build**, with the
sentinel build asserting `removed, free` instead; and **on a sentinel build a
`held` can be a lock nobody holds**, because the kernel does not drop an
`O_EXCL` file when the process that made it dies — `lock_windows.go`'s own
header says exactly that — so a holder killed while holding the lock leaves
`.held` behind and this source reports `held` at every later poll, where the
same kill on a `flock` build reads `free` at the next one. So the honest
sentence is per build and this is it: on the `flock` build `held` means the
kernel holds a lock for somebody, and on the sentinel builds `held` means the
sentinel file is there and the holder may be long gone. The stale sentinel is
the lock's own limit (`internal/bus` waits its budget and then refuses, naming
the file) and it is this source's limit too; this tool never removes it, and
never decides that a `held` has gone stale. Where neither can be probed, the
value is `unreadable: <reason>`, a change once. The file named is never removed
by this tool under any flag: a lock file another process holds is that
process's, and *"the vanished-means-stale arithmetic broke another writer's live
lock"* (SPEC-MERGE, rule 2, 2026-09-11) is the hurt that forbids it.

### What one tick costs, and which clock it runs on (amended 2026-09-13)

Every source is a read the caller pays for — in API calls against a shared
rate limit, in bytes over somebody's wire, in a subprocess on a shared bench —
so every source runs on a tick the caller named, every tick has a floor, and
what one tick costs is stated here so the caller can set the tick from the
cost rather than discover it.

| source | flag | tick flag (floor) | one tick costs | cap on the item lines |
|---|---|---|---|---|
| bus | `--bus --as` | `--interval` (5s) | one `nova-bus inbox`, no network; under `--refresh`, one `nova-bus wait` = one `git fetch`, a few hundred bytes each way when nothing landed | `--max-lines` kind `bus` |
| lines | `--line` | `--interval` | one `git log` on the checkout, no network | one line per name per state change |
| reports | `--reports` | `--interval` | one directory walk and one `stat` per `RESULT.md` | kind `report` |
| locks | `--lock` | `--interval` | `open`, `fstat`, `flock(LOCK_NB)`, `close`; no subprocess (the `fstat` is draft 4's regular-file check) | kind `lock` |
| entries | `--entry` | `--entry-interval` (5s) | one `gh pr view --json state,statusCheckRollup` per entry, 1–40 KB down (GraphQL pool) | kind `entry` |
| runs | `--run` | `--entry-interval` (**30s when `--run` is given**) | two `gh api` REST calls per head, up to about 50 KB down | kind `run` |
| pull requests | `--pr`, `--owned-prs` | `--forge-interval` (**30s**) | one `gh api graphql` per pull request per tick, under 2 KB each way, **and — only under `--not-mine` — one more for a pull request whose counts moved** (the id read, below), **and one `gh api user` per run only when `--owned-prs` is given** (draft 4, from the Astra read: the login is the listing's and no longer the suppression's, so a `--pr` watch with no `--not-mine` is exactly one call per pull request per tick); `--owned-prs` adds one `gh pr list` per repository per tick | kind `pr`; 20 owned per repository |
| branches | `--ref` | `--forge-interval` | one `gh api` REST call per branch, about 300 bytes down | kind `branch` |

`--forge-interval` is a third clock and it is required whenever `--pr`,
`--owned-prs` or `--ref` is given, exit 2 when missing, with **no default
and a 30-second floor** — the floor is higher than `--interval` because these
sources have no run length to pace by, they are one API call per watched thing
per tick against a shared hourly limit, and people write comments and push
branches at a rate a 30-second tick already over-serves.

**The 30-second floor is `--run`'s too, and that is a repair of draft 1**
(amended 2026-09-13, draft 2; both cold reads, finding 5/8). Draft 1 left
`--run` on `--entry-interval`'s 5-second floor while arguing the 30-second one
from the same budget, which put **the most expensive new source on the loosest
floor**: two REST calls per head every 5 seconds is 1,440 calls an hour per
head, so four watched heads spent a whole REST pool and the arithmetic below
said nothing about it. So: when any `--run` is given, `--entry-interval` may
not go below **30s**, refused by name with the floor (test 13). `--entry`
alone keeps the 5-second floor, because an entry read is one call and the
interval is meant to be the run's length in any case (rule 3).

**Whose limit, and which pool.** The limit is the **forge's**, reported by
`gh`, and not a number this spec owns: on github.com it is about 5,000 REST
requests an hour and, separately, about 5,000 GraphQL points an hour — **two
pools, not one** (amended 2026-09-13, draft 2; draft 1 spent them as a single
budget). `--ref` and `--run` spend REST; `--pr`, `--owned-prs` and `--entry`
spend GraphQL through `gh`'s own queries. A watcher's worst hour, worked at
the floors:

- six `--pr` at 30s: 720 GraphQL reads an hour, and — under `--not-mine` only
  — at most 720 more when every tick moves a count: 1,440, under a third of
  that pool.
- one `--owned-prs` repository at 30s: 20 pull requests plus one `gh pr list`
  is 21 reads a tick, **2,520 an hour**, half the GraphQL pool from one
  repeatable flag; two such repositories exhaust it, and the run says so
  through `calls=` rather than through a failure.
- two `--ref` at 30s: 240 REST calls an hour.
- twenty `--run` heads at the 30s floor: two calls each per tick, **4,800 REST
  calls an hour**, which is the REST pool. Ten heads is half of it.

There is no per-tick ceiling this tool enforces: the caller names the sources
and the clocks, and every `WAKE SOURCE` line for a **forge** source — `prs`,
`runs`, `branches` — carries
`calls=<n>`, the `gh` invocations that source made this run, so the spend is on
the record beside the news and a rate limit arrives as `unreadable:` rather
than as silence. `probe` adds **no** `nova-bus` read per poll and **one bounded
`git` walk of the pinged line's lane** — at most `--correlate-max` items and
`--correlate-bytes` consumed-and-decoded bytes, under a 10s-per-process wall
clock, resuming where the last poll stopped, plus at most one further
name-only commit listing of `--correlate-max` to count what remains (draft 8,
K3b: past that allowance the count is `remaining=-` and no walk is made) — and only
while a ping is outstanding (draft 4, finding 3; two inbox reads from draft 5,
finding M4, until **draft 7**, K3, which took the correlation off the inbox
altogether because a plain `inbox` prints every NEW note in full and so cannot
be bounded from here).
**`WAKE SOURCE locks` carries no `calls=` and no `login=`**
(amended 2026-09-13, draft 3, from both cold reads: draft 2 gave the four new
sources one grammar line and so gave the lock source two fields it can never
fill — *the lock source starts nothing at all* (**The only programs it
starts**), and a login is the prs source's fact). `self=` and `login=` are the
prs line's for the same reason: `self=` is the `--not-mine` read's and no other
source sets anything aside, and `login=` names the account `--owned-prs`
listed from (draft 4). The three clocks are independent and the loop sleeps until
the earliest due source, as rule 3 says.

**The interval floors are not defaults.** `--interval`, `--entry-interval` and
`--forge-interval` still have none (rule 3): the floor is the least a caller may
ask for, and asking is still required.

## State

One file, named by `--state`, holding a flat map of key to value: one entry per
watched thing, namespaced by source — `bus:line:<bytes>`, `bus:note:<id>`,
`entry:<repo>#<n>`, `report:<path>`, `line:<name>`, and since 2026-09-13
`pr:<owner/repo>#<n>`, `run:<owner/repo>@<sha>`, `branch:<owner/repo>:<name>`,
`lock:<path>` and `probe:<name>` — which holds exactly one of
`<sign sha>|sending|<stamp>|<note id>|<anchor>|<scope>|<read>`, the durable
intent, and `<sign sha>|pinged|<stamp>|<note id>|<anchor>|<scope>|<read>`, the
recorded ping —
**the same seven fields in both, with only the phase word moved** (draft 6, K3:
draft 5's intent had no note id to name and its ping dropped the anchor that
**probe**'s answer lookup then required on every poll, so each form lost exactly
what the other needed). `<note id>` is the id `nova-bus prepare` fixed before
any send and is never `-`; `<anchor>` is the bus checkout's head sha as it stood
before the send and is the **correlation range's floor and nothing else** (draft
6, K2: it is no longer evidence that anything was delivered); `<scope>` is the
first twelve hex characters of SHA-256 over the resolved bus path, `--remote`,
`--branch`, `--as` and `--line` joined by NUL, so a state file carried to
another checkout, lane, branch or caller cannot present another transport's ping
as this one's (draft 6, K6); `<read>` is how far the bounded correlation read
has got — `<lane commit>:<items taken inside it>`, or `-` before the first read
— so a read that ran out of budget resumes where it stopped and never at the
anchor, and the anchor is left standing as the range's floor (draft 7, K3), and
it is the one field a poll that sends nothing may rewrite — plus one
`fail:<source>`
per source, the **delivery queue** `queue:<n>` with its counter `queue:next`,
the advance marker `bus:advance`, and for `serve` one `serve:<id>` per note
holding exactly one of `queued|<stamp>`, `dispatching|<stamp>|attempt=<n>`,
`delivered|<stamp>|rc=<n>|redelivered=<0|1>`, `uncertain|<stamp>|attempt=<n>`,
`uncertain|<stamp>|attempt=<n>|rc=<n>` (an idempotent retry that returned
non-zero) or `cc|<stamp>` (rule 10) — a `delivered … rc=0` is the one record
that says the receiver finished with that id, and under `--on-note-idempotent`
it is the completion boundary the queue waits behind. It is written **after every poll**, through a temporary file in
the same directory and an atomic rename, so a call killed by the harness mid-poll
leaves either the previous state or the new one and never half of either.

**Every watched entry holds two things — the newest observation and what was
printed — and every unprinted observation is a queue record.** The stored form
of a watched key is `<value>|printed=<id|->`, where `<value>` is the
newest observed state value and `<id>` is the **delivery id** of the value the
window was last shown: a note's own id for a `bus:note:` entry, and for every
other key the first twelve hex characters of SHA-256 over `<key>\x00<value>`.
A poll whose observed value differs from the stored newest value **appends** a
record `queue:<n>` = `<delivery id>|<key>|<value>`, `<n>` taken from
`queue:next` and never reused, and then replaces the newest; it never replaces
an unprinted value, because the unprinted value is in the queue and a queue
record leaves only by being printed. An entry is **pending** while any queue
record names its key; `pending=<n>` on the opening line and the verdict is the
queue's length. Printing takes records in `<n>` order — oldest first, within
the per-kind cap — and a record is deleted, and `printed=<its id>` written,
only after its line reached stdout (rule 11, step 3). **Nothing coalesces.** A
second red observed while the first is unprinted is a second record and a
second line; red then green with the red unprinted prints red, then green, in
that order; sixty entry transitions elided over three calls print as sixty
lines. **Three things keep an observation out of the queue, all at observation
time, and they are these three** (amended 2026-09-13, draft 3, from both cold
reads: draft 2 said *the one thing* after the amendment had added two more,
and an arithmetic that says one of three is the false-quiet failure's own
shape): **`--final-only`**, which keeps a non-final entry or run value out,
because that is what the flag asked for; **`--to-only`**, which keeps an
`addr=cc` note out and counts it in `suppressed=` and `cc=` (**`--to-only`**);
and **a pull request tick, under `--not-mine`, whose whole movement the tool
proved was this actor's own emitted ids and whose `updated=` did not move**
(amended 2026-09-13, draft 4, and narrowed draft 6, K4: with
no `--not-mine` there is no third exclusion at all, because nothing is set
aside, and a tick whose own stamp moved is a change however well its counts
were attributed — a movement this read cannot account for is the one thing
suppression may not silence), which stores the moved counts and the new
`newest=` and queues nothing (**Comments and reviews on a pull request**). The
`updated=` watermark is stored on **every** tick and is never what is set aside
(draft 6). Each is this tool's own flag or
this tool's own rule deciding, each is counted where its source's counts are,
and none of the three drops a line silently. Every other observation whose
value differs from the stored one becomes a queue record. A key or value holding `%` or `|` is
stored with `%` escaped first as `%25` and then `|` as `%7C`, and unescaped in
the reverse order on load, so a value holding a literal `%7C` reloads as those
three characters and never as a pipe; the round-trip test covers a report path
that carries a `|`, a value holding a literal `%7C`, and one holding `%25`
(Stella, 2026-09-11).
`fail:<source>` is `<n>|<since stamp>|<reason>`: the source's consecutive-failure
streak, written on every failed poll, cleared on the first success, and read at
the start of the next call — so the streak spans calls (rule 8).

- **It is this tool's own file, in a format this tool chose**, and it is not a
  record of anything: delete it and you get a cold start, which is a correct if
  noisier watch. Nothing else may read it as truth.
- **A value is one line and round-trips.** The separator inside a composed value
  is `|` and not a tab, and a stored value compares equal to a freshly computed
  one byte for byte. The second lesson of 2026-09-11 is this sentence; the test
  that pins it writes, reloads, polls again and asserts quiet.
- **It is bounded where it grows with events, and unbounded where a bound
  would lose a delivery.** The `bus:line:` sighting memory and the **delivered**
  `bus:note:` marks — entries whose `printed=` equals their value's id and
  which no queue record names — are the two parts that grow with things that
  happen rather than with things being watched, so each is an LRU of **300**
  entries with the least recently *seen* evicted first; an evicted note is a
  change again, which is the safe direction. **The queue is never evicted and
  never truncated**, and a `bus:note:` entry a queue record names is not a
  candidate for eviction whatever its age: a pending record leaves the state
  only by being printed, so a backlog of 3,000 notes is 3,000 records in the
  file and `--max-lines` lines per call until drained, and the file's size is
  the honest cost of a window that has not read its mail. Pending is unbounded
  in the state and bounded in the output (`WAKE MORE`), never the other way
  round. The prototype
  deleted **all** of them once the count passed 300, which turns every standing
  error back into a change at once — a thundering false wake at exactly the moment
  the bus was noisiest.
- **One writer.** A state file written by two concurrent runs is a race with a
  silent outcome; see **The races**.

## The cold-start rule

**A first run is not a change, and that is true of every source but the bus**
(amended 2026-09-13, draft 3, from the Opus read: draft 2 named `--entry` and
`--reports` and left the amendment's four outside the rule, so a cold `watch
--lock … --ref …` returned instantly listing every lock and every branch —
*a listing nobody reads*, and not what this rule means; the intent was
everywhere but the rule, which already said *"recorded and not reported"* of a
pull request joining mid-run). With no state file, the first poll of
**`--entry`, `--reports`, `--pr`, `--owned-prs`, `--run`, `--ref` and
`--lock`** — all seven — **records** the world and reports nothing: every
entry, every report file, every conversation, every head's checks, every
branch tip and every lock state is new, so a cold watch would otherwise return
instantly with a listing of everything that exists, which is not what *wake me
on a change* means and is a listing nobody reads. A `--lock` that reads `free`
on a cold first poll is the clearest case: nothing was released, the tool
merely arrived. (`--line` is a view over the checkout's commits and not a
source (**Known limits**); its first poll is rule 2's, which judges by
`--offline-after` and not by a stored value.) `cold=true` on the opening `WAKE` line says the
run is in this state, and `--baseline` turns it off for the caller who does want
the world listed once — that is what `quickstart` passes.

**Once state exists, the first poll of a later run does report what moved while no
watcher was running.** That is the whole reason state is kept between calls: the
gap between two calls is the gap in which a window is thinking, and news that
arrived in it is the news it most needs.

**The bus is the exception, and it is not a choice.** A bus read with
`--advance-cursor` *consumes* what it reports; a cold first poll that swallowed
five notes to stay quiet would be exactly the failure in **The races** below,
caused deliberately. So a bus `INBOX NOTE` is a change on the first poll of a
cold run as on any other, and `--baseline` does not apply to it. **Never swallow
mail to make a quiet first poll.** A bus read *without* `--advance-cursor`
consumes nothing, and lists the same note on every poll while the cursor stands
behind it — also not swallowed, and suppressed once printed.

An unrecognised bus line on a cold first poll is a change for the same reason: it
is unrepeatable news about a bus that may be broken right now.

## Bounded output

The cap is `--max-lines`, default **40**, counted over the item lines — `WAKE
BUS`, `WAKE ENTRY`, `WAKE REPORT`, `WAKE LINE` and, since 2026-09-13, `WAKE
PR`, `WAKE RUN`, `WAKE BRANCH`, `WAKE LOCK` — **eight kinds, eight caps**, and
never over the verdict, the opening line, a `WAKE SOURCE`, a `WAKE NOTE`, a
`WAKE POLL` or a refusal. (Amended 2026-09-13, draft 2; both cold reads,
finding 7/6: `WAKE LINE` was counted by rule 5's arithmetic — the base's
`4 * --max-lines` over three listed kinds — and capped by neither this list
nor the `WAKE MORE` grammar, so twenty offline lines could not fit the
constant they were promised inside. `line` is a kind here and in `WAKE MORE`,
which makes the eight caps the eight kinds and the arithmetic below
recomputable.) Past it, one line per kind:

```
WAKE MORE kind=<bus|entry|report|line|pr|run|branch|lock> shown=<n> total=<t> n=<k> <remedy>   (line added: draft 2)
```

`<k>` is `total - shown`, the lines this poll did not print, and every one of
them is pending in the state file and printed by the next call (rule 11). `0`
means all; a negative cap is refused, because `0` already means all and a
negative number is a typo with two readings. The cap is **per kind**, as SPEC.md
requires, because a flat cap over a concatenated stream means the loud kind eats
the quiet one and the quiet one is the finding the window did not already know
about: forty churning entries must not hide one note.

The remedy names the flag that lifts the cap, and where the source has a file that
holds the whole list it names that instead — for the bus, the reader's own `OPEN`
file; for reports, the directory.

**There is no unrepeatable line, and the bound holds over consumed mail.** An
earlier draft exempted notes consumed by `--advance-cursor` from the cap, on the
argument that polling again cannot return a note the cursor has passed. Two facts
make that exemption unnecessary and its cost — 200 mail lines under a promise of
`4 * --max-lines + 10`, the bound of the day, now `8 * --max-lines + 17`
(draft 2) — dishonest. First, a note the cursor passed is in this
tool's own queue before the cursor moves, because the advance is a spooled
transaction (**The bus inbox**) and the queue is never evicted (**State**). It
is **not** on any list `nova-bus` prints by default — measured: a plain `inbox`
does not re-list a note the cursor has passed — and nothing here depends on it
being. Second, the cursor is not advanced on a poll
while any note is pending, and a note is pending until this tool has printed it
(**The bus inbox**, rule 11). So past the cap the line is `WAKE MORE kind=bus
shown=<n> total=<t> n=<k> pending; the next call prints them, the cursor waits`,
`<k>` is `total - shown`, the `<k>` notes stay pending in the state file, and the
next call — or a wider `--max-lines` — prints them before anything newer. The
prototype's `state advanced; poll again` was the false promise; this is the same
words made true by the ordering. A `WAKE BUS STANDING` line prints on every poll
it stands, and is counted against the bus cap on each of them, so a run of many
polls over a refusing bus still prints at most `--max-lines` bus lines per poll.

## The races

Four, all observed or trivially reachable, each with the rule that closes it.

**Two watchers advancing one bus cursor.** Two calls with `--advance-cursor` over
one `(bus, as)` pair interleave, each consuming the notes the other should have
relayed, and each returns a partial listing that looks complete. Closed by the
lock above: one advancing watcher per `(bus, as)`, second is `WAKE REFUSED` exit
2 naming the holder. A non-advancing watcher takes no lock and cannot lose
anything, so any number may run.

**A state file written by two runs.** Two watches sharing one `--state` path each
write the whole map, so the later write erases everything the earlier one learned
— including sighting memory, which silently resurrects standing errors, and
including entry values, which silently re-report churn. Closed by one writer: a
`watch` takes an exclusive lock on `<state>.lock` for the duration of the call and
a second run over the same path is `WAKE REFUSED` exit 2 naming the holder and
saying the fix, which is a state file per watch. Not closed by atomic rename — the
rename makes each write whole, and two whole writes of different truths is the
race.

**Mail consumed by a probe and not relayed.** The one that actually happened: a
test run of the watcher read the window's bus with `--advance`, consumed five
notes, and reported into a transcript nobody read. Closed three ways at once,
because one way was not enough — `--advance-cursor` is off by default; it requires
`--as` and is permitted only for the window's own name; and a run that advances
does so only behind its own print, so a consumed note is a printed note. **A watcher may not advance a cursor that is
not the window's own**, and there is no flag that lets it.

**A harness kill between observing and printing.** A call killed at its ceiling
after observing a change — before or after the state write, before or after the
item line reached stdout — re-reports that change on the next call. This race is
closed in the **safe** direction deliberately, by rule 11: observed state is
written first, item lines are printed second, `printed=` marks are written
third, and a kill at any boundary leaves the entry pending, so the duplicate is a
repeated wake and never a lost one. A window told the same news twice reads twice;
a window told it never does not. The one residual is named and closed: with
`--advance-cursor`, a kill between `nova-bus inbox --advance` returning and the
write of its output would lose this tool's record of any note that call listed
first, and — measured, **How the checkout receives mail** — a plain `inbox`
does not re-list a note the cursor has passed, so nothing would bring it back
on its own. So the advance is wrapped: `bus:advance=inflight` is written before
it and cleared after its output is durable, and a call that finds the marker
runs `inbox --open --open-max <carrying>` and spools every unprinted note
before anything else (**The bus inbox**, step 4). That is not left to the
prose: test 11 kills at exactly that point, with a backlog above `nova-bus`'s
default `OPEN` display cap of 20, and asserts every note the advance consumed
is printed by the following calls. **This race is closed by
ordering, not by refusal.** A refusal before the state write (lesson 63's
shape) would be the wrong tool here: there is nothing to refuse, because the
kill is the harness's and arrives at any instruction; the only choice the tool
has is which side a kill lands on, and it chooses the repeated wake every
time. The cost — a window can read one line twice — is in **Known limits**.

## The rules of the last two days

Twelve rules, 2026-09-09 to 2026-09-11. Each came from a hurt and each is written so
a test can be built from it. Where a rule changes a sentence above, that sentence
has been changed to match, and this section is the reason. Where a rule names a
prototype behaviour, it is listed by number in **What the prototype does that this
spec forbids**.

1. **Every wait has a written deadline and a default action.** `--max` is the
   deadline and `--on-deadline <word>` is the action. Both are required. A `watch`
   missing either is exit 2, `refusing to guess`, naming the one it lacks. The
   tool never waits past `--max`. A watch that reaches its deadline prints exactly
   one verdict line, `WAKE QUIET after=<d> polls=<n> default=<word>: deadline,
   default taken`, and exits 0. `QUIET` is the status word the caller reads. The
   tool takes no action itself. `<word>` is what the caller said it would do,
   echoed on the opening line and on the verdict, so the transcript records the
   decision. The caller decides. (The hurt: nineteen orphaned shells on 2026-09-09
   were waits with no deadline, and a wait with no default is a window that
   returns and then has to think about what it meant to do.)

2. **Ten minutes silent is offline.** `--line <name>` names a line to watch and
   may be given more than once. It requires `--bus`. A line's **last sign** is the
   stamp of the newest commit on the bus checkout's branch whose author is that
   name: a note, a receipt, or any other commit the line made. A line whose last
   sign is older than `--offline-after` is reported once, as a change, on one
   line: `WAKE LINE name=<name> state=OFFLINE last=<stamp> silent=<d>
   commit=<sha>`. `--offline-after` defaults to **10m**, because it is the
   family's rule and not a fact about one
   window: every line must agree about it, and it is the same number as
   `nova-board --stale`. (Amended 2026-09-13, draft 3, from the Fable read:
   draft 2 kept *it is the one duration here with a default* after the
   amendment gave `probe`'s `--silent-after` and `--answer-within` theirs. It
   is one of **three**, and the test all three pass is rule 3's — a duration
   gets a default here only when a different number per window would be a bug.) The tool never repeats an OFFLINE line while nothing
   changes: the state file holds `line:<name>` as `<sha>|<state>|<stamp>` — the
   commit the judgement was made on, the word it produced, and that commit's
   stamp, which is what `silent=` is computed from at print time rather than a
   duration stored in the file — and a later poll with the same value prints
   nothing. A new sign from the line is a change once, `state=BACK`. A line
   with no commit on the branch is `last=- commit=-` and is OFFLINE at the
   first poll after the watch has run for `--offline-after`. **A git author is
   the bench's identity and not the bus sender's, and where they differ this
   view cannot see the line — so it says so** (amended 2026-09-13, draft 4,
   from the Astra read, finding 4: a sender lane's commits can carry the
   human's name, so `--line <sender>` matches nothing and `--line <human>`
   matches several senders at once). A `--line` whose name authors no commit
   on the branch prints, **once per run** (draft 5, from the Fable read,
   finding L12: *once* left a second call undecided), `WAKE NOTE line <name>:
   no commit on this branch carries this author; this view reads a git author
   and not a bus sender` — beside its `last=- commit=-`, so a name that will
   never be seen is loud on the first poll instead of quietly reading OFFLINE
   for ever, and a later call in the same run prints it no more (a new run is a
   new note, because a run that starts with the name already wrong should say
   so once).
   Resolving a sender to a lane belongs to the program that owns the roster,
   and until this view is given that, the mapping's width is named here and
   pinned by test 2. The sign is read with `git log` against the bus checkout,
   read-only, under `--gh-timeout`, and nothing else is read. (Glenn, 2026-09-10:
   reassign a silent line's items after about ten minutes. Johnny ran out of
   credits at 00:35Z and the board said he held his items for an hour.)

3. **Poll cadence matches the watched thing's rate.** `--interval` has no default
   and is exit 2 when missing. It is the cadence for the bus, for `--line` and for
   report directories. Entries have their own cadence, `--entry-interval`,
   required whenever `--entry` **or `--run`** is given (amended 2026-09-13,
   draft 2, from the Fable read: `--run` shares the clock and draft 1's rule
   named only `--entry`), and it is the expected length of the
   hosted run: an entry is polled no more often than that. Both intervals have the
   5s floor — amended 2026-09-13, draft 2: `--entry-interval` has a **30s**
   floor when any `--run` is given, because a head costs two REST calls a tick
   and 5 seconds spends a pool (**What one tick costs**) — and neither has a default, which is the opposite of `--offline-after`
   in rule 2 on purpose: an interval is a fact about one window's round trip
   and only that window knows it, while ten minutes silent is the family's
   rule and every window must agree about it. A duration gets a default here
   only when a different number per window would be a bug. The loop sleeps
   until the earliest due source, so a 5s bus interval
   beside an 8m entry interval polls the bus every 5s and the entry every 8m.
   (Amdahl, on Glenn's word: an 8-minute CI run deserves one check at 8 minutes,
   not eight checks at one minute. The prototype's 30-second default made sixteen
   `gh` calls per entry per eight-minute run, and every one learned nothing.)

4. **It finds itself by a file, never by `pgrep`.** The tool never lists
   processes, never reads its own command line back from the process table, and
   never uses `/tmp` or `$TMPDIR`. (Amended 2026-09-13, draft 2: `probe --here`
   takes a process **count** from the kernel — `sysinfo`'s `procs`, a
   nil-buffer `kern.proc.all` length — which reads no entry, no pid and no
   command line, and leaves this rule and test 4's tripwire exactly as they
   are; the mechanism is under **probe**, rule 18.) Its only files are `--state`, the fixed-name
   temp file beside it, and `<state>.lock` beside it, which holds the pid of the
   run that took it. A second `watch` over the same `--state` path is `WAKE
   REFUSED`, exit 2, on one line naming the holder's pid and the path. Nothing the
   tool does touches the filesystem outside the directories it was given.
   (2026-09-09: a loop that pgrepped its own command line matched itself and never
   ended.)

5. **Wake output is bounded and never carries a body.** One line per event. A
   relayed note is its id, sender, stamp, commit, path and subject, never its body.
   A report is its path and size, never its contents. An entry is its counts and
   failing names, never a log. Past `--max-lines` events of one kind in one
   interval, one `WAKE MORE` line counts the rest. `--max-lines` is the N. The
   bound holds at the largest plausible state **with no exception for consumed
   mail**: 200 notes, 50 entries, 100 report files and 20 lines changing in one
   interval print at most `8 * --max-lines + N` lines **plus the two variable
   terms named below**, with or without
   `--advance-cursor`, on every poll of the call and not only the first.
   (Amended 2026-09-13, draft 3, from the Opus read: draft 2's own paragraph
   and test 5 both carried the two terms and this sentence — the rule's
   testable one — did not, which is the dishonest constant the rule was
   written against, in the rule itself.)

   **`N` is 17, and it is counted here rather than asserted** (amended
   2026-09-13, draft 2; both cold reads, finding 7/6). Draft 1 kept the base's
   `+10` while doubling the caps, and the `10` had never been counted: the
   unbounded lines are the opening line and the verdict (2), one `WAKE SOURCE`
   per source (**7**: bus, entries, reports, prs, runs, branches, locks — the
   `--line` view has no source line of its own), and one `WAKE MORE` per kind
   (**8**, now that `line` is a kind). That is **17**, fixed, and it is the
   whole of what a poll prints besides item lines. **Three** terms are variable
   and are named rather than folded into the constant, because a constant that
   hides them is the dishonest bound this rule was written against: at most
   **four run-wide `WAKE NOTE` lines** (the three the bus source can print in
   one call and the `prs` host-login note) plus **one capped-note per
   `--owned-prs` repository named**, **one `WAKE NOTE line <name>` per `--line`
   name that authors no commit, once per run** (amended 2026-09-13, draft 5,
   from the Fable read, finding M7: draft 4 added that note per name and left it
   outside every term of this bound, so twenty `--line` names made the stated
   bound short by twenty), and **one `WAKE POLL` line on stderr per
   failed source per poll** (at most 7 in a poll, and a source's third
   consecutive failure ends the call). So, at the largest plausible state —
   200 notes, 50 entries, 100 reports, 20 lines, 50 pull requests, 50 heads,
   50 branches and 50 locks changing in one interval, every kind above its
   cap — one poll prints at most `8 * --max-lines + 17` lines plus those three
   named terms, and with `--max-lines 40`, one `--owned-prs` repository and
   twenty `--line` names none of which authors a commit that is at most
   320 + 17 + 5 + 20 + 7 = 369. **`probe` is bounded separately and in the same
   style** (draft 8, K3a): one `WAKE PROBE` line per `--line` name, at most one
   partial-correlation `WAKE NOTE` per name per poll, and **at most 64** gap
   notes per name over the whole life of a record — a gap is named **once**,
   on the poll that first records it, and never again while it stands, so that
   term is bounded by the retained gap list's own cap and not by how many polls
   a record lives through. (Glenn, 2026-09-09: tool output costs tokens; test at the largest
   plausible state.)
   plausible state.)

6. **What woke you is named.** Every change line carries the identity of the thing
   that changed: a note's id and commit sha, an entry's repository, number and new
   state, a report's path, a line's name and last commit. The words `something
   changed` never appear in this tool's output, and no change line is printed
   without its identity field. (A wake that says only that the world moved sends
   the window back to look at all three places, which is the tick this tool
   replaces.)

7. **Never filter the status line.** Every line the bus source reads is classified
   as suppressed, relayed or standing, and every line is counted — a note
   already marked printed is suppressed, and is the one suppression the tool's
   own state decides. Nothing is dropped silently. Once per run, before the verdict, `WAKE SOURCE bus read=<n>
   suppressed=<n> relayed=<n> standing=<n>` prints the four counts, and they add
   up: `read` equals the sum of the other three. The suppress list decides what is
   hidden and never what is shown; a line the tool cannot classify is relayed.
   (Amended 2026-09-13, draft 2: `--to-only` is the second suppression this
   tool's own flags decide, so an `addr=cc` note held back by it is counted in
   `suppressed=` and broken out as `cc=`, which is a part of that number and
   not a fourth term — the sum holds with the flag as without it.)
   (2026-09-10: a grep that kept only NOTE lines dropped the `INBOX REFUSED` line
   and gave the window thirty minutes of false quiet.)

8. **The watcher's own failure is loud.** A poll of one source that fails is one
   `WAKE POLL` line on stderr and the watch goes on. The **third consecutive**
   failure of the same source ends the watch: the verdict is `WAKE BROKEN
   source=<s> failures=3 since=<stamp>: <reason>`, exit 2, because a watcher that
   cannot see its source is not watching, and a `WAKE QUIET` from it would be a
   lie. **The streak lives in the state file as `fail:<source>` and spans
   calls**: the first unreadable reason is a change and returns the call, and a
   counter that started at zero on every call would never reach three over a
   source whose error text changes, so the count, its start stamp and its last
   reason are written on every failed poll and read at the next call's start.
   A success clears it. `since=` is the streak's first failure, which may be a
   call or more ago. A source fails when the bus's `nova-bus` exits
   other than 0 or times out, when every entry is unreadable in one poll, or when
   every `--reports` directory is unreadable. Three is fixed and not a flag: it is
   a fact about the tool, not about the window.

   **The streak is every source's, and that is a repair of draft 1** (amended
   2026-09-13, draft 2; both cold reads, finding 3/4). Draft 1 added four
   sources and left this clause and the `WAKE BROKEN` grammar naming three, so
   a `watch` whose only source was `--pr` — legal, and demanded by test 13 —
   ran to `WAKE QUIET` when `gh` died: every pull request read
   `unreadable:`, which is a change once and standing forever after, and the
   call ended with the word this rule itself calls a lie. **That is the
   false-quiet failure this spec exists to close, reintroduced by an
   amendment.** So, per poll: the **prs** source fails when every watched pull
   request is unreadable **or a `--owned-prs` listing fails** (draft 3, from
   the Opus read); **runs** when every watched head is; **branches**
   when every watched `--ref` is; **locks** when every `--lock` path is
   unreadable — and `absent` is a **state and never a failure**, because a
   lock file that is not there is the answer, not the absence of one. Each
   source carries its own `fail:<source>` streak across calls, three
   consecutive failed polls of any one of the seven is `WAKE BROKEN
   source=<that source> failures=3`, exit 2, and a source that is failing
   while another is quiet is `sources-failing=<n>` on the **`WAKE QUIET`**
   verdict — the one verdict that carries the field, stated once under
   **Output grammar** (amended 2026-09-13, draft 3, from the Opus read: draft
   2 said *on the verdict* of a field the grammar gave to one line of four,
   and `WAKE STOPPED` was new that draft).

9. **The tool stamps; a typed time is never trusted.** Every stamp `nova-wake`
   prints or stores is the tool's own clock, read at the moment of writing, RFC
   3339 in UTC: the `at=` on the opening line, `since=` on `WAKE BROKEN`, and
   every stamp in the state file. A time that reaches this tool inside text, in
   a note's body, a report's contents or a caller's flag, is data and is never
   used to order, age or deduplicate anything. Bus notes are ordered by the
   bus's own commit stamp, which is a tool's stamp; a line's last sign is its
   newest commit's stamp and never a date in that commit's subject. There is no
   flag that sets a stamp and none will be added. (2026-09-11: a person stamped
   notes two hours ahead of the clock, and every reader that ordered by the
   typed time put them in the future.)

10. **`serve` wakes a harness from outside it, once per note, and spends
    nothing while idle.** `watch` runs inside a tool call and returns to a
    session that is already awake; `serve` is the other shape (Glenn,
    2026-09-11: a named friend should wake efficiently when a note for them
    lands, and be efficient while there is NO work, never polling once a
    second inside a turn that costs tokens). `serve --bus <dir> --as <name>
    --on-note <command> --interval <duration> --state <file> --hours <h>`
    runs as its own process outside any session — `<h>` is a **decimal**
    number of hours and not a whole one, as `nova-swarm run --hours` is, so a
    first run can ask for `0.02` of one and a working day is `8`, and a
    `--hours` naming a deadline **under one second** is refused by name (a
    float can name one no run reaches: `1e-12` rounds to `0s`, and a process
    that exits 0 having polled nothing is a green that did nothing; a deadline
    shorter than one `--interval` is not refused and polls once) — fetches the
    bus every `--interval` (a git fetch costs no tokens; the interval matches the
    latency a person will accept, never the second), and for each new note
    whose `To:` names `<name>` runs `<command> <id> [<id>…]`: **once, and a
    second time only on a person's word or under a declared idempotent
    receiver**. `serve` starts `<command>` for a note and for nothing else —
    never on an interval, never to receipt, never to look — so an empty
    minute costs one fetch and zero tokens. The command receives note ids
    and nothing else: never `inbox`'s output, never the `INBOX OPEN
    carrying=<n>` line or the carried list, never a body; the line's model
    opens the note itself (Johnny, 2026-09-11: the carrying dump is not a
    wake payload). **`To:` wakes; `Cc:` does not.** A note carrying the name
    on `Cc:` only (`INBOX NOTE … addr=cc`) is never dispatched, never
    receipted and never a turn: it is recorded `cc|<stamp>` and counted
    `cc=<n>` on the exit line, and the line reads it at its next natural
    turn — to means must act, cc means should know, and a broadcast to five
    is five turns (nova-tools #53). **Dispatch is coalesced.** Every note
    `queued` at the moment the command is not running is handed to one
    invocation, in bus order, at most `--batch-max` ids (default 20, the
    listing law); one turn reads k notes rather than k turns reading one.
    A note's delivery is durable states under `serve:<id>` in the state
    file, each written through the same temp-file-and-rename as everything
    else: `queued|<stamp>` when the note is first seen; `dispatching|<stamp>|
    attempt=<n>` written **before** the spawn, for every id in the batch;
    `delivered|<stamp>|rc=0|redelivered=<0|1>` written **after** the
    command exits 0, for every id in the batch. Exit 0 is
    the one acceptance boundary an arbitrary command offers, so `delivered
    rc=0` is *accepted* and there is no separate *completed*. A command that
    does not exit 0 has not accepted the note: the batch becomes
    `uncertain|<stamp>|attempt=<n>|rc=<rc>`, is counted `failed=`, prints
    `WAKE UNCERTAIN id=<id> attempt=<n> rc=<rc>: dispatch did not accept; …`,
    and blocks subsequent dispatches until resolved with `--redeliver` (never a
    silent loss, and never an automatic retry of an arbitrary command). **A restart
    first establishes that the receiver is idle, and only then runs what it
    finds `queued`.** Killing `serve` does not prove that the command it
    started died: the child may be alive and mid-turn, and the harness is
    one. So a `dispatching` entry found on restart is an **interrupted
    dispatch** that becomes `uncertain|<stamp>|attempt=<n>`, is **not** run,
    and prints `WAKE UNCERTAIN id=<id> attempt=<n>: dispatch interrupted;
    nova-wake serve … --redeliver <id> --on-note <command> runs it again` —
    because a command with no idempotency protocol may have acted, and a tool
    that ran it again would be choosing a duplicate action on the mind's
    behalf — and **an unresolved `uncertain` entry blocks this receiver's
    queue**: no `queued` note is dispatched and nothing is redelivered while
    one exists, `WAKE BLOCKED as=<name> uncertain=<id> queued=<n>: …` is
    printed once per call with the remedy, and the exit line's `queued=`
    carries what waited. `--redeliver <id> --on-note <command>` is a person's
    act — the person has ended the earlier command or watched it return, and
    the state stores no command, so the redelivery names its handler —
    refused unless the state is `uncertain`, and runs it once more as
    `attempt=<n+1>` with `redelivered=1`; the queue drains after it returns.
    With `--on-note-idempotent` — the caller's declaration of a **stronger
    receiver contract**, never an assumption about an arbitrary command:
    the command de-duplicates by id, durably; it tolerates a second
    invocation for the same id beside a live first one; **and its exit 0 for
    an id is a terminal acknowledgement that the work for that id has
    completed**, so a second invocation that finds the first still running
    either waits for it and returns its outcome, or exits non-zero — it
    never exits 0 for merely having seen the id, because "already accepted"
    says nothing about idleness, and idleness is what the queue behind it
    needs — an
    interrupted `attempt=1` is run once more as `attempt=2 redelivered=1`
    before anything queued, and **the completion boundary is that retry's
    `delivered|<stamp>|rc=0|redelivered=1` written to `serve:<id>` after it
    exits 0**: until that record exists no different queued id is
    dispatched (the entry sits `dispatching|<stamp>|attempt=2` while the
    retry runs, and a running command holds the queue as always); a retry
    that exits non-zero has not acknowledged the original dispatch and its
    entry becomes `uncertain|<stamp>|attempt=2|rc=<n>` — `WAKE UNCERTAIN
    id=<id> attempt=2 rc=<n>: retry not terminal; …` — which blocks the
    queue like any `uncertain` until a person's `--redeliver`, never a
    third automatic run; and an interrupted `attempt=2` is `uncertain` and
    blocks all the same, so nothing fires forever and nothing goes quiet. A
    receiver that cannot promise the terminal meaning of exit 0 must not be
    declared idempotent; it gets the default, a person's `--redeliver`.
    Continued automatic progress past a kill would need durable
    process-level receiver ownership and a handoff that proves no overlap,
    which this version does not claim. (Stella, 2026-09-11: with A running
    and B queued, kill `serve` alone and restart: A uncertain, B dispatched
    beside a live A, two turns on one harness.) An earlier draft
    wrote the id once before the spawn and called that exactly-once; Stella's
    second read showed it was at-most-once with a lost launch in the gap,
    her third that an automatic second run of an arbitrary command is a
    duplicate nobody chose, and her final read that a same-id concurrency
    tolerance does not establish idleness for a different id, so the
    idempotent retry needs a completion boundary and the flag names it.
    The handoff is therefore **at-most-once by
    default, with the uncertain case surfaced and never silent**. A command
    still running holds the next note as `queued`, because the harness is
    one and cannot take two turns at once. The command's exit code is
    recorded per batch as `WAKE FIRED ids=<n> first=<id> rc=<n>
    redelivered=<0|1>`; the tool never reads the command's output and never
    retries a non-zero exit on its own. It ends at `--hours` or a `stop` file
    and prints `WAKE SERVE fired=<n> notes=<n> redelivered=<n> uncertain=<n>
    queued=<n> cc=<n> max_wait=<d> idle=<duration>` on exit, where
    `max_wait` is the longest a note sat `queued` before its dispatch — the
    latency Stella's sixth idea asks to measure. What the command is (a
    `claude -p`, an `opencode run`, a `grok` invocation) is the line's
    business, never the tool's, and whether it runs at all is the line's
    person's: starting a `serve` under a name is that name's decision and
    nobody else's. With `--receipt`, `serve` sends the bus receipt for every
    id in a batch **after** `delivered` is written with `rc=0`, never at fire
    time and never for a non-zero exit: a receipt is the machinery's claim
    that the note was handed to the mind and the mind returned, and a receipt
    sent before the command returned would claim a handoff a kill could still
    lose. A note whose command failed stays on the open list unreceipted,
    which is where a note nobody has dealt with belongs. So the model never
    spends a turn on "Heard": a receipt is the machinery's, a reply is the
    mind's. (2026-09-11: one line sent 26 "Heard" receipts by hand, a turn
    each, and the coordinator read every one.) While a line has no work, its
    cost is one fetch per interval and zero tokens; while it has work, one
    wake per batch of notes and no poll inside the turn.

11. **Delivery is the printed line, not the state write.** A change is
    *delivered* when its line has been written to stdout, and nothing else —
    not the poll that observed it, not the state write that recorded it, not
    a cursor that moved past it. The order on every poll is fixed: (1)
    append every observed value to the queue and write the state — an
    observation never overwrites an unprinted one, (2) print the item lines
    from the head of the queue, up to the cap, (3) delete each printed record
    and write `printed=<id>` for each line that reached stdout, (4) print the
    verdict. An entry with a queue record is **pending**, stays pending
    across calls, is counted as `pending=<n>` on the opening line and the
    verdict, is never evicted (**State**), and is printed by the next call
    before anything newer — so a line elided by the cap is shown by the next
    call or by a wider `--max-lines`, and a call killed at any of the three
    boundaries replays rather than loses. A duplicate line is the cost and it
    is paid on purpose. (Stella, 2026-09-11, second read: a cursor is a claim about what
    a reader has been shown, and an entry already in state that the cap
    elided was never shown.)

12. **New mail reaches the checkout through `inbox --advance`, and through
    nothing else this tool does.** Stated in full under **How the checkout
    receives mail**: the fetch is inside `nova-bus`'s push, the version is
    pinned to this build's own and checked before the opening line, mail is relayed
    within two **advancing** polls under `--advance-cursor` — a poll advances
    only behind an empty bus queue, and a call that defers the advance says
    so — and without it the tool fetches nothing and prints `head-at=` so the
    standing checkout is visible. (Stella, 2026-09-11, second read: a perfectly functioning
    watcher can remain quiet while remote mail arrives.)


## The rules of 2026-09-12 and 2026-09-13 (amended 2026-09-13)

Six more, numbered on from the twelve above, each from a hurt named in the
amendment's table and each written so a test can be built from it.

13. **Every wait the window has is a source here, so no wait is a poll.** The
    tell recorded on 2026-09-11 was *"a status check I ran in the last minute,
    run again"*, and the measure was 1,204 turns against 652M cache-read
    tokens. A thing a window checks twice in a minute is either a source of
    this tool or a defect of this tool, and the answer to *I have nothing to
    wake on* is a flag on `watch`, never a `sleep` in a turn. The four sources
    added here are the four such checks the record names: a pull request's
    conversation, a head's checks, a branch's tip, a lock's release. A
    fifth is a spec change, as before; what is forbidden is the loop.

14. **A pull request the window owns is watched on the same call as the bus.**
    `--pr` and `--owned-prs` are sources of `watch`, polled on
    `--forge-interval`, and **any** new comment, review or review thread is a
    change that names the pull request, the kind, the id, the author and the
    review state — as is a conversation whose own `updatedAt` moved with
    nothing else, which is reported as `rescan=true` because the movement is
    somewhere this read does not resolve (amended 2026-09-13, draft 4, finding
    2).
    **Setting this actor's own words aside is opt-in and made of ids**, under
    `--not-mine`, and never of a forge login, which is a shared credential and
    not a window (draft 4, from the Astra read: two friends on one login
    suppressed each other's findings). Inside that narrowing the words are
    counted and never wake it **wherever the tick can prove they were the whole
    movement** — the proof is the ten-node read under **Comments and reviews on
    a pull request**, and a movement wider than that window wakes rather than
    guesses. (Amended 2026-09-13, draft 3, from the Fable read: draft 2's
    unqualified *never* stood against that section's *if any `d` is above 10 …
    it **is** a change*, with nothing saying which won. **The bound wins**, and
    the rule is what holds inside it; test 14 asserts the `d=12` wake.)
    (2026-09-12: two HOLDs unread for 100 and 45 minutes on
    pull requests the window owned, while its bus wait and CI watchers were
    armed and quiet — *"the bus wait is my only wake, and it does not know
    about PR comments."*)

15. **A wait ends on the first change, at its deadline, or on the caller's
    stop, and never otherwise.** `watch` returns on the first poll that
    produces a change, at `--max`, or when the caller sends it `SIGINT` or
    `SIGTERM`; on a stop it finishes the step of rule 11 it is in — an
    observation already made is written, a line already printed is marked —
    prints `WAKE STOPPED after=<d> polls=<n> pending=<n>: stopped by the
    caller` as its last line, releases `<state>.lock`, and exits 0: the stop
    was the caller's decision, the state is whole, and nothing observed was
    lost. It never re-arms, never loops past `--max`, never forks, never
    detaches, and the only files it has touched at exit are `--state`, its
    fixed-name temp file and `<state>.lock`, which the kernel releases with the
    process. A stop that arrives mid-poll of a forge source abandons that `gh`
    call; a stop that arrives between `nova-bus inbox --advance` returning and
    the write of its output is the same kill point rule 11 already recovers
    through `bus:advance=inflight`. (2026-09-10: nineteen orphaned shells in
    one sweep, *"thirteen polling a log for an EXIT line from a child three
    and three-quarter hours gone."*)

16. **The lock source probes and never holds.** A `--lock` tick takes the
    lock non-blocking and releases it in the next system call or is told it is
    held; it never waits for it, never creates the file, never writes to it,
    never deletes it, and never reads a pid out of it to test with `kill` or
    the process table — rule 4 stands for this source as for the tool. It
    probes the lock **this build's protocol takes** and refuses a path it has
    none for; it opens nothing but a regular file, follows no symlink at the
    path, and blocks on no open (amended 2026-09-13, draft 4, finding 6). A
    blocking contender is delayed by at most one probe gap per tick and is
    never denied, which is the claim test 17 can fail. (The
    same sweep: *"two waiting for `pgrep -f` … to come up empty, which it never
    can because the loop's own command line contains the string."*)

17. **A silent line is probed once, silence is never a diagnosis, and a
    declared rest is never probed at all.** `probe
    --line <name>` reads the line's last sign from the bus checkout (rule 2's
    definition: the newest commit on the branch authored by the name — a note,
    a receipt, anything) and prints it as `contact=`, which is transport
    contact and not scheduling eligibility (amended 2026-09-13, draft 4,
    finding 3). A line named in `--rest` is `RESTING` and nothing is sent,
    whatever its contact reads, and only the window's own hand ends that, after
    a fresh explicit return. Otherwise: within `--silent-after` it is
    `PRESENT`. Past it,
    with `--ping-draft`, the tool sends **one note identity** — the caller's
    draft, `To:` the line, fixed by `nova-bus prepare` before anything is sent
    and delivered by `nova-bus send --prepared` as `--as` (draft 6) — writing a
    durable intent naming that note id before the send and recording the ping
    against the sign it was made on in
    `probe:<name>`; a later probe that finds the
    same sign and a recorded ping **does not ping again**, whatever the
    interval, and one that finds an interrupted intent **reconciles it by asking
    the lane's fetched remote ref for that exact note id before it sends the same
    artifact again, or says `UNRECONCILED` and
    sends nothing** (draft 4, finding 5; re-derived in draft 5, from the Fable
    read, H1: the checkout carries a send's commit before the push does; and in
    draft 6, K1 and K2: a fresh send invents a second id, and a caller's commit
    past an anchor is not the ping). A note or
    receipt from the
    line **addressed to the caller** after the ping is `ANSWERED` once and
    clears the record; an unrelated commit on the lane is not an answer. No sign within `--answer-within` of the ping is `UNAVAILABLE`, and
    the reason is `unknown` — the tool never writes *out of credits*, *asleep*
    or any cause, because it has measured a silence and nothing else. A line
    that has said it is stopping is a line with a sign; the probe learns that
    from `--rest` and never from the line's own words, which it does not read
    (draft 5, from the Fable read, finding L11: *a stop note is a sign* was
    draft 3's answer and `--rest` has been the answer since draft 4). (Glenn,
    2026-09-12: *"If we don't hear
    from a friend for 5 minutes, we should try to ping them to wake up. If that
    doesn't work, then they have probably gone to sleep … If we assign to
    somebody who is not here, the work will never get done."* Grok, on #178:
    *"A deliberate stop of the poller must not be restarted by a liveness
    probe."*)

18. **READY is a measurement, and the tool takes it.** `probe --here` reads
    this bench's one-, five- and fifteen-minute load averages, its logical CPU
    count and its process **count** from the operating system at that instant —
    never by starting `ps` or `uptime`, never as a listing, and (amended
    2026-09-13, draft 2) never by enumerating `/proc`: the count is
    `sysinfo`'s `procs` on Linux and a nil-buffer `kern.proc.all` length
    divided by `sizeof(kinfo_proc)` on Darwin, which is the mechanism test 4's
    tripwire allows without an exemption — and prints them on
    one line, `WAKE HERE at=<stamp> load=<1m>,<5m>,<15m> cpus=<n> procs=<n>`,
    so a readiness receipt can carry the numbers it was decided on. With
    `--quiet-load <x>` the line appends `quiet=<true|false>`, the one-minute
    load against the caller's number — a comparison the caller asked for, not a
    verdict the tool reached. The word READY appears nowhere in this tool's
    output: the receipt is the window's and it is a promise about the next ten
    minutes, made on the numbers. (2026-09-09: READY sent, two heavy children
    started three minutes later; Mercury: *"READY only when the system is quiet
    enough for the task."*)

## probe — is a named line here, and is this bench quiet (amended 2026-09-13)

```
nova-wake probe --bus <dir> --line <name> --state <file> [--silent-after <d>] [--answer-within <d>]   (draft 2: defaults 5m, 2m)
      [--rest <file>] [--refresh --remote <name> --branch <name> --interval <duration>] [--ping-draft <file> --as <name>] [--gh-timeout <seconds>]
      [--correlate-max <n>] [--correlate-bytes <n>]                        (draft 7: defaults 300, 262144)
nova-wake probe --here [--quiet-load <x>]
```

**Two faces, one question: can work be handed over right now?** `--line` asks
it of another line; `--here` asks it of this bench. Neither decides **what to
do**: both measure and print, and the window decides on the numbers, which is
the whole of what the two hurts asked for. (Amended 2026-09-13, draft 2, from
the Fable read: *neither decides* sat beside an exit code that does decide.
`--line`'s exit answers the verb's one question and gates an assignment —
never the line, never the person, and it is `--line`'s alone: `--here` reports
numbers and exits 0 on any reading, gating nothing. The scoped rule is under
**Exit codes** and is stated there once.)

**Contact, not progress, not capacity — and contact is not eligibility.** The
last sign is contact: a receipt proves a harness ran a receipt and nothing
about a task (#178: *"A read receipt, wake delivery, task acceptance and
progress are not the same event"*). The probe reports `last=<stamp> silent=<d>
commit=<sha> contact=<FRESH|STALE|NONE>` and one state word, and **the two are
printed side by side and never merged** (amended 2026-09-13, draft 4, from the
Astra read, finding 3: draft 3 mapped fresh contact onto `PRESENT` and *any*
newer sign onto `ANSWERED`, so transport contact was the whole of scheduling
eligibility — a line that had said it was stopping read as present, and was
pinged against that choice a few minutes later). `contact=` is the transport
reading and nothing else: `FRESH` within `--silent-after`, `STALE` past it,
`NONE` where the lane carries no commit by that name at all. The state word is
the scheduling reading, and each state names the evidence it stands on:

| state | what was observed | what it claims | exit |
|---|---|---|---|
| `RESTING` | the line is named in `--rest` | it has declared it is not taking work; nothing is sent, whatever `contact=` reads | 1 |
| `ANSWERED` | a note or receipt **from the line, addressed to the caller**, landed after the ping was pushed | this probe was answered — once, and the record clears | 0 |
| `PRESENT` | `contact=FRESH`, no rest declaration stands, and **no ping record stands** (draft 5) | contact is fresh and nothing says otherwise; never that the line accepted work | 0 |
| `SILENT` | `contact=STALE` or `NONE`, no record stands, and nothing was sent — no `--ping-draft` given, or a send that did not push | the number is the report | 1 |
| `PINGED` | past `--silent-after`, one note for this silence known to have reached the bus — `pushed=true` returned to this call, or a reconcile that found the commit on the lane's fetched remote ref — and `--answer-within` has not yet run out | a ping is outstanding | 1 |
| `UNAVAILABLE` | `--answer-within` ran out after the ping with no answer | reason unknown | 1 |
| `UNRECONCILED` (draft 5) | an intent from an interrupted send stands and this call could not fetch the lane | whether the line was pinged is **not known**: nothing is claimed, nothing is sent, and the next call with `--refresh` decides | 1 |

`PRESENT` carries *and no ping record stands* since draft 5 (from the Fable
read, finding M2): draft 4's row asked only for `contact=FRESH` while the
paragraphs held a ping outstanding until it was answered or ran out, so one
observation had two right answers. A fresh sign that is not addressed to the
caller is not an answer (**An answer is a note from the line to the caller**),
so it does not end a ping — the record decides the word, and `contact=` is
printed beside it either way.

And the transitions. **This table is total**: every state has a cell for every
event, and a cell is either the state the probe reaches or `—`, which means
*this event cannot be observed in this state* and is accounted for beneath
(amended 2026-09-13, draft 5, from the Fable read, finding M2: draft 4 gave six
rows and closed with *nothing else moves a state*, which forbade transitions the
rest of this section requires).

| from ↓ · on → | rest | return | reconcile | no-fetch | fresh | stale | ping | answer | timeout |
|---|---|---|---|---|---|---|---|---|---|
| `PRESENT` | `RESTING` | — | `PINGED` or `SILENT` | `UNRECONCILED` | `PRESENT` | `SILENT` | `PINGED` | — | — |
| `SILENT` | `RESTING` | — | `PINGED` or `SILENT` | `UNRECONCILED` | `PRESENT` | `SILENT` | `PINGED` | — | — |
| `PINGED` | `RESTING` | — | — | — | `PINGED` | `PINGED` | — | `ANSWERED` | `UNAVAILABLE` |
| `ANSWERED` | `RESTING` | — | `PINGED` or `SILENT` | `UNRECONCILED` | `PRESENT` | `SILENT` | `PINGED` | — | — |
| `UNAVAILABLE` | `RESTING` | — | — | — | `PRESENT` | `UNAVAILABLE` | — | `ANSWERED` | `UNAVAILABLE` |
| `UNRECONCILED` | `RESTING` | — | `PINGED` or `SILENT` | `UNRECONCILED` | `UNRECONCILED` | `UNRECONCILED` | — | `ANSWERED` | — |
| `RESTING` | `RESTING` | `PRESENT`, `SILENT`, `PINGED` or `UNAVAILABLE`, by the record and `contact=` | — | `RESTING` | `RESTING` | `RESTING` | — | `RESTING` | `RESTING` |

The events, each one thing a probe can observe on one call: **rest**, an entry
for the line in `--rest`; **return**, the window taking that entry out after a
fresh explicit return from the line; **fresh**, a sign within `--silent-after`
that is not addressed to the caller; **stale**, no sign within `--silent-after`;
**ping**, a note sent and pushed this call (`pushed=true`); **reconcile**, an
intent from an interrupted send resolved against the fetched remote ref, either
way; **no-fetch**, an intent standing that this call cannot fetch; **answer**, a
note or receipt from the line addressed to the caller after the ping;
**timeout**, `--answer-within` running out with no answer.

Why each `—` cannot be observed, in one place: **return** needs an entry in
`--rest`, and only the `RESTING` row has one. **ping** needs a silence with no
record standing against this sign, so no row holding a record reaches it, and
nothing is sent from `RESTING` at all. **reconcile** and **no-fetch** need a
`sending` intent, which only an interrupted send leaves — so they are observed
in the three states a send is made from (`PRESENT`, `SILENT`, `ANSWERED`, each
across the interrupted call) and in `UNRECONCILED`, which leaves the intent
standing, and not in `PINGED`, `UNAVAILABLE` or `RESTING`, which hold a record
or no record but never an intent to resolve (amended draft 6, K5: draft 5 let
`PRESENT` reach `UNRECONCILED` on **no-fetch** while marking its **reconcile**
`—`, which is the same interrupted send read two ways). **A reconcile reaches
two states**, `PINGED` where the note is on the lane or a resend lands, and
`SILENT` where the resend did not push — draft 5's single `PINGED` cell promised
an outcome the send does not owe. **answer** and **timeout** need a record:
`PRESENT`, `SILENT` and `ANSWERED` (whose record cleared as it printed) hold
none, and a note from the line to a caller with no ping outstanding is the bus
source's news and not this probe's. **timeout** additionally needs a **complete**
correlation read (draft 6, K3): a partial read is not a timeout observation, so
it stays on the `PINGED`→`PINGED` cell. And `UNRECONCILED` cannot **timeout**,
because a window measured from a ping this call could not confirm has not
started.

Four cells were the finding, and each decides something (draft 5, M2):
`SILENT`→`PRESENT`, a line that starts signing again with no ping ever sent;
`PINGED`→`PINGED` on a fresh sign, which is *an unrelated commit is not an
answer* written into the table and not only into the paragraph;
`UNAVAILABLE`→`PRESENT`, the silence this ping measured ending in contact rather
than in an answer — the record is **retired**, one `WAKE NOTE probe <name>: the
silence this ping measured ended in a sign and not in an answer; the ping is
retired` is printed so the fact survives the clearing, and the next silence is a
new silence with a new ping; and `RESTING` entered and left with a record
standing — entering clears nothing, and the first probe after the entry is taken
out judges the record as it stands: within `--answer-within` of the ping,
`PINGED`; past it, `UNAVAILABLE`; against a sign the line has since moved past,
retired as above. In practice the fresh explicit return that ends a rest is
itself the newer sign, so the record retires and the word is `PRESENT`; the
other two are written down because a window may take an entry out for its own
reasons.

Where two events land on one call they are applied in the order of the columns,
so a rest wins over every reading and a reconcile happens before any send —
which is why `reconcile` and `no-fetch` now stand left of `ping` (draft 6, K5:
draft 5 wrote this sentence over a table whose `ping` column came first, so the
prose and the matrix ordered one call two ways). This
matrix is the whole of what moves a state, and **no clock leaves `RESTING`**: a
rest ends on the line's own return and the window's hand, never on a timer and
never by this tool (Grok, on #178: *"A deliberate stop of the poller must not be
restarted by a liveness probe."*).

**`--rest <file>` is where a declared rest lives, and this tool only reads it**
(draft 4, finding 3). One entry per line — `<line name> <note id> <stamp>`: the
name that rests, the id of the note **in which the line said so**, and when, so
the declaration is on the record and a person can open the note this tool does
not read. Blank lines and lines beginning `#` are skipped; a malformed entry is
exit 2 naming the file and the line number (**No guessed anything**); a file
that cannot be read is exit 2 and never an empty roll. A named line found there
is `RESTING` before anything else is decided — no draft is sent and no ping is
recorded — and `contact=` is still printed beside it, because a resting line
that is still committing is a true thing to see and is still not an assignment.
Without `--rest` no rest is known and none is invented: this tool never infers
a stop from a line's own words, which it does not read.

**An answer is a note or a receipt from the line to the caller, read from the
lane and never from the caller's cursor; an unrelated commit is not an answer**
(draft 4, finding 3; the cursor half is draft 5, from the Fable read, finding
M4). While a record stands — the `sending` intent or the `pinged` record, and
only then — the probe makes **one bounded, incremental, read-only correlation
read of its own** per poll, and it is **not an inbox call** (draft 7, K3). It
moves no cursor, writes no `OPEN`, writes nothing on the bus at all and starts
no `nova-bus`: it is `git`, read-only, over the same ref the reconcile reads —
the lane's **fetched** remote ref where this call fetched, the bus checkout's
head where it did not, and `head-at=` says which moment was read.

**What it walks.** The pinged line's lane is `from-<line>/`: one file per note
whose first lines are a `Key: value` header, and one append-only `RECEIPTS`
file whose every line is `<stamp> <target>` (SPEC.md, **nova-bus** — *The
cursor, the open list and the catalogue*, which is the bus's on-disk layout;
*The header*, which is the eight keys and their order; *The receipt rule*,
which is that line). This read uses that layout as **data**, exactly as a
person reading the bus in a browser does, and asserts nothing about it that
those sections do not already say. It walks the lane's commits **forward from
the ping's anchor** — the floor the `probe:<name>` record already carries — and
of each commit takes only what that commit added under `from-<line>/`:

- a **note file**: the **header only**. The read stops at the first blank line,
  never opens the body, and takes `From`, `To`, `Cc`, `Re`, `Id` and `Date` and
  nothing else. It is an answer when the header addresses the caller on `To` or
  `Cc` and its `From` resolves to the pinged line — the same condition draft 4
  wrote and draft 6 kept, with the caller's cursor nowhere in it;
- a line appended to **`from-<line>/RECEIPTS`**: an answer when its target is
  the ping's own id. A receipt is the one answer that is exact by construction,
  and reading the lane is what makes it visible at all.

`ANSWERED` still requires the note or receipt to be **from** the pinged line
and **addressed to the caller**. A cursor commit, a receipt to somebody else, a
note to a third line: none of them is an answer, and draft 3 took all three for
one. The anchor is the range's floor and **that is the anchor's whole job**
(draft 6, K2: it is a correlation range and never evidence that a note was
delivered).

**Why the lane and not the inbox** (draft 7, K3). A plain `inbox` lists a note
only while the caller's own cursor stands behind it, and once the cursor has
passed it only `--open --open-max <n>` lists it (measured, **How the checkout
receives mail**) — so draft 4's single plain read made `ANSWERED` depend on
where the caller's cursor happened to stand: a window reading its own mail with
`--advance` between two probes moved the reply onto its `OPEN` list, and the
next probe read `UNAVAILABLE` of a line that had answered. Draft 5 answered
that with the union of two listings and draft 6 tried to bound the second one,
and **the pair cannot be bounded from outside**: `nova-bus inbox` prints every
**NEW** note in full on every run, and `--open-max` caps only the carried list
(SPEC.md, *The cursor, the open list and the catalogue*), so the size of the
plain read is the bus's business and not this tool's — and the only way to cap
it from here is to truncate a friend's mail after asking for it, which is not a
bound, it is a capture and a cut. The lane read has no cursor in it, so there
is nothing to be on the wrong side of; it reads headers and not bodies; and its
size is this tool's own to bound before the bytes are read rather than after.

**The budget, and `correlation=partial` rather than a guess** (draft 7, K3;
the bounds, the gaps and item handling redrawn **draft 8**, K3a-c). One poll's
read is **the bounded read** named below, under **five** bounds and no others:

- `--correlate-max <n>` **items**, default **300** — the bound **State**
  already uses for its sighting memory. An **item** is one note header read or
  one appended `RECEIPTS` line;
- `--correlate-bytes <n>` bytes, default **262144**, counted as **the bytes
  this read consumes from the `git` adapter and decodes** — every byte handed
  back to this process by the object reader and every byte it inflates in its
  own buffers, summed over the poll;
- **4 KiB decoded per note file**, the depth one header is read to looking for
  the blank line that ends it — **a fixed cap, not a flag and not a share of
  `--correlate-bytes`** (draft 9, K4a). It does not move when the byte budget
  moves and no option in this spec raises it, so a header that does not end
  inside 4 KiB is unreadable by this probe **at every budget** (test: **the
  over-header-cap header is a permanent gap**);
- **a wall clock on the reading itself**: no single `git` process this read
  starts may run longer than **10s**, and the whole read may not run longer
  than `--interval` or 30s, whichever is smaller — the two-minute law applied
  to a read the same way `--gh-timeout` applies it to the forge;
- **one item of buffer at a time**: the read streams, holding at most one
  item's 4 KiB and one `RECEIPTS` line at once, so peak resident bytes are a
  constant and not a function of how deep the lane is.

**Those five are what this tool can enforce, and naming them is the point**
(draft 8, K3b). Draft 7 said the byte budget counted *every byte this read
takes out of the object store*, and no program driving `git` can promise that:
`git` inflates objects, reads pack indexes, may walk a whole delta chain and
may map more than it hands back, none of it visible to the caller. What can be
enforced is what crosses into this process — consumed and decoded bytes — and
this process's own time and its own buffers. **Physical I/O inside `git` is not
bounded here and is not claimed to be**, and a lane whose objects are
pathological is held instead by the item bound and the wall clock, which is why
the clock is one of the five and not an afterthought.

**An item that does not fit is not read** (draft 8, K3c). Draft 7's budget
could stop in the middle of a header or in the middle of a `RECEIPTS` line, and
a bookmark of `<lane commit>:<items taken inside it>` cannot describe half an
item. So item handling is **all-or-none**: before each item the read asks
whether that item's whole cost — up to 4 KiB for a header, its own length for a
receipt line — fits in what is left of both budgets. Where it does not, **the
item is not read at all**, the poll stops **before** it, and the bookmark names
**that item**, so the next poll begins by attempting it again. No item is ever
half-read, no bookmark ever points inside one, and **a poll never advances past
content it did not cover**.

**An item that cannot fit a whole budget, or cannot be read at all, is a
coverage gap** (draft 8, K3a; the first case split in two, **draft 9**, K4a and
K4b, because one of them a caller can clear and the other one cannot).
**Three** cases, one treatment:

- **a note file whose header has no blank line within 4 KiB** — the fixed
  header cap among the five bounds above. This gap is **permanent at every
  budget**: `--correlate-bytes` bounds the poll, the 4 KiB bounds the single
  header, and raising the first raises nothing about the second, so **no
  `--correlate-bytes` value a caller can pass ever reads this header** (draft
  9, K4a; test: **the over-header-cap header is a permanent gap**). It is the
  bus's own `INBOX UNREADABLE` case and not this probe's verdict, and the fix
  belongs where that note was written or on the bus, never on a flag here;
- **an item whose header does end within 4 KiB but whose whole cost is larger
  than the whole of `--correlate-bytes`**, so that no poll *at this budget* can
  take it. This gap is **the budget's and not the item's**: it stands only
  while the budget stands, and a `--correlate-bytes` raise that makes the item
  fit reads it on the next poll and **resolves** it (draft 9, K4b; test: **the
  within-cap header over the total budget clears on a raise**);
- **an object this read cannot get from `git` at all**: missing, corrupt, or a
  read that failed — named by its reason, never guessed past. Restoring the
  object resolves it and no budget change does (test: **an unreadable
  object**).

Each is **skipped, counted and named once** — one `WAKE NOTE probe <name>:
<lane commit>:<item> could not be read (<reason>); it is a coverage gap and no
unavailability is declared` — its position is **retained in the record** so a
later poll can come back to it, and the line carries `gaps=<n>`.

**A gap is a potential answer this read did not see**, and that is the whole of
why it is kept. The skipped item may be exactly the note that answers the ping.
So while any gap stands the correlation is **`partial`**, the state word stays
**`PINGED`** however far `--answer-within` has run, and **`UNAVAILABLE` is not
reachable at all**. **Which remedy applies depends on which of the three kinds
it is, and the line names it** (draft 9, K4a and K4b): a later poll with more
budget left, or an explicit `--correlate-bytes` raise, resolves a **budget**
gap, and where the resolved item is the answer that poll is `ANSWERED` (test:
**the within-cap header over the total budget clears on a raise**); a
**header-cap** gap and an **unreadable object** are not budget gaps and **no
`--correlate-bytes` raise resolves either** (tests: **the over-header-cap
header is a permanent gap**, **an unreadable object**). A caller must never be
told to raise a budget against a cap that is not a budget. A gap is never aged
out, never rounded away and never turned into a negative by the clock.

**`complete` is the tip *and* zero gaps**, both halves, and that is its whole
definition (draft 8, K3a). A read that reached the ref's tip with `gaps=0` is
`correlation=complete`; a read that reached the tip with a gap standing is
`correlation=partial remaining=0 gaps=<n>`, because **reaching the tip is not
covering the lane**. Only a **complete** read with no answer past
`--answer-within` is a **timeout**, and a timeout is the one path to
`UNAVAILABLE`. **No `UNAVAILABLE` without complete negative evidence**: nothing
incomplete — a budget that stopped short, a gap, a range that could not be
formed, a fetch that did not happen — is ever evidence *against* a friend.
Incomplete coverage is an unknown, and an unknown is `PINGED`.

**The retained gap list is bounded too.** A record carries at most **64** gap
positions. A poll that would record a sixty-fifth stops **before** that item
and leaves the bookmark there, so the rest stays behind the bookmark as
ordinary uncovered lane rather than growing the state file without limit: a
bound on a read that becomes an unbounded bound on the state is not a bound
(**Bounded output**, and Glenn's rule that a tool is tested at the largest
plausible state).

**Where a budget runs out before the lane's tip**, the poll prints and records:

- the line carries `correlation=partial`, `remaining=<n|->` and `gaps=<n>`;
- **`remaining=` counts items, never commits** (draft 8, K3b). Draft 7 made it
  "the number of lane commits between the position reached and the ref's tip",
  and a commit count is the wrong unit for an item budget: nine hundred notes
  added in **one** commit are `remaining=1` in commits and nine hundred items
  of work, so a caller reading `remaining=1` would expect one more poll and
  need three. `remaining=<n>` is **items** — note headers plus appended
  `RECEIPTS` lines — between the bookmark and the tip, the same unit
  `--correlate-max` spends;
- **and `remaining=-` where the exact count is itself past budget.** Counting
  what remains means walking the rest of the lane, which is the unbounded read
  this section exists to avoid — `git rev-list --count` scans history, and
  counting items means listing each commit's paths as well. So the count is
  attempted only inside a **counting allowance** of at most `--correlate-max`
  further commits, names and paths only, **no object opened**; where the
  remainder is larger than that allowance the field is **`remaining=-`**, which
  reads *more than this poll could count* and is never a number the tool did
  not measure (**No guessed anything**);
- one `WAKE NOTE probe <name>: this poll's correlation read covered <n> items
  to <sha>, <n|more than <n>> items remain, <n> gaps stand; the correlation is
  partial and no unavailability is declared` is printed;
- **the state word stays `PINGED`** however far `--answer-within` has run, and
  the record stands;
- the position reached is written into the record as its **seventh field** —
  `<lane commit>:<item offset within it>[;<gap>,...]`, the item the next poll
  attempts first followed by the gap positions still owed — so the **next**
  poll resumes there and not at the anchor.

**The order items are read in is fixed, so a bookmark means the same thing
twice** (draft 8, K3b). Commits are taken in the lane's first-parent order
forward from the anchor; within one commit the items are **the note files that
commit added under `from-<line>/`, by path, compared bytewise**, and then **the
lines that commit appended to `from-<line>/RECEIPTS`, in file order**. An item
offset is an index into that sequence. Without a fixed order a bookmark is a
number about one run on one machine, and a resume is a guess; with it, two
implementations, two platforms and two polls resume at the same item.

**A budget of one item still makes progress, and no poll ever spins** (draft 8,
K3c). With `--correlate-max 1` a poll reads exactly one item and the bookmark
moves by one; with a `--correlate-bytes` that fits one header, one header. The
two behaviours that would be bugs are written down so a test can fail them: a
poll must never **advance past an item it did not read**, and **a poll that
made zero progress while uncovered items or gaps remain may not say
`correlation=complete`** (draft 9, K4c; test: **a budget of one item never
spins**). A poll whose next item cannot fit any budget records it as a **gap**
and moves the bookmark past it, which is progress; a poll that takes no item
and records no gap **while uncovered content or a standing gap remains** is the
spin, and the test named below asserts against it.

**A poll that reads nothing because there is nothing left to read is complete,
and that is how a silent friend times out** (draft 9, K4c). Draft 8 wrote the
rule above without its qualifier, and unqualified it says a lane with nothing
in it can never be `complete` — which would hold a genuinely silent friend at
`PINGED` for ever and take `UNAVAILABLE`, the one verdict rule 17 asks this
verb for, off the board entirely. So two reads that take no item are **complete
negative evidence** and not spins:

- **an empty lane**: the pinged line's lane carries no item at all past the
  ping's anchor. Nothing is uncovered and no gap stands, so the read is
  `correlation=complete remaining=0 gaps=0` and `--answer-within` runs on it to
  `UNAVAILABLE` (test: **the empty lane times out**);
- **a covered lane whose tip has not moved**: an earlier poll reached the tip
  with `gaps=0`, and this poll finds the ref at the **same** tip with no new
  item since. Nothing was read because nothing arrived and the earlier
  coverage still stands, so this poll is `correlation=complete` too and
  `--answer-within` runs on it to `UNAVAILABLE` (test: **the drained lane at an
  unchanged tip times out**).

The distinction is **progress against what is owed**, never bytes read: a poll
that takes no item *while* an uncovered item or a standing gap remains is the
spin and may not say `complete`; a poll that takes no item *because* the lane
is covered to a tip that has not moved is the timeout this verb exists to
reach. `UNAVAILABLE` still requires the tip **and** zero gaps, so neither case
weakens **No `UNAVAILABLE` without complete negative evidence** — they are what
that sentence means when the lane is quiet.

So the read is bounded **per poll** and complete **over polls**: a lane nine
hundred notes deep is covered in three polls of the default budget rather than
in one long one, and the answer is found on the poll that reaches it. The
anchor stays in the record as the range's true floor — a person checking by
hand starts there — and the seventh field is only how far this tool has got.

An unanswered friend is **never** called `UNAVAILABLE` on a read that could not
have seen the answer. A probe that makes no correlation read at all prints
`correlation=-`, `remaining=-` and `gaps=-`. Where the recorded position is no
longer an ancestor of the ref — a bus whose history was rewritten — the read
restarts from the anchor and the retained gaps are dropped with the bookmark
that named them, because those positions no longer name anything and a gap
nobody can return to is not a gap but a claim; the restart itself is
`correlation=partial`. Where the **anchor** is not an ancestor either, no range
can be formed at all, and that is `correlation=partial`, `remaining=-` and one
`WAKE NOTE` naming the anchor, never an `UNAVAILABLE` by inference.

**The header this read parses is the bus's header, under the bus's own
semantics** (draft 8): the keys, their order and case, the blank line that ends
them, and the resolution of a `From`, `To` or `Cc` value to a line identity are
SPEC.md's, **nova-bus** — *The header*. This tool writes **no second header
grammar and no second identity rule**; where that section and this one could be
read two ways, that section wins, and a header this read cannot parse under
those semantics is a **coverage gap** by the rule above and never a note ruled
out. A parser that diverged here would make *addressed to the caller* mean one
thing in `nova-bus inbox` and another in this probe, and the two would disagree
about the same note on the same lane — which is a friend's availability decided
by whose parser ran.

**The bounded read — one primitive, and this file owns it** (draft 8). What is
specified above is not a paragraph about one verb; it is a primitive, and it
has a name. **The bounded read** is: *a read-only walk of a bus lane from a
floor commit forward, taking note headers and appended `RECEIPTS` lines and
never a body, under the five bounds above, with all-or-none item handling, the
fixed within-commit order, a resumable bookmark, retained coverage gaps — of
which the fixed-header-cap kind is permanent at every budget (draft 9, K4a) —
and `complete` only on the tip with zero gaps, withheld from a poll that made
no progress only while uncovered items or gaps remain (draft 9, K4c).* **SPEC-WAKE.md owns that
definition** and SPEC-BUS-REPLY.md (#267, head `dc679ab9`) **references it**
rather than restating it. It lives here for two reasons a reader can check:
this is the document whose tool reads `git` objects itself, while #267's read
half is by its own **no second read path** law an additive flag on `nova-bus
inbox` and reads through the bus's existing listing; and a bound is only real
where the bytes are consumed, which is here. At `dc679ab9` that document names
the read half but has not pinned it — so the reference is **owed by #267 and
not yet written in it**, and this paragraph is the definition it cites when it
is. Where the read half needs a bound this one does not have, the bound is
added **here** and both follow it. Two definitions of one primitive is how two
tools come to disagree about whether a lane was fully read, and that
disagreement lands on a friend's availability.

What this correlation proves is *this line wrote to me after my ping*; where
the answer is a receipt naming the ping's id, or a note whose `Re` names it,
the lane read has also seen *this answers that* and prints one `WAKE NOTE probe
<name>: <id> answers ping <ping id> by its <Re line|receipt>` (draft 7, K3:
reading headers on the lane shows the `Re` line an `inbox` listing does not).
It is the stronger reading and not the gate — a line that answers in a fresh
note is answering — so `ANSWERED` stays the addressed-and-from condition and
the residual below stays named. The ping's
id is printed on the `WAKE PING` line and carried on `WAKE PROBE` as
`pinged-id=`, `nova-bus receipt --note <that id>` from the line is a
correlation anybody can check by hand, and the residual — a note the line was
already writing to the caller as the ping landed — is named here rather than
discovered. **`pinged-id=` is never `-` once a ping has been prepared** (draft
6, K3). Draft 5 reconciled without ever reading an id back and printed
`pinged-id=-` on every reconciled record, so the one field a person could check
by hand was blank on exactly the calls that most needed checking, and what was
left to check was an anchor and a range of commits. The id is fixed by `nova-bus
prepare` before any send, it is in the intent, it is in the record, and it is
what the reconcile matches on the remote — so the hand check is the same check
on every path, and the ping's own clock remains this tool's stamp rather than a
`SEND OK commit=` stamp, which is rule 9's two clocks said out loud.

Exit 1 is Conventions' NO and it says *do not assign now*: `SILENT` and
`PINGED` are 1 as well as `UNAVAILABLE` because Glenn's rule holds new
assignments while the answer is awaited, and a scheduler that read 0 for
*pinged, waiting* would assign into the gap. `UNRECONCILED` is 1 for the
strongest form of that reason (draft 5): the call does not know whether a ping
is outstanding, and *do not assign now* is the only honest answer a tool that
has not reached the lane can give. `RESTING` is 1 for a different
reason and the difference is worth one sentence: the other five measure a
silence or its uncertainty, and this one relays a choice the line made and wrote down (draft 4). A `probe` that could not run —
a missing flag, a bus that is not a checkout, or, **under `--ping-draft`**, a
draft or a `--line` that `nova-bus send` refuses — is 2.

**This table reads no roster, and an unknown `--line` is a reading and not a
refusal** (amended 2026-09-13, draft 3, from the Opus read: draft 2's repair
deleted the roster read eight lines below this paragraph and left the exit
case here saying *a `--line` not on the roster*, which is where an implementer
reading the exit table would write the roster check straight back). Without
`--ping-draft` nothing is sent and no name is resolved, so a `--line` this
tool cannot find — a typo, a line that never joined — is exactly a name with
no commit on the bus lane: `last=- commit=-`, and the state word is
**`SILENT`**, exit 1. **No sign is the oldest sign there is**, and a misspelt
name must never read `PRESENT` and must never be worth an assignment. With
`--ping-draft` the same name reaches `nova-bus send`, which owns the roster,
refuses it by name, and the probe relays that refusal as `WAKE REFUSED`, exit
2 — the exit-2 case this paragraph names, and it is that flag's alone.

**The ping is the caller's note, sent once.** `--ping-draft <file>` is a bus
draft in `nova-bus draft`'s shape, `To:` the line, written by the caller: this
tool composes nothing, so what a ping says is a person's words and the roster
decides whether the name resolves. **The roster is `nova-bus`'s file and this
tool never reads it** (amended 2026-09-13, draft 2, from the Fable read: draft
1 made `probe` a second reader of a file `nova-bus` owns, against *"no opinion
about what a bus is beyond what those two programs tell it"*). A name is
resolved by the program that owns the roster: `nova-bus send` refuses a name
it does not know and the probe relays that refusal as `WAKE REFUSED`, exit 2,
with the bus's own reason. So the name check happens where the ping happens,
and **`--as` is required with `--ping-draft`, and on any call that finds a
`probe:<name>` record standing** (draft 6, K6; draft 5 required it only with the
flag). Without a draft there is nothing to send and nothing to sign, and a probe
with no record standing is a read of the checkout's commits (rule 2) that needs
no roster at all — but a standing record was made *for* a caller, the
correlation read asks whether a note or receipt on the lane is addressed to
**that** caller (draft 7, K3: the question is the same one draft 6 put to
`nova-bus inbox --as <caller>`, asked now of the lane's own headers), and a
record read without that caller is a record read blind: such a call is
`WAKE REFUSED`, exit 2,
naming the key and the flag, and it sends nothing and writes nothing. Test
19's *a `--line` not on the roster is exit 2* is scoped to the `--ping-draft`
call for the same reason.

**A record is bound to its transport, and a mismatch is refused rather than
acted on** (draft 6, K6). A name is not a transport: draft 5's `probe:<name>`
named a line and nothing else, so a state file copied to a second checkout,
pointed at a second remote or branch, or run under a second caller presented
another transport's ping as this one's — a `PINGED` or an `UNAVAILABLE` about a
note that was never sent on this lane at all. So every record carries `<scope>`
(**State**), over the resolved bus path, `--remote`, `--branch`, `--as` and
`--line`; a call whose own scope differs from a standing record's is `WAKE
REFUSED`, exit 2, on one line naming the key, both scopes and the fix — a state
file of its own — and it sends nothing, clears nothing and writes nothing.
Refusing is the answer rather than re-scoping or overwriting, because the one
thing this tool cannot do is decide which of two transports a standing ping
belonged to.

**The ping is prepared before it is sent, and its identity is fixed there**
(draft 6, K1). Under `--ping-draft` the probe first runs `nova-bus prepare --bus
<dir> --as <caller> --file <file>`, which is the bus's own operation for exactly
this class: it uses the existing participant, recipient and draft validation,
computes the existing deterministic note id, assigns `Date` once, **performs no
network, no Git write, no checkout write, no index update and no delivery**, and
prints one artifact carrying `id`, `path`, `note` and `sha256`
(**SPEC-BUS-DELIVERY**, *Two operations, one identity*). The probe saves that
artifact whole beside the state file at `<state>.probe.<name>.prepared`, through
the same temporary-file-and-atomic-rename the state itself uses, **before**
anything is sent — the bus's own instruction is *do not prepare again while
pending; retry the saved artifact*, and this tool obeys it. A `prepare` that
fails wrote no bus state and sent nothing: it is relayed as `WAKE REFUSED`, exit
2, with the bus's own reason, and no record is made.

**The prepared note's own recipient is compared with `--line` before any send**
(draft 6, K6). The artifact carries the complete rendered note, so the `To:` the
roster resolved is readable without this tool reading the roster — the rule
above is untouched. Where it does not resolve to `--line` the probe refuses,
`WAKE REFUSED`, exit 2, naming both, rather than sending a person's words to one
friend and recording a delivered probe against another. Draft 5 had no such
check anywhere: the draft went straight to `send`, and a draft addressed to
somebody else became this line's ping.

The send is then `nova-bus send --bus <dir> --remote <name> --branch <name>
--as <name> --prepared <artifact>` — `probe`'s one write to a bus, only under
this flag, only for a line past `--silent-after` with no ping recorded against
its current sign — and its result is one `WAKE PING id=<id> to=<name>
commit=<sha> pushed=<true|false> attempt=<n>` line; a first send for a silence
is `attempt=1`, and `id=` is the **prepared** id on every attempt (draft 6:
draft 5 sent `--file` and printed whatever id that call invented, which is the
whole of K1). `pushed=false` is printed and is not a ping: *"a bus send alone
does not wake a stopped harness"*, and a send that did not land did not even
reach the
bus. **The state records `probe:<name>` = `<sign sha>|pinged|<stamp>|<note
id>|<anchor>|<scope>|<read>` after `SEND OK pushed=true` returned to a living
call, **or** after a reconcile that found the prepared note id on the lane's
fetched remote ref — and never otherwise** (amended 2026-09-13, draft 3, from
the Fable read; the fields are draft 6's and draft 7's; the reconcile half is
**draft 7**, which admits in this sentence the second path the reconcile
paragraphs below have described since draft 4 — a kill between a pushed send
and the state write leaves `sending`, and the fetch that finds the id is the
same evidence the send's own `pushed=true` was, arriving one call later).
Draft 2
wrote *after `SEND OK` and never
before* two lines under *`pushed=false` … is not a ping*, and SPEC.md's
grammar is `SEND OK id=<id> path=<path> commit=<sha> pushed=<true|false>` — a
note committed to the checkout and not pushed is a `SEND OK` — so one reading
of draft 2 recorded a ping no friend could see, held the record against the
sign so no later probe would send again, and let `UNAVAILABLE` follow
`--answer-within` later, gating an assignment on a note that never left this
bench. So: a `SEND OK pushed=false` is a note in this checkout and nothing
more. Nothing is recorded, the state word is `SILENT` and not `PINGED`, the
line still prints so the failure is visible, and **the next probe offers the
same prepared artifact again**.

**A send is intended before it is made, one identity is offered to the bus, and
the only evidence that it arrived is that identity on the lane's fetched remote
ref** (the intent is draft 4, from the Astra read, finding 5; the fetched ref is
draft 5, from the Fable read, H1; the one identity is **draft 6**, K1 and K2,
from the Astra read at fb00295e).

Draft 3 answered a kill between a pushed send and the state write by sending
again, which is a second ping for one silence. Draft 4 gave the ping a durable
intent and an anchor and reconciled it **against the bus checkout**, which
carries a send's commit before the push does. Draft 5 moved the evidence to the
lane's **fetched** remote ref, which is right, and then asked it the wrong
question — *does it carry a commit by this caller newer than the anchor?* Two
more defects came out of that one question:

- a fresh `nova-bus send --file` assigns a **new** note id per call, so draft 5's
  resend after an interrupted attempt was a **second note** and not a retry of
  the first; and the bus's push deliberately carries an earlier unpushed local
  commit along with the new one, so both notes land together and the friend is
  pinged twice for the one silence a guarantee promised once (K1);
- *a commit by the caller past an anchor* is not the ping. Any other note the
  caller sent in that gap reads the same, so a kill before the ping was even
  constructed, followed by one unrelated note, reconciled as **delivered** — a
  `PINGED` and then an `UNAVAILABLE` standing on a ping nobody ever sent (K2).

Two defects, one cause: the tool was inventing a delivery protocol beside the
bus's own. So draft 6 deletes the **anchor-commit reconcile** — the category and
not the sentence, as draft 5 deleted the local-state reconcile before it — and
uses the mechanism `nova-bus` already has for exactly this failure: **one
prepared identity, offered until it lands**. The anchor stays, with its other
job: it is the floor of the answer-correlation range and nothing more.

**The one evidence is that note's id on the fetched remote ref.** A ping reached
the bus when a note with the **prepared id** is on the lane's remote ref as this
call has just fetched it: `git fetch <remote> <branch>`, then the bus's own
already-published check over `<remote>/<branch>` — the note at the prepared
path, with the prepared id, whose bytes match the artifact's `sha256`
(**SPEC-BUS-DELIVERY**, step 1; a same-id different-content note, an unsafe path
or an inconsistent INDEX is a refusal there, and this tool relays it as one and
preserves the evidence). Nothing else counts — not the checkout's head, not an
unpushed commit, not another note by the same caller however recent — and a
reconcile therefore costs a fetch or does not happen.

- **Before** the send, `probe:<name>` = `<sign sha>|sending|<stamp>|<note
  id>|<anchor>|<scope>`, written after the artifact is saved and before the send
  is started. The note id is the prepared one, so the intent names **what** was
  being sent and not merely that something was (draft 6, K3); the anchor is the
  checkout's head before the send and is the correlation range's floor. A kill
  anywhere after this leaves the send **known to be uncertain, and known by
  name**, which is the whole of what the intent buys.
- After the send returns `SEND OK pushed=true` the record becomes `<sign
  sha>|pinged|<stamp>|<note id>|<anchor>|<scope>`: the same six fields with the
  phase word moved, so nothing a later call needs is ever dropped by a
  transition (draft 6, K3). A `SEND OK pushed=false`, a refusal, or a `send` that
  exits non-zero clears the intent: nothing is recorded, the word is `SILENT`,
  the artifact is kept, and the next probe offers it again.
- **A probe that finds `sending` reconciles before it does anything else, and
  the reconcile is a fetch.** Under `--refresh` it fetches the lane and asks one
  question of the fetched remote ref: **is the note with the recorded id there?**
  **It is** — the ping reached the bus: the record becomes `pinged` carrying the
  same six fields, the line carries `reconciled=true` and `pinged-id=<that id>`,
  and **nothing is sent**. **It is not** — that note has not reached the bus,
  whatever this checkout holds and whatever else this caller has pushed: one
  `WAKE NOTE probe <name>: a ping was interrupted and note <id> is not on the
  lane's remote ref; sending the same prepared note again`, and `nova-bus send
  --prepared` is run over the saved artifact, `attempt=2` on the `WAKE PING`
  line.
- **A resend is the same note, which is why it is not a second ping** (draft 6,
  K1; draft 5 asserted this and could not keep it). `send --prepared` is the
  bus's own retry and confirmation: it re-checks the remote for that id and
  returns `already-published` without writing a note, a commit or a push; it
  completes an exactly-matching partial local write and **reuses an existing
  pending commit** of that same note rather than making a second one; and it
  refuses unrelated local commits ahead of the named remote rather than
  publishing them (**SPEC-BUS-DELIVERY**, steps 1 to 4). So the unpushed commit
  an interrupted attempt left behind is **this note's own**, and the bus carrying
  it along on the next push is what delivers the one identity, not what
  duplicates it. `--answer-within` is measured from the attempt whose
  `pushed=true` landed, `attempt=` counts what this bench tried and not what a
  mailbox received, and no count of pings for a silence is incremented by a
  resend.
- **A resend that does not land leaves no ping** (draft 6, K5). A negative
  reconcile followed by a `pushed=false`, a refusal or a non-zero exit clears the
  intent, the word is `SILENT`, the artifact is kept, and the next probe offers
  the same identity again — the `reconcile` column's second outcome, which draft
  5's matrix did not have and its prose required.
- **With no network there is no answer, and the tool says that instead of
  guessing.** Without `--refresh` — or with a fetch that fails — an intent
  cannot be reconciled at all, so the state word is **`UNRECONCILED`**, exit 1,
  the line carries `reconciled=false`, and one `WAKE NOTE probe <name>: a ping
  was interrupted and this call cannot reach the lane's remote ref; run again
  with --refresh` is printed. **Nothing is sent**, the record and the artifact
  are left exactly as they stand, no `--answer-within` window opens, and the tool
  claims neither `pinged` nor `SILENT`. Draft 4 sent again in this case, which is
  how it produced the *two pings* its own guarantee then had to carve an
  exception for; draft 5 neither sent nor claimed, and draft 6 keeps that
  unchanged.
- **What the reconcile proves, exactly** (draft 6, K2): that the note whose id
  this tool fixed before sending is on the lane, or that it is not. It is an
  identity match and no longer an inference from recency or authorship, so draft
  5's own residual — *another note the caller sent in the gap reads the same* —
  is **gone** rather than printed beside the answer. `reconciled=true` stays on
  the line because a person should see which path a record took, and the exit
  stays 1 either way. The residual that remains is the one named above the
  matrix, and it is about the answer and not the ping: a note the line was
  already writing to the caller as the ping landed satisfies the correlation, and
  `pinged-id=` is what a person checks it against by hand.
- **The guarantee, in one sentence a test can falsify**: for one silence this
  tool ever offers the bus exactly one note identity — the id `nova-bus prepare`
  fixed before the first send — and it offers that identity again only on a call
  whose own fetch found no note with that id on the lane's remote ref, so the
  lane never carries two notes for one silence and no `PINGED` or `UNAVAILABLE`
  ever stands on a note the lane does not carry. Test 19 drives the three kill
  points of the prepared path — **after `prepare` and before the send**, **after
  the send's local commit and before its push**, and **after the push and before
  the intent is replaced** — and asserts at each the notes **on the remote**,
  their ids, and the state word.

**The bus is the channel because it is the one every line has consented to by
being on the roster**; a line whose harness needs more than a note to wake (a
`serve`, a webhook, a cron) has that adapter at its end, and this tool does not
know or run it (#178: *"Configure participants and transport adapters"* — the
adapters are theirs). Without `--ping-draft` the probe is a measurement only.

**A probe never writes a `--state` another run owns, and never writes one at
all unless a send was intended** (amended 2026-09-13, draft 2, from the Opus
read; *unless it pinged* until draft 5, from the Fable read, finding L10 —
draft 4 put a write before the send and left this heading saying the opposite).
Draft 1 gave `probe --line` a `--state` and a `probe:<name>` write and said
nothing about the lock, while **The races** closes exactly this for `watch`
(*"the later write erases everything the earlier one learned"*) and `--here`
is explicit that it takes no state and no lock — the silence for `--line` was
the loudest kind. So, stated: `probe --line` takes the same exclusive
`<state>.lock` beside the file, for the duration of its call and by the same
primitive `watch` uses, and a probe over a state file a `watch` (or another
probe) holds is `WAKE REFUSED`, exit 2, on one line naming the holder's pid,
the lock path and the fix — a state file of its own. A probe therefore never
shares a map with a live watcher and never writes over one: the only key it
ever writes is `probe:<name>`, and it is written in exactly these **six**
places, counted rather than asserted (draft 5, finding L10: draft 4 said
*exactly three* with a fourth two paragraphs above it; the sixth is draft 7,
K3) — **before a send**, the durable
intent with the prepared note id and the anchor (draft 4; the id is draft 6);
**after `SEND OK pushed=true`**, the ping recorded against the sign it was made
on (draft 3); **on a send that did not
land**, the intent cleared (a `pushed=false`, a refusal, a non-zero exit);
**on `ANSWERED`**, the record cleared, once; and **on a retired ping**, where a
fresh sign ended the silence an unanswered ping measured (draft 5, the
`UNAVAILABLE`→`PRESENT` cell); and **on a correlation read that stopped short
of the lane's tip or left a coverage gap behind it**, the `<read>` field of a
record that already stands — its bookmark and its retained gap positions, which
are one field and one write (draft 8, K3a) — and **nothing else** — not the phase word, not the sign, the stamp, the note id,
the anchor or the scope, and never a record created or cleared (draft 7, K3).
That sixth write is what makes the bounded read incremental instead of a read
that starts again from the anchor every poll and so never finishes a deep lane;
it is a bookmark in a record already written and not a new claim about the
line, which is why it is allowed where a measurement is not. A probe that did
none of those six — `PRESENT`, `RESTING` (draft 4), `SILENT` with no send
attempted, a `PINGED` whose read reached the tip and sent nothing, an
`UNRECONCILED` that leaves the intent exactly as it found it (draft 5), and
`--here`, which takes no state and no lock at all — writes **nothing**, and
leaves the file's bytes as it found them. A measurement is not a state
change. The prepared artifact beside the file is
written **once**, before the first send of a silence, and is deleted with the
record it belongs to — on `ANSWERED`, on a retired ping, and on a cleared intent
(draft 6): it is this tool's own file by the rule this section already states,
and a person who wants the note's words has `pinged-id=` and the bus.

**Blocking, or one-shot.** With `--refresh --remote --branch --interval`, a
probe that has pinged blocks for the rest of `--answer-within`, fetching each
`--interval` through `nova-bus wait --timeout <interval>` exactly as `watch
--refresh` does (the checkout fast-forwards; nothing advances) and making the
correlation read again after each, and it returns **`ANSWERED` the poll a note
or receipt from the line addressed to the caller appears on that read** or
`UNAVAILABLE` at the window's end — one turn, one answer. `UNAVAILABLE` there
is still owed a **complete** correlation — the tip **and** `gaps=0` (draft 8,
K3a) — (draft 7, K3): a window that ends on a read still short of the lane's
tip, or with a gap standing however far it read, returns `PINGED` with
`correlation=partial`, `remaining=<n|->` and `gaps=<n>`, and because each `--interval` resumes the read where the
last one stopped, a deep lane is covered across the polls that call already
makes rather than in one unbounded read at the end. Draft 3's *returns
`ANSWERED` the poll a new sign appears* stood here until draft 5 (from the
Fable read, finding M3): it is the exact defect finding 3 closed, left standing
in the one paragraph that decides when the blocking call returns, and a fresh
sign that is not addressed to the caller returns nothing — the probe keeps
waiting, because an unrelated commit is not an answer. Without `--refresh`
the probe reads the checkout as it stands, says so with `head-at=` on its line
and `WAKE NOTE probe reads the checkout as it stands; nothing fetches without
--refresh`, returns at once with `PINGED` — or, where an intent from an
interrupted send stands, with `UNRECONCILED` and nothing sent (draft 5, H1) —
and the **next** probe judges the
window from the recorded stamp: the window is measured by the tool's clock
(rule 9) across calls, never by how many times it was asked. `--answer-within`
is bounded by the same 60-minute ceiling as `--max`, for the same reason.

**`--silent-after` defaults to 5m and `--answer-within` to 2m** (amended
2026-09-13, draft 2, from the Opus read). Rule 3 states the test a duration
must pass to get a default here — *a duration gets a default only when a
different number per window would be a bug* — and these two pass it exactly as
`--offline-after`'s ten minutes does: they are Glenn's five-minute
availability rule of 2026-09-12 and the family's answer window, relayed as the
family's rule (*"If we don't hear from a friend for 5 minutes, we should try
to ping them"*), and two windows disagreeing about when a friend is silent is
the bug the rule was given to prevent. Draft 1 left them undefaulted, which
made the family's number a per-caller guess. **The 2m has the same source as
the 5m** (amended 2026-09-13, draft 3, from the Fable read, which found the
5m quoted and the 2m asserted with nothing behind it): the rule's second half
— *"If that doesn't work, then they have probably gone to sleep"* — names a
wait between the ping and the conclusion and does not measure it, and two
windows disagreeing about that wait reassign a friend's work at different
moments, which is the same bug the 5m was given to prevent. So the family's
answer window is the family's number, from Glenn's rule of 2026-09-12, and it
is stated as the family's rather than quoted as his. They join
`--offline-after` as the **durations** with a default; `--max-lines` is a line
cap and `--gh-timeout` a timeout, and the three of them together with these
two are the five **No guessed anything** names (draft 3, from the Fable read:
draft 2 called a line cap a duration). Every other duration on this tool still
has none, and a caller who means a different window still says so.

**What the probe will not infer.** It never writes a cause. It never pings a
line whose sign is fresh, whatever the caller believes. It never pings a line
named in `--rest`, whatever its sign reads (draft 4). It pings **once** per
silence under the guarantee stated above — **one prepared note identity**, a
durable intent that names it, an anchor that bounds the answer read, and a
reconcile that asks the lane's fetched remote ref for that exact id before any
second send (draft 6, K1 and K2) — and where it cannot fetch it neither sends
nor claims, which is `UNRECONCILED`
(draft 5, H1; draft 4 promised one ping *wherever the reconcile can read the
lane* and sent a second one wherever it could not, and draft 3 promised *never
twice* beside a rule that deliberately sent again). **It never invents a
delivery protocol beside the bus's own** (draft 6): the identity is `nova-bus
prepare`'s, the retry is `nova-bus send --prepared`'s, and this tool contributes
the notebook and the fetch. **It never reads this bench to decide
whether an interrupted note reached the bus** (draft 5): a `pushed=true`
returned to a living call is the remote's answer and is taken as one, but after
a kill the checkout's own commits are this bench talking to itself, and the
lane's fetched remote ref carrying **that note's id** is the only thing it will
take for delivery. It never counts a note it did not prepare as this probe's
ping (draft 6, K2 and K6). It never reads the process table of this or
any bench to
decide whether a line is running (*absence of a process is not absence of a
session*, 2026-08-25). It never receipts, never replies, never reassigns:
Glenn's rule continues *reconcile ownership before transferring*, and that is
a person's act on the record, not a tool's on a timer.

**`--here` reads three numbers and prints them.** Load averages from the OS
(`sysinfo` on Linux, `vm.loadavg` by `sysctl` on Darwin, and on a platform
with neither the line says `load=-`), the logical CPU count so the load can be
read against it, and the number of processes.

**The process count is a number the kernel already has, and no listing of any
kind** (amended 2026-09-13, draft 2; both cold reads, finding 3/4). Draft 1
said the count came from *"`/proc` entries, `kern.proc.all`"*, which is a
**listing** — enumerating `/proc` is reading the process table entry by entry —
and test 4's source tripwire forbids the string `/proc` in this package
outright, so the code rule 18 demands could not be written without turning
test 4 red. The repair is the mechanism, not an exemption: a tripwire with a
hole in it is not a tripwire. So `here.go` asks for a **count and never a
list**:

- **Linux:** `sysinfo(2)` returns `procs` beside the load averages — the same
  call that already answers `load=`, one struct, no path under `/proc` opened
  and no directory read. It is the kernel's `nr_threads`, a **task** count and
  not a process count, so a Linux `procs=` and a Darwin `procs=` are different
  numbers about the same bench and must not be compared across platforms
  (amended 2026-09-13, draft 3, from the Fable read). Each is a load number
  read against its own `cpus=`, which is all rule 18 asks of it.
- **Darwin:** `sysctl` for `kern.proc.all` with a **nil buffer** returns the
  byte length the answer would need and nothing else; the count is that
  length divided by `sizeof(kinfo_proc)`. **Both halves are named here because
  a test cannot be written from words that leave them unstated** (amended
  2026-09-13, draft 3, from the Fable read): `sizeof(kinfo_proc)` is in no
  standard-library type — it is **648 on 64-bit Darwin** and is a named
  constant in `here.go`, beside the mib `kern.proc.all` is reached by
  (`1.14.0`, through `SYS___SYSCTL`) — and the kernel's answer to a nil buffer
  is an **estimate** carrying room for processes that may start before the
  real read, so the count may run a little high. That is the reading and not a
  defect: `procs=` is a load number, the estimate errs in the direction of
  *busier*, and a window deciding whether this bench is quiet is better served
  by the high number than the low one. No entry is fetched, so no pid, no
  name and no command line is ever in this process's memory.
- **A platform with neither** prints `procs=-`, as `load=-` already does, and
  is a reading rather than a refusal.

Rule 4 is untouched and is not widened: the tool still never *lists*
processes, never reads a pid, a name or a command line, never filters by name,
never starts `ps` or `uptime`, and test 4's tripwire keeps the literal `/proc`
and `"ps"` bans over `internal/wake` and `cmd/nova-wake` with **no file
exempted** — `here.go` included, which is what makes it a check of the
mechanism above. Test 19's tripwire adds `exec.Command` over `here.go`.

**The count is a load number and names nobody** (amended 2026-09-13, draft 2,
from the Opus read): `procs=` says how busy this bench is and cannot say
*whose* work it is, so a window that finds the bench loud learns that it is
loud and must name the noise itself, from its own record of what it started.
Naming stays the window's, which is the other half of Mercury's correction —
the receipt is a promise about the next ten minutes, and only the window knows
what it is about to run.

`--here` takes no state, no
lock and no flags but `--quiet-load`, and exits 0 on a reading; a reading it
cannot take is 2 and says which. The receipt a window sends with these numbers
is *"a promise about the next ten minutes, not a report about the last one"*
— so the tool's line carries `at=` and the window quotes it, and a READY formed
from a plan has nothing to quote.
## Tests this spec demands

One test per rule above, named for the rule, beside the tests the work list names.
Each is proven able to fail by a mutation before it is trusted.

**The numbering is a rule's up to 14 and a source's after it** (amended
2026-09-13, draft 2, from the Opus read, which found *one test per rule, named
for the rule* untrue of the six the amendment added). Tests 1 to 14 are rules
1 to 14. Then: **test 15** pins the `--run` source and **test 16** the `--ref`
source, neither of which is a rule of its own — they are the sources rule 13
demanded; **test 17** pins rule 16 (the lock probe never holds); **test 18**
pins rule 15 (a stop is a verdict); **test 19** pins rules 17 and 18 (`probe
--line` and `probe --here`). No rule is left without a test and no test is
left without what it pins.

1. `TestAWatchNamesItsDeadlineAndItsDefault`: no `--max` is exit 2 naming
   `--max`; no `--on-deadline` is exit 2 naming `--on-deadline`; both missing is
   one run naming both; a watch whose sources never change ends at `--max` with
   exactly one `WAKE QUIET` line carrying `default=<word>` and the tail `deadline,
   default taken`, exit 0. The clock is injected.
2. `TestTenMinutesSilentIsOfflineOnce`: a bus checkout whose last commit by one
   line is eleven minutes old prints one `WAKE LINE ... state=OFFLINE` with that
   commit's stamp and sha; a second poll with nothing changed prints nothing for
   that line and is not a change; a new commit by the line prints `state=BACK`
   once; nine minutes is not offline; **a name no commit on the branch
   authors** prints the `this view reads a git author and not a bus sender`
   note exactly once beside `last=- commit=-`, a second poll prints it no
   second time, and a mutation that drops the note turns the test red (draft
   4, finding 4).
3. `TestTheEntryIntervalIsTheRunLength`: with `--interval 5s --entry-interval 8m`
   over an injected sixteen-minute clock the bus is polled 192 times and the entry
   twice; `--interval` missing is exit 2; `--entry` without `--entry-interval` is
   exit 2.
4. `TestASecondWatcherOnOneStateFileRefusesOnOneLine`: two watches over one
   `--state`; the second prints one `WAKE REFUSED` line naming the pid and the
   path, exit 2; the source tripwire finds no `pgrep`, no `ps`, no `/proc`, no
   `os.TempDir` and no literal `/tmp` in the package — **with no file exempted,
   `here.go` included** (amended 2026-09-13, draft 2; both cold reads: the
   process count is `sysinfo`'s `procs` and a nil-buffer `kern.proc.all`
   length, so the code rule 18 demands passes this tripwire as written, and a
   mutation that reads the count by walking `/proc` turns this test red).
   Amended 2026-09-13, draft 2: a `probe --line` over a `--state` a `watch`
   holds is one `WAKE REFUSED` line naming the holder's pid and the path, exit
   2, and a `probe` that pinged nothing leaves the state file's bytes
   unchanged.
5. `TestWakeOutputIsBoundedAtTheLargestPlausibleState`: 200 notes, 50 entries,
   100 reports, 20 lines and — amended 2026-09-13, draft 2 — **50 pull
   requests, 50 heads, 50 branches and 50 locks** changing in one poll (50 and
   not 20, so every new kind is above the 40-line default cap and its `WAKE
   MORE` line is actually reached; at 20 the new kinds never produced one and
   the test could not fail on them) print at most `8 * --max-lines + 17` lines
   plus the three named variable terms of rule 5 — at most four run-wide `WAKE
   NOTE` lines, one capped-note per `--owned-prs` repository, one `WAKE NOTE
   line <name>` per `--line` name that authors no commit (draft 5, finding M7),
   and one `WAKE
   POLL` per failed source per poll — measured in lines and bytes on stdout plus stderr, and no printed
   line contains a note's body or a report's contents; 20 `WAKE LINE` changes
   under `--max-lines 5` print five and one `WAKE MORE kind=line … n=15`, and a
   mutation that leaves `line` uncapped turns the test red; the same with
   `--advance-cursor` on, where the bus prints `--max-lines` notes and one `WAKE
   MORE kind=bus ... n=160`, the state holds 160 queue records, the cursor has
   not moved, four further calls drain them in order and the cursor moves on
   the fifth, after its print, with `nova-bus` asserted to have received
   `--advance` exactly once; each of the first four calls prints `WAKE NOTE
   bus advance deferred` once; a backlog of 3,000 notes leaves 3,000 queue
   records after the first call with none evicted and `bus:note:` marks of
   300 older delivered notes evicted around them; a mutation that lets the
   LRU take a pending entry turns the test red; a bus refusing for twelve
   polls prints at most `--max-lines` bus lines on each poll.
6. `TestWhatWokeYouIsNamed`: every `WAKE BUS`, `WAKE ENTRY`, `WAKE REPORT`,
   `WAKE LINE` and — amended 2026-09-13, draft 2 — every `WAKE PR`, `WAKE RUN`,
   `WAKE BRANCH` and `WAKE LOCK` line in a mixed run carries its identity
   field (the pull request's `<owner/repo>#<n>`, the head's
   `<owner/repo>@<sha>`, the branch's `<owner/repo>:<name>`, the lock's
   `path=`), and `something
   changed` appears nowhere on stdout or stderr.
7. `TestEveryBusLineIsClassifiedAndCounted`: a bus transcript of twelve lines, one
   of them `INBOX REFUSED`, yields `WAKE SOURCE bus read=12` with the three counts
   summing to twelve and the `REFUSED` line relayed verbatim; a mutation that
   drops one line turns the test red.
8. `TestThreeFailedPollsEndTheWatchLoudly`: a bus that exits 1 three times in a
   row ends the watch with `WAKE BROKEN source=bus failures=3`, exit 2; two
   failures then a success is two `WAKE POLL` lines, `fail:bus` gone from the
   state, and the watch goes on to its deadline; three failed polls spread over
   **three separate watch calls** with three different reasons — each call
   returning on the changed reason — end the third call `WAKE BROKEN
   failures=3` with `since=` the first call's stamp; one unreadable entry
   beside a readable one under `--final-only` wakes the call and prints the
   unreadable line. Amended 2026-09-13, draft 2 (both cold reads): a watch
   whose **only** source is `--pr`, with a `gh` that cannot run, ends its third
   poll `WAKE BROKEN source=prs failures=3`, exit 2, and **never** `WAKE
   QUIET` — the same for a `--run`-only, a `--ref`-only and a `--lock`-only
   watch, each naming its own source; a `--lock` path that is merely `absent`
   is a state and clears the streak; and a mutation that leaves any of the four
   new sources out of the streak turns the test red by ending the call quiet.
9. `TestTheToolStampsAndATypedTimeIsData`: with an injected clock, the opening
   `WAKE` line's `at=` and `WAKE BROKEN`'s `since=` equal the clock and not the
   wall; a bus note whose subject and body carry a time two hours ahead is
   ordered by its commit stamp; a `--line` whose newest commit's subject carries
   a future date is judged by the commit stamp; the source tripwire finds no
   flag named `--at`, `--stamp` or `--now`, and no time parse over a note's body
   or a report's text.
10. `TestServeSurfacesTheUncertainAndWakesOnlyTo`: a fixture bus with two
    notes `To:` the name, one `Cc:` the name only, and one for another name;
    `serve` with a fake `--on-note` that records its arguments runs once with
    the two `To:` ids in bus order, never the `Cc:` id and never the third,
    the `Cc:` note is recorded `cc` and the exit line says `cc=1`; killed
    with an injected kill point **after** `dispatching` is written and
    **before** the spawn, the restart runs nothing, prints `WAKE UNCERTAIN`
    naming the id and the `--redeliver` command, and the fake records zero
    further calls; `--redeliver <id>` runs it once as `attempt=2
    redelivered=1` and is `WAKE REFUSED` for an id that is not `uncertain`;
    killed **after** the spawn and **before** `delivered` the same; with
    `--on-note-idempotent` the same two kills each run it once more,
    `redelivered=1`, and a second interruption is `uncertain`; killed after
    `delivered`, the restart fires zero more; three notes landing while the
    command runs are `queued` and fired in **one** invocation carrying three
    ids after it returns, never beside it, and `--batch-max 2` fires two then
    one; with `--receipt` a receipt is sent for every id in a batch only
    after `delivered rc=0`, and a command exiting 3 gets `WAKE FIRED rc=3`
    and no receipt for any id; the fake `--on-note` is asserted to receive
    ids only — no argument or stdin holds an `INBOX` line, `carrying=`, or a
    note body; **the surviving child**: the fake `--on-note` for A blocks
    until a release file appears, B lands while A runs and is `queued`,
    `serve` is SIGKILLed with A alive, and the restart prints `WAKE
    UNCERTAIN` for A and `WAKE BLOCKED as=<name> uncertain=A queued=1`, the
    fake records zero further calls while A lives, the exit line says
    `uncertain=1 queued=1`, and a second restart prints `WAKE BLOCKED` again
    and still fires nothing; after A is released and returns, `--redeliver A
    --on-note <fake>` runs A once as `attempt=2 redelivered=1` and the next
    call fires B in its own invocation, never beside A; with
    `--on-note-idempotent` the restart runs A's retry once and still fires B
    only after that retry returns 0; **the completion boundary** (Stella,
    2026-09-11, final read): with `--on-note-idempotent`, the fake for A
    blocks until a release file appears, B lands and is `queued`, `serve`
    is SIGKILLed with A alive, and the restart runs A's retry, which the
    fake answers **immediately with "already accepted" and exit 75** while
    the original A still runs: the entry becomes `uncertain attempt=2
    rc=75`, `WAKE UNCERTAIN id=A attempt=2 rc=75: retry not terminal` and
    `WAKE BLOCKED … uncertain=A queued=1` are printed, B stays queued, the
    fake records no call for B while A lives, and a further restart runs no
    third attempt; after A is released and returns, `--redeliver A --on-note
    <fake>` exits 0, `serve:A` reads `delivered rc=0 redelivered=1`, and only
    then does the next call fire B in its own invocation; the same schedule
    with a fake whose duplicate **waits for the original and then exits 0**
    fires B in the call after the retry returns, never before, and the
    state file shows `dispatching attempt=2` for A for as long as the retry
    ran; a mutation that dispatches B on the retry's non-zero return, one
    that treats the retry's spawn rather than its `delivered rc=0` as the
    boundary, and one that runs `attempt=3` on its own, each turn the test
    red; `--redeliver` without `--on-note` is
    refused naming the flag; a mutation that dispatches B on a restart with
    A `uncertain` turns the test red, and the state codec round-trips a
    value holding a literal `%7C` and one holding `%25` byte-identically;
    an hour with no note fires nothing, the fake was never
    started, and the exit line says `fired=0`; `max_wait=` equals the
    injected clock's longest queued interval; a mutation that writes
    `delivered` before the spawn, one that runs a `dispatching` entry on
    restart without `--on-note-idempotent`, and one that dispatches an
    `addr=cc` note, each turn the test red.
11. `TestDeliveryIsThePrintedLine`: 60 entry changes under `--max-lines 40`
    print 40 and `WAKE MORE kind=entry ... n=20`, the verdict says
    `pending=20`, and the next call with `--max-lines 0` prints exactly those
    20 first; the loop is killed, with an injected kill point, after the
    observed write, after the item lines, and after the `printed=` marks, and
    in each case the next call prints every line the killed call had not
    marked and nothing it had; with an injected stdout that fails mid-write,
    no `printed=` mark is written for the failed line; a mutation that writes
    `printed=` before the print turns the test red; one entry observed red,
    then green, across two polls with the red still unprinted, prints two
    `WAKE ENTRY` lines for it, red first, and a mutation that overwrites the
    unprinted value turns the test red; with `--advance-cursor` and a fixture
    bus of 25 notes — above `nova-bus`'s default `OPEN` display cap of 20 —
    against the real installed `nova-bus`, the loop is killed with an
    injected kill point between `nova-bus inbox --advance` returning and the
    write of its output; the state then holds `bus:advance=inflight`; the
    next call is asserted to run `inbox --open --open-max 25`, `25` being
    the `carrying=` it read and not a constant, prints the `WAKE NOTE ...
    recovered 25 notes from OPEN` line, and the following calls print all 25
    `WAKE BUS` lines — the residual named in **The races** is a repeated
    wake, never a lost one; a mutation that passes a fixed `--open-max 20`,
    and one that clears the marker before the output is written, each turn
    the test red.
12. `TestNewMailReachesTheCheckoutThroughTheAdvance`: a bare remote and two
    clones; a note is pushed from the other clone while one watcher runs alone
    with `--advance-cursor` and is relayed within two polls; a second note
    pushed while the watcher holds unprinted notes is not relayed until they
    have printed, the deferring call prints `WAKE NOTE bus advance deferred`
    once, and the note is relayed within two polls of the queue draining; the
    same without `--advance-cursor` is not relayed, `head=` and `head-at=` on `WAKE SOURCE
    bus` do not move, and the `WAKE NOTE` about fetching is printed once;
    with `--refresh` the note is relayed within one poll, `nova-bus` is
    asserted to have been run as `wait` with `--timeout` and without
    `--advance`, the cursor never moves, the first poll runs `inbox --open
    --open-max <carrying>` once and lists the carried notes before the new
    one, and `--refresh --advance-cursor` together are exit 2; a `wait` that
    exits non-zero on one poll (the remote unreachable) is one `WAKE POLL`
    line, every queue record is intact afterwards, and the next successful
    poll relays the note; in a build without item 3a, `--advance-cursor` is
    `WAKE REFUSED … not in this build; use --refresh`, exit 2; a
    fake `nova-bus` answering `nova-bus v0.10.4` is `WAKE REFUSED` naming both
    versions, exit 2, before the opening line.

13. `TestEveryWaitIsASource` (amended 2026-09-13; further amended 2026-09-13,
    draft 2): a `watch` naming only
    `--lock`, only `--ref`, only `--run` or only `--pr` runs — one source is
    enough — and a `watch` naming none of the eight is exit 2 naming all eight
    flags in one line; `--pr` or `--ref` without `--forge-interval` is exit 2
    naming it; `--forge-interval 10s` is refused naming the 30s floor; `--run`
    without `--entry-interval` is exit 2; **`--run` with `--entry-interval 5s`
    is refused naming the 30s floor, and `--entry` alone with `--entry-interval
    5s` runs** (draft 2, the `--run` floor). **The flag is `--ref`**: `--branch
    o/r:x` as a source is a refusal naming `--ref`, and `watch --refresh
    --remote origin --branch main --ref o/r:main` parses, watches one forge
    branch and fetches the bus branch `main` — the one invocation draft 1 could
    not parse, and a mutation that restores `--branch` as the source flag turns
    this test red. **`--to-only`** (draft 2, the test draft 1 demanded of
    nobody): with the flag, an `INBOX NOTE … addr=cc` is not printed, not
    queued, carries no `printed=` mark in the state file afterwards, and is
    counted in both `suppressed=` and `cc=1` on the `WAKE SOURCE bus` line with
    `read=` still equal to the sum of the three; the same note without the flag
    is one `WAKE BUS` line and a change; `--to-only --advance-cursor` is exit 2,
    `--to-only --refresh` runs; and a mutation that counts the cc note outside
    `suppressed=` breaks the sum and turns the test red. **The two id files**
    (draft 4): a `--not-mine` file holding a blank line, a `#` comment and two
    node ids parses and sets two ids aside, one holding a line that is not a
    node id is exit 2 naming the file and the line number, and one that cannot
    be read is exit 2 before the opening line; `probe --rest` is the same three
    cases over `<name> <id> <stamp>` entries; and a mutation that treats either
    unreadable file as an empty set turns the test red. **A quiet cold poll of
    the four new sources** (draft 3, from the Opus read, which found the
    cold-start rule naming two of seven and no test demanding otherwise): a
    first run with **no state file** naming `--pr`, `--run`, `--ref` and
    `--lock` at once — a conversation with four comments, a head with five
    checks, two branches that exist and one that does not, a held lock and a
    free one — prints **no item line at all**, records all of them, and runs to
    its deadline as one `WAKE QUIET` with `cold=true` on the opening line;
    `--baseline` over the same fixture prints all of them; and a mutation that
    reports any one of the four on its first poll turns the test red.
14. `TestAnOwnedPullRequestWakesOnAReviewAndNotOnItself` (amended 2026-09-13,
    draft 2; fixture logins are `login-a` and `login-b` rather than friends'
    names — a fixture is not a person, from the Fable read): with a fake `gh`
    whose `api user` answers `login=me`, a pull request whose newest item is a
    review `CHANGES_REQUESTED` by `login-a` prints one `WAKE PR` line with
    `newest=review:<id> by=login-a review=CHANGES_REQUESTED` and a `url=`; the
    same state on the next tick prints nothing; **under `--not-mine`
    holding the id of a comment this actor posted**, that comment raises
    `comments=`, reads `self=1` and is **not** a change (draft 4: the same
    comment without the flag **is** a change, and a mutation that sets a node
    aside by its author rather than its id turns the test red); a new comment
    by `login-b` after it is; a count that falls by one is a change; **two arrivals in one
    tick with this actor's own last, under `--not-mine`** (draft 2, the case a
    fake `gh` passed for
    free): a comment by `login-b` and then one by `me` land between ticks,
    `comments=` moves by 2, the last node is `me`, and the call **is** a change
    printing `by=login-b self=1` — the fetch of `last:min(d,10)` nodes is
    asserted on the fake, a fake that answers only the `last:1` shape fails the
    test, and a mutation that decides *only self* from the last node alone
    turns it red; a kind that moves by 12 with **every one of the ten fetched
    nodes** set aside is still a change, because `d` is above the ten-node
    window, and `self=10` says what was read — **the bound beats rule 14's
    *never wake it*, and a mutation that suppresses this wake turns the test
    red** (draft 3; draft 2 wrote *all twelve fetched nodes* of a read that
    fetches `min(d,10)`); **this actor's own eleventh comment, under `--not-mine`** (draft 3, the
    case draft 2 left undefined): eleven comments whose ids are all in the
    file, over eleven forge
    ticks are eleven `d=1` ticks, **none** of them a change and none of them a
    wake, because each tick stores the whole observed value — a mutation that
    leaves the moved counts unstored makes the eleventh `d=11` and wakes the
    window on its own words, and one that keeps the old `newest=` wakes it one
    tick later with every `d` at zero, and each turns the test red; a friend's
    comment on the twelfth tick is still a change, so neither mutation can be
    hidden by over-suppressing;
    **two writers on one login** (draft 4, finding 1): with the same fake `gh`
    answering `login=me`, two distinct actors both write as `me` — this
    window's comment, then the other actor's review `CHANGES_REQUESTED` — and
    **with no `--not-mine` both are changes**, the review printing `by=me
    self=0 review=CHANGES_REQUESTED`; with `--not-mine` holding only this
    window's comment id, that comment is `self=1` and not a change while the
    other actor's review on the same login **is** one; a mutation that restores
    author-based suppression drops the review and turns the test red.
    **A reply on an older thread** (draft 4, finding 2): no count moves,
    `newest=` is unchanged, and the pull request's `updatedAt` moves — one
    `WAKE PR … self=0 rescan=true newest=- by=- review=-` line, a change, with
    the fake asserted to have made exactly **one** `api graphql` call on that
    tick; the same tick under `--not-mine` is still a change and still
    `self=0`; a tick on which no field moved at all prints nothing; and a
    mutation that drops `updated=` from the value leaves the reply invisible
    and turns the test red.
    **A watermark is stored on every tick, and a moved stamp is never silent**
    (draft 6, K4, replacing draft 5's finding-M5 assertions): with `--not-mine`
    holding this actor's comment id, a tick in which that comment and an
    older-thread reply land together is a change, `rescan=true`, `self=1`, and
    the stored `updated=` is asserted **advanced to the observed stamp** — and
    the case is driven in **both orderings and at equal stamps**: the reply
    earlier and the comment later, the comment earlier and the reply later, and
    both carrying one identical stamp. A mutation that suppresses the tick when
    the observed stamp is not newer than the attributed node — which is draft
    5's rule — leaves the reply invisible for ever and turns the test red, and a
    mutation that leaves the stored stamp behind makes the following three ticks
    report the same movement again and turns the test red for the other reason.
    **A push names a movement and does not account for it** (draft 5, finding M6;
    amended draft 6, K4): a tick on which only `updatedAt` and `headRefOid` move
    prints `push=true head=<sha>` and is a change; the same tick with that head
    sha listed in `--not-mine` prints `push=true self=1 rescan=true` and is
    **still a change**, with a reply-plus-own-push fixture — an older-thread
    reply landing on the same tick as this actor's push — asserted to wake the
    window on that tick; the fake `gh` is asserted to have made exactly one `api
    graphql` call on each of them; a mutation that drops `headRefOid` from the
    standing query loses `push=` from the line and turns the test red, and a
    mutation that restores draft 5's *not a change at all* loses the reply and
    turns the test red. **`--not-mine` is re-read each forge tick** (draft 5,
    finding L15): an id appended to the file between two ticks is set aside from
    the next tick and not before, and a mutation that reads the file once per
    run turns the test red.
    `--owned-prs` with 23
    open pull requests watches 20, prints the `capped at 20 of 23` note once,
    and a pull request that opens mid-run has its existing 4 comments recorded
    and not reported; `gh api user` failing under `--owned-prs` prints the
    `owned-prs: host login unreadable` note once, lists nothing, counts that
    tick as a failed poll of the prs source (rule 8) and leaves a `--pr` watch
    in the same run untouched, and a `--pr`-only watch never makes the call at
    all (draft 4);
    the fake `gh` is asserted
    to receive exactly one `api graphql` call per pull request per forge tick
    **when no count moved**, exactly two when one did **under `--not-mine`**
    (draft 2; draft 4 scopes the second call to the flag), exactly one when a
    count moved without it, and no call
    between ticks; every pull request unreadable for three forge ticks is
    `WAKE BROKEN source=prs failures=3`, exit 2 (draft 2); no printed line
    contains a comment's body.
15. `TestARunIsAnEntryWithoutAState`: a head with `pending=3 pass=2` moves to
    `pending=0 pass=4 fail=1 failing=race`, printing one `WAKE RUN … final=true`
    under `--final-only` and one per transition without it; a head with no
    checks is not final; 101 check runs is `unreadable: more than 100 check
    runs on this head`, a change once; the fake `gh` records two `api` calls
    per head per `--entry-interval` tick, and (amended 2026-09-13, draft 2)
    `--run` with `--entry-interval` below 30s is refused naming the floor.
16. `TestABranchMovingIsItsTwoShas` (amended 2026-09-13, draft 2): a branch at
    `a1` moving to `b2` prints
    `WAKE BRANCH o/r:x head=b2 was=a1`; a deletion prints `head=- was=b2`; a
    creation prints `was=-`; **a force-push back to a sha seen before is a
    change and prints both ends** — `b2` back to `a1` prints `head=a1 was=b2`,
    which is the falsifiable form of draft 1's *a force-push to a sha seen
    before is still a change* (from the Opus read: `a1` to `b2` and back to
    `a1` **between two ticks** stores the value it started with and is
    invisible, so the old wording asserted something no fixture could fail —
    that within-a-tick blindness is a known limit, named beside `mtime:size`'s);
    a `git/ref/heads/<name>` answer that is an array rather than a ref object
    is `unreadable:` and never `absent` (draft 2, from the Fable read); one
    `gh api` call per branch per forge tick; the source flag is `--ref`, and
    `--branch o/r:x` is a refusal naming it.
17. `TestALockReleasedIsAChangeAndTheProbeNeverHolds`: a lock file held by the
    test with `flock` reads `held`; released, the next poll prints `WAKE LOCK
    path=<p> state=free was=held`; re-taken, `state=held was=free`; removed,
    `absent` **on a `flock` build and `free` on the `O_EXCL` sentinel build,
    where the two are one fact** (amended 2026-09-13, draft 2; both cold reads:
    draft 1 demanded `absent` of a build that cannot produce it, so this clause
    is per build and the test asserts the platform's own word); the file's
    bytes and mtime are unchanged after fifty probes and
    it is never created when missing; a second goroutine holding a blocking
    `flock` request throughout is granted the lock within one probe gap and
    never starved; the source tripwire finds no `os.Remove`, no `os.Create`
    and no write on the lock path in `internal/wake/lockfile.go`, **and the
    probe's `TryLock` is asserted not to write the holder pid `LockFile`'s
    acquire writes** (draft 2, from the Fable read); every `--lock` path
    unreadable for three polls is `WAKE BROKEN source=locks failures=3`, while
    an `absent` path clears the streak (draft 2). **Protocol and file type**
    (draft 4, finding 6): on the `flock` build the probe is asserted to open
    `<path>` and on the sentinel build to `lstat` `<path>.held` and never open
    `<path>` at all; a symlink at the lock path is `unreadable: symlink at the
    lock path` and the link's target is asserted unopened; a **FIFO** at the
    lock path is `unreadable: fifo at the lock path, not a regular file` and
    the poll returns within one interval, which is the assertion a blocking
    open cannot pass; a directory and a device are the same refusal with their
    own words; the path replaced by a new regular file between two polls reads
    the new file's state and nothing is removed or created to learn it; and a
    mutation that drops `O_NONBLOCK` and one that drops `O_NOFOLLOW` each turn
    the test red — draft 4's third mutation, over a path with no lock protocol
    on this build, is withdrawn with the case it named (draft 5, from the Fable
    read, finding M8: the three build files partition every build, so no fixture
    could produce that refusal and no mutation over it could fail). **A
    sentinel `.held` outlives its holder** (draft 5, finding L13): on the
    sentinel build a holder killed while holding the lock leaves `<path>.held`,
    and the source is asserted to read `held` at every later poll and to remove
    nothing; the same kill on the `flock` build reads `free` at the next poll,
    and a mutation that deletes a sentinel it judges stale turns the test red.
    **The blocked contender** (draft 4): a
    second goroutine in a blocking `flock` is granted the lock **no later than
    one probe gap after the holder releases**, measured against the injected
    clock over fifty probes — the falsifiable form of draft 3's *never
    starved*.
18. `TestAStopIsAVerdict`: a `watch` sent `SIGTERM` mid-`--max` prints `WAKE
    STOPPED after=<d> polls=<n> pending=<n>` as its last line and exits 0;
    sent with an observation written and unprinted, the next call prints it;
    sent with a line printed and unmarked, the next call prints it again and
    nothing else; `<state>.lock` is free after the exit; the only paths in the
    temp directory afterward are `--state`, its fixed-name temp file and
    `<state>.lock`; a `SIGTERM` during a fake `gh` call ends the call within
    one second.
19. `TestAProbePingsOnceAndNeverDiagnoses`: on a bare bus with two clones and
    an injected clock, a line whose last sign is 4m old is `PRESENT` exit 0;
    at 6m without `--ping-draft` it is `SILENT` exit 1 and the fake `nova-bus`
    records no `send`; at 6m with `--ping-draft` it is `PINGED` exit 1, one
    `WAKE PING` line, `nova-bus prepare` run exactly once and `nova-bus send
    --prepared` run exactly once as `--as <caller>` over the saved artifact
    (draft 6), the `WAKE PING id=` equal to the artifact's `id`, and
    `probe:<name>` holding all six fields — the sign sha, `pinged`, the stamp,
    that note id, the anchor and the scope; a second
    probe at 7m sends nothing and is `PINGED`; a receipt by the line **addressed to the
    caller** pushed
    from the other clone under `--refresh` is `ANSWERED` exit 0 within one
    `--interval` and the record clears, while **an unrelated commit by the
    line** — a cursor push, and a note from the line to a third name — pushed
    in the same way leaves the state `PINGED` exit 1 with the record intact,
    and a mutation that answers on any newer sign turns the test red (draft 4,
    finding 3, and the same mutation over the blocking return path, draft 5,
    finding M3); **the answer is read from the lane and not from the caller's
    cursor** (draft 5, from the Fable read, finding M4): with the caller's own
    `nova-bus inbox --as <caller> --advance` run by hand between the two probes,
    so the reply is on the caller's `OPEN` list and a plain `inbox` no longer
    lists it, the next probe still reaches `ANSWERED` exit 0, and the fake
    `nova-bus` records **no** `inbox` read at all for that poll (amended draft
    7, K3: draft 6 asserted two reads here, and the correlation is now the
    tool's own lane walk) — a mutation that correlates from an `inbox` listing
    instead leaves that probe `PINGED` and then `UNAVAILABLE` of a line that
    answered, and turns the test red; the fake `nova-bus` is asserted to record
    **no** `inbox` read on any probe (draft 5, finding L15, widened draft 7);
    with no sign by 8m `UNAVAILABLE` exit 1
    and the word `credits` appears nowhere on stdout or stderr; **the
    transitions are driven as a table** (draft 5, finding M2): `SILENT` to
    `PRESENT` on a fresh sign with no ping ever sent, `PINGED` to `PINGED` on a
    fresh sign not addressed to the caller, `UNAVAILABLE` to `PRESENT` on a
    fresh sign with the retired-ping `WAKE NOTE` printed and the record gone,
    `UNAVAILABLE` to `ANSWERED` on a late answer, and a `RESTING` entered with a
    record standing and left with it still standing; a mutation that moves a
    state on an event the matrix marks `—` turns the test red; a `send` that
    exits 1 prints `pushed=false`, writes no `probe:` record, and the next
    probe sends again; **a `send` that exits 0 with `SEND OK … pushed=false` —
    committed to the checkout and not pushed — is the same case** (draft 3,
    from the Fable read): one `WAKE PING … pushed=false` line, the state word
    `SILENT` and not `PINGED`, no `probe:<name>` key standing in the state file
    afterwards — the intent written before the send was cleared, which is the
    assertion draft 4's *byte-identical* could not carry for a file an intent
    had passed through (draft 5, finding L10) — and the next probe at 7m
    **sends again**; a mutation that
    records the ping on `SEND OK` alone makes that probe send nothing, reach
    `UNAVAILABLE` at 8m on a note that never left the bench, and turns the test
    red; **one prepared identity is offered to the bus until it lands, and an
    interrupted send is reconciled by that identity on the lane's fetched remote
    ref** (draft 4, finding 5; re-derived draft 5, H1, whose repair was that the
    checkout carries a send's commit before the push does; re-derived **draft
    6**, K1 and K2, whose repair is that a fresh send invents a second id and
    that a caller's commit past an anchor is not the ping). The fake `nova-bus`
    is killed at **three** points on the prepared path and the observable is
    asserted at each, **against the real disposable bare remote and not against
    the send's own output**: the notes the remote carries, and their ids. Killed
    **after `prepare` and before the send** — the artifact is saved, the intent
    stands, and the remote carries nothing — the next probe under `--refresh`
    fetches, finds no note with the recorded id, prints the interrupted-ping
    `WAKE NOTE` naming that id, runs `send --prepared` over the **same**
    artifact as `attempt=2`, and reaches `PINGED` exit 1 with the remote carrying
    **exactly one** note whose id is the prepared one. Killed **after the send's
    local commit and before its push** — the note is in this checkout and on no
    remote ref — the record holds `sending` with the same id and the next probe
    reconciles negatively, resends the same artifact, and the remote is again
    asserted to carry **exactly one** note with that id: the mutation that sends
    a fresh `--file` instead of the saved artifact makes the remote carry
    **two** notes, which is K1's duplicate ping, and turns the test red; the
    mutation that reconciles against the checkout — its head, its unpushed
    commits, or the `SEND OK` output — makes that probe record `pinged`, send
    nothing and reach `UNAVAILABLE` at 8m on a note that never left the bench,
    and turns the test red. Killed **after the push and before the intent is
    replaced** — the note is on the remote ref — the next probe under `--refresh`
    finds **that id** there, prints `reconciled=true` with `pinged-id=` equal to
    it and never `-`, records `pinged`, sends nothing, and `nova-bus send` is
    asserted to have been run **exactly once more than the kills forced** for
    that silence. **An unrelated note is not a ping** (draft 6, K2): with the
    probe killed **before** `prepare` and the caller pushing one unrelated note
    to the lane, the next probe finds no intent to reconcile and pings for the
    first time; and with the intent standing and the recorded id absent while an
    unrelated note by the caller sits past the anchor, the reconcile is negative,
    the artifact is resent, and a mutation that asks *any commit by the caller
    past the anchor* reaches `PINGED` and then `UNAVAILABLE` on a note nobody
    sent, and turns the test red. In all three kills the same call **without**
    `--refresh`, and the same call with a fetch that fails, is `UNRECONCILED`
    exit 1 carrying `reconciled=false`, prints the unreconciled `WAKE NOTE`, runs
    `nova-bus send` **not at all**, leaves the record and the artifact exactly as
    it found them, and prints neither `PINGED` nor `SILENT` anywhere on its
    output; a mutation that sends over an unreconciled intent, and one that
    answers `PINGED` on it, each turn the test red, and one that writes no intent
    before the send turns all three kills red because the kill then leaves
    nothing to reconcile. **A negative reconcile whose resend does not push is
    `SILENT`** (draft 6, K5): `send --prepared` returning `SEND OK pushed=false`
    after a negative reconcile leaves no `probe:<name>` key, prints `SILENT`, and
    the next probe offers the same artifact again — and a mutation that reaches
    `PINGED` there turns the test red. **The guarantee** is asserted directly,
    over fifty randomised kill points across the three: for one silence the
    remote ever carries **at most one** note, its id is the prepared one, and
    `send --prepared` runs again only on a call whose own fetch found that id
    absent from the lane's remote ref; **the correlation read is bounded per poll,
    incremental across polls, and says when it is partial** (draft 7, K3,
    replacing draft 6's inbox-cap assertions) — **the 900-note lane**: the
    pinged line's lane carries 900 notes past the ping's anchor and the answer
    — a note from that line addressed to the caller — is the **850th**. Under
    the default budget the first poll is `correlation=partial remaining=600
    gaps=0`, `PINGED`, exit 1; the second is `correlation=partial
    remaining=300 gaps=0`, `PINGED`, exit 1, **at 8m, past `--answer-within`,
    and never `UNAVAILABLE`**; the third reaches the answer and is `ANSWERED`,
    exit 0, with `pinged-id=` on the line. **`remaining=` is asserted in
    items** (draft 8, K3b): the same 900 notes delivered in **one** lane commit
    give the same `remaining=600` then `remaining=300`, and a mutation that
    counts commits prints `remaining=1` on the first poll and turns the test
    red — **the 900-notes-in-one-commit test**. Asserted with it: **no `nova-bus` is started
    by the read** on any of the three, the bytes each poll **consumes from the
    `git` adapter and decodes** are **under `--correlate-bytes`** (draft 8,
    K3b: measured at this process's boundary, which is the bound the spec
    makes, and not inside `git`, which it does not) and the items under
    `--correlate-max`, **no note body is opened** (each poll's reads stop at
    the header), the wall clock on each `git` process is **under 10s**, the
    read's peak resident bytes are **flat across the three polls** (draft 8,
    K3b: a constant, not a function of lane depth), the caller's `CURSOR` and
    `OPEN` are **byte-identical** after all three, and the only state byte that
    changes between polls one and two is the record's seventh field. **No poll
    stops inside an item** (draft 8, K3c): every bookmark names an item
    boundary, and a mutation that spends the last of the byte budget on half a
    header turns the test red on the next poll's resume. A mutation that restarts each poll from the
    anchor turns it red by never reaching the 850th note; one that reads the
    lane unbounded and truncates after turns it red on the byte assertion; one
    that declares `UNAVAILABLE` on a partial correlation turns it red on poll
    two; one that advances the cursor or writes `OPEN` turns it red on the
    byte-identical assertion. The same lane drained to 200 notes is
    `correlation=complete remaining=0 gaps=0` on one poll and does reach
    `UNAVAILABLE` at 8m; **the over-header-cap header is a permanent gap** (draft 8,
    K3a; **corrected draft 9, K4a** — draft 8 asserted here that a
    `--correlate-bytes` raise reads this header, and it cannot, because the
    4 KiB header cap is fixed and is not a share of the byte budget): the 850th
    note's header has no blank line within 4 KiB, so it is a coverage gap —
    every poll from the second on prints `PINGED correlation=partial gaps=1`,
    at 8m, at 20m and at an hour, and **never `UNAVAILABLE`**, while the gap is
    named exactly **once**; and the same lane re-run at `--correlate-bytes
    1048576`, and again at `4194304`, is **still** a gap and still not
    `ANSWERED`, because no budget reaches past the cap —
    `expected=WAKE PROBE name=peer state=PINGED correlation=partial remaining=0 gaps=1`
    on every one of those polls. A mutation that ages the gap out, one that
    reads the tip as complete with the gap standing, one that declares
    `UNAVAILABLE` on `gaps=1`, one that names the gap every poll, and **one
    that lets a raised `--correlate-bytes` read an over-cap header** each turn
    it red. **The within-cap header over the total budget clears on a raise**
    (draft 9, K4b): the same lane with the 850th note's header ending **inside**
    4 KiB — 3 KiB of it — polled at `--correlate-bytes 2048`, so the item's
    whole cost is larger than the whole budget and it is a gap at that budget,
    `expected=WAKE PROBE name=peer state=PINGED correlation=partial remaining=0 gaps=1`;
    re-run at the default `--correlate-bytes 262144` the item fits, the gap is
    resolved on the poll that reads it, and because that item is the answer the
    poll is `ANSWERED`, exit 0,
    `expected=WAKE PROBE name=peer state=ANSWERED correlation=complete remaining=0 gaps=0`.
    A mutation that leaves the gap standing after the raise turns it red, and
    one that treats this fixture as the header-cap one and refuses to clear it
    turns it red on the same line. **An unreadable object** (draft 8, K3a): the same
    lane with the 850th note's blob deleted from the object store is
    `PINGED correlation=partial gaps=1` for ever with the reason named once,
    never `UNAVAILABLE`, and restoring the object reaches `ANSWERED`. **A
    budget of one item never spins** (draft 8, K3c; the `complete` rule
    qualified **draft 9**, K4c): `--correlate-max 1` over
    the 900-note lane advances the bookmark by exactly one item per poll,
    reaches the answer on the 850th poll, and **no poll both takes no item and
    records no gap while uncovered items remain** — and because the counting
    allowance is itself `--correlate-max` commits, which is one, the remainder
    is past it and the field is `-`:
    `expected=WAKE PROBE name=peer state=PINGED correlation=partial remaining=- gaps=0`
    on the first poll; `--correlate-bytes` set so that one header fits and two
    do not behaves the same way; a mutation that advances the bookmark past an
    item it did not read turns it red by missing the answer, and one that
    returns `correlation=complete` from a poll that took no item **while
    uncovered items or gaps stood** turns it red at once. **The empty lane
    times out** (draft 9, K4c): the pinged line's lane carries **no** item past
    the ping's anchor, so the poll reads nothing, nothing is uncovered and no
    gap stands — it is complete negative evidence, `PINGED` before
    `--answer-within` and `UNAVAILABLE` at 8m, exit 1,
    `expected=WAKE PROBE name=peer state=UNAVAILABLE correlation=complete remaining=0 gaps=0`.
    A mutation that applies draft 8's unqualified rule — no `complete` from a
    poll that read nothing — keeps this probe at `PINGED` at 8m, at an hour and
    for ever, and turns the test red; that mutation is the acceptance line
    draft 9 repairs. **The drained lane at an unchanged tip times out** (draft
    9, K4c): the 200-note lane already covered to its tip with `gaps=0` by an
    earlier poll is polled again with **no new item** and the ref at the
    **same** tip; the second poll takes no item, stays `complete`, and
    `--answer-within` runs on it to `UNAVAILABLE` at 8m, exit 1,
    `expected=WAKE PROBE name=peer state=UNAVAILABLE correlation=complete remaining=0 gaps=0`.
    A mutation that turns the unchanged tip into `partial` because the poll
    read nothing turns it red at 8m; a mutation that reports `complete` here
    **while a gap from the earlier poll still stands** turns it red on the gap
    fixture, because `complete` is still the tip **and** zero gaps. **The retained gap list is capped**: a lane with 100 unreadable
    items records **64** and stops the bookmark before the sixty-fifth, and the
    state file's `probe:` line stays under its bound; and a **receipt** naming the ping id, appended to that
    lane's `RECEIPTS` inside the range, is `ANSWERED` on its own with no note
    at all; **a record is bound to its transport
    and `--as` is required wherever one stands** (draft 6, K6): the same state
    file presented with a different `--remote`, a different `--branch`, a
    different `--bus` or a different `--as` is `WAKE REFUSED` exit 2 naming both
    scopes, sends nothing and leaves the file byte-identical, a standing record
    read with no `--as` is `WAKE REFUSED` exit 2, and a `--ping-draft` whose
    prepared note resolves to a `To:` other than `--line` is `WAKE REFUSED` exit
    2 naming both with `nova-bus send` never run; a mutation that drops the
    scope, or that sends on a mismatch, turns the test red;
    **a declared rest is never probed** (draft 4, finding 3): with the line
    named in `--rest` and its sign 6m old, the probe is `RESTING` exit 1
    carrying `rest=<the declaring note id>` and `contact=STALE`, `nova-bus
    send` records nothing, the state file is byte-identical, and the same holds
    with the sign 4m old (`contact=FRESH`); with the entry taken out, the same
    call at 6m pings; no elapsed time on any clock leaves `RESTING`, and a
    mutation that expires a rest on a timer or that pings a resting line turns
    the test red; `--here` prints one `WAKE HERE` line whose `load=` has
    three fields, whose `procs=` is at least 1, and the tripwire finds no
    `exec.Command` **and no `/proc` or `"ps"`** in `internal/wake/here.go`
    (amended 2026-09-13, draft 2: the count is `sysinfo`'s `procs` or a
    nil-buffer `kern.proc.all` length, and a mutation that walks `/proc` turns
    this test and test 4 red); `--answer-within 90m` is
    refused naming the 60m ceiling; `contact=` and the state word are asserted
    to be two fields on every `WAKE PROBE` line, and a mutation that prints
    `PRESENT` from a rest entry, or a rest from fresh contact, turns the test
    red (draft 4). Amended 2026-09-13, draft 2: a `--line`
    **`nova-bus send` does not know** is exit 2 carrying the bus's own reason,
    and the tripwire finds no read of a roster file in `probe.go` — the name
    is resolved by the program that owns the roster, so the case is scoped to a
    `--ping-draft` call and a probe without one never asks; **the same
    misspelt `--line` with no `--ping-draft` is not a refusal at all** (draft
    3): it has no commit on the lane, prints `last=- commit=-` and the state
    word `SILENT`, exit 1, and a mutation that answers `PRESENT` for a name
    with no sign turns the test red; `--as` is required
    only with `--ping-draft`; with no `--silent-after` and no `--answer-within`
    the probe uses 5m and 2m and says so on its line as `silent-after=5m
    answer-within=2m` (draft 3, the grammar's new field); a `probe --line` over a
    `--state` a `watch` holds is `WAKE REFUSED` exit 2 naming the holder, and a
    `PRESENT` or `SILENT` probe leaves the state file byte-identical.

## Known limits

- **It cannot make the window act.** It returns, the harness wakes the session,
  and what the session does next is the session's. The lost-note failure this tool
  closes was never that a note went missing; it was that nobody came back to look.
- **It watches seven sources and no others** (three until 2026-09-13; the
  amendment of that date is the spec change the earlier sentence demanded).
  No filesystem watch of arbitrary trees, no log tailing, no process
  liveness, no schedule, no runner-pool health. `--line` is not a source: it
  is a view over the bus checkout's commits (rule 2). An eighth source is a
  spec change, and a flag that ran an arbitrary command each poll would make
  this a `cron` with a blocking call, which is the thing it replaces.
- **The lock probe owns the lock for one gap** (amended 2026-09-13): between
  the `flock` that was granted and the `close` that releases it, a
  non-blocking contender can be told `held`, and a **blocking** contender can
  be granted the lock one gap later than the holder's release, because the
  probe queues beside it (amended 2026-09-13, draft 4, from the Astra read,
  finding 6, which is a repair of draft 3's *a blocking contender is not
  affected*): delayed by at most one gap per tick, never denied and never
  starved. Named under `--lock`, pinned by test 17. (Amended 2026-09-13,
  draft 2, from the Opus read: **two `nova-wake` watchers probing one path can
  read each other** — each is a non-blocking contender in the other's gap, so
  a `held` may be the other watcher and not the lane's holder. The value is
  self-correcting on the next tick and the gap is two system calls wide, but
  a window watching a lock two of its own watchers also watch should expect
  one spurious `held`.)
- **A branch that moves and moves back between two ticks is invisible**
  (amended 2026-09-13, draft 2, from the Opus read): the state value is the
  head sha, so `a1` to `b2` and back to `a1` within one `--forge-interval`
  stores what it stored before and wakes nobody — the same shape as
  `mtime:size`'s rewrite, and the answer to *why did it not wake*. A shorter
  interval narrows the window and nothing closes it; the forge's own event
  stream would, and it is not a source here.
- **A comment's words are not relayed** (amended 2026-09-13): `WAKE PR`
  carries counts, a kind, an id, an author, a review state and a URL. The
  window opens the item. This is rule 5 and not a gap.
- **What a forge does not stamp into `updatedAt`, the prs source cannot see**
  (amended 2026-09-13, draft 4, finding 2): a reply on an older thread, a
  dismissal and an edit are caught when the pull request's own stamp moves,
  and reported as `rescan=true` because this read cannot say where. This tool
  models no forge's stamping rules; what it guarantees is that it never
  reports quiet about a conversation whose stamp it saw move.
- **A git author is not a bus sender** (amended 2026-09-13, draft 4, finding
  4): `--line` and `probe --line` read commits by author name (rule 2), so a
  sender whose commits carry another human's author name cannot be seen here
  and one human's name can cover several senders. A name that authors no
  commit says so once, on the first poll, rather than reading OFFLINE for
  ever; resolving a sender to a lane belongs to the program that owns the
  roster.
- **A queued run and a running one read the same** (amended 2026-09-13):
  `pending=` counts both, because that is what the forge reports. The
  runner pool is not a source.
- **`probe` measures contact, and only on the bus** (amended 2026-09-13): a
  line that works without writing to the bus for six minutes reads as
  `SILENT`, and a harness that needs more than a note to wake is not woken
  by the ping alone. The receipt of the ping is the line's sign, and the
  adapter beyond the bus is the line's. **Contact is never eligibility**
  (draft 4, finding 3): `PRESENT` says contact is fresh and nothing more, a
  rest is known only from `--rest` and never inferred, and `ANSWERED` proves
  that the line wrote to the caller after the ping and not, by itself, that the
  note answered it. The lane read does see the `Re` line and a receipt's target
  (draft 7, K3), so an exact correlation is **reported** where the answer
  carries one; where it does not, the residual stands and `pinged-id=` is what
  a person reads it against.
- **A ping is one identity, offered until it lands** (draft 4, finding 5;
  re-derived draft 5, H1; re-derived again **draft 6**, K1 and K2): the note id
  is fixed by `nova-bus prepare` before any send, every send is `nova-bus send
  --prepared` of that one artifact, and an interrupted send is reconciled by
  asking the lane's **fetched remote ref** for that exact id before any resend —
  so a kill produces no second note a friend can see, and no unrelated note by
  the caller is ever counted as the ping. Where this tool cannot fetch, it
  reports `UNRECONCILED` and neither sends nor claims. The residual that remains
  is on the **answer** and not the ping — a note the line was already writing to
  the caller as the ping landed — and `pinged-id=` is what a person checks it
  against; draft 5's own reconcile residual is gone with the anchor-commit
  reconcile that produced it. **What this rests on, checked rather than assumed**
  (draft 6): `prepare` and `send --prepared` are in `nova-bus` at this head —
  `internal/bus/prepared.go` and `cmd/nova-bus/main.go` — and their contract is
  **SPEC-BUS-DELIVERY**, whose own status line still reads *not implemented* and
  is stale against that code. This tool depends on the contract, so a change to
  it is a change here.
- **The correlation read bounds what it consumes, not what `git` does** (draft
  8, K3b). `--correlate-bytes` is consumed-and-decoded bytes at this process's
  boundary; `git` may inflate objects, read pack indexes, walk a delta chain or
  map more than it returns, and none of that is visible here or bounded by this
  spec. What holds a pathological lane instead is the item bound and the wall
  clock on each `git` process, which are the other two of the five bounds. A
  tool that promised physical I/O it cannot see would be promising a number it
  never measured, which is the same defect as a guessed `remaining=`.
- **A coverage gap can stand for ever, and that is the safe end of it** (draft
  8, K3a). An item this read cannot take — a header with no end inside 4 KiB,
  an item larger than a whole budget, an object `git` cannot give it — leaves
  the state at `PINGED correlation=partial gaps=<n>` with no clock that ends
  it, because the skipped item may be the answer and *skipped* is not
  *absent*. The record says so on every line, and **only one of the three kinds
  has a remedy a caller here can apply** (draft 9, K4a): a `--correlate-bytes`
  raise resolves an item larger than the whole budget and nothing else, because
  **the fixed 4 KiB header cap does not move with the byte budget** — an
  over-cap header is permanent for this probe at every budget, and an
  unreadable object is resolved by restoring the object. What this tool will
  not do is convert an unread item into a verdict about a friend. Where the bus
  itself cannot read that note either, the fix belongs on the bus (`INBOX
  UNREADABLE`) and not here.
- **A quiet lane is complete; a rewritten one is not** (draft 9, K4c). An empty
  lane, and a covered lane at an unchanged tip, are complete negative evidence
  and `--answer-within` runs to `UNAVAILABLE` on them. What that rests on is
  that the fetched ref's tip is the same object it was — the one thing a
  rewritten history breaks — and there the read already restarts from the
  anchor as `correlation=partial` with the retained gaps dropped, so a rewrite
  can delay a timeout but can never manufacture one.
- **What this tool assumes about other systems, rather than verifies** (draft 5,
  from the Fable read, finding L9). Verified at this head, against this repo:
  the lock protocols and their build tags (`internal/bus/lock_unix.go`,
  `lock_windows.go`, `lock_other.go`), and the bus listing behaviour measured on
  2026-09-11 against `nova-bus v0.10.3` (**How the checkout receives mail**).
  **Assumed**, and verified against no forge or kernel: that a thread reply
  moves a pull request's `updatedAt` and that dismissing a review moves no count
  and creates no node — draft 5 also assumed that a pull request's `updatedAt`
  is never older than the item whose arrival moved it, and draft 6 **withdraws**
  that assumption together with the suppression it was holding up (K4); that
  `O_NONBLOCK`
  keeps the open of a FIFO from blocking; that a kernel grants a released
  `flock` to a blocking waiter no later than one probe gap; and that
  `fcntl(F_GETLK)` does not see a `flock` on every platform. Each is a claim
  about somebody else's program, held because this tool's behaviour is
  falsifiable either way: where an assumption fails, the failure shows up as a
  change reported that nothing explains, and never as a quiet this tool
  promised.
- **Its view of a forge is `gh`'s.** Rate limits, authentication and a forge's own
  eventual consistency are `gh`'s behaviour, reported verbatim and never retried
  around. A check that the forge has not created yet is invisible, which is why
  the no-checks case is not final.
- **`mtime:size`** is the report identity; see the limit named above.
- **A line can be printed twice, never zero times.** A harness kill between
  an item line reaching stdout and its `printed=` mark leaves the entry
  pending, and the next call prints it again (**The races**, rule 11). The
  duplicate is the price of never losing a wake, and it is deliberately not
  refused away: there is no moment before the write at which a refusal would
  mean anything, because the kill is not the tool's to see coming.
- **Quiet is only as true as the sources.** `WAKE QUIET` means *these sources said
  nothing in this window*, not *nothing happened*. It is a report and not a
  guarantee, as `WAIT TIMEOUT` is — and without `--advance-cursor` it means
  *the checkout as it stands said nothing*, which `head-at=` makes visible.
- **Two advancing polls of latency on mail, none while mail is unread, and a
  pinned `nova-bus`.** New mail arrives through the push inside `inbox
  --advance` and is listed by the following `inbox`; a poll advances only
  behind an empty bus queue, so a window that has been shown more than it has
  read is not fetched for until it reads; a `nova-bus` other than this build's
  derived pin (`AcceptBus`) is refused.
- **A `serve` dispatch can be uncertain, and a person clears it.** After a
  kill between the `dispatching` write and the `delivered` write the note is
  `uncertain` and waits for `--redeliver`; only under `--on-note-idempotent`
  does the restart run it once more, marked `redelivered=1`, and the queue
  behind it waits for that retry's `delivered rc=0`, the receiver's terminal
  acknowledgement — a retry that returns non-zero is `uncertain` again and
  a person's (rule 10). Never
  a silent duplicate, and never a silent loss.

## What it deliberately does not do

- **No default source, no default bus, no default state file, no default
  repository.** Three of the prototype's paths were one line's home directory and
  one team's issue tracker, and a tool with those in it is a tool only that line
  can run.
- **No reading a coordination file to invent its own arguments.** The prototype
  read a merge lane's `lane.json` to decide which entries to watch when `--prs`
  was absent. That is a guessed path and a guessed scope at once, and it means a
  watch's subject can change under it because a file somebody else owns changed.
  Entries come from flags.
- **No `--advance` by default, and no cursor moved for another name.** Stated
  three times above, which is the right number for the failure it closes.
- **No writing anywhere but its own state file.** `watch` sends nothing,
  receipts nothing, comments on nothing (amended 2026-09-13, draft 2: *"it
  sends nothing"* was written of the tool and is true of `watch`; the three
  write-side calls and their verbs are named once under **The only programs it
  starts**), and its only write outside `--state` is the one
  `nova-bus inbox --advance` makes when the caller asked for `--advance-cursor`
  — the one fetch and the one write-side call `watch` has. (Amended
  2026-09-13: `probe --ping-draft`'s `nova-bus send` is the second, a person's
  draft sent once under a flag that names it, and `serve --receipt`'s receipt
  the third; `watch` still writes nothing to a
  bus or a forge under any flag — it comments on no pull request, marks no
  thread resolved, cancels no run, deletes no branch, removes no lock.)
- **No daemon, no background process, no detach** (amended 2026-09-13).
  `watch` and `probe` run inside the tool call that started them and end
  with it; `serve` is the one process outside a session and it exists for
  one job (rule 10). Nothing here re-arms itself, schedules a next call, or
  survives the harness that ran it.
- **No delivery of notes, and no decision** (amended 2026-09-13). A `WAKE PR`
  is not a review verdict, a `WAKE RUN` is not a merge decision, a `WAKE
  LOCK … free` is not a grant to take it, a `WAKE PROBE … UNAVAILABLE` is not
  a reassignment, and `WAKE HERE` is not READY. The window reads the line and
  decides; the tool has told it what moved and what the numbers were.
- **No model of a runner pool, a harness, or a friend's transport**
  (amended 2026-09-13). #178 asks that CI runners and toolchain pools be
  read for health beside the friends, and that each friend's wake adapter be
  configurable; the first is a source this amendment does not add (a queued
  run is visible as `pending=` standing still), and the second lives at the
  friend's end of the bus, where `serve` already is.
- **No acting on a report.** `RESULT.md` is prose another line wrote, relayed as a
  path and a size. Nothing parses it, and nothing in it is an instruction.
- **No exit code for *what* changed.** One bit of news in an exit status is a
  grammar that cannot grow; the second token of the last line is the answer and is
  readable by a person as well as a scanner.
- **No retry loop around a forge.** A failed poll is one `WAKE POLL` line on
  stderr and the watch goes on, still bounded by the deadline. The third failed
  poll in a row of one source ends the watch as `WAKE BROKEN` (rule 8). A failed poll on
  the *first* call is not special here, unlike `nova-bus wait`'s first fetch,
  because the unreadable state value is itself the news and the window gets it as
  a change.

## Work list — building it in Go under `cmd/`, like `nova-bus`

Standard library only, no third-party imports, no hardcoded paths, and the repo's
shared packages used rather than re-spelled.

1. **`internal/wake/state.go`** — the state map: load, save through a temp file and
   rename, the `|` composition with `%25` then `%7C` escaping and the reverse on load, the `printed=<id>` half of every value, the
   `queue:<n>` records and `queue:next`, the pending predicate as *a record
   names this key*, `bus:advance`, `fail:<source>` streaks, the 300-entry
   LRUs over `bus:line:` and **delivered** `bus:note:` keys with pending keys
   exempt, and an
   exclusive lock on `<state>.lock` reusing `internal/bus`'s lock (`lock.go`,
   `lock_unix.go`, `lock_other.go`) rather than a second lock implementation.
   Tests: round-trip quiet on a second poll (the 2026-09-11 lesson), eviction
   order with a pending record surviving 300 newer delivered ones, an
   observation appended behind an unprinted one and never over it, an
   unparsable file is an error and not a cold start, a killed write
   leaves the old file intact.
2. **`internal/wake/source.go`** — the `Source` interface: `Poll(ctx) ([]Item,
   error)` returning items that each carry a state key, a state value and a
   display line and a delivery id. Every change decision is one comparison in one
   place — observed value against `printed=` — so a fourth source cannot invent
   its own.
3. **`internal/wake/bus.go`** — run `nova-bus inbox`, or `nova-bus wait`
   without `--advance` under `--refresh`, under a timeout; classify by
   first tokens with a **suppress** list and a printing default case; the standing
   vs first-sighting split; the first-poll `inbox --open --open-max <carrying>`
   under `--refresh`; the `nova-bus version` pin; `head=`/`head-at=` from
   `git log -1`; and `--advance-cursor` refused as not in this build. Tests: an
   `INBOX REFUSED` wakes and is relayed verbatim; a repeat is `STANDING` and does
   not wake; an unknown future token prints; `--refresh` fetches and moves no
   cursor; a note past the cap is pending, not lost.
3a. **`internal/wake/advance.go`** — after item 3 is read and against the
   pinned binary: the `--advance-cursor` guard (`--as` required, lock held for
   the call, the four-step transaction — plain `inbox`, spool, print,
   `--advance` only behind an empty bus queue under the `bus:advance` marker —
   and the `inbox --open --open-max <carrying>` recovery with its re-read).
   Tests: `--advance-cursor` without `--as` is exit 2; the cursor waits behind
   the print; the recovery's `--open-max` equals `carrying=` and is never a
   constant; tests 5, 11 and 12's advancing halves, green against the real
   `nova-bus v0.10.3` before the flag is admitted.
4. **`internal/wake/entry.go`** — the `gh` call per entry, the bucket rule, the
   five-field value, batching at 8, `unreadable:<reason>` as a value, and
   `--final-only`'s FINAL predicate as a pure function over the value. Tests:
   churn wakes without the flag and does not with it; green and red both wake
   under it; an entry with no checks is not final; a different failing name with
   the same counts wakes; a `gh` timeout is a change once and not twice.
5. **`internal/wake/report.go`** — walk each `--reports` directory for `RESULT.md`
   at any depth, `mtime:size` values, `new` vs `modified`, a vanished file is not
   a change. Tests: a touched file with the same size and mtime does not wake (the
   named limit, pinned as behaviour rather than left to be discovered).
6. **`cmd/nova-wake/main.go`** — flags, refusals that name what the flag wants and
   report **every** independent problem in one go, the opening `WAKE` line, the
   poll loop with the deadline and the interval floor, the `60m` ceiling refusal,
   the verdict line, and `internal/bounded` for the per-kind cap. One `oneline`
   call on every value that reaches a line.
7. **`cmd/nova-wake/quickstart`** — the natural first run: `--baseline`, a short
   `--max`, one source if one can be inferred from the flags given, and a
   statement of what it chose, in the shape `nova-memory quickstart` and
   `nova-check quickstart` already use.
8. **Onboarding, which `internal/ci/onboarding_test.go` will require the moment
   the directory exists** — a usage banner ending in an `example:` block whose
   lines run, a `### First run` in `docs/CLI.md`, `nova-wake help` on stdout at exit
   0, and a one-line refusal for a bad invocation rather than the banner.
9. **Tests for the two lessons, named as such** — `TestNoFalseWakeOnReload` and
   `TestBlocksRatherThanTicks` (a watch whose sources never change returns once, at
   its deadline, having printed one verdict line). A spec whose lessons are not
   pinned by a test is a spec that will buy them again.
10. **`docs/SPEC.md` and `docs/CLI.md` wiring** — the binary count in
    docs/SPEC.md's opening paragraph, a `## nova-wake` section or a pointer to
    this file, and the `### First run` in [`docs/CLI.md`](CLI.md). CONTRIBUTING
    says a wording change to a rule here is a rule change; this file is that
    rule.
11. **`internal/wake/line.go`** — the `--line` view: `git log` on the bus checkout
    under the timeout, the last sign per name, the `<sha>|<state>|<stamp>`
    state value, OFFLINE once and BACK once. Plus, in `main.go`:
    `--on-deadline` echoed on the opening line and the verdict, `--entry-interval` with a per-source due time,
    the pid in `<state>.lock`, the `WAKE SOURCE` counts, and the three-in-a-row
    `WAKE BROKEN`. Tests: the eight in **Tests this spec demands**.
12. **`internal/wake/serve.go`** — the `serve` loop: the fetch per interval,
    `To:` dispatched and `Cc:` recorded, the coalesced batch under
    `--batch-max`, the `serve:<id>` states `queued`, `dispatching
    attempt=<n>`, `delivered rc=<n> redelivered=<0|1>`, `uncertain` and `cc`,
    each written before or after the step it names and never during it, the
    `uncertain` surfacing with `--redeliver --on-note`, the queue blocked
    behind an unresolved `uncertain` (`WAKE BLOCKED`) and the one-more-run
    only under `--on-note-idempotent` with the queue held until that retry's
    `delivered rc=0` and `uncertain … rc=<n>` on a non-zero return, ids and nothing else on the command line, the
    receipt per id after `delivered rc=0` and never before, `max_wait`, the
    `stop` file and `--hours`. Tests: test 10, with the kill points injected.

13. **`internal/wake/pr.go`** (amended 2026-09-13; draft 2) — the standing `gh
    api graphql`
    call per pull request, the **five**-field value including `updated=` and
    **the `rescan=true` change when only the stamp moved** (draft 4), **the
    second call that fetches
    `last:min(d,10)` nodes of a kind whose count moved** and the decision
    over what it fetched (draft 2), **a suppressed tick storing the
    whole observed value — moved counts, `updated=` and new `newest=` — and
    queueing nothing** (draft 3), **`--not-mine` as the only thing that sets a
    node aside, by id, with no suppression and no second call when the flag is
    absent, and `gh api user` read once per run only for `--owned-prs`'s
    listing** (draft 4, from the Astra read: a login is a shared credential and
    not a window),
    `--owned-prs` set refresh with the 20 cap, the
    cold-join rule **and a failed `gh pr list` as a failed poll** (draft 3),
    batching at 8, `unreadable:` as a value, **`fail:prs`
    when every watched pull request is unreadable in one tick** (draft 2), the
    cold first poll recording and reporting nothing (draft 3).
    Tests: test 14.
14. **`internal/wake/run.go`** — the two `gh api` calls per head, the entry
    bucket rule reused (not re-spelled) over check runs and statuses, FINAL
    without an entry state, the 100-run limit as `unreadable:`, **the 30s
    `--entry-interval` floor when `--run` is given and `fail:runs`** (draft 2).
    Tests: test 15.
15. **`internal/wake/branch.go`** — one `gh api` call under **`--ref`** (draft
    2), `<sha>|absent` value, an array answer as `unreadable:` (draft 2),
    both ends on the line, `fail:branches` (draft 2). Tests: test 16.
16. **`internal/wake/lockfile.go`** — the probe through `internal/bus`'s lock
    primitives with a `TryLock` that releases at once **and writes no pid, so
    it is not `LockFile`'s acquire** (draft 2); `held|free|absent`, with
    `free` the sentinel build's word for both (draft 2);
    never create, write or remove; `fail:locks` when every path is unreadable
    (draft 2). Tests: test 17, with the tripwire.
    Amended 2026-09-13, draft 4 (Astra finding 6): **the protocol-to-path
    mapping per build (`<path>` under `flock`, the `<path>.held` sentinel
    under `O_EXCL`, `unreadable:` for a path this build has no protocol for),
    the `O_RDONLY|O_NOFOLLOW|O_NONBLOCK` open with the `fstat` that refuses
    every non-regular file before any lock is attempted**, and the delay a
    blocking contender observes.
17. **`cmd/nova-wake/main.go`** — `--forge-interval` as a third clock with its
    30s floor, `--to-only` with the `--advance-cursor` refusal and its cc note
    counted inside `suppressed=` (draft 2), **`--ref` as the source flag with
    `--branch` kept for the bus branch on every verb** (draft 2) **and no
    cross-suggestion between `--ref` and `--refresh` in either refusal**
    (draft 3), the cold-start rule over all seven sources and
    `sources-failing=` on `WAKE QUIET` alone (draft 3), the four new
    kinds **and `line`** under `internal/bounded` — eight caps (draft 2) —
    the `signal.NotifyContext` stop with the
    rule-11 step boundary and `WAKE STOPPED`, the `sources=` list, the
    verdict's four new counts, and the widened `WAKE BROKEN` over all seven
    sources (draft 2). Tests: tests 13 and 18; test 5's `8 *
    --max-lines + 17` half.
18. **`internal/wake/probe.go` and `internal/wake/here.go`** — the sign read
    reusing `line.go` printed as **`contact=`** beside the state word, the
    **`--rest` roll read and `RESTING` before anything else is decided, with no
    clock that ends it**, the **ping prepared by `nova-bus prepare` before any
    send, the `sending` intent naming that note id with the checkout head as its
    correlation anchor, and every send `nova-bus send --prepared` of the one
    saved artifact, resent only where a fetch found that id absent from the
    lane's remote ref** (draft 6), and **`ANSWERED` from one bounded incremental
    read-only `git` walk of the pinged line's lane per poll — note headers and
    `RECEIPTS` lines from the ping's anchor forward under `--correlate-max` and
    `--correlate-bytes`, resuming from the record's seventh field, a note or
    receipt from the pinged line addressed to the caller and never any newer
    commit, `correlation=partial remaining=<n>` where the budget stopped
    short** (draft 4; bounded draft 6; the lane walk, the budget and the resume
    are **draft 7**, K3), **all-or-none items, the fixed within-commit order,
    retained coverage gaps with `gaps=<n>`, `remaining=` in items or `-`, and
    `complete` only on the tip with zero gaps** (**draft 8**, K3a-c), **the
    fixed 4 KiB header cap as a permanent gap no `--correlate-bytes` raise
    clears, the budget gap that a raise does clear, and `complete` withheld
    only where a poll made zero progress while uncovered items or gaps
    remain — an empty lane and an unchanged covered tip timing out to
    `UNAVAILABLE`** (**draft 9**, K4a-c), the `probe:<name>`
    record written after **`SEND OK pushed=true`** on a living call or after a
    reconcile that finds the prepared id on the fetched remote ref, and never
    otherwise (draft 3, qualified draft 7)
    **under `<state>.lock`, with no other key written and nothing written at
    all when nothing was sent** (draft 2), the unknown `--line` as `SILENT`
    rather than a roster refusal (draft 3),
    the one `nova-bus send`, the name resolved by `nova-bus` and no roster read
    (draft 2), the 5m/2m defaults (draft 2), the blocking half through
    `nova-bus wait
    --timeout <interval>` as `--refresh` already does, the **seven** state words
    and their exit codes; `here.go` reading load, CPUs and **a process count
    with no listing — `sysinfo`'s `procs`, a nil-buffer `kern.proc.all` length
    divided by `sizeof(kinfo_proc)`** (draft 2)
    through the OS with no subprocess, one build file per platform. Tests:
    test 19.
19. **Onboarding** — `nova-wake help` grows by the new flags and `probe`; a
    `### First run` sentence in `docs/CLI.md` for `probe --here`, the natural
    first probe because it needs no bus; SPEC.md's `## nova-wake` paragraph
    names the seven sources and the new verb.

## What the prototype does that this spec forbids

`bin/wake-on-change.sh` (zsh, 290 lines) proved every idea above and is a thing to
be grateful to. These are the places it is **not** a model, each with the reason.

1. **Hardcoded paths.** `$HOME/rowan-working/rowan-stella` as the bus,
   `$HOME/rowan-working/wake/state.json` as the state, `$HOME/deepseek-working-*`
   and `$HOME/freddy-working-*` as the report roots. Forbidden: no guessed paths,
   and two other lines' directory layouts are not a tool's business.
2. **A hardcoded default repository**, `mas-bandwidth/schema`, twice — once as a
   default and once as a fallback when the lane file had no `.repo`. Forbidden.
3. **Reading `merge-lane/lane.json` to invent its own watch list.** A guessed path
   whose content decides the scope of the run. Entries come from flags.
4. **Third-party programs in the hot path.** `jq` parses every poll's JSON,
   `python3` writes the state file, and `perl -e 'alarm shift; exec @ARGV'` is the
   timeout because this Mac has no `timeout`. Standard library only: `encoding/json`,
   `os.Rename` and `context.WithTimeout` are all three of those, and none of them
   is a dependency a line has to install before the watcher works.
5. **A default deadline.** `MAX=1200` means an invocation with no `--max` waits
   twenty minutes, which is the right number and the wrong rule: the one thing a
   caller must state is when to give up.
6. **Unconditional `--advance`.** Every bus poll advances the window's cursor,
   from any invocation, including a test run. This is the five-note failure, and
   the spec closes it three ways.
7. **A cap that lies.** `... +N more change lines (state advanced; poll again)`
   over consumed bus notes, with the cursor already at `HEAD`: the words were a
   promise nothing kept. Rule 11: the cursor waits behind the print, the
   elided notes are pending in the state, and the next call prints them.
8. **A tab-joined state value** — forbidden by name, with a test.
9. **Unbounded sighting memory swept to zero.** `if (( ${#seenlines} > 300 ))` then
   `unset` **all** of them, which re-wakes every standing error at once. An LRU.
10. **No lock anywhere.** Two runs share one state file and one cursor; both races
    above are open in the prototype.
11. **`exit 0` on everything, including a state file it could not parse** — the
    `jq` reload is `2>/dev/null` and a failure reads as a cold start, which is the
    false-quiet failure with a silencer fitted. Exit 2, naming the file.
12. **Usage printed by `sed`-ing its own source**, which makes the banner a
    property of the comment block's line numbers. `help`, on stdout, exit 0.
13. **Ungrammatical output.** `no change in 1200s` has no token, no fields and
    nothing a scanner can anchor on; `CHANGE after=…` has a token but its item
    lines (`bus: …`, `PR 942: …`, `report: …`) are three ad-hoc shapes. One
    grammar, first token `WAKE`.
14. **Silent truncation.** `cut160` cuts a relayed line to 160 bytes with no mark,
    so a reader cannot tell a cut from an author's own ellipsis.
    `oneline.TailBytes` and the `...+<dropped>B` mark exist for this.
15. **`--prs` as bare numbers against one `--repo`.** An entry's name includes its
    repository.
16. **One cadence for every source.** `INTERVAL=30` polls a bus that answers in
    seconds and a CI run that answers in eight minutes at one rate: sixteen `gh`
    calls per entry per run, learning nothing. Rule 3: `--interval` and
    `--entry-interval`, no defaults.
17. **A deadline with no default action.** `no change in 1200s` and the window has
    to remember what it meant to do. Rule 1: `--on-deadline` is required and is
    echoed on the verdict.
18. **Scratch under `/tmp`.** `TMPD="${TMPDIR:-/tmp}/wake-on-change.$$"` holds
    every poll's `gh` output. Rule 4: the only files are beside `--state`.
19. **No way to tell a silent line from a quiet one.** The prototype watches
    notes, checks and files, and a line that stopped writing all three looks
    exactly like a line that is busy. Rule 2: `--line` and OFFLINE once.
20. **A failing source that ends the run as quiet.** After the first `nova-bus
    exit=N` line, every later failure is `(still failing)` under STANDING, and a
    run in which the bus never answered ends with `no change in 1200s`. Rule 8:
    three in a row is `WAKE BROKEN`, exit 2.
21. **Lines read and never counted.** An unknown line is relayed, which is right,
    but nothing says how many lines were read, so a bus that printed nothing and
    a bus that printed twelve bookkeeping lines look the same. Rule 7: `WAKE
    SOURCE bus read=<n>` and the counts add up.
22. **A second state file under the same guessed directory.** `child-report.sh`,
    the report-side prototype, keeps its triage watermark at
    `$HOME/rowan-working/wake/triage.json` beside the watcher's `state.json`, both
    reachable through `ROWAN_WORKING` and `TRIAGE_STATE` from the environment.
    Its `--max 40` and `--since` are good ideas in the wrong place. Rule 4 and the
    no-environment law: a report source's state is `--state` and nothing else.

## Ideas folded on 2026-09-11

| source | the idea, in six words | disposition |
|---|---|---|
| Stella, spec repairs | pending retained until delivery; LRU never pending | already, rule 11 and **State** |
| Stella, spec repairs | plain inbox does not list OPEN | already, measured (**How the checkout receives mail**) |
| Stella, spec repairs | grow `--open-max` to the count, retry | step 4: re-read on a short list, marker kept |
| Stella, spec repairs | leave advancement out of v1; use `wait` | `--refresh` (wait without advance); `--advance-cursor` refused until item 3a |
| Stella, spec repairs | serve: uncertain, not silent retry | rule 10: `uncertain`, `--redeliver`, `--on-note-idempotent` |
| Stella, spec repairs | receipt only after durable acceptance | already, rule 10 (`delivered rc=0`); per id in a batch |
| Johnny, addition 1 | an in-session poll is not serve | the verb paragraph and the lessons table |
| Johnny, addition 2 | empty minutes cost zero tokens | rule 10: the command starts for a note and nothing else; test 10 |
| Johnny, addition 3 | quiet poll is counts, not carrying | rule 10: ids only, never `inbox` output; test 10 |
| Johnny, ideas 1–3 | serve is the wake; machinery sends Heard | already, rule 10 |
| Johnny, idea 4; Emma, C2 | `Cc:` is not `To:` | rule 10: to wakes, cc does not (nova-tools #53) |
| Stella, ideas 1–2 | machinery observes; coalesce per item; batch decisions | rule 10: coalesced dispatch, `--batch-max`; `--final-only` already (entries) |
| Stella, idea 6 | measure latency and duplicate wakes | rule 10: `max_wait=`, `redelivered=`, `uncertain=` on the exit line |
| Emma, A1–A2, C1 | event-driven wake, silence on idle | already, rule 10 |
| Emma, B1–B3 | nova-bus counts by default, batch advance | not folded: `nova-bus`'s (Emma's tool, #53); `serve` never feeds the carrying list to a model regardless |
| Emma, D | rolling coordinator session boundary | not folded: a window's practice, not a tool rule |
| Rowan, ideas 1, 9 | serve for every line; no status polls | rule 10, narrowed: `serve` is an opt-in adapter per consenting line — starting one under a name is that name's person's decision (the receipt paragraph), never a fleet setting; no status polls is the first lesson (Stella's closing read) |
| Stella, closing read | recovery proves idleness before any dispatch | rule 10: an unresolved `uncertain` blocks the receiver's queue, `WAKE BLOCKED` with the remedy; `--redeliver` names `--on-note`; `--on-note-idempotent` declares concurrency tolerance; `%` escaped as `%25` in the state codec (test 10) |
| Stella, final read | the idempotent exception needs a completion boundary | rule 10: `--on-note-idempotent` names the stronger contract — exit 0 for an id is terminal completion, never "already accepted"; the boundary is the retry's `delivered rc=0 redelivered=1` in `serve:<id>`, before which no different id is dispatched; a non-zero retry is `uncertain … rc=<n>` and a person's (state, grammar, test 10) |
| Rowan, idea 4 | pointers in the window, detail in children | not folded: a window's practice |
| Freddy, idea 2 | webhook wakeups over polling | already, rule 10 (a fetch outside the session); a webhook is a v2 source |
| Freddy, idea 5 | bundle acknowledgements | rule 10: one batch, receipts per id by machinery |
| DeepSeek, idea 3 | filtered event wakeups per interest | already, rule 10 (`--as`, `To:`) |
| DeepSeek, idea 1 | state digest plus a cursor | already, **State** (the queue and `printed=`) |
| nova-tools #53 | `to` wakes, `cc` does not | rule 10; the send side (`--to all`, `wakes=`) stays nova-bus's |
| ideas #273 | evidence arriving after the belief | already, rule 11 (observed before printed, nothing coalesces) and `head-at=` |
| ideas #357 | relay a note, never execute it | already, the data paragraph at the top |
| nova-tools #35 | the table races | already: `nova-bus`'s ids and push-retry are what `serve` and `--refresh` rely on; the version is pinned |

## Ideas folded on 2026-09-13

| source | the idea, in six words | disposition |
|---|---|---|
| Emma, emma-0fd8c03d5f24 | `wait --event` on OS/bus events, no polling | folded as sources of `watch`: `--ref` (draft 2; `--branch` in draft 1), `--run`, `--lock`; no new verb — **Why `watch` and not a new `wait`** |
| Rowan, 2026-09-12 (the two HOLDs) | owned PR comments are the bus too | `--pr`, `--owned-prs`, rule 14 |
| Rowan, 2026-09-11 (the measurement) | fewer turns; a check twice a minute is a tell | rule 13; the cost table under **What one tick costs** |
| Glenn, 2026-09-12 (five-minute rule) | five silent, one ping, two unanswered, unavailable | `probe --line`, rule 17; the state words and exit 1 |
| Glenn, #178 | delivery, contact, read, acceptance, progress are distinct | `probe` measures contact only and says so; `WAKE PING pushed=` is delivery to the bus, not a wake |
| Glenn, #178 | one targeted probe; hold assignments; cause unknown | rule 17: one ping per silence, exit 1 while waiting, the word `credits` forbidden by test 19 |
| Glenn, #178 | explicit rest is respected, not pinged away | `--rest`: a declared rest is `RESTING`, nothing is sent whatever the contact reads, and no clock ends it (draft 5, finding L11: *a stop note is a sign* was draft 3's answer and this row outlived it) |
| Glenn, #178 | a mailbox write is not proof the harness woke | `WAKE PING` reports the push; `ANSWERED` needs the line's own sign |
| Glenn, #178, 2026-09-13 comment | runners and pools in the health picture | not folded: `--run`'s `pending=` standing still is the visible tell; a runner-pool source is a later spec change (**Known limits**) |
| Grok sitting, #178 | wake on `addr=to` only; empty minutes are not turns | `--to-only`, refused with `--advance-cursor` |
| Grok sitting, #178 | a deliberate stop is not restarted by a liveness probe | `--rest` and rule 17: a resting line is never pinged, and one ping per silence is the reconcile's guarantee against the lane's remote ref rather than a fresh sign's (draft 5, finding L11) |
| Grok sitting, #178 | a person is not a restartable pool slot | `probe` reports; reassignment is a person's act on the record |
| Mercury, 2026-09-09 | READY only when quiet enough for the task | `probe --here`, rule 18: numbers, never the word |
| Rowan, 2026-09-10 (the nineteen shells) | every wait ends on its own; never pgrep yourself | rule 15 (`WAKE STOPPED`), rule 16 (probe never holds), rule 4 unchanged |
| Rowan, 2026-08-25 | absence of a process is not absence of a session | `probe` never reads a process table to judge a line; `--here` counts processes on this bench only, as a load number |
| the amendment's brief | exit code says change, deadline or refusal | declined in part: refusal is 2, ran is 0, the token says which; `probe` takes the reserved 1 as a gate — **Exit codes** |

## What this draft does not do

This draft does not carry a second, conflicting spelling of the four forge item lines: `WAKE PR` with `self=<login|->`, `rescan=<n>`, `push=<n>` and `newest=<stamp|->`, nor a bare second `WAKE RUN` or `WAKE BRANCH`, nor a `WAKE LOCK` with `state=<held|free|->`; each line appears once, in the `<owner>/<repo>` form with the fields the tool prints.

# nova-sprint: the sprint table

Four tables on nova-table, the mechanical moves between them, the notifications
that bring the coordinator its decisions, and the verbs. The coordinator decides;
the system moves cards without mistakes and tells the coordinator what needs it.

## 1. The tables

```
SPRINT TABLE

3011/33011 9.1% -> ETA

work  | waiting | ready | working | review | merging | landed
readers | asked | reading | ok | broken
merge | queued | merged | stuck | ci | state | since
fleet | ready | working | done | ok% | status | load
```

| table | rows | members | bookkeeping for |
|---|---|---|---|
| work | streams | primaries | the units of work, driven to landed |
| readers | readers | read cards | the reads of primaries in review |
| merge | streams | primaries | merging, made visible |
| fleet | fleet members | work cards | the swarm across machines |

The view shows work, readers, merge, fleet in that order, with the summary line
landed / all primaries, percent, ETA. Every count cell is an ordered set.

Each table keeps its member records under a prefix of its own, so a primary's
record in work and its record in merge are separate. One deployment's tables,
view and keys all carry one prefix (`--prefix`), so two sprints share a store
without touching each other.

Beside the columns shown, the merge table has two hidden columns: `returned`,
where a primary sent back from merging waits (the table layer never places a
removed member again, so an accept after a return moves it back), and `ctl`,
where each stream's control card holds the stream's state, cause, ci and
`since`. The fleet table has a hidden `ctl` column where each member's control
card holds its status and its ok and failed counts. `done` holds a member's
finished work cards, ok and failed; `ok%` (the column `okpct`, labelled `ok%`)
is ok / (ok + failed) from the member's counts. The text cells (ci, state,
since, ok%, status, load) are display copies of the control cards, written
after each step; the control cards are written with the moves.

## 2. The cards

**Primary.** One unit of work, between an issue and a pull request. One stream
for life. Fields: stream, score, brief, needs, head, attempt, fix, work (its live
work card), asked (its readers), readers (the two whose ok it was accepted on),
counters (failed, reworks, broken_reads, stuck, returns), and the last CI
observation (ci, ci_head, ci_run, ci_source).

**Work card** (consumer). What a child with a worktree is handed: the brief and
the place to work, and on a later attempt the fix. Identity `<primary>.w<attempt>`.
Fields: primary, stream, kind=work, attempt, fix, member, gen (its assignment
generation), ok (set only when finished), head, report. It takes its primary's
score. The primary names its live work card.

**Read card.** One reader's read of one primary at one attempt. Identity
`<primary>.r<attempt>.<reader>`. Fields: primary, stream, kind=read, reader,
attempt, head, verdict, finding. It takes its primary's score. A read card
exists because a member has one place per table and a primary has two readers
at once.

## 3. The lifecycle of a primary

Six states, fixed, in one Go file (`internal/sprint/lifecycle.go`) mirrored by
the TLA+ model (`tla/SprintTables.tla`): waiting, ready, working, review,
merging, landed. Landed is final and means the code is on the development
branch. A primary that stops any other way leaves the table; its record,
outcome and reason are kept.

| move | cause | class |
|---|---|---|
| waiting -> ready | everything it needs has landed | mechanical |
| ready -> working | start: a work card is cut and dealt | mechanical |
| working -> review | its work card finished, ok or failed | mechanical; failed notifies for judgment |
| working -> ready | its work card was withdrawn because no fleet member is up | mechanical, notifies |
| review -> merging | accept: two different readers said ok at this head | the coordinator's verb; refused without the two |
| review -> working | rework with a fix: the next attempt is delegated at once | the coordinator's verb |
| review -> ready | rework with a fix when no fleet member is up | the coordinator's verb |
| merging -> review | the stream's CI went red and the coordinator sent it back, or return | the coordinator's verb |
| merging -> landed | its batch, green on the stream branch, merged to the development branch | mechanical |
| any open state -> off the table | drop, with the reason | the coordinator's verb |

A primary is working if and only if it has a live work card. Nothing retries by
itself. Nothing leaves review except by the coordinator.

## 4. Order

A score is given once, at admission. Work cards, read cards and the merge place
copy it. No move changes it. A reworked primary therefore sits ahead of the
primaries admitted after it. Only `rank` changes a score, every copy with it,
and it is the coordinator's decision, receipted.

## 5. The fleet

- start deals each work card to the up member with the shortest ready queue.
- A member going down: its unfinished work cards are dealt to up members.
- No member up: unfinished work cards are withdrawn; primaries return to ready.
- A member coming up: ready queues are levelled in one call; the newest cards move.
- ok% is computed from the member's ok and failed counts behind done. load and
  status are reported, never typed by the coordinator except `fleet up|down` to
  override.

Every work card carries an assignment generation bound to its identity, attempt
and member. It is 1 when the card is cut and changes on every redeal, drain,
level move and withdrawal. A take by id and every finish name the generation
the worker holds (`<card>@<gen>`); one that names none is refused, and one
whose generation is not the live one is refused as stale and changes nothing.
A take by selection (`--as` and `--limit`) takes the live generation; a finish
by selection without `--as` is refused. A finish that arrives first moves the card to
done, which no redistribution touches. A retried finish with the same operation
id (`--op`) returns the original result, with no second counter or notification.

## 6. The readers

- ask deals every primary in review that lacks reads to TWO DIFFERENT readers,
  each to the shortest asked queue, keeping order. One read card per reader.
  Work that came back failed is not read: it waits for the coordinator.
  `ask --another` deals a primary already asked to one more reader.
- A reader moves its own read cards: asked -> reading -> ok | broken, with the finding.
- A primary is acceptable when two different readers have an ok read card at
  its current attempt and head. One reader's ok alone is never enough, whoever
  the reader.
- A primary in review whose reads are exhausted (asked, with no read card
  asked or reading, without two different readers' ok at its head, and with no
  open judgment) is a judgment, "reads exhausted", written by the step that
  causes the condition (a read, or an ack of its last judgment).
- A broken read notifies the coordinator. rework sends the primary back with
  the finding as the fix and delegates the next attempt at once (section 3);
  the primary's read cards are retired in the same step and its readers are
  kept on it. When the fixed work returns, both readers are asked again at the
  new head, on new read cards of the new attempt. A report against a retired
  read card is refused, naming the retirement.

## 7. Merging

1. In work order, never random: the head of the stream's queued cell first.
2. In batches onto one branch per stream; the batch is proved there; a green
   batch merges to the development branch once.
3. When a merge is not possible or not easy the stream stops and the
   coordinator is told. The coordinator may act on the card, the stream, or
   several streams together.

accept places the primary in merge `queued` with its score, and retires, in
the same step, every read card of the primary still asked or reading (marked
retired by accept): a report against it is refused, naming the retirement. A batch moves
queued -> merged only when it has landed; merged only grows and equals work
landed. A card that cannot merge goes to stuck and its stream stops. A stuck
card is a barrier: the merge step never passes an earlier stuck or queued card.

| stream state | means |
|---|---|
| waiting | nothing queued |
| merging | batches are moving |
| stopped | it needs the coordinator; ci and the notification say why |
| landed | every primary of the stream has landed |

Every step that changes what is queued keeps the stream's state true: accept
makes a waiting stream merging; the merge step, return and drop make a merging
stream with nothing queued or stuck waiting, and a stream landed when every
primary of it left on the table has landed. A stopped stream stays stopped
until it resumes. accept closes the open card judgments of the primaries it
accepts.

`since` is the clock time the state last changed. The merge step is mechanical
and is given its facts by the caller (what merged, what conflicted, ci result);
it never decides. Causes of a stop: conflict on a card; stream branch red; a
card needs a card of another stream first; the merge queue rejected.

A cross-stream need is recorded as data on the stuck card (the needed card and
its stream); it is resolved when that card has landed, and ranking the needed
card is not landing it. The notification names both streams and both cards.
`resume` moves the stream's stuck cards back to queued at their unchanged scores
and sets the stream merging; it is refused while a cause is unresolved, naming
it. The other causes are resolved by the coordinator, who says what was done
(`resume --did`); after a red stream branch `--did` is required, and a resume
without it is refused. A cross fact is refused unless the other card is placed,
in another stream, and not landed. `return` sends a merging primary back to review (off queued
or stuck), from where it is reworked, dropped or accepted again.

## 8. Notifications

One stream of notifications, written by the same step as the move that caused
it and visible at that step's logical commit (section 10), never before; read
from the coordinator's cursor. Two kinds.

**happened**: no decision. stream started merging; batch landed; stream landed;
two readers said ok (ready to accept); work came back ok; fleet member up or down;
cards returned to ready because no member is up; ci green on a primary; an
operation was abandoned.

**judgment**: needs the coordinator. Each names the decisions open to it.

| notification | decisions |
|---|---|
| work came back failed | rework with a fix, drop |
| a reader found it broken | rework with the finding, ask another reader, drop |
| stream stopped: conflict on a card | resolve and resume, rework, drop |
| stream stopped: stream branch red | take the suspect off and resume, rework the suspect |
| stream stopped: needs a card of another stream first | rank that card first, wait, look at both, return, drop |
| stream stopped: the merge queue rejected | resume, return, drop |
| ci red on a primary | rework with a fix, return, drop, look |
| a primary came back a second time for the same cause | stop and look |
| a primary is blocked on something dropped | replace, drop |
| reads exhausted | ask another reader, rework, drop |
| a judgment notification has waited past its due time | act |
| a stream has made no progress past its deadline | look |

A CI result, red or green, recorded for a primary in any state is always a
notification (red: judgment; green: happened). `ci` records the observation
(primary, head, run, status, source) and moves no card; a result for a head that
is not the primary's current one is labelled as such; a retried report of the
same run is recorded once. Stream-batch CI in merging is the merge step's fact.

Each carries: id, kind, type, stream, the primaries (a set, bounded, with the
count), what happened, who reported it, attempt, how many times before, the
clock time, the decisions. Notifications of one type, stream and cause are
grouped into one line with a count. Marked ones (repeats, overdue) sort first.
A primary that came back a second time for the same cause is marked on the
notification of that cause, and "stop and look" is added to its decisions.

A judgment is open per card and per cause. A verb discharges only the
obligations it actually resolved: rework resolves a failure, a broken read and
a red CI on the primaries it reworks; drop resolves everything on the primaries
it drops, and accept every card judgment of the primaries it accepts; ask --another resolves a broken read; a green CI on the current head
resolves a red one; return resolves a red CI on the primaries it returns, and
answers its stream's red or rejected batch (recorded; that judgment stays open
while the stream is stopped); resume resolves the stream's stop.
`ack <notification> --reason <text>` says the coordinator looked and nothing is
to be done ("look", "act"): it closes that judgment and records the reason. It
is refused for the judgment of a stopped stream while the stream is stopped. Acting on one card of a
group leaves the rest of the group open. A stopped stream keeps an open
judgment until it is no longer stopped. `--answers <notification>` names what a
verb answers and is refused for a notification the verb resolves nothing of;
each answer is recorded as a `decided` notification. `wait <notification>`
records a next review time; it does not hide the notification. Reading the
inbox or advancing the cursor resolves nothing.

`inbox` always shows every open judgment, whatever the cursor; the cursor only
bounds the happened and decided lists. Each judgment has a due time, its time
plus the deadline or the review time `wait` set, and overdue is computed at
read time from the clock, so a dead coordinator is visible to anyone who runs
inbox. Each stream has `since` (the last change of its state) and `progress`
(the last change of its state or of any of its counts); a stream that has not
landed and whose progress is older than its deadline is shown as stalled by
inbox and where. This is pull visibility; nothing claims to detect a dead
process.

## 9. What is always true

Checked by `nova-sprint check`, and by the model. Sets of primaries are compared
exactly, member by member, never by their counts.

1. A card is in one place in each table it is in.
2. Primaries in work working and live work cards (fleet ready + working) are a
   bijection: each primary in working has exactly one live work card, the one it
   names, and each live work card has exactly one primary in working.
3. Primaries with read cards in asked or reading are in review.
4. Primaries in merge queued + stuck = primaries in work merging.
5. Primaries in merge merged = primaries in work landed.
6. No primary enters merging without ok read cards from two different readers at its head.
7. A score never changes except by rank: every copy has its primary's score.
8. No card lost or made twice: a card in flight names a primary on the table,
   and a primary has at most one live work card.
9. A stopped stream has an open judgment notification.

Rules 2, 3, 4, 5 and 9 hold whenever no operation is pending; 1, 6, 7 and 8
always. A rank is the one step that changes scores: while a rank is pending, a
copy may carry the rank's own new score, and any other difference breaks
rule 7. `check` reports a pending operation: in flight while it is younger than
the grace, cut after it.

## 10. Steps that touch more than one table

The table layer applies one table per batch. Every mutating verb is one
operation, whatever it touches, under one durable sprint-wide fence: a
pending-operation record naming every participant table and its manifests, in
a fixed order (fleet, readers, merge, and the work table last), each a phase
that is idempotent by its operation id.

Every mutating verb reads the fence in the same read as its pre-state (before
and after it reads the tables), before evaluating any guard: if an operation is
pending, the verb first finishes it (repair) and reads again, or, when that
operation's writer is still at it or it cannot finish, refuses naming it; it
never acts on a partial state. The verb takes the fence only at the generation
it read, so no other operation applied anything since its read. Its manifests
apply in order; a table's changes over the table layer's bound are several
manifests of that table under one operation id family, so a verb over a large
set is one invocation. The release of the fence is the logical commit: in one
step it writes the notifications, opens and closes the judgments, records the
streams' progress and the caller's result, and empties the fence.

`nova-sprint repair` finishes a pending operation from its record: it applies
only the phases not yet applied (an applied phase replays its receipt), each
still guarded by its own expectations, so it can never overwrite newer work.
An operation whose first phase never applied is abandoned after the grace:
nothing of it happened, and a notification says so (happened: an operation was
abandoned, which verb, by whom, how old). Nothing is abandoned silently. `check` and `where` show a pending operation. The model
includes the cut between every two phases. A multi-table batch in the table
layer retires this section.

## 11. Verbs

Each takes a set and is one step. A set is ids, a stream, a column, `--limit n`,
or an inbox group (`--group n`). Each prints what moved, what did not and why,
and the summary line. Every judgment verb takes `--answers <notification>`.
Every store verb takes `--redis`, `--prefix`, `--actor`, `--op <id>` (the same
id again returns the recorded result), `--json` and `--max`.

| verb | does |
|---|---|
| init | creates the four tables and the view; `--readers`, `--members` |
| add | admits primaries into a stream: waiting if they need something, else ready; `--count n` generates ids |
| resolve | waiting -> ready where needs have landed |
| start | ready -> working, cuts and deals work cards; `--limit n`, a stream, or ids |
| take | a worker moves work cards fleet ready -> working; `--as <member>`, `<card>@<gen>` |
| finish | work cards done ok or failed; primaries to review; `--as <member>`, `<card>@<gen>` |
| ask | deals primaries in review to two different readers; `--another` |
| queue | a reader's read cards or a member's work cards, oldest first (`--as`), or a stream's merge queue (`--stream`) |
| read | a reader records ok or broken with the finding; `--as <reader>`, `--begin` |
| accept | review -> merging and into merge queued; refused without two readers; named ids all or nothing, a selection moves the eligible |
| rework | delegates the next attempt at once with a fix; ready when no member is up |
| return | merging -> review, off the merge queue |
| drop | off the table with the reason |
| rank | changes a score and every copy |
| merge | one mechanical merge step for a stream: `--batch n`, given facts |
| resume | a stopped stream moves again, with what was done; refused while a cause is unresolved |
| fleet | `up|down <member>`, `level` |
| reader add | declares readers |
| ci | records a CI observation for primaries in any state |
| wait | sets a judgment's next review time |
| ack | closes a judgment the coordinator looked at, with the reason |
| inbox | every open judgment and the notifications since the cursor, grouped, judgment first; `--open n`, `--read` |
| card | everything about one primary |
| check, repair | section 9 and section 10 |
| where | the view, once or `--watch`, with a pending operation and stalled streams |
| play | plays the world outside the table through these verbs, seeded (section 12) |
| teardown | drops the tables, the view and every key under the prefix; `--confirm <prefix>` |

The read verbs (queue, where, inbox, card, check) have `--json`, one object for a
program.

## 12. The driver

`nova-sprint play` plays the outside world on a tick (`--every`), seeded
(`--seed`) so a run repeats: workers taking and finishing work cards (`--fail`),
readers reporting read cards (`--broken`), each stream's merge step with its
facts (`--batch`, `--stuck`, `--cross`, `--red`), members going down and up
(`--flap`), and the mechanical moves that need no decision (resolve, ask,
level, and start with `--start`). Everything it does is a nova-sprint verb run
through the command's own entry point, printed as the line to type with its
summary shortened; what it knows it reads from the read verbs' `--json`. It
never runs accept, rework, drop, rank, return or resume. It keeps running while
things wait for the coordinator, says what waits and for how long, tolerates
the coordinator writing at the same time, and stops when every stream has
landed. Its facts come through one interface (a worker's result, a reader's
finding, a merge batch's outcome, which members are up); the seeded source is
one implementation.

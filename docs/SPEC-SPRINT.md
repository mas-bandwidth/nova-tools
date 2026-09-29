# nova-sprint: the sprint table

Four tables on nova-table, the mechanical moves between them, the machine that
makes them once a second, the notifications that bring the coordinator its
decisions, and the verbs. The coordinator decides; the system moves cards
without mistakes and tells the coordinator what needs it.

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

The view shows work, readers, merge, fleet in that order. The one line under
the title is the word `STOPPED` when the machine is stopped, and the summary
line (landed / all primaries, percent, ETA, with no machine text) when it is
running; a RUNNING machine that has not ticked for 5 s shows
`STOPPED (no tick for Ns)`. The coordinator is not printed in the view
(`where --json` carries it). Every count cell is an ordered set.

Each table keeps its member records under a prefix of its own, so a primary's
record in work and its record in merge are separate. One deployment's tables,
view and keys all carry one prefix (`--prefix`), so two sprints share a store
without touching each other.

Beside the columns shown, the merge table has two hidden columns: `returned`,
where a primary sent back from merging waits (the table layer never places a
removed member again, so an accept after a return moves it back), and `ctl`,
where each stream's control card holds the stream's state, cause, ci and
`since`. The fleet table has a hidden `ctl` column where each member's control
card holds its status, a hidden `withdrawn`
column where a work card withdrawn because no member was up is kept (the table
layer never places a removed member again), and hidden `ok` and `failed`
columns that hold a member's finished work cards, finished ok and finished
failed. `done` and `ok%` are the table's own formulas over those two cells,
computed at render and never written: `done:sum(ok+failed)` and
`okpct:pct(ok/ok+failed):pooled:ok%` (the column `okpct`, labelled `ok%`). A
member with no finished card shows `0` and `0.0%`; the footer pools ok% over
the members (every ok over every finished card, never a mean of the members'
percentages). The text cells (ci, state, since, status, load) are display
copies of the control cards, written after each step; the control cards are
written with the moves.

## 2. The cards

**Primary.** One unit of work, between an issue and a pull request. One stream
for life. Fields: stream, score, brief, needs, head, attempt, fix, work (its live
work card), asked (its readers), readers (the two whose ok it was accepted on),
counters (failed, reworks, broken_reads, stuck, returns), and the last CI
observation (ci, ci_head, ci_run, ci_source).

**Work card** (consumer). What a child with a worktree is handed: the brief and
the place to work, and on a later attempt the fix. Identity `<primary>.w<attempt>`.
Fields: primary, stream, kind=work, attempt, fix, member, gen (its assignment
generation), dealt and taken (the clock times it was dealt and taken),
first_dealt (the attempt's first deal, kept through every redeal), ok (set
only when finished), head, report. It takes its primary's score. The primary
names its live work card.

**Read card.** One reader's read of one primary at one attempt. Identity
`<primary>.r<attempt>.<reader>`. Fields: primary, stream, kind=read, reader,
attempt, head, asked and begun (clock times), verdict, finding. It takes its
primary's score. `queue` shows each card's times. A read card
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
| waiting -> ready | everything it needs has landed or was waived | mechanical |
| ready -> working | deal: the machine's tick cuts and deals a work card | mechanical |
| working -> review | its work card finished, ok or failed | mechanical; failed notifies for judgment |
| working -> ready | its work card was withdrawn because no fleet member is up | mechanical, notifies |
| review -> merging | accept: two different readers said ok at this head | the coordinator's verb; refused without the two |
| review -> working | rework with a fix: the next attempt is delegated at once | the coordinator's verb |
| review -> ready | rework with a fix when no fleet member is up | the coordinator's verb |
| merging -> review | the stream's CI went red and the coordinator sent it back, or return | the coordinator's verb |
| merging -> landed | its batch, green on the stream branch, merged to the development branch | mechanical |
| any open state -> off the table | drop, with the reason | the coordinator's verb |
| waiting -> landed | release of a reached sentinel, and only that | the coordinator's verb |
| ready -> waiting | add inserts a sentinel in front of it | mechanical |

The store holds every step's plan to this table before it applies it, whatever
step built it: a primary is admitted waiting or ready, moves only by a row of
the table, and leaves only from an open state; a unit that does not is refused,
naming the move. A step refused whole writes nothing, not even the rows of a
stream it would have declared. A primary leaves waiting only when every need has landed or
was waived: a step that may move a primary to ready builds its plan on the
snapshot it read, and the lifecycle judges the needs against it; a plan
without it moves nothing waiting to ready. The lifecycle is judged first on
every unit of a plan; the needs rule is then judged against the landings of
the units the lifecycle kept only, so a landing the lifecycle refuses
satisfies no need. A work-table move whose expectation names no place is
judged from where the plan's pre-state places the card, and is refused when
the pre-state does not hold it. A primary is working if and only if
it has a live work card. Nothing retries by itself. Nothing leaves review
except by the coordinator.

## 4. Order

A score is given once, at admission. Work cards, read cards and the merge place
copy it. No move changes it. A reworked primary therefore sits ahead of the
primaries admitted after it. Only `rank` changes a score, every copy with it,
and it is the coordinator's decision, receipted.

## 5. The fleet

- The machine's tick deals ready primaries, oldest first by score, each to the
  up member with the shortest ready queue, no ready queue longer than two
  (MaxReadyPerMember): work is dealt late and little.
- A member going down: its unfinished work cards are dealt to up members.
- No member up: unfinished work cards are withdrawn (kept in `withdrawn`);
  primaries return to ready. The tick deals the same card again at a new
  generation; the attempt advances only on rework. A primary with a withdrawn
  card is ready, never working.
- A member coming up: ready queues are levelled in one call; the newest cards move.
- done and ok% are computed by the table from the member's `ok` and `failed`
  cells.
- A fleet member says it is there by beating: `nova-sprint fleet beat
  <member>`, run on the machine every few seconds, writes its last beat time
  (to the second) and its load in one write of its own record, outside the
  tables, whether the machine is RUNNING or STOPPED. The load is the machine's
  CPU busy percent of all its cores, measured between beats; where that cannot
  be measured, the one-minute load average over the logical cores, capped at
  1000%. `--load <percent>` gives it instead.
- A member's status is derived, never typed: up while its last beat is at
  most 15 s old, down past that or when it has never beaten, and held while
  the coordinator holds it, whatever it beats. `fleet down <member>` holds a
  member and takes it down; `fleet up <member>` releases the hold, adding a
  member the sprint does not know, and brings it up at once when its beat is
  fresh.
- The tick's first part (presence) applies one change of derived status a
  tick, ups first: a member going down has its unfinished work cards dealt to
  the members up, or withdrawn when none is; a member coming up levels the
  ready queues; each change writes one happened notification that says why.
  While STOPPED, beats are accepted and the fleet's cells show the derived
  status, but nothing is dealt; the first tick after `start` applies what
  changed.
- The status cell shows held, up or down. The load cell shows the highest load
  of the last 10 s with one decimal while the beat is fresh, and is empty
  otherwise, never a zero.
- A beat from a machine the sprint does not know writes one happened
  notification, "an unknown machine is beating: <name>; add it with nova-sprint
  fleet up <name>". Teardown removes every beat record.

Every work card carries an assignment generation bound to its identity, attempt
and member. It is 1 when the card is cut and changes on every redeal, drain,
level move and withdrawal. A take by id and every finish name the generation
the worker holds (`<card>@<gen>`); one that names none is refused, and one
whose generation is not the live one is refused as stale and changes nothing.
A take by selection (`--as` and `--limit`) takes the live generation; a finish
by selection without `--as` is refused. A finish that arrives first moves the card to
the member's `ok` or `failed` cell (counted in done), which no redistribution touches. A retried finish with the same operation
id (`--op`) returns the original result, with no second counter or notification.

## 6. The readers

- ask deals every primary in review that lacks reads to TWO DIFFERENT readers,
  each to the shortest asked queue, keeping order. One read card per reader.
  The machine's tick asks for every such primary; `ask` is the coordinator's
  own.
  Work that came back failed is not read: it waits for the coordinator.
  `ask --another` deals a primary already asked to one more reader, for that
  attempt only (the readers kept on the primary stay the pair it was asked
  of, and after a rework the two are asked again); before
  the first ask of its attempt it is refused, naming `ask` and the tick as
  what asks first.
- A reader moves its own read cards: asked -> reading -> ok | broken, with the finding.
  A report on a card still asked is accepted: it is the begin and the report in
  one step, and `begun` is stamped with it.
- The read that completes two different readers' ok at a primary's head writes
  the judgment ready to accept; accept, rework and drop close it.
- A primary is acceptable when two different readers have an ok read card at
  its current attempt and head. One reader's ok alone is never enough, whoever
  the reader. A reader counts once, and a read card counts only when the row it
  occupies, the reader its id names and its reader field are one reader; a card
  that disagrees counts for no one, and check reports it.
- A primary in review with no read outstanding, not acceptable and no open
  judgment is a judgment written by the step that causes it (a read, an ack
  that closes its last judgments, all those one call closes counted together
  and the judgment written once, or a green CI that closes its last one):
  reads exhausted when it was asked at its attempt, else stranded in review
  (failed work acknowledged, or never asked). An ack of that judgment itself
  does not write it again; ask closes stranded in review.
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
With `--batch n` the batch is the first n queued cards by score; a
`--conflict` or `--cross` fact naming a card outside it is refused, listing the
batch. The merge step that lands a card moves every waiting primary whose needs
have all landed to ready, and marks reached every sentinel whose needs have all
landed, in the same step; the machine's tick is the backstop and does neither
again.

| stream state | means |
|---|---|
| waiting | nothing queued |
| merging | batches are moving |
| stopped | it needs the coordinator; ci and the notification say why |
| landed | every primary of the stream has landed |

Every step that changes what is queued keeps the stream's state true: accept
makes a waiting stream merging; the merge step (when it lands the last queued
card of a stream not done), return and drop make a merging stream with nothing
queued or stuck waiting, with `since` then, and a stream landed when every
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
or stuck), from where it is reworked, dropped or accepted again; it opens the
judgment returned to review on the primary, and the card's cross need goes
with it. A conflict stop has no cross need, and resume checks a need only
when the stop's cause is cross. A merge step on a stream with nothing queued
(before its first stuck card) is refused and writes nothing.

## 8. Notifications

One stream of notifications, written by the same step as the move that caused
it and visible at that step's logical commit (section 10), never before; read
from the coordinator's cursor. Two kinds.

**happened**: no decision. stream started merging; batch landed; stream landed;
work came back ok; fleet member up or down; cards returned to ready because no
member is up; ci green on a primary; an operation was abandoned; a sentinel
landed, released by the coordinator; the machine started or stopped; a stream
resumed because the card it needed landed.

**judgment**: needs the coordinator. Each names the decisions open to it.

Every judgment type, the verbs that answer it, and whether `ack` is one of
them. `ack` answers only a judgment whose own decisions list it: information
to be seen. For every other judgment it is refused, and the refusal prints that
judgment's decisions as commands. For the types that list it, `ack` is still
refused when it would leave a primary held by nobody (no outside actor, no move
the tick would make, no other open judgment on it).

| notification | answered by | ack |
|---|---|---|
| work came back failed | rework (with a fix), drop | no |
| a reader found it broken | rework (with the finding), ask --another, drop | no |
| stream stopped: conflict on a card | resume (resolved), rework, drop | no |
| stream stopped: stream branch red | return the suspect and resume, rework the suspect | no |
| stream stopped: needs a card of another stream first | rank that card first (the tick resumes when it lands), wait, card (look at both), return, drop | no |
| stream stopped: the merge queue rejected | resume, return, drop | no |
| ci red on a primary | rework (with a fix), return, drop, card (look), ack (looked, nothing to do) | yes |
| a primary came back a second time for the same cause | card (stop and look) | no |
| a primary is blocked on something dropped | drop, ack (waives the dropped need) | yes |
| reads exhausted | ask --another, rework, drop | no |
| ready to accept | accept, rework, drop | no |
| returned to review | rework, accept (while its reads stand at its head), drop | no |
| stranded in review | rework, drop (and ask when never asked) | no |
| sentinel reached | release, add --before (do more before going on), drop | no |
| the sprint is done | clear, add | no |
| repair skipped changes the store refused as recorded | card (look), return, drop, rework, ack | yes |
| an operation was stuck | check, ack | yes |
| a reminder could not be delivered | goal set (a new route), goal drop, ack | yes |
| cannot ask | reader add, rework, drop, wait | no |
| no fleet member is up | fleet beat (on a machine), fleet up (releases a hold), wait | no |
| a work card is past its deadline | fleet down (the member), wait, drop | no |
| a read card is past its deadline | ask --another, wait, drop | no |
| a stream has had no merge step past its deadline | merge --stream, card (look), wait | no |
| an invariant is broken | card (look at the card), repair, wait | no |
| a judgment has waited past its due time (overdue) | a decision of the judgment, wait | as the judgment |
| a stream has made no progress past its deadline (stalled) | where, queue (look) | no |

A condition the tick keeps (cannot ask, no member up, a deadline passed, an
invariant broken; a failing reminder too) is answered for a while by
`wait <note> --for <duration>`: the judgment is closed and the condition held
until that much running time has passed (STOPPED time does not count); when it
has and the condition still holds, the tick raises it again, and when the
condition clears first the hold is closed. `wait` on any other judgment sets
its review time, which counts running time from when it was set, as every
deadline does. "The sprint is done" has no due time and is never marked
overdue.

The machine's tick writes its own judgments (section 14): cannot ask, no fleet
member is up, a work card or a read card past its deadline, a stream with no
merge step past its deadline, an invariant is broken.

The step that lands or drops the last open primary of the sprint (every
primary landed or off the table) writes one judgment, the
sprint is done: n landed, m dropped. It is written when every primary has
landed or been dropped, even with none landed; it has no due time and is never
overdue; only an add that admits a card closes it. add with a need on a
dropped primary writes the blocked judgment in the same step. The blocked
judgment names the dropped needs; acknowledging it waives those only (a need
dropped later is its own judgment), and `card <id>` shows each waived need, by
whom and when.

A primary in review is never silent. With ok reads from two different readers
at its head, some open judgment on it offers accept (ready to accept, or
returned to review). Otherwise, with nothing open on it and no read
outstanding, it is stranded in review (failed work, or never asked after its
last judgment closed) or its reads are exhausted. Acknowledging exactly that
judgment does not write it again.

A CI result, red or green, recorded for a primary in any state is always a
notification (red: judgment; green: happened). `ci` records the observation
(primary, head, run, status, source) and moves no card; a result for a head that
is not the primary's current one is labelled as such; a retried report of the
same run is recorded once. Stream-batch CI in merging is the merge step's fact.

Each carries: id, kind, type, stream, the primaries (a set, bounded, with the
count), what happened, who reported it, attempt, how many times before, the
clock time, the decisions. Notifications of one type, stream and cause are
grouped into one line with a count; a subject is listed and counted once.
Each group has an id that does not move while it is open: the id of its oldest
open notification (a stalled stream's is `stale:<stream>`); overdue marks a
group and does not split it. `inbox` prints each group's id and size (the
members a verb given the group acts on), and each judgment's decisions as the
commands that make them, one per line, with the group id, `--expect` and
`--answers` filled in; a cut list of primaries ends with
`nova-sprint inbox --open <id>`.

A red branch's notification lists the suspects the caller named
(`merge --red --suspect <id>...`, each a card of the batch), or says none was
named and gives the batch's first and last card and the command that lists
the batch; its decisions include resume with what was done. Marked ones (repeats, overdue) sort first.
A primary that came back a second time for the same cause is marked on the
notification of that cause, and "stop and look" is added to its decisions.

A judgment is open per card and per cause: a step that would open one of a
type already open on the card writes no second (a second ci red on the same
card is the one already open). A verb discharges only the
obligations it actually resolved: rework resolves a failure, a broken read and
a red CI on the primaries it reworks; drop resolves everything on the primaries
it drops, and accept every card judgment of the primaries it accepts; ask --another resolves a broken read; a green CI on the current head
resolves a red one; return resolves a red CI on the primaries it returns, and
answers its stream's red or rejected batch (recorded; that judgment stays open
while the stream is stopped); resume resolves the stream's stop.
`ack <notification> --reason <text>` answers a judgment that lists ack (the
table above): it closes that judgment and records the reason. It is refused
for the judgment of a stopped stream while the stream is stopped. Acting on one card of a
group leaves the rest of the group open. A stopped stream keeps an open
judgment until it is no longer stopped. `--answers <notification>` names what a
verb answers. It is accepted for every decision the notification itself lists
(return on a red, rejected or cross stop, since taking the suspect off is a
return; drop on a rejected, conflict or cross stop; rank on the cross stop
that names the card; ask --another on reads exhausted, which it closes: the
new read outstanding is what keeps the primary from being exhausted), and
refused for a notification the verb resolves nothing of; one refused answer
refuses the whole step, which writes nothing (a verb refused has written
nothing). Each answer is
recorded as a `decided` notification; a stopped stream's judgment stays open
while it is stopped. `wait <notification>`
records a next review time; it does not hide the notification. Reading the
inbox or advancing the cursor resolves nothing.

`inbox` always shows every open judgment, whatever the cursor; the cursor only
bounds the happened and decided lists. Each judgment has a due time, its time
plus the deadline or the review time `wait` set, and overdue is computed at
read time from the clock, so a dead coordinator is visible to anyone who runs
inbox. The deadline counts running time, as the tick's deadlines do: time the
machine was STOPPED is not counted, so a sprint stopped for hours shows
nothing overdue because of those hours. Each stream has `since` (the last change of its state) and `progress`
(the last change of its state or of any of its counts); a stream that has not
landed and whose progress is older than its deadline is shown as stalled by
inbox and where. A stream with nothing on the table (every primary dropped,
or restored empty by a clear) is waiting with no `since` and is never stale.
This is pull visibility; nothing claims to detect a dead process.

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
10. (`check` reports a pending operation under this number.)
11. A primary anywhere but waiting has every need landed or waived. A need that
    was dropped and acknowledged is recorded on the card as waived, by whom and
    when, and counts as satisfied; nothing else does. add refuses needs that
    would make a cycle, naming it.

Rules 2, 3, 4, 5 and 9 hold whenever no operation is pending; 1, 6, 7, 8 and
11 always; 5 and 6 skip sentinels, which land by release and are never read or
merged. A rank is the one step that changes scores: while a rank is pending, a
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
An entry whose expectation no longer holds (a writer outside the fence changed
its member), or that the store refuses on a bound or a rule, is skipped; the
rest applies, the fence is released, and one judgment, "repair skipped changes
the store refused as recorded", lists every skipped entry and why. Before
repair applies a cut operation's entries one by one, it judges the entries
left together by the lifecycle against a fresh read, counting the skipped ones
and those whose expectation no longer holds as not happening; an entry the
lifecycle refuses is skipped too, and the skip judgment lists it with why. A
first manifest refused past the grace is applied entry by entry, like a later
one: a card left half-moved by it is named in the skip judgment. No pending
operation blocks the sprint for good. rework, return, drop and ack of the
primary close that judgment. When the skipped entry was accept's work entry,
the primary is in review with its merge card still queued: rework and return
take that orphan card off (into returned) in the same step, so rule 4 holds
again and a later accept moves it back.
An operation is abandoned only when none of its entries applied and the table
layer holds no record of its first manifest, after the grace: nothing of it
happened, and a notification says so (happened: an operation was
abandoned, which verb, by whom, how old). Nothing is abandoned silently. An
operation pending past its grace that the machine's tick cannot finish shows on
the machine line (the tick fails and says why); the first step that writes once
repair has freed the fence, a verb's or the tick's, writes one judgment: the
operation, how long it was stuck and what repair did. Every operation's result
is recorded at its commit, under the caller's operation id or its own, in the
epoch's record of results; teardown removes those records with the epoch. A
writer whose operation another writer finished (the tick, another verb,
repair) reports the recorded result, as a replay, and is never told it was
cut. A step that loses every attempt to other writers says so and applied
nothing. `check` and `where` show a pending operation. The model
includes the cut between every two phases. A multi-table batch in the table
layer retires this section.

## 11. Verbs

Each takes a set and is one step. A set is ids, a stream, a column, `--limit n`,
or an inbox group (`--group <id>`; a group number is refused, naming the ids).
Every verb taking `--group` takes `--expect <n>`: when the group's members now
number otherwise the verb is refused, changes nothing, and names the size now
and the members added or gone; a verb given `--group` prints how many it acted
on. Each prints what moved, what did not and why,
and the summary line. Every judgment verb takes `--answers <notification>`.
Every store verb takes `--redis`, `--prefix`, `--actor`, `--op <id>` (the same
id again, for the same verb with the same arguments, returns the recorded
result; recorded for another verb or other arguments it is a conflict and is
refused), `--json` and `--max`. A card's and a control card's text fields
(brief, fix, finding, report, reason, note, return reason, ci note, did) are
at most 8 KiB, and every manifest is checked against
the table layer's bounds before anything is written, split by entries and by
bytes, so no step can wedge the sprint. Every command checks first that the
store's table function library is this build's, and refuses (exit 2) with the
command that loads it.

| verb | does |
|---|---|
| init | creates the four tables and the view; `--readers`, `--members`, `--coordinator` (the one actor who releases sentinels; default the actor) |
| add | admits primaries into a stream: waiting if they need something, else ready; `--count n` generates ids; `--sentinel <id>`, `--before`/`--after <id>` (section 16) |
| release | lands reached sentinels, the coordinator's alone, with `--reason` |
| resolve | waiting -> ready where needs have landed (the tick does it; by hand for a stuck case) |
| start, stop | set the machine RUNNING or STOPPED (section 14) |
| run | ticks once a second while the machine is RUNNING |
| tick | one tick by hand |
| take | a worker moves work cards fleet ready -> working; `--as <member>`, `<card>@<gen>` |
| finish | work cards done ok or failed; primaries to review; `--as <member>`, `<card>@<gen>` |
| ask | deals primaries in review to two different readers; `--another` |
| queue | a reader's read cards or a member's work cards, oldest first (`--as`), or a stream's merge queue (`--stream`) |
| read | a reader records ok or broken with the finding; `--as <reader>`, `--begin` |
| accept | review -> merging and into merge queued; refused without two readers; named ids all or nothing, a selection moves the eligible |
| rework | delegates the next attempt at once with a fix; ready when no member is up; without `--fix` each primary's fix is the finding of its broken read, else the report of its failed work, and a primary with neither is refused by name |
| return | merging -> review, off the merge queue |
| drop | off the table with the reason |
| rank | changes a score and every copy |
| merge | one mechanical merge step for a stream: `--batch n`, given facts; `--red [--suspect <id>...]` |
| resume | a stopped stream moves again, with what was done; refused while a cause is unresolved |
| fleet | `up|down <member>`, `level` |
| reader add | declares readers |
| ci | records a CI observation for primaries in any state |
| wait | sets a judgment's next review time |
| ack | closes a judgment the coordinator looked at, with the reason |
| inbox | every open judgment and the notifications since the cursor, grouped, judgment first; `--open <id>`, `--read` |
| card | everything about one primary |
| check, repair | section 9 and section 10 |
| where | the view, once or `--watch`, with a pending operation and stalled streams |
| play | plays the world outside the table through these verbs, seeded (section 12); refused while no machine is running |
| goal | `set`, `show`, `drop`: each person's goal and route, pushed by the tick (section 15) |
| clear | stops the sprint and clears all work in it: a new epoch (section 13); `--confirm <prefix>` |
| teardown | drops the tables, the view and every key under the prefix, of every epoch; `--confirm <prefix>` |

The read verbs (queue, where, inbox, card, check) have `--json`, one object for a
program; `queue --stream <s> --col waiting` lists a stream's waiting cards;
`card` shows each need with its state and what needs the card; where, inbox and card take `--at-epoch <n>` to read an earlier epoch as
it was. Every store verb takes `--epoch <n>`, the epoch the caller holds (a
worker's cards, from `queue`; the driver passes it on every worker's, reader's
and merge verb): a sprint at another epoch refuses the step, saying when it was
cleared and naming the new epoch.

## 12. The driver

`nova-sprint play` plays the outside world on a tick (`--every`), seeded
(`--seed`) so a run repeats: workers taking and finishing work cards (`--fail`),
readers reporting read cards (`--broken`), each stream's merge step with its
facts (`--batch`, `--stuck`, `--cross`, `--red`), members going down and up
(`--flap`: a member's machine falls silent, stops beating, and beats again
later; `--hold` plays those as the coordinator's hold instead;
`--silent <member>@<from>+<for>` silences one member for a while). The driver
beats every member it plays. The mechanical moves are the machine's (section 14): the driver
plays only the outside actors, and refuses to play (exit 2) while no machine is
running. Everything it does is a nova-sprint verb run
through the command's own entry point, printed as the line to type with its
summary shortened; what it knows it reads from the read verbs' `--json`. It
never runs accept, rework, drop, rank, return, resume, release, resolve, ask,
start, stop, tick or run. It keeps running while
things wait for the coordinator, says what waits and for how long, tolerates
the coordinator writing at the same time, and stops when every stream has
landed (every primary on the table landed). It reads a stream's merge queue just before that stream's merge step,
and the other streams' queues only when a fact needs them; `--flap` silences an
up member's machine and brings a silent one back with the same chance; with
`--hold` the driver releases every hold it took before it stops. Its facts come through one interface (a worker's result, a reader's
finding, a merge batch's outcome, which members are up); the seeded source is
one implementation.

## 13. Epochs and clear

The four tables are bound to one epoch of the sprint (the table layer's epoch
key, one hash under the sprint's prefix). Every step reads, writes and names
its keys at the epoch it started at: the notification stream, the open
judgments, the cursor, the operation records and the fence are per epoch.
Because the table layer binds every card record to its epoch, a card is held at
an epoch by a stored id of that epoch, and a card id is used again in a later
epoch.

`nova-sprint clear` stops the sprint (the machine is set STOPPED first and left
STOPPED) and clears all work in it: it finishes a
pending operation, or abandons it with the old epoch, then advances the epoch
once, atomically, recording when and the shape to restore. It deletes nothing.
At the new epoch every table is empty with the same rows (streams, readers,
members), every stream waiting, every member with its status and no work
counted; these are written at the new epoch in the same verb, and a clear cut
before they are is finished by the next clear. The old epoch stays where it is
and readable (`where`, `card` and `inbox --at-epoch <n>`), and every writer
still holding it is refused as stale: nothing of the old epoch lands in the
new one. Operation ids and notification ids carry their epoch (`~<n>` after
the first): a caller's operation id recorded at an earlier epoch is refused,
naming the epoch, and never run again as new work; `ack` and `wait` of a
judgment of another epoch are refused, naming it; and a step given
`--answers` naming a judgment of another epoch is refused whole: nothing
moves, and the refusal names the id's epoch and when the sprint was cleared.
Every command builds its store pinned to the sprint's epoch, so no verb after
a clear touches the old epoch; a writer caught mid-step by a clear is told the
sprint was cleared. clear reads the shape it restores after the advance, and a
restore the last clear owes is performed first by the next step that reads
the sprint. The machine's records (its state, its STOPPED spans, the heartbeat)
and the coordinator are the sprint's, not the epoch's: a clear keeps them. A
tick in flight at a clear holds the epoch of its read: its next part is refused
as stale, writes nothing, and the tick stops there; `run` goes on at the new
epoch, where the machine is STOPPED until `start`. clear prints the epoch
before and after, what the old epoch held as counts, the machine's state
before, and the sprint line.

## 14. The machine

The machine has two states, RUNNING and STOPPED, held in one record in the
store; a new sprint is STOPPED. `start` sets RUNNING, `stop` sets STOPPED;
setting the state it has changes nothing and says so; each change is a
happened notification (who, when), and the store keeps every STOPPED span and
their total. `run` is the process that ticks once a second (TickEvery) while
RUNNING and does nothing while STOPPED; `tick` is one tick by hand. The state
is read at the start of each tick and before each of its parts: after `stop`
returns STOPPED no part begins, and the part in flight finishes. Every verb works in both states; only the tick's duties
wait. The sprint line of every verb and `inbox` says
`machine: running`, `machine: running (catching up: <n> moves due)`,
`machine: STOPPED`, or `machine: STOPPED (no tick for Ns)` when the state is
RUNNING and nothing has ticked for 15 s (MachineSilence); a failed tick keeps
its error on the heartbeat, with the count of failed ticks in a row, and the
line shows it. A tick that did nothing writes the heartbeat at most once every
5 s (HeartbeatIdleEvery); a STOPPED machine's tick only records that it
looked. `where` shows the same
state as the one line under its title (section 1).

A tick first finishes an operation pending past its grace (T5). It then reads
the fence and the tables' shapes. It reads what is due from the state whenever
it reads the whole sprint, and it reads the whole sprint when any table's
revision changed, after `start`, after a tick that did not finish (a part that
lost to other writers, a stale epoch, a halt, a failure, or moves left past a
bound), and once every minute (TickFullEvery); otherwise it does nothing
else. Otherwise it runs its parts in order, each one operation of the
engine on a fresh read, sharing the fence with every verb: resolve (T1:
every stream's waiting cards in score order; a card whose needs have all landed moves to ready; a sentinel is never
moved, and is marked reached when all it needs has landed), resume (T7: a
stream stopped only on a cross need whose card has landed), deal (T3), level
(T4), ask (T2: two different readers for each primary in review with no read
card at its attempt and work not failed), check (T6), deadlines. Each part is
bounded per tick (200 moves, 50 notes): the rest are due, the next ticks
catch up, and the machine line says so. A card made ready is dealt in the same
tick. Running a tick twice in a row changes nothing the second time.

The tick writes a judgment once while its condition holds and closes it when
the condition clears: cannot ask (fewer than two different readers are free;
one condition per primary whatever its count of free readers),
no fleet member is up, a work card past its deadline (15 minutes dealt and not
taken, 2 hours taken and not finished, or 2 hours from its first deal and not
finished, however often it was dealt again), a read card past its deadline (30
minutes asked and not begun, 2 hours begun and not reported), a stream with no
merge step past its deadline (30 minutes), an invariant is broken (the rule
and the cards). Deadlines count running time: time spent STOPPED does not
count. A judgment the tick keeps is answered by its decisions or held by `wait`
(section 8), never by `ack`, except a failing reminder, whose ack is held on
the condition in no inbox until the condition clears and comes back.

Every open judgment is due 10 minutes of running time (DeadlineJudgment) after
it was written, or at the review time a `wait` set; when that passes, the tick
writes one overdue line naming it and marks it held, and marks it again only
after the mark was lifted (the judgment closed, or a wait moved its due time
on).

`inbox` computes, from the machine's record and at read time as it computes
the stale line, three groups no notification holds: the machine is not
ticking (RUNNING and no tick for 15 s: its run loop is not running; the
commands start the loop or stop the machine), the tick keeps failing (three
failed ticks in a row, with the last error), and the machine is STOPPED and
moves are due (primaries ready, work cards withdrawn, waiters whose needs have
landed; the command is `start`). `init` writes the machine STOPPED from the
start, so the time before the first `start` is a STOPPED span and counts
toward no deadline. `clear` writes, at the new epoch, the happened line that
the machine is STOPPED by the clear.

## 15. Reminders

The people who work on a sprint each have a goal: a text of what to keep doing,
and a route that reaches them (`goal set <name> --file <path> --to
file:<absolute path>`, `goal show [<name>]`, `goal drop <name>`). While the machine is
RUNNING the tick pushes each person's goal down its route once every five
minutes of running time (RemindEvery), and at once when the goal or its route
is set or the machine starts; nothing is pushed while it is STOPPED. The file
route replaces one file with a header line (`REMINDER <n> to <name> at <time>,
sprint <prefix>, epoch <n>`) and the text, whole, so a watcher of the file sees
one current reminder. A route that fails is one judgment, "a reminder could not
be delivered", closed when a later delivery arrives. `where` shows each
person's last push. The people and their goals are the sprint's, not the
epoch's: a clear keeps them and resets their pushes.

## 16. Sentinel cards

A sentinel is a primary of kind sentinel, a stop in its stream, admitted by
`add --stream <s> --sentinel <id>`, at the end of the stream or
`--before`/`--after` a card (its score between its neighbours; refused when no
score lies between; the line is never renumbered). It waits for every primary
of its stream that sorts before it and has not landed, and for the needs it
names in other streams. Every primary of its stream that sorts after it waits
behind it: a card added later waits on the latest unlanded sentinel before it
(add prints `waits behind sentinel <id>`). Inserted in line, the waiting cards
behind it wait on it too, the ready ones go back to waiting, and the ones in
flight are printed as already past the stop and waited for. A card added
`--before` a sentinel is a need of it and un-reaches it. It is never dealt,
read or merged. When its needs have all landed or been waived, the step that
landed the last of them (or the tick, as the backstop) marks it reached and
writes one judgment. Only `release <id> --reason <text>`, by the sprint's
coordinator (`init --coordinator`, shown by `where`), lands it. The same step
moves what waited behind it to ready, marks reached any sentinel now due, and
always writes a notification that it landed. A dropped need blocks it like any
waiting card; ack waives the need. It counts in the sprint line and in waiting
and landed, and the sprint is not done while one waits. A ready primary whose
work card was withdrawn (no member up) stays ready when a sentinel is inserted
in front of it, and is waited for as past the stop: check's rule 2 holds that
the primary of a withdrawn card is ready.

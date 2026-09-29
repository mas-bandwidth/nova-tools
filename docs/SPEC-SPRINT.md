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

## 2. The cards

**Primary.** One unit of work, between an issue and a pull request. One stream
for life. Fields: stream, score, brief, head, attempt, fix, counters
(failed, reworks, broken_reads, stuck, returns), readers (who said ok at head).

**Work card** (consumer). What a child with a worktree is handed: the brief and
the place to work, and on a later attempt the fix. Identity `<primary>.w<attempt>`.
Fields: primary, stream, kind=work, attempt, fix, ok (set only when finished),
head, report. It takes its primary's score. The primary names its live work card.

**Read card.** One reader's read of one primary at one head. Identity
`<primary>.r<attempt>.<reader>`. Fields: primary, stream, kind=read, reader,
head, verdict, finding. It takes its primary's score. A read card exists because
a member has one place per table and a primary has two readers at once.

## 3. The lifecycle of a primary

Six states, fixed, in one Go file mirrored by the TLA+ model:
waiting, ready, working, review, merging, landed. Landed is final and means the
code is on the development branch. A primary that stops any other way leaves the
table; its record, outcome and reason are kept.

| move | cause | class |
|---|---|---|
| waiting -> ready | everything it needs has landed | mechanical |
| ready -> working | start: a work card is cut and dealt | mechanical |
| working -> review | its work card finished, ok or failed | mechanical; failed notifies for judgment |
| working -> ready | its work card was withdrawn because no fleet member is up | mechanical, notifies |
| review -> merging | accept: two different readers said ok at this head | the coordinator's verb; refused without the two |
| review -> ready | rework with a fix | the coordinator's verb |
| merging -> review | the stream's CI went red and the coordinator sent it back, or return | the coordinator's verb |
| merging -> landed | its batch, green on the stream branch, merged to the development branch | mechanical |
| any open state -> off the table | drop, with the reason | the coordinator's verb |

Nothing retries by itself. Nothing leaves review except by the coordinator.

## 4. Order

A score is given once, at admission. Work cards and read cards copy it. No move
changes it. A reworked primary therefore sits at the head of ready. Only `rank`
changes a score, and it is the coordinator's decision, receipted.

## 5. The fleet

- start deals each work card to the up member with the shortest ready queue.
- A member going down: its unfinished work cards are dealt to up members.
- No member up: unfinished work cards are withdrawn; primaries return to ready.
- A member coming up: ready queues are levelled in one call; the newest cards move.
- ok% is computed from ok and failed cells behind done. load and status are
  reported, never typed by the coordinator except `fleet up|down` to override.

## 6. The readers

- ask deals every primary in review that lacks reads to TWO DIFFERENT readers,
  each to the shortest asked queue, keeping order. One read card per reader.
- A reader moves its own read cards: asked -> reading -> ok | broken, with the finding.
- A primary is acceptable when two different readers have an ok read card at
  its current head. One reader's ok alone is never enough, whoever the reader.
- A broken read notifies the coordinator. rework sends the primary review ->
  ready with the finding as the fix, cuts the next work card attempt into the
  fleet's ready queue, and retires the primary's read cards. When the fixed work
  returns, the primary is asked of the same two readers again.

## 7. Merging

1. In work order, never random: the head of the stream's queued cell first.
2. In batches onto one branch per stream; the batch is proved there; a green
   batch merges to the development branch once.
3. When a merge is not possible or not easy the stream stops and the
   coordinator is told. The coordinator may act on the card, the stream, or
   several streams together.

accept places the primary in merge `queued` with its score. A batch moves
queued -> merged only when it has landed; merged only grows and equals work
landed. A card that cannot merge goes to stuck and its stream stops.

| stream state | means |
|---|---|
| waiting | nothing queued |
| merging | batches are moving |
| stopped | it needs the coordinator; ci and the notification say why |
| landed | every primary of the stream has landed |

`since` is the clock time the state last changed. The merge step is mechanical
and is given its facts by the caller (what merged, what conflicted, ci result);
it never decides. Causes of a stop: conflict on a card; stream branch red;
a card needs a card of another stream first; the merge queue rejected.

## 8. Notifications

One stream of notifications, written in the same step as the move that caused
it, read from the coordinator's cursor. Two kinds.

**happened**: no decision. stream started merging; batch landed; stream landed;
two readers said ok (ready to accept); work came back ok; fleet member up or down;
cards returned to ready because no member is up.

**judgment**: needs the coordinator. Each names the decisions open to it.

| notification | decisions |
|---|---|
| work came back failed | rework with a fix, drop |
| a reader found it broken | rework with the finding, ask another reader, drop |
| stream stopped: conflict on a card | resolve and resume, rework, drop |
| stream stopped: stream branch red | take the suspect off and resume, rework the suspect |
| stream stopped: needs a card of another stream first | rank that card first, wait, look at both |
| a primary came back a second time for the same cause | stop and look |
| a primary is blocked on something dropped | replace, drop |
| a judgment notification has waited past its deadline | act |
| a stream has not changed state or count past its deadline | look |

Each carries: id, kind, type, stream, the primaries (a set, bounded, with the
count), what happened, who reported it, attempt, how many times before, the
clock time, the decisions. Notifications of one type, stream and cause are
grouped into one line with a count. Marked ones (repeats, overdue) sort first.
A judgment notification is open until a verb that answers it names it or acts
on its cards; the answer is recorded as a `decided` notification. Overdue is
computed at read time from the clock, so a dead coordinator is visible to
anyone who runs inbox.

## 9. What is always true

Checked by `nova-sprint check`, and by the model.

1. A card is in one place in each table it is in.
2. Primaries in work working = primaries of work cards in fleet ready + working.
3. Primaries with read cards in asked or reading are in review.
4. Primaries in merge queued + stuck = primaries in work merging.
5. Primaries in merge merged = primaries in work landed.
6. No primary enters merging without ok read cards from two different readers at its head.
7. A score never changes except by rank.
8. No card is lost or made twice.
9. A stopped stream has an open judgment notification.

## 10. Steps that touch two tables

The table layer applies one table per batch. A step that touches two tables
writes them in a fixed order under one operation id, the work table last, and
records the operation before the first write. `nova-sprint check` finds a step
cut short and `nova-sprint repair` finishes it from the operation record. The
model includes the cut between the two writes. A two-table batch in the table
layer retires this section.

## 11. Verbs

Each takes a set and is one step. Each prints what moved, what did not and why,
and the summary line. Every judgment verb takes `--answers <notification>`.

| verb | does |
|---|---|
| init | creates the four tables and the view |
| add | admits primaries into a stream: waiting if they need something, else ready |
| resolve | waiting -> ready where needs have landed |
| start | ready -> working, cuts and deals work cards; `--limit n`, a stream, or ids |
| take | a worker moves work cards fleet ready -> working; `--as <member>` |
| finish | work cards done ok or failed; primaries to review |
| ask | deals primaries in review to two different readers |
| queue | a reader's own read cards, oldest first; `--as <reader>` |
| read | a reader records ok or broken with the finding; `--as <reader>` |
| accept | review -> merging and into merge queued; refused without two readers |
| rework | back to ready with a fix; cuts the next work card when started |
| drop | off the table with the reason |
| rank | changes a score |
| merge | one mechanical merge step for a stream: `--batch n`, given facts |
| resume | a stopped stream moves again, with what was done |
| fleet | `up|down <member>`, `level` |
| inbox | the notifications since the cursor, grouped, judgment first; `--open n`, `--read` |
| card | everything about one primary |
| check, repair | section 9 and section 10 |
| where | the view, once or `--watch` |

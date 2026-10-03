# nova-sprint: the sprint table

Four tables on nova-table, the mechanical moves between them, the machine that
makes them as soon as a line comes on the log, the notifications that bring
the coordinator its decisions, and the verbs. The coordinator decides; the
system moves cards without mistakes and tells the coordinator what needs it.
The coordinator's day, as a runbook for whoever holds the seat, is
[SPRINT-COORDINATOR.md](SPRINT-COORDINATOR.md).

## 1. The tables

```
SPRINT TABLE

3011/33011 9.1% -> ETA

work  | waiting | ready | working | review | merging | landed | cost
readers | asked | reading | ok | broken
merge | queued | merged | stuck | ci | state
friends | ready | working | width | done | ok% | status
fleet | ready | working | width | done | ok% | status | load
```

| table | rows | members | bookkeeping for |
|---|---|---|---|
| work | streams | primaries | the units of work, driven to landed |
| readers | readers | read cards | the reads of primaries in review |
| merge | streams | primaries | merging, made visible |
| fleet | fleet members | work cards | the swarm across machines |
| friends | friends | job cards | who of the friends is here to help, and her jobs |

The work table's last column, `cost` (the owner, 2026-10-01: "can you please
add a final column to the work stream table, which is "cost". This is the sum of
each landed card's total cost for that work stream, and then a total at the
bottom."), is a stream's landed cards' cost: the sum, over its landed primaries,
of each one's total, which is, over its consumers (section 2, What a card cost),
each one's actual cost where one was reported, else its predicted one, a
consumer with neither adding nothing. It is in US dollars and cents, rounded up to the next cent (the card keeps the exact figure)
(`$1.24` for 1.2345), `-` for a stream with no priced landed card, and the footer is
the sum over the streams' cells as shown, in dollars and cents. A stream with some unpriced
landed cards shows the sum of the priced ones; `nova-sprint card <id>` and its
JSON carry the detail. The merge that lands a primary writes its total on it
(`cost`), the charged figure of the total the card carries (section 2, What a
card cost), and sets the stream's sum over all its landed primaries on the
stream's control card; `SyncMirrors` shows it in the cell. The sum is set
from the cards, never added to: a replayed merge writes the same, and `clear`
empties it with the tables.

The friends table (the owner, 2026-10-02: "add a friends table, above fleet and
below merge. friends | status for now. up/down/held"; "friends should be
configured in nova-config"; "you should use heartbeats from each friend to track
their state, and sort them alphabetically, and then by status, like with fleet";
and the same day: "please give friends in the friends table the same ready,
working, width, done, ok%, status that we have for machines, but no load, since
they don't correspond to a machine (at the moment...)"; "you can even use the
inbox/outbox standard in friend's working dirs") has one row per friend and the
fleet table's columns but `load`: `ready`, `working`, `width`, `done`, `ok%`,
`status`, with `ok` and `failed` hidden under `done` and `ok%` as the fleet's
are. Its rows are nova-config's friend rows and nothing else: `friend sync`
(`--pg`, else NOVA_PG_DSN, as nova-config takes it) copies their names into the
store's `friends` record, adding a friend the record lacks, taking off one
nova-config no longer has with her beat and her jobs, and keeping the hold of a
friend that stays; a config that cannot be read or holds no friend row is
refused (exit 3) and changes nothing.

A friend's unit of work is a job, and her jobs are the inbox/outbox standard of
her working directory, `<root>/<friend>-working` (`friend sync --root <dir>`,
else `HOME`): she works only inside it; the coordinator delivers a job as the
directory `inbox/<job>/` (its `BRIEF.md` and everything the job needs), and
only the coordinator reaches out; the friend makes `outbox/<job>/` when she
starts the job and writes `outbox/<job>/REPORT.md` when it is done, and only
she writes there. `friend sync` reads every friend's directory and writes her
job cards into the store (`friend-jobs:<friend>`): each directory under
`inbox/` is a job (a file, or a name beginning with a dot, is none); it is
`ready` while `outbox/<job>/` is not there, `working` while it is there without
`REPORT.md`, and `done` once `REPORT.md` is there, done ok or done failed by
the one rule of a report's verdict: the first line of `REPORT.md` whose key,
after any markdown marks (`#`, `*`, `-`, `_`, spaces), is `Verdict` or
`Status` in any case; its first word HOLD, FAIL, FAILED or BROKEN, in any case,
is a job done failed, and any other word, or no such line, is a job done ok. The
records are set from the directories, never added to, so a sync after a sync
writes nothing and says so; a friend with no directory, or no `inbox/`, has no
jobs; a directory that cannot be read is refused (exit 1), naming it, with
nothing written. The sync reads the directories and never writes them, and
runs where they are (the coordinator's machine), by the coordinator's loop or
by hand after a job is delivered or collected; the view reads the store, never
a directory (the card is the persistent store). `ready` and `working` count
her job cards in those states; `width` is her width, the jobs she works at
once: her nova-config friend row's `width` (`nova-config friend set <friend>
--width <n>`, at least 1, 8 by default; the owner, 2026-10-02: "6/1 seems a bit
wrong -- need to setup width for friends? Start at 8 for each?"), which friend
sync writes to her row each pass (a row whose width is below 1 is refused, exit
1, nothing written), summed in the footer; `ok` and `failed` count her jobs done; `done` is `sum(ok+failed)`
and `ok%` is `pct(ok/ok+failed)`, pooled over the friends in the footer, the
fleet table's own formulas.

A friend says she is there with `friend beat <friend>`, which her own machinery
runs every second (`FriendBeatEvery`) beside her harness (it writes
`friend-beat:<friend>`, the time to the second; a friend not in the record is
refused, exit 1). Her status is the friends' rule (`sprint.FriendStatus`):
`held` while the coordinator holds her (`friend down`; `friend up` releases the
hold), whatever she beats; else `up` while her last beat is under
`FriendAsleepAfter` (15 s) old; else `asleep`, and `asleep` when she has never
beaten. A beat wakes her at once. `friend up` is not a beat: a friend released
with no beat in the last 15 s is `asleep` until she beats. A friend's statuses
are `up`, `held` and `asleep`; `down` is the fleet's word and never hers. A
friend `asleep` shows `working` 0 in the table, its footer and `where --json`:
her jobs stay in her outbox and count again when she beats, and `ready` and
`done` are as they were (the owner, 2026-10-02 9:48 PM ET: "[a friend] being down,
she automatically is 0/8 working OK?"). The
owner, 2026-10-02 9:44 PM ET, on a friend shown up while she was gone: "two
minutes is too long. 1m", "maybe even 30 secs."; and at 9:46 PM ET: "heartbeat
should ping once every 10sec", then "or every 1sec if you really want, then
after 15 sec. asleep. better." The fleet's rule (`MissedBeatsDown` windows of
`BeatDeadline`, down past 45 s, section 5) is separate and stays longer: a
machine down has its cards taken back, a friend holds none. The
rows are in the fleet table's order (`FleetOrder`): up, then held, then asleep,
each by name. The table is drawn by `where` from those records when it draws
the frame, never stored as a table: no tick, step, epoch or clear touches it,
`teardown` deletes its records (the roster, each friend's beat and jobs), and
the stored view `sprint` has the four tables only. Its footer is the table
layer's: the sums of `ready`, `working`, `width` and `done`, the pooled `ok%`,
and a blank status cell, as the fleet table's; an empty friends table is its
header, its one rule and that footer at zero, as every empty table is.

The frame of `where` and `where --watch` shows work, friends, fleet in that order: the
readers and merge tables are hidden from it (the owner, 2026-10-02: "I feel like
reading and merging is something you can handle now. it seems to work, so please
hide the reader and merge tables."); `where --all` draws every table, work, readers,
merge, friends, fleet, and `where --json` carries every table and every row of it
with the flag or without. The one line under
the title is the word `STOPPED` when the machine is stopped, and the summary
line (landed / all primaries, percent, ETA, with no machine text) when it is
running; a RUNNING machine that has not ticked for 5 s shows
`STOPPED`, with no count of seconds. Every count cell is an ordered set.
The summary line shows `held=N` after the percent when cards are held back:
waiting behind a sentinel not released, admitted held (`add --held`), or
waiting on one of those through a need (`sprint.HeldBack`, read from the work
table's waiting cells alone; `where --json` carries it as `held`). The ETA is
over the dealable cards, the ones neither landed nor held, so loading a wave in
waiting behind sentinels leaves the estimate where it was (nova-tools#5096
item 16; the owner: "I'd like to really really load up the sprint in waiting,
and stick sentinels in"): `3/10 30.0% held=4 -> ETA 12m`. A reading process
shows the largest estimate of the last 10 s (the owner, 2026-10-01: "take
largest ETA in last 10 secs, so it is a stable value"), held over the same
cards to land: an add, a drop or a release changes the primaries on the table
or the held ones, which a landing never does, and makes the held estimate
dirty (the owner, 2026-10-02: "When you add new cards, the ETA needs to be
made dirty and recalculated."; nova-tools#5171), so the first read of `where`,
`where --json` and the dashboard after the tick that drains the change shows
the estimate recomputed over the new count at the rate measured. The verbs' sprint
line, printed after every step, reads no cards and keeps the ETA over every
card left.

The stored view `sprint` (`nova-table watch --view sprint`) says the same:
its summary line is `STOPPED`, and nothing more (no counts, no percent, no
ETA), while the machine is STOPPED, and the progress line
(`3/10 30.0% -> ETA`) while it is RUNNING. The view carries the text as its
state (`nova-table view state`): `init`, `stop` and `clear` write `STOPPED` and
`start` clears it, each in the same MULTI/EXEC as the machine's state record,
so the view and the record never disagree. A `stop` or `start` that finds the
machine in that state already writes the view's state again from the record.
The view knows no heartbeat: a RUNNING machine that has stopped ticking keeps
its progress line there, and `where` and `inbox` say it is not ticking.

A frame of the view holds the words `SPRINT TABLE`, that line and the
tables, and nothing else: no time, no pending operation, no stalled stream, no line about
the people and no coordinator (`where --json` carries them; `check`, `inbox` and
`goal show` say the same in their own words). The merge table has no `since`
column. Every table is shown, with its header, empty or not, and every
stream row is shown in the work table, at zero when it has no cards, with its
footer; a table with no row shows its header, the one rule under it and its
footer, with no second rule (the owner, 2026-10-02: "when the work stream table
is empty, please just show the summary row"). A row's first cell is its
identity. The readers table is one row, its first cell blank (the owner,
2026-10-02: "please remove 'all'"),
whose four cells are the sums over every reader (readers away or down counted
too), and it has no footer, which would say the same thing twice (the owner,
2026-10-01: "If you raise reader widths, I would like you to change the table to
just be one row, sum of all"; "i don't reallllly need to see all readers, i just
need to see reader *progress* overall got it?"). The merge table is one row, its
first cell blank,
the same way (the owner, 2026-10-01, about 21:00 ET: "Can we please (for next
sprint) do the same for merge"): queued, merged and stuck are the sums over every
stream; ci and state, which do not add up, show the value across the streams that
most needs the coordinator's eye: ci `red`, then `green`, then `-`; state
`stopped`, then `merging`, then `waiting`, then `landed`, then `-` (a word the
order does not name comes after `landed` and before `-`), and a stopped state the
number of streams stopped beside it (`stopped 1`), so one stopped stream of four
is not hidden. Both are the text of `where --all` (and `where --all --watch`) only: the
readers and merge tables keep a row per reader and per stream, `where --json` lists each,
and the stored view `sprint` drawn by `nova-table watch --view sprint` shows each.
The fleet table shows each member, with its footer. `where --watch` redraws the frame in
place once a second (`--every`, any duration above 0): the cursor is hidden
while it watches and restored when it ends or is interrupted (SIGINT or
SIGTERM: exit 0); each frame is built whole and written with one write, however
large, from the top of the screen, every line cleared to its end and the screen
below the frame cleared, so a shorter frame leaves nothing behind. Nothing
scrolls, at any size of the screen: a frame taller than the screen is cut at the
bottom, and nothing is added to say so; a line is cut to one column less than
the screen is wide; and where the size of the screen cannot be read (the output
is not a terminal) the frame is written whole. `where` without `--watch` prints
one frame, whole; `where --json --watch` prints one object a second and draws
nothing. `dashboard` serves a page that is a second view of the same JSON
([SPEC-SPRINT-DASHBOARD.md](SPEC-SPRINT-DASHBOARD.md)); the frame `where` draws stays
the canonical view.

Each table keeps its member records under a prefix of its own, so a primary's
record in work and its record in merge are separate. The tables are named
plainly: `work`, `merge`, `readers` and `fleet`, and the view is `sprint`; the
names carry no prefix and there is no flag or variable for one. A store holds
one sprint; a second sprint is a second store.

Beside the columns shown, the merge table has two hidden columns: `returned`,
where a primary sent back from merging waits (the table layer never places a
removed member again, so an accept after a return moves it back), and `ctl`,
where each stream's control card holds the stream's state, cause, ci and
`since`; the `since` cell is hidden too: the machine keeps it, `where` does not
show it. The fleet table has a hidden `ctl` column where each member's control
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
written with the moves. A step whose write committed and whose display copies
then failed to sync reports OK, for every verb, with the sync's error on its own
line, never FAIL: the table holds what the step wrote.

## 2. The cards

**Primary.** One unit of work, between an issue and a pull request. One stream
for life. Fields: stream, score, brief, needs, head, attempt, fix, finding and why (a rework's, kept
for the attempt a rework with no member up deals later), work (its live
work card), asked (its readers), readers (the two whose ok it was accepted on),
counters (failed, reworks, broken_reads, stuck, returns), returned_attempt (its
attempt when it was last returned to review, by any return, an orphan merge
card's included), and the last CI observation for its current head (ci,
ci_head) and of any head (ci_run, ci_run_status, ci_source).

**Work card** (consumer). What a child with a worktree is handed: the brief and
the place to work, and on a later attempt the fix, the finding of the broken read that
caused it and why the attempt before ended. Identity `<primary>.w<attempt>`.
Fields: primary, stream, kind=work, attempt, fix, finding, why, member, gen (its assignment
generation), dealt and taken (the clock times it was dealt and taken),
first_dealt and first_taken (the attempt's first deal and first take, kept
through every redeal and withdrawal), untaken_since (the first deal since its last take: a take unsets it, and
no redeal or withdrawal rewrites it, so a member handed the card after
someone else's take gets its own dealt bound and a flapping member cannot
reset the clock), redeals (how many times this attempt's card was dealt
again after a take of it ended without a finish, its member down or away
while the card was working; a take never resets it; a card dealt and not
taken whose member goes down is dealt again with its count kept, so a card is
retired for repeated failure at members, never for members flapping while it
sat ready; a take that ended is counted when the card is dealt again, at once
or, withdrawn because no member had room, by the deal that places it later,
and until then take_ended marks it; a working card whose member goes down
with its count at MaxRedeals, 3, stays withdrawn, its primary ready and dealt
no more, and the judgment "a card reached its bound" names it until a rework
with a fix or a drop; so the card is dealt again after each of its first three
ended takes and retired when a fourth ends; each counted redeal's
line in the log says "redeal n of 3"; a take the provider failed is an ended
take: the member's failed finish whose report begins `provider failure`
(docs/SPEC-CARD-CONTRACT.md, section 4) withdraws the card with take_ended
and returns its primary to ready, with no failed-work judgment and no count
against the primary's failed, and the next deal places it again, counting the
take; the work card keeps provider_error, the last such line (cut to 200
bytes, until the card is dealt again), and a record of each such take,
provider_take_<n> (its route, model, member, end, usage and line), which stays
through the redeals and which `card` prints as one ATTEMPT line for the take
(`end=provider failure: <line>`, `take=<n>`) before the card's own; a take whose child left no
result at all is an ended take in the same way: the member's failed finish whose report
begins `no result:` (docs/SPEC-CARD-CONTRACT.md, section 4) is withdrawn, redealt on a
route the card has not been drawn when another remains, counted against the same bound and
never judged as failed work, its record's line beginning `no result:` and its ATTEMPT line
`end=no result: <line>`; a card retired by provider failures has the
judgment "a card reached its bound" name the provider and that last error
line; tla/CardContract.tla, ProviderFailure; a launch its member refused at
staging, before any child ran, is the member's failure and never the card's:
the member's failed finish whose report begins `staging refused` withdraws the
card WITHOUT take_ended, so it spends none of the redeal bound, returns its
primary to ready with no failed-work judgment, tells the inbox "staging
refused on <member>: <reason>", and keeps a record, staging_take_<gen> (its
route, model, member, end and reason), which `card` prints as one ATTEMPT line
(`end=staging refused: <reason>`, `gen=<g>`) before the card's own; the deal,
the level and a down member's redeal never place the attempt's card on a
member that refused it, which refuses it once (one record, one note), and when every
member up has refused it the judgment "a card reached its bound" names the
members and the reason, once, and it is dealt no more until a member that has
not refused it is up, a rework or a drop; tla/CardContract.tla, StageRefused
and Restage), ok (set
only when finished), head, report. It takes its primary's score. The primary
names its live work card.

**Read card.** One reader's read of one primary at one attempt. Identity
`<primary>.r<attempt>.<reader>`. Fields: primary, stream, kind=read, reader,
attempt, head, asked and begun (clock times), verdict, finding, usage. It takes its
primary's score. `queue` shows each card's times. A read card
exists because a member has one place per table and a primary has two readers
at once.

**What a card cost.** The owner, 2026-10-01: "the producer card by the time it
gets to landed, should have the history of consumer cards that did work for it,
and their token counts, what (provider,model) tuple did the work, how much each
consumer card cost, total for producer"; "One is a guess/prediction. the other is
actual"; "No changes in sprint tables at all." The primary is the producer card;
its consumers are its work cards' takes and its read cards. Each consumer keeps
what it cost in its own `usage` field (and a take the provider failed in its
provider_take_<n> record), one line of key=value words (`internal/cardcost`,
`Usage`), so no table gains a column, line or row:

- what the member or reader reported from its child (`finish --usage`,
  `read --usage`): `wall`, `budget`, the tokens by class as the harness reported
  them (`input` uncached, `cache_read`, `cache_write`, `output`, `reasoning`),
  `requests`, `max_prompt` (the largest prompt of one request), `model` (the
  provider/model the harness ran) and `actual_usd` with `actual_by=harness` when
  the harness reported its own cost: opencode prices each message from its own
  model table and keeps it as a float, and the figure is the decimal of the
  store's float sum of those, the harness's computation and never an invoice; a
  class not reported is left out, never written as 0, and a cost is never
  guessed;
- added by the step when the card ends: `wait` (dealt to taken; a read's asked to
  begun) and `run` (taken to the end; begun to the end), in seconds; and the
  prediction: `price_route` (a work card's route by name, a pinned card's by its
  model; a read's an enabled route of the provider/model its harness reported),
  `prices` (the route's price sheet copied, `Prices.Copy`, so a later change of
  prices rewrites nothing) and `predicted_usd` (cardcost.Predict: each class's
  tokens times its price per million, reasoning at the output price when the
  sheet bills it so, the per-request fee times the requests, then the gateway
  percent; a run whose largest prompt is above the route's `long_context` is
  priced at the long prices as a whole, `long=yes`), or `unpriced=<why>` (no
  route, no price sheet, no token, a class with no price); `cost=` says which of
  the two the record holds: both, predicted, actual or none.

A finish or a read that reports usage reads the routes with its tables to price
it: the routes set and each route's record alone (`routes`, `route:<name>`), never
a tier's array or the sprint row, the keys a worker's ACL user may read
(internal/redisacl, the member role; `TestEveryStepReadsOnlyKeysItsRoleMayRead`). A failed take and a returned read record the same: they still cost tokens
and time. Each run has one record and one only: a work card's `usage` holds its
own ended take (finish), and a take the provider failed is in its
provider_take_<n> record alone, so a redealt card never counts it twice; a read
card's `usage` holds the run that gave the verdict, and each run returned
without one is kept as read_take_<n> (1 for the first), so a read asked again
of a reader keeps every run it had (asked again in place at most
`MaxReadReasks` times, a read card holds at most three such records, far
under the 64 a reader looks for).

The cost is tracked in the card (the owner, 2026-10-01: "The cost needs to be
tracked IN THE CARD"). In the same step that ends a consumer (a take finished ok
or failed, a take the provider failed, a launch refused at staging when it
reported a cost, a read ok or broken, a read returned without a verdict), the
primary gets one record of it, `cost_record:<card>#<run>` (`#g<gen>` a work
card's take, `#v` a read's verdict run, `#r<n>` a read's returned run n): its
kind, card, attempt, take, generation, member or reader, route, model, end and
time, and its usage record with the prices used. The same step updates the
primary's total, `cost_total`: each class summed over the records that reported
it, the times summed, each cost summed over the records that hold it with how
many did, who reported the actual, and `charged_usd`, each record's actual cost
where reported, else its predicted one. A record is set once per key, so a step
planned again or replayed adds nothing twice. The history is bounded at 64
records (`MaxCostRecords`): a record past it is still added to the total, and
`cost_cut` counts the records the list left out. A read's records reach the
primary from the read step, a change of the work table: while the machine runs,
it waits in the work table's queue for the next tick's pump with the read's
words, as a finish's change of its primary does. `card <id>` prints, from the
primary alone, a COST line for each record (kind, card, attempt, take, member
or reader, route, model, end, the tokens, wait, run, predicted, actual and
`actual_by`) and a COST TOTAL line (`predicted_of=<n>/<consumers>`,
`actual_by=harness`, `charged_usd`, and `cut=<n>` when the list was cut);
`--json` carries the same value as `cost`. No reader or member removed, no read
card retired and no consumer record cleaned up can lose cost: the record is
already in the card. A figure not known prints `-`, never 0.

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
| review -> merging | accept: two different readers said ok at this head | mechanical (the tick), unless its CI is red at its head or it was returned to review at its attempt; the coordinator's verb takes those; refused without the two |
| review -> working | rework with a fix: the next attempt is delegated at once | the coordinator's verb |
| review -> ready | rework with a fix when no fleet member is up or none is below its width; the tick deals it when one has room | the coordinator's verb |
| merging -> review | the stream's CI went red and the coordinator sent it back, or return | the coordinator's verb |
| merging -> landed | its batch, green on the stream branch, merged to the development branch | mechanical |
| any open state -> off the table | drop, with the reason | the coordinator's verb |
| waiting -> landed | release of a reached sentinel (or a held one that waits for nothing), and only that | the coordinator's verb |
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

- A member is a fleet machine with a width: the most work cards it runs at
  once (its child cap; `init --members m1:64` or `fleet up m1 --width 64`;
  default 64). It holds up to DealAhead (two) times its width, ready and
  working together: its width working and as many again ready behind them, so
  a lane that frees takes its next card at once (the owner, 2026-10-01: "The
  WHOLE POINT of nova-sprint is to feed the fleet at width and keep it working
  at that width until done."; "deal at most 2X width ahead per-machine in
  fleet"). The member runs its width; the rest wait in its ready column, and
  its loop takes a freed lane's next card in the same pass that reports the
  finish. The fleet table shows it in the width
  column beside working; the footer row sums the widths of the members up, the
  fleet's total width that can take a card (eight machines of 64 up total 512),
  and folds ready, working, done and ok% over the same members: a held or down
  member's numbers show on its own row and not in the total (the owner,
  2026-10-02: "width 132?!"). The row is the truth: the member's
  loop (`nova-swarm member`) reads its width with its queue every tick
  (`queue --as <m> --json` carries `width`) and runs that many; its `--width`
  is a twin's override.
- The card decides its model (the owner, 2026-10-01). A brief's line 1 names
  its tier, `tier: flash|pro|frontier` (none is flash), and a primary's `tier` field,
  written by `rework --tier`, names it instead from then on (the card is the store), and a `model:
  <provider>/<model>` header line under it pins the card, with its `tokens:
  <n>|unmetered` and `deadline: <seconds>|<duration>` lines (a pin without
  either is refused: a member with no override could not launch it);
  `cardhdr.ReadModel` is the one parser, and `add` refuses a brief whose model
  lines it cannot read. A card admitted before (an unknown tier, a pin short
  of a line) is not dealt and is judged under the tier its line 1 names. The routes are nova-config's `route` kind, applied to
  the store (`routes`, `route:<name>`): each a tier, a provider and model, a
  budget, a deadline and enabled; each tier's route array is nova-config's
  `tier` kind (`tier:<name>`, read with the routes in the same round trip), an
  ordered list of route names, a name repeated for more turns, and a tier with
  none takes its enabled routes in name order. The deal (and a redeal, and a
  rework's next attempt) resolves the card at deal time: a pin is its route
  (`pin`) and moves no index; a store with no route deals as before (the member
  runs its override); a frontier card with no pin is the coordinator's and is
  not dealt; otherwise the deal takes the array's entry at the tier's rolling
  index, the fleet table's `route_index_flash` or `route_index_pro`, a uint64
  counter modulo the array's length as the member rule's `deal_index` is, and
  moves the index by one for each card dealt, written in the deal's batch with
  its cards (tla/RouteIndex.tla, RouteIndexAdvancesOncePerCard, RouteFair); a
  redeal or a later attempt takes the next entry whose route was not taken for
  the card while another remains, the index moved past the entries it skipped
  (ExcludedNeverDrawn), and an entry that names no enabled route of the tier is
  skipped the same way. The work card keeps `route`, `model`, `tokens` and `deadline` (its
  packet hands them to the member) and the primary `routes`, every route taken
  for it. A card no route serves stays ready: the deal refuses it naming the
  tier, and the tick writes one judgment, `no route serves the tier`, per tier
  (its subject `stream:tier:<tier>`, the primaries listed), never one per
  card, closed when the tier is served (tla/DirtyTick.tla, RouteGuard; witness
  W20). The failed-work judgment names the route and model; `card <id>` prints
  an `ATTEMPT` line per attempt (route, model, member, dealt, taken, finished,
  usage, end); `routes` prints the tiers and each route with its attempts, ok, failed,
  provider failures and mean wall, each pinned model a row of its own
  (`pin:<provider>/<model>`). The routes are read once a tick, by its first
  part that deals or checks, before that part's read of the tables (one round
  trip with no route, two with routes; `TickResult.RouteTrips`), and shared by
  the tick's later parts. A member that cannot launch a taken card (no model,
  budget or deadline from its packet or its override) reports it at once as a
  `--failed` finish, `launch refused: <why>`, never leaving it working.
- The twin (`mem:<file>`) holds no routes: routes are config, nova-config's
  rows applied to a store's Redis, and a twin has no config store to apply
  from. A twin deals as a store with no route does (the member's override);
  the route paths are driven on the in-memory store in the tests
  (`Mem.SetRoutes`) and on a real store.
- Two things are called routes. The `route` kind is the sprint's: what a card
  runs on, drawn at the deal. `NOVA_SWARM_ROUTES` is native's own list for a
  launcher outside the sprint: on a provider's 5xx it names, on its
  `NATIVE PROVIDER-5XX` line, the route after the one that failed. Under the
  sprint the member sets no such list (the line names `next=-`); the member
  reads the line as `provider failure`, and the redeal or rework that follows
  leaves the failed route out from the kind's own history. The list stays for
  native run by hand. A run the provider failed with no 5xx line, read from
  the harness's own log and transcript (`NATIVE PROVIDER-FAIL`,
  docs/SPEC-CARD-CONTRACT.md section 4), is finished the same way: the card
  returns to the deal, the redeal leaves out every route drawn for the card
  while another remains and draws the same one only when none does, and
  `routes` counts the take against the route it ran on: an attempt, failed,
  and a provider failure. The 5xx hand-back above is finished the same way,
  its line's cause the reason (`provider: class=<class> status=<status|-> msg=<words>`,
  docs/SPEC-CARD-CONTRACT.md section 4).
- The machine's tick deals every ready primary the fleet has room for in one
  step, in stream turns (each stream's oldest first by score), one card at a
  time to the next up member round the fleet (the rolling index `deal_index`)
  that is below its room, DealAhead times its width: 150 ready over eight
  machines of width 64 all go to working in one tick, 18 or 19 a machine. A
  machine at its room takes no more, whoever deals: the `deal` verb refuses a
  card no up member has room for, and a rework with no member below its room
  sends its primary ready with the fix, for the tick to deal
  (`tla/DirtyTick.tla`, `Room` and `WidthRespected`, which bound the room;
  `TestAReworkIsNotDealtToAMemberAtDealAheadTimesItsWidth`). A member takes
  its ready cards in stream turns, so it starts every stream alike.
- Every rolling index (the fleet's `deal_index`, the readers' `ask_index`, the
  work table's `stream_index`, `stream_index_ask` and `stream_index_accept`) is
  a counter: a uint64 from 0 that goes up by one with every placement and by
  one for every name passed over (a member down or full), the next name the
  counter modulo the count, in name order. It is written as a decimal with the
  step that moves it and persists across plans, ticks, stops and loops; only a
  clear resets it. Eight machines dealt 20 cards a tick: `m1..m8 m1..m8
  m1..m4`, then `m5..m8 m1..m8 m1..m8`, the counter 20, then 40.
- A member going down: its unfinished work cards are dealt to up members.
- No member up: unfinished work cards are withdrawn (kept in `withdrawn`);
  primaries return to ready. The tick deals the same card again at a new
  generation; the attempt advances only on rework. A primary with a withdrawn
  card is ready, never working.
- A member coming up: ready queues are levelled in one call; the newest cards move.
- The rebalance, once at the start of every tick, before any table's update
  (the owner, 2026-10-01: "both for readers and fleet, there needs to be a
  rebalance step done at the start of each tick. it's simple. just once before
  tick, rebalance each table."): the fleet's level moves ready cards (dealt,
  not taken) from a member holding cards it cannot start to one with free
  lanes, evening the members' backlogs (held less width) to within one, never
  past DealAhead times a width; a working card on an up member stays. It is a
  safety too ("and it's a safety, if ever there are cards on a held or down
  machine, rebalance moves them away."): no card stays on a held or down
  member; its cards, ready or working, are dealt to the members up as a member
  going down sends them, or withdrawn when none is up.- done and ok% are computed by the table from the member's `ok` and `failed`
  cells.
- A fleet member says it is there by beating: `nova-sprint fleet beat
  <member>`, run on the machine every few seconds, writes its last beat time
  (to the second) and its load in one write of its own record, outside the
  tables, whether the machine is RUNNING or STOPPED. The load is the machine's
  CPU busy percent of all its cores, measured between beats; where that cannot
  be measured, the one-minute load average over the logical cores, capped at
  1000%. `--load <percent>` gives it instead.
- A member's status is derived, never typed: up until it has missed three beat
  windows of 15 s in a row (`MissedBeatsDown`, `BeatDeadline`; one missed beat,
  such as a store round trip that timed out, marks nothing, and a beat resets
  the count, which is never stored), down past that or when it has never
  beaten, and held while the coordinator holds it, whatever it beats. A card
  is taken back from a member only when the member is down by this rule
  (tla/DirtyTick.tla, Lapse). A member's verb the server does not answer is
  sent again, three tries in all (internal/sprintwire). `fleet down <member>` holds a
  member and takes it down; `fleet up <member>` releases the hold, counts as a
  beat of a member that has beaten, adds a member the sprint does not know,
  and brings it up at once when it is alive.
- The fleet comes from the inventory. `nova-sprint fleet sync` makes the fleet
  table match nova-config's machine rows, in one step, and types no machine name
  and no width. The inventory is read through the config package by the config
  tool's own address rules (`--pg`, else `NOVA_PG_DSN`, the password from the
  variable `NOVA_PG_PASSWORD_ENV` names). A machine's width is its row's
  `width` field, set directly (`nova-config machine set <m> --width <n>`;
  `nova-config machine width` prints it); no friend row, no beat and not the
  machine's `slots` take part. A machine with width 1 or more is a member, and
  one with width 0 is none. A row with no width has the default, half the
  logical cores the machine's last `fleet beat` reported (the beat carries
  `cores`; `fleet beat --cores <n>` gives them instead; `sprint.WidthOfCores`),
  which the sync writes as a number; until a beat reports them the machine is
  no member and a NOTE line says it joins at the sync after it beats. The sync
  writes only what differs: a member the table lacks is added at its width,
  down until it beats (presence brings it up, as for `fleet up`); a member whose
  width differs has its width set; a row whose machine has width 0 is held,
  and its unfinished work cards are dealt to the members that stay up (the
  same move as `fleet down`); a row with no machine row at all is held the same
  way and, when no card stays on it after that redeal (no withdrawn card, and no
  finished card that is the live work of a primary on the table and not
  landed), removed in the same run: the step takes its control card off the
  table, held by the sync, and the verb deletes its row and then its beat
  record: the row only while its control card is still on no cell at the
  revision the delete read, and the beat only while the row is gone and the
  card still on no cell, each checked and deleted as one atomic change for all
  the members together (one transaction with a WATCH on the cards' records,
  under the record key the table layer writes them at, and on the table's
  rows, around the table layer's row delete), so a `fleet up` that placed the
  card again in between, and anything dealt to the member after it, keep the
  row, and a rejoined member's beat is never deleted; the members whose beat
  records are owed a delete are written down before the rows go (the record
  `fleet-drop-debt`, which teardown removes), so a cleanup cut short at any
  point is finished by the next sync, one with nothing else to write included; its width leaves the
  fleet's total, one line saying so; while cards stay on
  it, it stays held and a NOTE line says so, and a later sync removes it. A
  machine row that comes back places the same control card again before the
  step, under the fence too (the table layer's cell add; a batch never places a
  removed card; the records read in read sets of at most the table's bound) and
  the sync releases it, and `fleet up` does the same for a removed member. A member that stays has its status
  untouched, and the deal's rolling index moves only with the cards a held
  member's redeal places. A hold is marked by who made it (the control card's
  `held_by`): the sync marks the holds it makes, and releases them when the
  machine is back in the inventory with room (the member comes up when it
  beats). A hold the coordinator made with `fleet down` carries no mark and
  stays: the member takes its width, and the sync says so and names the
  `fleet up` that releases it. A sync after a sync writes nothing and says so. `--check` prints the drift and
  writes nothing: exit 0 when there is none, 2 when there is, 3 when the config
  or the sprint store cannot be read, or the config holds no machine row (a store that is not the fleet's would
  hold every member down). It is the coordinator's verb, like every fleet move.
  Each move but the removal is one `fleet up` or `fleet down` already makes,
  for many members in one plan, and the drift it reports is the plan it writes
  (a hold of a member with no machine row is a removal when its cards all find
  room).
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
- The fleet table's rows are ordered by status, up first, then held, then
  down, and by machine name within each (the owner, 2026-10-01: "Please sort
  the fleet table such that we sort first alphabetically by machine name (as
  is current), then stable sort by status, such that "up" is first, then
  "held" then "down""). The display step that writes the status cells puts the
  rows in that order when they are not (the table layer's row order); the
  footer row stays last.- A beat from a machine the sprint does not know writes one happened
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

- A reader row has a state, as a fleet member has: up, away or down. The rows
  are the coordinator's: `init --readers` and `reader add` declare them, and no
  beat, no queue and no loop record makes one. A reader with its row says it is
  there by asking for its own queue: `queue --as <reader>` writes its beat (a
  record outside the tables, as a member's beat is); the queue of a name with no
  row writes none, and its `--json` answer carries `reader: false` (`true` for a
  row), which the reader loop prints once as `MEMBER NOT A READER <name>: ...`
  naming `nova-sprint reader add <name>` (nova-tools#5096 item 23). It is up while its
  last beat is within the beat bound (`ReaderBeatBound`, the fleet's 15 s),
  away when it beat and has lapsed, down when it has never beaten; the
  coordinator's `reader away <reader>` holds it away whatever it beats, and
  `reader up <reader>` releases the hold. The readers table has no `status`
  column and `where` shows no reader's state; the state is never stored in the
  table. The state is read, never typed: the tick reads it once, with its first
  read, and every part plans on that reading.
- The readers' rebalance runs once at the start of every tick, before any
  table's update (the owner, 2026-10-01: "just once before tick, rebalance each
  table."): first its safety ("if ever there are cards on a held or down
  machine, rebalance moves them away"): every read asked or reading of a reader
  that is not up is taken back (retired by `away`) while a reader up without a
  card at its attempt could take it, and the tick's ask asks it again; with
  none, it stays and is judged as below. Then the level: asked reads (not
  begun) move from the reader with the largest load (asked and reading) to the
  next reader up round the readers at or below the mean, until no two loads
  differ by more than one, so no reader up is idle while another holds a
  backlog. A moved read is retired (by `level`) and asked of the other reader
  at the same attempt and head, its route kept, as a fresh ask (not returned,
  reasked 0); a primary's two reads stay with two different readers, and no
  reader is asked an attempt it already had. A read is moved at most once: the
  read card a level move asks carries `leveled`, and the level moves no card
  that carries it, so a late read is not asked afresh
  on reader after reader (the owner, 2026-10-01: "This pesky one card that
  doesn't clear thing... this is a failure mode we must fix. We can't get
  stuck on the last card."). A reader is named for its machine, `reader-<m>`,
  one reader per machine, and runs at the machine's width: `queue --as
  reader-<m> --json` carries m's fleet row's `width` as a member's own queue
  carries its row's, and the reader runs that many reads at once (the owner,
  2026-10-02: "why not just have as many readers as workers per-machine"); a
  reader named for no fleet row is handed no width and begins nothing. The
  sprint holds no reader's width of its own, so readers are levelled by count
  and none is bounded at DealAhead times a width.
- ask deals every primary in review that lacks reads to TWO DIFFERENT readers
  UP, in work order, each the next reader round the readers from the readers'
  `ask_index` that has no read card at the attempt, placed or retired. One read
  card per reader. Reworked work is asked by the same rotation: a read is a
  fresh child on a freshly drawn route, so the readers of an earlier attempt
  are not preferred, and a busy reader is not asked again only to have the
  next tick's level move the read (the owner, 2026-10-01, deleting the
  preference for the readers kept on the primary: "yes on the decision.").
  A reader away or down is never asked. A read asked, and not begun, of a
  reader that is not up is taken back by the next ask (the tick's, in the same
  step that asks the primary again): its read card is retired (by `away`), the
  primary stays at its attempt, no redeal is spent and no `ask --another` is
  owed, and the next reader up that has no read card at that attempt is asked.
  A read begun stays with its reader, except one its reader returns with no
  verdict (`read --as <reader> --return <card> --reason <text>`: its launch
  did not run, or it gave no verdict). A return is not a read: the read card
  goes back to asked on its reader's row, stamped `returned`, one happened
  note `a reader returned a read` carries the reader, the card and the
  reason, no finding counts against the work and no bound of the primary is
  spent, and the next tick asks it of another reader up that has no read card
  at the attempt (the returned card retired, by `returned`), or, when none is
  free, of the same reader again, in place; either way on a route drawn
  afresh as a new read's is, leaving out the route it returned on while the
  tier has another, and drawn only when the ask is not refused, so a reader
  whose launches failed
  is not counted as having read the attempt (tla/DirtyTick.tla,
  JudgedOnlyAfterTheBound). Each return counts itself on the read card (its
  `reasked` field, moved by `read --return`, whatever the tick does and
  however many readers are up), and a read card goes back to asked at most
  `MaxReadReasks` (2) times at its attempt; the return after that is counted
  as a read: the card is retired (by `returned`), and a primary no reader is
  left to read is the ask's `cannot ask` judgment (or, with fewer than two
  readers up, `fewer than two readers up`), for the coordinator (reader add,
  rework, drop; tla/DirtyTick.tla, ReasksBounded and StrandingIsJudged). Its
  member does not begin a read it returned again before
  `member.ReadStageRetry`. A return of a read the caller does not hold is
  refused, and so is a second return of a read returned and not begun since:
  a return is counted once. With fewer than two readers up the tick
  asks none: it raises one judgment, `fewer than two readers up: <readers and
  their states>`, for the sprint (not one for each primary), closed when two
  are up or no primary waits; `reader up` and `reader add` answer it.
  The machine's tick asks for every such primary; `ask` is the coordinator's
  own. Each read card the ask creates carries a route as a work card does
  (`route`, `model`, `tokens`, `deadline`), and `tier`, the tier it is drawn
  from: the tier of the card it reads, the tier the deal draws that card's
  work from (line 1's tier, flash when it names none, so a card that pins a
  model and names no tier is read on flash; a frontier card, a tier no route
  serves, is read on pro; the owner, 2026-10-01: "i think readers being
  conservatively the same tier as the work being done seems fine?"), raised
  to the read tier set for its stream (`stream set <s> --read-tier <tier>`, the
  stream's control card's `read_tier`) or else for the sprint (`set --read-tier
  <tier>`, the work table's `read_tier` property) when that is stronger, and
  never lowered (nova-tools#5096 item 27: a pro card's reads run on a tier at
  least as strong as the writer's); its packet hands the reader that tier and
  the reader's JOB.md names it. It is drawn at that tier's rolling index on the
  fleet table, which the deal and the reads share and the ask moves once a
  read (`internal/sprint/route.go`, readRouteOf;
  tla/RouteIndex.tla, THE READS); its packet hands the reader that route, so a
  reader loop needs no `--model`, and a reader started with `--model`,
  `--tokens` and `--deadline` runs its reads on those. A store with no route
  asks with none, and the reader runs its own; a read whose tier no enabled
  route serves is asked with none too, and the deal's tick raises that
  tier's judgment, `no route serves the tier`, at once for every primary in
  review whose reads wait or were asked with no route (`route.go`,
  readRouteMissing), closed when a route serves the tier.
  Work that came back failed is not read: it waits for the coordinator.
  `ask --another` deals a primary already asked to one more reader, for that
  attempt only (the primary's `asked` field still names the two the attempt
  was asked of, and after a rework two readers are asked round the readers);
  before
  the first ask of its attempt it is refused, naming `ask` and the tick as
  what asks first.
- A reader moves its own read cards: asked -> reading -> ok | broken, with the finding.
  A report on a card still asked is accepted: it is the begin and the report in
  one step, and `begun` is stamped with it.
  A read whose stage fails (the head could not be checked out) is never a verdict: its member
  runs the read again once, after `member.ReadStageRetry`; a second stage failure is returned
  (`read --as <reader> --return <card> --reason <the stage's reason>`), and the next tick asks
  it again as above.
- The read that completes two different readers' ok at a primary's head writes
  the judgment ready to accept; accept, rework and drop close it.
- The machine's tick accepts every acceptable primary in review whose work did
  not fail, except one whose CI is red at its head ("ci red on a primary" is
  the coordinator's: a green at its head, or the coordinator's accept, takes
  it) and one the coordinator returned to review at its attempt ("returned to
  review" decides it: accept, rework, drop; its reads stand, and a rework's new
  attempt with its own two reads is the tick's to accept again). An
  acceptable primary the tick does not accept is told as ready to accept when
  no open judgment on it offers accept.
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
- A broken read names its defect (docs/SPEC-CARD-CONTRACT.md section 3, a broken
  read): a broken verdict whose finding names no file, line or rule the work breaks
  is no read at all, neither ok nor broken; its member hands it back as a return
  whose reason begins `no finding:`, the primary stays in review, and the tick asks
  another reader, so the coordinator is never asked to judge on nothing. The verb
  itself holds the same rule: `read --broken` whose finding names none is refused,
  the read stays the reader's, asked or reading, to report with a finding or to hand
  back (`--return`), and no verdict, finding or judgment is written. A broken
  read's finding is recorded, and its judgment shows it, in full: every line of the
  reader's report and body, never its first line alone.
- A broken read notifies the coordinator. rework sends the primary back with
  the finding as the fix and delegates the next attempt at once (section 3);
  the primary's read cards are retired in the same step. When the fixed work
  returns, its finish asks no reader: the machine's ask, in the tick the
  finish wakes, asks two different readers round the readers, at the new
  head, on new read cards of the new attempt, each with the route it draws
  (one path asks). A report against a retired
  read card is refused, naming the retirement.
- A rework of a primary a reader passed (an ok read at its head when it is sent
  back) keeps that head (`passed_head`). When the next attempt's worker finds
  nothing to do or commits nothing (its failed finish begins `nothing to do:` or
  `no commit:`, and pushed no head), the card was right: it is no failed work and
  no judgment; the primary goes back to review at the passed head, and the tick
  asks two readers at the new attempt (`TestNothingToDoAtAHeadAReaderPassedIsBackInReview`).
  With no pass at the head it is failed work for the coordinator, as before.

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

`land` is the coordinator's landing step as one command (section 11): it
merges each batch's heads in work order onto a branch cut from the base, checks
and pushes it, and reports it through this merge step, with the facts above
when it cannot; it adds no state of its own.

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
| a primary is blocked on something missing | drop, ack (waives the named missing need) | yes |
| reads exhausted | ask --another, rework, drop | no |
| ready to accept | accept, rework, drop | no |
| returned to review | rework, accept (while its reads stand at its head), drop | no |
| stranded in review | rework, drop (and ask when never asked) | no |
| sentinel reached | release, add --before (do more before going on), drop | no |
| repair skipped changes the store refused as recorded | card (look), return, drop, rework, ack | yes |
| an operation was stuck | check, ack | yes |
| a reminder could not be delivered | goal set (a new route), goal drop, ack | yes |
| cannot ask (two readers are up, and a primary has no two to be asked of) | reader add, rework, drop, wait | no |
| fewer than two readers up | reader up, reader add, wait | no |
| no fleet member is up (when every member that beats is held, it says so and offers only fleet up and wait) | fleet beat (on a machine), fleet up (releases a hold), wait | no |
| a card reached its bound (an attempt's work card redealt MaxRedeals, 3, times after takes that ended, the provider's failures among them, and a take of it ended again; the judgment names the provider and the last error line when the provider failed that take) | rework with a fix (a new attempt), drop, wait | no |
| a work card is past its deadline | fleet down (the member, only when it has held the card its own whole deadline: never the member a late card was just redealt to, nor one it was withdrawn from), wait, drop | no |
| a read card is past its deadline | ask --another, wait, drop | no |
| a stream has had no merge step past its deadline | merge --stream, card (look), wait | no |
| an invariant is broken | card (look at the card), repair, wait | no |
| a judgment has waited past its due time (overdue) | a decision of the judgment, wait | as the judgment |
| a stream has made no progress past its deadline (stale) | where, queue (look), wait | no |
| stalled: nothing holds a card (rule 12) | the decisions its place allows and that would be accepted (ask --another for a primary asked already, never ask), else look at the card; drop; wait | no, while the stall stands |

A condition the tick keeps (cannot ask, fewer than two readers up, no member up, a deadline passed, an
invariant broken; a failing reminder too) is answered for a while by
`wait <note> --for <duration>`: the judgment is closed and the condition held
until that much running time has passed (STOPPED time does not count); when it
has and the condition still holds, the tick raises it again, and when the
condition clears first the hold is closed. `wait` on any other judgment sets
its review time, which counts running time from when it was set, as every
deadline does. A judgment whose review time has not come is quiet: not overdue, and
listed after every judgment that is not quiet, marked `quiet until=<time>`, for the
whole period, so it does not sit at the top of every inbox read.

The machine's tick writes its own judgments (section 14): cannot ask, fewer than two readers up, no fleet
member is up, a work card or a read card past its deadline, a stream with no
merge step past its deadline, an invariant is broken, a stall (rule 12: one
judgment for each card nothing holds, or for the stall a chain of waiting
cards ends at; the stall judgment itself holds nothing).

The sprint done is no judgment. The tick's last part, done, finds the sprint
done when nothing is waiting, ready, working, in review or merging and at least
one primary landed or was dropped (even with none landed), and writes one
happened note addressed to the coordinator: the sprint is done: n landed, m
dropped, took <duration> from the first start, with the hint "to continue: add
work, then nova-sprint start". In the same step the machine stops itself
(section 14): its record STOPPED with the cause done, the view's state DONE.
The inbox shows a note addressed to the coordinator first, above the
judgments; the note is on the notes stream, and the coordinator's goal route,
when there is one, is pushed it. add with a need on a
dropped primary writes the blocked judgment in the same step. The blocked
judgment names the dropped needs; acknowledging it waives those only (a need
dropped later is its own judgment), and `card <id>` shows each waived need, by
whom and when. An add counts only valid candidate IDs as proposed dependencies;
a missing prerequisite refuses the dependent too. An add naming its ids is
all or nothing, as every verb that names its cards is: one refused id refuses
them all. A stored waiting primary
or sentinel whose need has no record gets one missing-need judgment from
resolve or the tick, naming those needs. Acknowledging it waives only its
named needs that are still missing; it never waives a live prerequisite or a
missing need discovered later. Acknowledging several dependency judgments for
the same primary combines their named waivers in one guarded card change.
When the named prerequisites exist again,
resolve closes that missing-need judgment and still waits for them to land.

A primary in review is never silent. With ok reads from two different readers
at its head, some open judgment on it offers accept (ready to accept, or
returned to review). Otherwise, with nothing open on it and no read
outstanding, it is stranded in review (failed work, or never asked after its
last judgment closed) or its reads are exhausted. Acknowledging exactly that
judgment does not write it again.

A CI result, red or green, recorded for a primary in any state is always a
notification (red: judgment; green: happened). `ci` records the observation
(primary, head, run, status, source) and moves no card; ci and ci_head hold the
last result for the primary's current head, which the tick's accept reads; a
result for a head that is not the primary's current one is labelled as such
and leaves ci and ci_head as they are; a retried report of the same run
(ci_run and ci_run_status, whatever its head) is recorded once. Stream-batch CI in merging is the merge step's fact.

Each carries: id, kind, type, stream, the primaries (a set, bounded, with the
count), what happened, who reported it, attempt, how many times before, the
clock time, the decisions. A judgment about particular cards names them in
fields of its own: the card a stream stopped on (a conflict, a cross stop), the
card a cross stop needs and its stream, the sentinel reached, the suspects of a
red branch. Its printed commands read those fields, never a card's place in its
set of primaries. Notifications of one type, stream and cause are
grouped into one line with a count; a subject is listed and counted once.
Each group has an id that does not move while it is open: the id of its oldest
open notification (a stalled stream's is `stale:<stream>~<epoch>`, carrying the
sprint's epoch as every judgment id does, so `wait` takes it); overdue marks a
group and does not split it. `inbox` prints each group's id and size (the
members a verb given the group acts on), and each judgment's decisions as the
commands that make them, one per line, with the group id, `--expect` and
`--answers` filled in — a judgment about one card instead names the card (`rework
s1-1 --fix '<fix>'`, `ask s1-1 --another`, `drop s1-1 --reason '<why>'`), so no
group token is pasted; a cut list of primaries ends with
`nova-sprint inbox --open <id>`.

A red branch's notification lists the suspects the caller named
(`merge --red --suspect <id>...`, each a card of the batch), or says none was
named and gives the batch's first and last card and the command that lists
the batch; its decisions include resume with what was done. Marked ones (repeats, overdue) sort first.
A primary that came back a second time for the same cause is marked on the
notification of that cause, and "stop and look" is added to its decisions.

A judgment is open per card and per cause: a step that would open one of a
type already open on the card writes no second (a second ci red on the same
card's head takes the open one's place, with its run and note; a late read or
work card is its own cause, naming its card, so two late reads of one primary
are two judgments, each closing when its own card moves). A verb discharges only the
obligations it actually resolved: rework resolves a failure, a broken read and
a red CI on the primaries it reworks; drop resolves everything on the primaries
it drops, and accept every card judgment of the primaries it accepts; ask --another resolves a broken read; a green CI on the current head
resolves a red one; return resolves a red CI on the primaries it returns, and
answers its stream's red or rejected batch (recorded; that judgment stays open
while the stream is stopped); resume resolves the stream's stop.
Judgments are the coordinator's: `ack`, `wait` and every verb given
`--answers` are refused for any actor but the sprint's coordinator
(`init --coordinator`), naming the coordinator, as `release` is; workers and
readers keep their own verbs (take, finish, read).
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
nothing). A verb that names its cards or notes (ids, `--group`, an ack's
notes) applies all or none: when one is refused the step writes nothing and
names every one, the refused with why and the rest as not written; a verb
given a selection instead (`--stream`, `--col`, `--limit`, `--read-ok`) moves
what is eligible. Each answer is
recorded as a `decided` notification; a stopped stream's judgment stays open
while it is stopped. `wait <notification>`
records a next review time; it does not hide the notification (it is listed quiet,
after the others, until then). Reading the
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
inbox (and by `where --json`). A stream with nothing on the table (every primary dropped,
or restored empty by a clear) is waiting with no `since` and is never stale. A
stream whose every card on the table and not landed waits (behind a sentinel, or on
a need) is held by what it waits on, which the table shows, and is never stale.
`wait stale:<stream>~<epoch> --for <duration>` writes the time on the stream's
control card (`stale_review`), and the stream is not shown stale before it.
This is pull visibility; nothing claims to detect a dead process.

## 9. What is always true

Checked by `nova-sprint check`, and by the model. Sets of primaries are compared
exactly, member by member, never by their counts.

1. A card is in one place in each table it is in.
2. Primaries in work working and live work cards (fleet ready + working) are a
   bijection: each primary in working has exactly one live work card, the one it
   names, and each live work card has exactly one primary in working.
3. Primaries with read cards in asked or reading are in review.
4. Primaries in merge queued + stuck = primaries in work merging. A merge
   card names a card it needs only while it is stuck in a stream stopped for a
   cross: a return, a conflict stop and a resume each clear the need.
5. Primaries in merge merged = primaries in work landed.
6. No primary enters merging without ok read cards from two different readers at its head.
7. A score never changes except by rank: every copy has its primary's score.
8. No card lost or made twice: a card in flight names a primary on the table,
   and a primary has at most one live work card.
9. A stopped stream has an open judgment notification.
10. (`check` reports a pending operation under this number.)
11. A primary anywhere but waiting has every need landed or waived. A need that
    was dropped or missing and acknowledged is recorded on the card as waived,
    by whom and when, and counts as satisfied; nothing else does. add refuses needs that
    would make a cycle, naming it. The walk follows what a waiting card waits
    for: the needs it names, and its place in line (a card behind a sentinel
    needs the sentinel; a sentinel needs every card of its stream before it),
    so a need across streams behind the gates on both sides is refused when it
    closes a loop through them. Only the add that closes a cycle is refused;
    a cycle already in a store is reported by `check` as "a cycle through
    <loop>: <n> cards can never be reached", and the tick raises it as "an
    invariant is broken".
12. Nothing stalls. Every primary on the table that has not landed is held by
    one of: (a) an outside actor before its deadline (its live work card in an
    up member's ready or working cell, a read card asked or reading, its merge
    card queued in a stream that merges); (b) the next tick, whose own parts,
    called on the state, move it or write a judgment naming it; (c) an open
    judgment naming it, or its stream while the stream is stopped or it merges
    there, or the tick's judgment on it that the coordinator acknowledged; (d)
    what it waits on, itself held, followed through the chain (a need not
    landed, a sentinel not released, a place in the ready queues, counted on
    the members the deal may give it: a member below its room that refused
    its card at staging holds no place for it); (e) with
    the machine STOPPED, the next tick. A chain that ends in nothing or in a
    cycle holds nothing. A judgment past its due time that no overdue mark
    holds, a stopped stream with no open judgment, and an operation pending
    past its grace that a tick since has not finished are stalls too. `card`
    prints what holds a primary (`HELD`).
13. The log replays to the tables: replaying the epoch's move lines from
    empty gives every card's place and generation (the control cards aside),
    and a card the log never placed, or placed elsewhere, is a violation
    naming it (section 17). `check` and the property test hold it; the tick's
    check does not read the log.
14. The log and the inbox agree: every notification is written to both, so
    each notification line of the log (an update aside) has its inbox entry
    with the same id, kind, type and text, and each inbox entry its line.
    `check` and the property test hold it.

Rules 2, 3, 4, 5, 9, 12, 13 and 14 hold whenever no operation is pending (while one
is, 12 judges only the operation); 1, 6, 7, 8 and
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
it read, so no other operation applied anything since its read. A step changes
each card once: two changes of one card in one plan that agree (one
expectation, at most one place change, no field set to two values or both set
and unset) are one entry; two that disagree refuse the step whole before
anything is written, naming both causes. Its manifests apply in order; a
table's changes over the table layer's bound are several
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
nothing. `check` shows a pending operation (`where --json` carries it). The model
includes the cut between every two phases. A multi-table batch in the table
layer retires this section.

## 11. Verbs

Each takes a set and is one step. A set is ids, a stream, a column, `--limit n`,
or an inbox group (`--group <id>`; a group number is refused, naming the ids).
Every verb taking `--group` takes `--expect <n>`: when the group's members now
number otherwise the verb is refused, changes nothing, and names the size now
and the members added or gone; a verb given `--group` prints how many it acted
on. Each prints what moved, what did not and why,
and the summary line. A verb run on a store with no sprint in it yet (a table it reads is
not there) says `no sprint here yet: init makes its tables; run: nova-sprint init
--coordinator <name>`, never the table layer's words. Every judgment verb takes `--answers <notification>`.
Every store verb takes `--redis`, `--actor`,
`--op <id>` (the same id again, for the same verb with the same arguments, returns the recorded
result; recorded for another verb or other arguments it is a conflict and is
refused), `--json` and `--max`. `--actor` has no default: it is `--actor`, else
NOVA_SPRINT_ACTOR, and a verb that writes with neither is refused. Every verb
has one class of who may run it. The coordinator's verbs (init, add, quack, release,
resolve, start, stop, ask, accept, rework, return, drop, rank, brief, move, resume, land, fleet
up, fleet down, fleet level, fleet sync, friend sync, friend down, friend up, reader add, reader away, reader up, reader remove, stream remove, wait, ack, clear, teardown, repair,
goal set, goal drop, play) are the sprint's coordinator's alone: the first
init names the coordinator (`--coordinator`, else the actor), a later init is
refused unless its actor is that coordinator and never changes it (the seat
moves by `coordinator`, whose class is the seat's: below), and
another actor is refused (exit 2) with nothing written; a store with no
coordinator takes init and teardown only. The workers' verbs (take, finish,
read, fleet beat, friend beat) are anyone's who names the member, reader or friend, and their
actor is that name, whatever `--actor` or NOVA_SPRINT_ACTOR say: the record
names the worker the verb was run as, as the server's does. The reports (merge, ci) want an
actor; the machine's verbs (tick, run, friend clean) are recorded as the machine; the reads
(queue, inbox, card, check, where, dashboard, goal show) need no actor, except `inbox
--read`, which moves the coordinator's cursor and is the coordinator's alone:
anyone reads the inbox, and nothing another actor does hides anything from
the coordinator. A card's and a
control card's text fields
(brief, fix, finding, report, reason, note, return reason, ci note, did) are
at most 8 KiB, except the brief: a brief is a child's whole brief, so it is at
most 16 KiB (16384 bytes; the card lint advises at most 12000, and
`internal/cardlimits` holds both numbers, which the store and the lint read; the
bound is a constant of the model and does not change the model). A field over its bound refuses the step whole, nothing
written, naming the field, its size and the bound. Every manifest is checked against
the table layer's bounds before anything is written, split by entries and by
bytes, so no step can wedge the sprint. Every command checks first that the
store's table function library is this build's, and refuses (exit 2) with the
command that loads it.

| verb | does |
|---|---|
| init | creates the four tables and the view; `--readers`, `--members`, `--coordinator` (the one actor who releases sentinels; default the actor; the seat then moves by `coordinator`), `--owner` (who may give the seat and whose name a take carries; set once, kept by a clear, removed by teardown), `--rules <file>` (the child rules file every brief is held to, one required sentence per line; its absolute path is recorded as the key `sprint:rules`, kept by a clear and removed by teardown; refused at once when the file cannot be read or holds no rule; this repository's is `fleet/child-rules.txt`); its line names every reader of the sprint (`readers=`), and on a twin a NOTE says a member it adds is up after the next tick |
| add | admits primaries into a stream: waiting if they need something, else ready; `--count n` generates ids; `--sentinel <id>`, `--before`/`--after <id>` (section 16); `--brief <text>` or `--brief-file <path>` gives the brief (the file's bytes as they are, its one trailing newline cut, the whole file read so the lint and the size refusal see all of it, a file over 1 MiB refused naming that cap and its true size; both together, or a file that cannot be read, is refused with exit 2), and every packet carries it whole; a brief is a child's whole brief, so `add` holds every brief to the card lint's child rules (`swarm.LintCardChildWith`, in process, the rules of `nova-swarm lint --card --child-rules`) and refuses one that fails with the lint's own `LINT DRIFT brief <check>: <line>: <excerpt> remedy=...` lines on stderr, exit 2, nothing written; a `--count` card and a sentinel with no brief are not linted, and an add of cards with no brief says so on a NOTE line naming `brief`, the verb that gives one, and `--rules <file>` names the rule file, read at add time, for this add (over the one `init --rules` recorded, over the built-in general rules; a `--rules` with no brief is refused, and a file that cannot be read is refused naming its absolute path); `nova-swarm template --name card` prints a card that passes the general rules; `--brief-dir <dir>` adds one card per `*.md` file in the directory in byte order of file name, and `--brief-file <path>` given alone (no ids, `--count` or `--sentinel`) or again adds one card per named file in the order given (with either form no positional id; the card id is the file's base name without `.md`, which add says on a NOTE line under its ADD line, refused naming the file when not one; one `--brief-file` with no ids, `--count` or `--sentinel` is that form too, one card named by its file, nova-tools#5096 item 19, and with ids, `--count` or `--sentinel` it is the brief of the cards they name); each brief's `Needs:` line (the first `Needs:` header line, else the `DEPENDS-ON:` line of its typed header block; ids comma separated, text after an opening parenthesis cut, so `none` or `-` is no needs, and an `owner/repo#n` reference is no need) becomes that card's needs, a need naming no primary on the table or in this add refused naming the file and the id, `--needs <a,b>` on the line adds to every card's (a need named by both is stored once), and `--sentinel <id>` with either form admits one stop after the cards, `--before`/`--after`/`--score` applying to every card; one failing brief refuses the whole call, every failing file named with its findings, exit 2, nothing written; two cards of one many-brief add that name one file in their `PATHS:` header lines (commas or blanks between the files), neither needing the other through the add's needs, are refused, exit 2, nothing written, naming the file and the two cards, unless `--allow-shared-paths` (nova-tools#5096 item 17); the one-brief form reads the brief's `Needs:` or `DEPENDS-ON:` line as the cards' needs when `--needs` is not given; `--held` admits every card of the add held (section 16) |
| quack | cuts quack cards, the sprint's end-to-end test cards, into a running store: `quack --streams <a,b,...> --count <n> --repo <clone url> [--tiers <t,...>] [--base <branch>]` adds n cards to each stream (a stream new to the sprint is made), the tiers (default `flash,pro`) taken in turn down each stream, the base default `dev`; each card's id is `quack-<stamp>-<stream>-<nnn>` and its brief asks for the one file `quacks/<id>.txt` holding the line `quack`, the stamp twelve hex digits drawn once per call (two passes share one with a chance of about one in 2^48, so a pass's files are new to the repository's history, which a clear does not empty), every call drawing its own; under `--op` the call is held to the caller's arguments (streams, count, tiers, repository, base), never to the cards its stamp makes, so the same call retried, or two of it overlapping, replays the store's recorded result while other arguments under that op are refused, and an op this store has no record of (after a teardown, or in another store) adds new cards under a fresh stamp; each brief is a child's whole brief (line 1 its tier, `BASE:`, `REPO:`, what to do, the known answer, the finish and the read) closing with the RULES paragraph of the rule set `add` would hold it to, and is held to the card lint as `add` holds a brief; every card of every stream is checked before anything is written (a stream long enough to make an id over 128 characters is refused, naming the stream and the length), one step adds every stream's cards or none (several streams' named cards are all or none, as one stream's are), and a refusal names every missing or bad input at once, exit 2, nothing written |
| release | lands reached sentinels, and held ones (`add --held`) that wait for nothing; clears the hold of a held card, which goes to ready when it waits for nothing else; the coordinator's alone, with `--reason` |
| resolve | waiting -> ready where needs have landed (the tick does it; by hand for a stuck case) |
| start, stop | set the machine RUNNING or STOPPED (section 14) |
| run | ticks on every line of the log (at most every 100 ms) and once a second while the log is quiet; before each tick it reads its own binary's file, and when a new build was installed under it since it began it stops (`RUN STOP the binary this loop runs was replaced ...`, exit 3) so its supervisor starts the new one: a loop never ticks the store with older code than the verbs run |
| tick | one tick by hand |
| take | a worker moves work cards fleet ready -> working; `--as <member>`, `<card>@<gen>`. The width is hard: a member's working cards never pass its fleet row's width, held here whatever is asked; a take by count is cut to the room, a take by id past it is refused; a take by count that took fewer than asked says why on a NOTE line (the member at its width, its ready queue empty, or not up) |
| finish | work cards done ok or failed; primaries to review; `--as <member>`, `<card>@<gen>`; `--usage <text>` (what the run spent) is kept on the attempt's record, timed and priced (section 2, What a card cost) |
| ask | deals primaries in review to two different readers; `--another`; `ask <primary> --instead <reader>` takes that reader's read, asked or reading, of the primary at its attempt back (`retired_by: coordinator`, a later report of it refused naming that) and asks one other reader in the same step, chosen and routed as `--another` (the owner, 2026-10-01: "get the verbs in man."); refused, nothing changed, when the primary is not in review, the reader holds no live read of it, no other reader is free, or with `--another`, `--group` or more than one primary |
| queue | a reader's read cards or a member's work cards, oldest first (`--as`; `--json` carries the worker's `width`: a member's fleet row's, a reader's its machine's, `reader-<m>`), or a stream's merge queue (`--stream`) |
| routes | each route of the store (nova-config's `route` kind) with what its attempts did: attempts, ok, failed, provider failures, mean wall from take to finish; `TIERS flash=<n> pro=<n>` first, the enabled routes per tier; `--json` |
| stats | the epoch's pass in seconds, each as median, max and count, from one read of the work, fleet and readers tables (every primary's work and read cards of every attempt, retired ones too, in read sets; `sprint.Stats`, pure): the stages (deal wait: admitted to the first work card's `first_dealt`; finish to two reads: the last ok take's `finished` to `accepted`; accept to land; total: admitted to landed), each member's work cards (cards, failed, take wait `dealt` to `taken`, run wall the usage's `wall`, report lag `finished` - `taken` - wall), each reader's read cards (cards asked, begin wait `asked` to `begun`, run wall, report lag `read` - `begun` - wall), and each route's takes from the primaries' cost records (takes; ok: a work take finished ok or a read with its verdict; provider: provider failure or no result; failed: every other end; a launch refused at staging is no take; a read whose record names no route counts on its card's route; run wall); members, readers and routes in name order; changes nothing; `--json` |
| read | a reader records ok or broken with the finding; `--as <reader>`, `--begin`; `--usage <text>` (what the read spent) is kept on the read card, timed and priced (section 2, What a card cost) |
| accept | review -> merging and into merge queued; refused without two readers; named ids all or nothing, a selection moves the eligible |
| rework | delegates the next attempt at once with a fix, and writes on that attempt's work card `fix`, `finding` (its broken reads' findings, each once) and `why` (how the attempt before ended: failed with its report, finished and found broken, or sent back), each cut to MaxCardTextBytes with a trailing `...` and never refused for its size, and kept on the primary too for a rework that deals later; its packet carries them to the child's JOB.md and `card` prints per attempt; ready when no member is up; a primary at its redeal bound (ready, its work card withdrawn) is reworked too, with `--fix`, its withdrawn card taken off; without `--fix` each primary's fix is the finding of its broken read, else the report of its failed work, and a primary with neither is refused by name; `--tier <flash|pro|frontier>` writes the tier on the primary (`tier`), and this attempt's deal and every later deal and read of the card draw from it over its brief's line 1, so a flash card that failed twice is reworked on pro, the same card and brief; a word that names no tier is usage (exit 2), and a card whose brief pins a model is refused by name, since it runs on its pin whatever its tier |
| return | merging -> review, off the merge queue |
| drop | off the table with the reason |
| rank | changes a score and every copy |
| brief | replaces the brief of a primary that has not started (the owner, 2026-10-01: "What other things should you be able to do to mutate a stopped sprint" / "Are there other verbs you need as you work with sprints?" / "I don't want you manually hopping in and working around it and doing manual stuff."): `brief <id> (--brief <text> \| --brief-file <path>) [--rules <file>]`; the new brief is held to the card lint and the size bound as `add --brief` holds one (the same function, refused exit 2, nothing written, with the lint's own lines); refused (exit 1, nothing written) on a RUNNING machine (`nova-sprint stop` first) and for a card that is no primary or has started: only a primary waiting or ready with no work card ever dealt (attempt 0) takes one, a card dealt, working, in review, merging or landed keeps its brief, its state named, and is refused so whatever the machine's state, with what changes it instead: from review `rework <id> --fix`, the next attempt's change (from merging after a `return`); from any open state a `drop` and the new brief added as a new card; once landed, a new card. The card keeps its id, stream, score and needs; before this verb the coordinator dropped the card and added it again, which changed its id and place (`sprint.Brief`) |
| move | moves primaries that have not started to another stream (the owner, 2026-10-01: "What other things should you be able to do to mutate a stopped sprint" / "Are there other verbs you need as you work with sprints?" / "I don't want you manually hopping in and working around it and doing manual stuff."): `move <id>... --stream <s> [--before <id> \| --after <id> \| --score <n>]`, one step, all or none for the ids named; refused (exit 1, nothing written) on a RUNNING machine (`nova-sprint stop` first), for a card that is no primary or has started (only a primary waiting or ready with no work card ever dealt moves; a card dealt, working, in review, merging or landed keeps its stream, its state named), and for a card of the destination already (`rank` changes a place in line). The destination is placed exactly as `add` places cards (the same plan, on the sprint without the moved cards): a stream new to the sprint is made as `add --stream` makes one, the cards go in line by `--before`/`--after`/`--score`, else at the end in the order named, waiting or ready by their needs and the stream's sentinels, a reached sentinel behind them no longer reached, a cycle of needs refused naming it, and a ready card the destination would put behind a sentinel refused by the lifecycle (ready -> waiting is only the effect of inserting a sentinel; `--before` the sentinel moves it). The card is the same card moved: its id, brief, needs and admission stay, and a need naming it still holds (a need is by id) (`sprint.MoveCards`) |
| merge | one mechanical merge step for a stream: `--batch n`, given facts; `--red [--suspect <id>...]` |
| land | the coordinator's landing step as one command, an external delivery (a git push) and a store write (the merge step): for each stream named (`--stream`, again for more; default every stream with cards queued and not stopped), in stream order, the merge queue up to its first stuck card, in work order, cut into batches of consecutive cards whose briefs name one repository and one base (`REPO:` and `BASE:`, read as staging reads them; `--base` for a card naming none); each batch's heads merged `--no-ff` with the message `land <id> (sprint stream <s>)` onto a branch cut from the base's tip on origin, in a clone (`--repo-dir`, else a clone kept under the directory each line names, its name the readable repository and a hash of it; every clone reused has its origin's fetch URL and its one push URL held to the repository the cards name before any git, the host compared without case and the path with it); a caller's `--epoch` the sprint has left refused before any git; `--check <command>` run once per batch in the clone before the push; the queue head, its heads and attempts, and the epoch read again just before each push; the push plain, never forced, and on a rejection the base fetched and the batch rebuilt on its new tip once; then the batch reported by the merge step `merge --stream s --batch n` runs, fenced to the epoch land read and guarded in the same store step to plan only while the queue still starts with the batch's cards at the heads and attempts land read and pushed (a rework keeps a card's id and epoch, not its head); the pins are the step's arguments, so an `--op` replay returns only that batch's receipt. A head that is not a commit on origin or whose merge stops on unmerged paths ends its batch before it, the cards before it land, and it is reported with `--conflict` and git's words as the note; git failing for any other reason (an identity, a hook, the disk, the network) blames no card: nothing is pushed or reported and the batch is refused; a check that fails, with `--red`, nothing pushed; a second rejected push, with `--rejected`. A push that landed and a report that did not (a clear, a card accepted ahead of the batch, a return, between the two) is `LAND FAILED`, exit 2, and the one remedy named is to run land again, which rereads the queue and lets its own checks decide: a card as it was is recorded with no new push (its merges and push are no-ops), a card reworked since is merged at its new head or meets a real conflict, and after a clear there is nothing to report (tla/Land.tla). A bare `merge --batch n` is never offered: after a rework the queue starts with the same ids at a head the base does not hold, and the merge step alone would record it. One line per batch, `LAND OK|REFUSED|FAILED stream= cards= base= tip= ids=<first>..<last>` (a batch whose git ran also says each step's seconds, `fetch= merge= check= queue= push= report=`, and its `--json` item `times`),
 then `LAND DONE batches= cards= refused=`; `--dry-run` reads the store only and changes nothing, and refuses what land refuses before its git, in land's words: a batch whose card names no base (and no `--base`) or no repository (and no `--repo-dir`) is refused, land and dry run alike, naming every problem at once, each cause on its own line with its one next command (the
first on the `LAND REFUSED` line, each other on a `NOTE` line and in the `--json` item's
`also`: each head of the batch that is not a commit id, with its return), and on a twin,
which has no git, a `NOTE` that `merge --stream <s> --batch <n>` records the landing in
land's place; a head that is not a commit id stops the dry run where land stops, the cards before it a batch, that card refused with the conflict fact land would record and nothing recorded; `--json`. A batch landed and reported tags the branches its cards' work cards of every attempt record (`branches_queued=<n>` on its line, `prune` on its item; never the base, an empty name, an option-like name or one not under `sprint/`, each said on a NOTE and counted as `branches_kept=<n>`), and the cleanup deletes only canonical successful-attempt branches from origin later, many in one push, each with an explicit lease against its recorded head; advanced or recreated tips, unowned branches and all recorded stream bases stay on origin, and a retry keeps the original lease; then removes the clone's remote-tracking refs of branches origin no longer holds, never while a landing builds or pushes: the one-shot land once after every stream, the land loop (`run --land`) between rounds when a round finds nothing queued or 256 branches wait, a line per clone `PRUNE OK|FAILED branches= refs= dir= took=` (`--json` `prune`); a failed cleanup fails no landing, and the loop keeps its branches and tries again after a minute; the queue is the process's memory, so a crash or a stop loses it and those branches stay on origin; a dry run queues and deletes nothing and says how many it would queue |
| resume | a stopped stream moves again, with what was done; refused while a cause is unresolved |
| fleet | `up|down <member>`, `level`; down and up say on the member's MOVED line where its cards went (nova-tools#5096 item 21): down `moved=N to m2(n),m3(n); stayed=K withdrawn: <primaries>` (a card no member up has room for, or at its redeal bound, is withdrawn), up `moved=N to <member>(n) from m2(n),...` when the level moves cards onto it; a member going down in the tick's presence part says the same |
| friend sync | the friends table's rows made nova-config's friend rows (section 1) |
| friend clean | the retention rule of the friends' working directories (docs/FRIENDS.md; ideas#833), run nightly from a loop row on the machine that holds them, never by the server and never on the store: `friend clean [--pg <dsn>] [--root <dir>] [--days <n>] [--dry-run]`. For each friend row of nova-config (as `friend sync` reads them; the coordinator is one), `<root>/<friend>-working` (`--root`, else HOME); a friend with no directory there is said and skipped. A job is `inbox/<job>/` or `jobs/<job>/`, done when `outbox/<job>/REPORT.md` is a regular file, its age that file's. Inside a done job at least `--days` old (default 3) a clone (a directory holding `.git`) is removed when `git status --porcelain` is empty, it holds no stash and no commit of `HEAD` or a local branch is missing from every remote-tracking ref (`git log HEAD --branches --not --remotes`, no network); build output (`node_modules`, `target`, `gocache`, `gocache-*`, `.gocache`, `go-build`, `wt-*`) that is no clone is removed. A clone that fails the check, or whose git fails, is dirty: listed each run, `FRIENDS-CLEAN DIRTY friend= path= age=<d>d why=`, and removed once its job is 14 days old whatever its state, its line saying `dirty=<why>`. Nothing else is touched: the brief and any text of the job, `outbox/`, every file outside `inbox/` and `jobs/`; a link is never followed; a job under `jobs/` that is itself a clone is one target, one under `inbox/` is never removed (a NOTE). Every removal is `safepath.RemoveUnderRoots` under the job's directory. The friend's one build cache, `<friend>-working/.cache/go-build`, is held under 10 GiB by the member's trim (`internal/gocache`). `--dry-run` says `WOULD-REMOVE` in place of `REMOVED` with the bytes and removes nothing. Lines `FRIENDS-CLEAN REMOVED\|WOULD-REMOVE friend= path= bytes= age=<d>d kind=clone\|build[ dirty=<why>]`, `FRIENDS-CLEAN CACHE ...`, `FRIENDS-CLEAN FRIEND <f> dir= jobs= done= freed= listed=` (or `absent`), `FRIENDS-CLEAN FAILED friend= path=: <why>`, and last `FRIENDS-CLEAN OK freed=<bytes> listed=<n>` (a dry run adds `dry-run: nothing was removed`), or `FRIENDS-CLEAN INCOMPLETE ... failed=<n>`, exit 1; a config that cannot be read or holds no friend row, exit 3, nothing removed; `--json` one object with the lines |
| friend beat | a friend's beat, `friend beat <friend>`, run by its own machinery every second; through the sprint's server it is `friend beat <friend>` and nothing more |
| friend down, friend up | hold a friend (status `held`, whatever it beats) and release the hold (not a beat: `asleep` until she beats) |
| reader add | declares readers |
| reader away | holds readers away whatever they beat: no read is asked of them, and a read asked and not begun is asked of another at the next tick |
| reader up | releases the hold; the reader's state is then its beat's |
| reader remove | takes readers off the readers table; refused (exit 1, nothing written) when a named reader is no row or holds a read card, asked, reading, ok or broken, naming the reader and its read cards |
| stream set | `stream set <s>... --read-tier <flash|pro|default>`: the read tier of the streams named, their control cards' `read_tier`, over the sprint's (`set`); `default` takes a stream's off; the coordinator's; refused whole, nothing written, for a stream that is no row, a tier that is not flash or pro, or another actor |
| set | `set [--read-tier <flash|pro|default>] [--dealt-max <duration|default>]`: the sprint's settings, the work table's properties `read_tier` (every card's reads raised to it, never lowered) and `dealt_max` (how long a work card may wait dealt and never taken before it is a judgment; default 3 times the take deadline, 6 hours); the coordinator's; refused whole, nothing written, for a tier that is not flash or pro, a bound that is not a duration above zero, nothing to set, or another actor; a clear starts the next epoch with neither |
| stream remove | takes streams off the work and merge tables (the owner, 2026-10-01: "remove work streams a/b/c" / "you should have a verb to remove work streams" / "they should only succeed on a STOPPED sprint machine"): each stream's row of both tables, with the stream's control card, the one card `add` made for it, which the merge row's delete takes off the table (its record kept); refused (exit 1, nothing written) on a RUNNING machine (`nova-sprint stop` first), for a stream that is no row of either table, named, and for a stream that holds a card (a primary or a sentinel placed in any column of its work row, landed included, or a merge card in its merge row), naming how many of each and the remedy (`nova-sprint clear --confirm sprint`, or `drop`); all or none for the streams named. A clear keeps the streams and does not bring a removed one back. The table layer never places a removed member again within an epoch, so `add --stream <s>` of a stream removed in this epoch is refused, naming the clear, and adds it fresh after the next clear (`sprint.StreamRemove`, `sprint.RemovedStream`) |
| ci | records a CI observation for primaries in any state |
| wait | sets a judgment's next review time |
| ack | closes a judgment the coordinator looked at, with the reason |
| inbox | every open judgment and the notifications since the cursor, grouped, judgment first; `--open <id>`, `--read`; `--json` carries `judgments` (each with `id`, `kind`, `type`, `what`, `stream`, `size`, `cards` whole, `notes`, and `answers`: every decision with the exact command lines that make it, in order), `happened` (the notifications since the cursor, grouped), `done` (the machine has stopped because the sprint is done) and `groups`, every group in the order the text prints; `--wait --timeout <d>` blocks until the inbox holds a judgment, or a note addressed to the coordinator, that was not in it when the wait began (by note id: a held or waited judgment is open already and never wakes it; the owner, 2026-10-02: "push notifications for inbox from nova-sprint so she doesn't have to poll"), or the machine stops having run, or `<d>` passes; it sleeps on the tick-end notes and looks at the inbox at each one and every 15 s while nothing ticks; then it says how it ended on one line (`inbox --wait: new=<group id,...>`, `inbox --wait: the machine stopped`, `inbox --wait: nothing new in <d>`) and shows the inbox; `--json` carries `woke` and `new` (the new groups' ids), the line only for a wait that found nothing, on stderr (stdout stays one JSON object); `--wait --push <dir>` keeps running until it is interrupted: each new judgment and note to the coordinator is written once as `<dir>/<note id>.md` (the group as `inbox --open` prints it, then `clock: <RFC3339>`; a `:` in a read-time group's id becomes `-`), said as `INBOX OK pushed=<id> file=<path>` (`--json`: one object a file), and the files there are its cursor: a note with a file is never written again, so a restart pushes nothing twice and misses nothing; a timeout is quiet and the loop goes on, the machine stopping is its line and the loop goes on; a local write, into the coordinator's own inbox directory; `--push seat` is the holder's inbox, and follows the seat ("Handing over the seat"); `--push` takes no `--read`, `--open` or `--at-epoch`; `--json` carries `coordinator`, the seat's holder |
| card | one primary's story, told from the log: for a card in flight, first what holds it now (each open judgment with the commands that answer it, or the actor and its deadline); its place in its stream's line; its brief, and the fix its attempt was given; its timeline in local time, an attempt at a time ("attempt 2, because attempt 1 failed"), one line per event a person would name (two readers asked, a merge and its batch, a step and its answer are one line each), a finish and a read with the first line of their words; the reports, findings and fixes whole as paragraphs; a card that has ended says so in one line; a COST line per consumer that ended and the COST TOTAL (section 2, What a card cost); `--fields` prints every field of the primary and its cards instead; `--json` carries both, the timeline's events with the log lines each tells, and the cost |
| queue --as, take | a member's or a reader's cards (a reader's `queue --as` is its beat), each with its packet: what it is handed so that it needs no other read to learn its task (the card, its epoch and generation, the brief, this attempt's fix, the notes on it, for a rework the finding of the read that found the attempt before broken and why that attempt ended (the work card's own words: the primary's are written at the next tick's drain, after a member may have taken the card), and for a work card the branch to work on, `sprint/<card>.g<gen>.e<epoch>` (the epoch makes it one per epoch, a card id coming back after a clear, and the generation one per launch, a card dealt again within an epoch, withdrawn from a member or redealt after a staging or provider failure, being another launch whose push must not meet the first's), and the one to start from, the attempt before's branch for a rework, with `base_head`, the head that attempt finished ok at, which a rework is staged from (docs/SPEC-CARD-CONTRACT.md: never a branch name alone, which may never have reached origin); for a read card the work it reads: the worker, its head, branch and base, and the worker's report), and the command that reports it (a work card's names `--head <commit>`: a finish without `--head` records the card's id as its head, which `land` refuses as not a commit id); `queue --as <w> --packets <n> [--have <id,...>]` hands only the packets the worker asks for: the first n cards it may start (asked, ready) and every card in flight (reading, working), each not named in `--have`; every other card is listed with its id, column, attempt and gen, and the answer's epoch, which are its claim, and no packet (a reader of width 8 holding 150 asked reads with 2.5 KB briefs: 445,525 bytes without the flag, 51,623 asking for 8; the fleet load test of 2026-10-01 measured 579,181 bytes a pass); without `--packets` every card carries its packet, as before; take prints the packets of the cards it took, `--json` as `packets`; finish takes `--branch` and `--base`, which the work card keeps and the reader's packet and card show; a fleet member (`nova-swarm member`) pushes the child's commit to origin's `sprint/<card>.g<gen>.e<epoch>` before its finish, so the finish's `--head` is the pushed sha the merge queue carries and the merge reads the work from origin; a finish is ok only with the result's shape, its verdict ok and a pushed commit, and every other is a `--failed` finish naming no head and no branch, its report starting with the reason (`no RESULT.md shape`, `nothing to do: <why>`, `verdict <word>`, `no commit: <why>`, `push refused: <git's line>`), so it opens the failed-work judgment and never goes to review with nothing to read (docs/SPEC-CARD-CONTRACT.md section 4) |
| log | the epoch's log, every line in order: --card (a primary with its work, read and merge cards), --stream, --member, --since, --at-epoch, --json (section 17); a line's words are printed under it, a brief by its size and the card that shows it (`card <id>`), never whole (`--json` carries it) |
| check, repair | section 9 and section 10 |
| where | the view, once or `--watch` (redrawn in place, section 1): work, friends and fleet; `--all` draws the readers and merge tables too (hidden from the default frame; the owner, 2026-10-02: "please hide the reader and merge tables"); its title line names the seat's holder (`SPRINT TABLE  coordinator friend-b`, with `(taken 5:21 PM)` after a take until the next handover is given); `--json` carries every table and the pending operation, the stalled streams, the people, the coordinator and `seat`, its last change |
| coordinator | moves the seat: `coordinator <name> --reason <text>`, given by its holder or the owner; `--take --approved-by <owner>`, taken by `<name>` itself; prints the handover after (below) |
| handover | what the next seat needs, from the store, in one screen (below); `--json` |
| play | plays the world outside the table through these verbs, seeded (section 12); refused while no machine is running |
| goal | `set`, `show`, `drop`: each person's goal and route, pushed by the tick (section 15) |
| clear | stops the sprint and clears all work in it: a new epoch (section 13); `--confirm sprint` |
| teardown | drops the tables, the view and every key of the sprint, of every epoch; `--confirm sprint` |

The read verbs (queue, where, inbox, card, check) have `--json`, one object for a
program; `queue --stream <s> --col waiting` lists a stream's waiting cards;
`card` shows each need with its state and what needs the card; where, inbox and card take `--at-epoch <n>` to read an earlier epoch as
it was. Every store verb takes `--epoch <n>`, the epoch the caller holds. Every
report of an outside actor on a card it was handed (take by id, finish, read,
ci, and a merge step by an actor other than the coordinator) must name it:
`queue`, `card` and the inbox's printed merge commands print it, and a report
without it, or with an epoch the sprint has left, is refused naming the clear.
A clear moves the epoch and a card of the same name in the new epoch is
another card, so the guard keeps a worker, reader or merger from before a
clear from reporting on it. The coordinator's verbs (accept, merge, rework,
drop, rank, resume, return, release, resolve, start, stop and the rest) name
no handed card: each acts on the cards it reads in the step's own read of the
epoch, and with no `--epoch` runs at the epoch that read finds (a clear between
the read and the write is read again), so they need none; the coordinator
given `--epoch` is held to it like any other actor.

### Handing over the seat

The holder (or the owner, `init --owner`, else NOVA_SPRINT_OWNER) gives the seat: `coordinator <name> --reason <text>`; with the holder away, `<name>` takes it with the owner's name, `coordinator <name> --take --approved-by <owner> --reason <text>`, refused without that name or with another (the owner, 2026-10-02: "you can be given coordinator status, or you can take it (with my permission only)").
Either is one commit of the coordinator, the seat's record and a happened note, its log line `seat: <from> -> <to>: <reason>, by <actor>` or `seat TAKEN: <from> -> <to>, approved by <owner>: <reason>`; a take's note is addressed to the old holder; every coordinator verb then takes the new name and refuses the old.
`handover` prints the holder and since when, the machine and the progress, each stream's counts, the sentinels held with what waits behind each, every open judgment with its answer lines, the members held or down and by whom, the routes disabled, the last ten decisions (release, drop, fleet down, rework with a fix, seat) with their reasons, and the lines the next seat runs first; `coordinator` prints it after the change, the leaving seat's receipt.
`inbox --wait --push seat` writes to the holder's inbox, `~/<holder>-working/inbox/sprint-judgments/` (refused when `~/<holder>-working/inbox` is not there), reads the holder at every look, and after a seat change pushes every open judgment into the new holder's inbox; a note addressed to someone with an inbox there goes to theirs.
The next seat runs first: `nova-sprint where`, `nova-sprint inbox --wait --push seat`, then reads this section.

## 12. The driver

`nova-sprint play` plays the outside world on a tick (`--every`), seeded
(`--seed`) so a run repeats: workers taking and finishing work cards (`--fail`),
readers reporting read cards (`--broken`), each stream's merge step with its
facts (`--batch`, `--stuck`, `--cross`, `--red`), members going down and up
(`--down`: a member's machine that is up falls silent and stops beating;
`--up`: one that is down beats again; `--flap` is `--down` and `--up` with the
one chance; `--hold` plays the downs as the coordinator's hold instead;
`--silent <member>@<from>+<for>` silences one member for a while). A machine
that is down takes no work; when it comes back it takes work again. The driver
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
and the other streams' queues only when a fact needs them; `--down` silences an
up member's machine and `--up` brings a silent one back; with
`--hold` the driver releases every hold it took before it stops. Its facts come through one interface (a worker's result, a reader's
finding, a merge batch's outcome, which members are up); the seeded source is
one implementation.

`play --simulation` is the simulation that stresses the sprint, six locked
chances: a reader finds the work broken, 10 percent (`--broken 0.10`); work
comes back not ok from the fleet, 10 percent (`--fail 0.10`); a merge needs
help from the coordinator within its stream, 10 percent (`--stuck 0.10`); a
merge needs help across streams, 1 percent (`--cross 0.01`); a machine that is
up goes down, 1 percent each second (`--down 0.01`); a machine that is down
comes back, 10 percent each second (`--up 0.10`). `--simulation` sets all six,
and a chance flag given beside it (before or after) sets that one chance and no
other; `--red` is not one of the six and stays as it is. A chance is per report
(`--broken`, `--fail`: drawn each time a card is reported, so a card reworked
and reported again draws again), per merge batch (`--stuck`, `--cross`,
`--red`) or per member and second (`--down`, `--up`, `--flap`): a tick of
another `--every` than a second draws the chance of at least one such event in
that time. The draws come from the seed; play prints the chances its seeded
source draws with, once it may play and before its first tick (`chances:
broken=0.1 ... red=0 seed=1 every=1s`; at another `--every`, `down` and `up`
are the chance of one tick, not the flags' own). A play that is refused prints
nothing on stdout. A chance outside 0 to 1 is refused. The state of which
machines are down is the play process's: a play run again starts with every
machine up.

## 13. Epochs and clear

The four tables are bound to one epoch of the sprint (the table layer's epoch
key, one hash of the sprint). Every step reads, writes and names
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
their total. `run` is the process that ticks: it blocks on the epoch's log
(XREAD BLOCK from the last line it has seen, pipelined with a read of the
log's last id: one round trip a wait), and a line wakes it, so a step that
frees room or makes cards ready (a finish, a merge, a drop, a release, fleet
up, start) is ticked on at most TickFloor (100 ms) after the tick before
began; a quiet log ticks it TickEvery (1 s) after the tick before began. It
moves nothing while STOPPED; `tick` is one tick by hand. The state
is read at the start of each tick and before each of its parts: after `stop`
returns STOPPED no part begins, and the part in flight finishes. Every verb works in both states; only the tick's duties
wait. `inbox` says `machine: running`, `machine: STOPPED` or `machine: DONE`,
and nothing after the word: `machine: STOPPED` is also what it says when the
state is RUNNING and nothing has ticked for 15 s (MachineSilence), on a store
a run loop ticks. A twin (`mem:<file>`) is ticked by hand and nothing ticks
between its commands, so there a RUNNING machine says `machine: running`
however long since its last tick, and the inbox's judgment that it is not
ticking names `nova-sprint tick` (a twin refuses `run`). The sprint line of every
verb says the same of a running machine after the progress
(`3/10 30.0% -> ETA -  machine: running`, the ETA a dash until five cards have
landed); a STOPPED machine has no ETA, so its
line is `STOPPED`, followed with cards on the
table by the progress alone (`STOPPED  3/10 30.0%`); the STOPPED text is the
one the header of `where` shows, which carries no progress; a failed tick keeps
its error on the heartbeat, with the count of failed ticks in a row, and the
inbox judges it (the line carries no suffix). A tick that did nothing writes the heartbeat at most once every
5 s (HeartbeatIdleEvery); a STOPPED machine's tick only records that it
looked. `where` shows the same
state as the one line under its title (section 1).

The machine stops itself when the sprint is done (section 8): the tick's last
part, done, says so to the coordinator and, in the same step, sets the record
STOPPED with the cause done; no part runs after it and the next ticks look.
`inbox` then says `machine: DONE`, the header of `where` and the view say
`DONE`, and the sprint line reads `STOPPED  9/9 100.0% done`. While the machine
runs with every card landed, before that tick, the line has no ETA:
`9/9 100.0% done in 1h2m0s  machine: running`, the time from the machine's
first start of the sprint. An add of a card leaves the machine STOPPED and
takes the cause off (`STOPPED`), and `start` runs it again; a start of a done
sprint with nothing added stops again at its first tick; a `stop` of a done
machine takes the cause off and writes nothing.

A tick first finishes an operation pending past its grace (T5). It then reads
the fence and the tables' shapes. It reads what is due from the state whenever
it reads the whole sprint, and it reads the whole sprint when any table's
revision changed, after `start`, after a tick that did not finish (a part that
lost to other writers, a stale epoch, a halt, a failure, or moves left past a
bound), and once every minute (TickFullEvery); otherwise it does nothing
else. Otherwise it runs its parts in order, each one operation of the
engine on a fresh read, sharing the fence with every verb: first the start,
once (the rebalance of the fleet table and of the readers table, section 5 and
section 6), then resolve (T1:
every stream's waiting cards in score order; a card whose needs have all landed moves to ready; a sentinel is never
moved, and is marked reached when all it needs has landed), resume (T7: a
stream stopped only on a cross need whose card has landed), deal (T3), accept
(R9: every acceptable primary in review the tick does not hold, section 6,
moves to merging and into its stream's merge queue, and the coordinator is
told once for each stream "ready to merge"; the merge is the coordinator's, and
the note names `land --stream <s>`, which a `run --land` does itself),
ask (T2: two different readers up for each primary in review with
fewer than two read cards at its attempt and work not failed; a read asked of a
reader that is not up is taken back first, section 6), check (T6: section 9, and the
no-stall rule 12), deadlines, overdue, done (the sprint done: the machine
stops). Each part is
bounded per tick (200 moves, 50 notes): the rest are due, the next ticks
catch up, and the machine line says so. A card made ready is dealt in the same
tick. Running a tick twice in a row changes nothing the second time.

The tick writes a judgment once while its condition holds and closes it when
the condition clears (closing a primary's last judgment in review, it writes
the judgment the primary needs next, as every step that leaves one in review
does): cannot ask (two readers are up and fewer than two different readers are
free for a primary, who has not already read its attempt;
one condition per primary whatever its count of free readers),
fewer than two readers up (the sprint's, one whatever the primaries waiting:
the ask asks none while it stands, section 6),
no fleet member is up, a work card past its deadline, by its state (dealt,
never taken, ready or withdrawn again before a take: the dealt bound from
untaken_since, the first deal since its last take; a card's own deadline starts
at its take, and a dealt card waiting in its member's ready queue is the
machine's queue, not the card's fault (nova-tools#5096 item 22: nine judgments at
once on a bench whose cards dealt ahead aged in ready); the dealt bound is the
sprint's `set --dealt-max`, else 3 times the take deadline, 6 hours (a member
holds at most 2 times its width, so a card at the back of its queue is taken
within two take deadlines of a member that works); its judgment says
`<card> dealt, never taken, at <member>:ready (dealt <time>, over the dealt bound
<bound>)` and offers `fleet level`, `fleet down <member>` (when that member has had
the whole bound itself) and `wait`; not finished, working or withdrawn from a
take: 2 hours from the attempt's first take; no redeal or withdrawal rewrites
either, and the time a card spends withdrawn counts; the no-stall rule holds a
card to the same deadline; a lateness is one judgment per card and kind, and
once raised it stays raised while its cause stands, whether or not the card is
late at that moment: not finished until the attempt's work card is finished,
reworked or dropped, never taken until it is taken, not begun until the read
begins, not reported until it reports; a redeal or a return to ready closes
none of them; while raised it is updated in place with where the card is, each
update a line of the log; no judgment is closed by a move that does not
resolve its cause), a read card past its deadline (30
minutes asked and not begun, 2 hours begun and not reported), a stream with no
merge step past its deadline (30 minutes), an invariant is broken (the rule
and the cards), stalled (rule 12: what nothing holds, and why). Deadlines count running time: time spent STOPPED does not
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
commands start the loop or stop the machine; on a twin, which ticks only by
hand, they tick by hand or stop it), the tick keeps failing (three
failed ticks in a row, with the last error), and the machine is STOPPED and
moves are due (primaries ready, work cards withdrawn, waiters whose needs have
landed, primaries in review never asked at their attempt; the command is
`start`). A tick that fails writes one happened note to the coordinator for each
error text it fails with (the error, the tick's number and the time) and one when
it works again (the count of failed ticks); the tick end wakes `inbox --wait`
on them. `init` writes the machine STOPPED from the
start, so the time before the first `start` is a STOPPED span and counts
toward no deadline. `clear` writes, at the new epoch, the happened line that
the machine is STOPPED by the clear.

### The server

The owner, 2026-10-01: "single threaded server, pipelined batches like redis." / "I think we
should not use redis as the transport, but have a client/server" / "so we have our own
redis-like thing that the distributed things talk to." / "simple client/server always wins."

`nova-sprint run --listen <host:port>` makes the run loop the sprint's server as well as its
tick: the one writer of the sprint, beside the store. A worker started with `nova-swarm member
--server <host:port>` sends its verbs there and reads and writes nothing of the store from its
own machine. A request is a batch: the worker's verbs, each the argument list it would give
`nova-sprint`, in the order to run them. The server runs each through the verb's own code, in
its own process, and answers with each verb's exit code and what it printed, one answer a verb,
in order. One batch, and one tick, at a time: neither runs during the other. The server keeps
nothing between requests.

The server runs the workers' verbs only: `take`, `finish`, `read` and `queue`, each beginning
`<verb> --as <worker>` with one worker's name, `fleet beat <member> --load <percent>` and
nothing more, and `friend beat <friend>` and nothing more. No later word of a verb, wherever it stands, is a flag named `as`, `redis` or
`actor`: the server gives the store and the actor, and puts them before the worker's words. A
`take`, a `finish` and a `read` name the epoch their worker holds (`--epoch`). A `queue`'s
`--packets` is a count from 0 to 1024 and its `--have` card ids, each given once: a worker asks
for the packets it can use this pass and no others (a reader of width 8 with 150 asked reads:
445,525 bytes an answer before, 51,623 after; a member with 32 cards working and 32 ready:
184,003 before, 10,637 after). A verb the server does not run is answered exit 2, saying
nothing was changed, and the batch goes on.

The coordinator's verbs go to the server too. The server listens a second time on the
loopback address at the same port, and there it runs any verb of the command but the ones it
runs for nobody: itself (`run`, `tick`), `land` and `play`, which work outside the store for
seconds or minutes, `fleet sync` and `friend sync`, which read the config store with their
caller's own credentials, and `friend clean`, which works on the directories of the machine it
runs on, nor a read that waits for the sprint to move (`where --watch`, `inbox --wait`): the
server moves the sprint on the one line of control such a verb would hold. With
`NOVA_SPRINT_SERVER=<host:port>` set (the loopback address `run --listen` prints), every verb the
server runs, the reads included, is not run where it is typed: its arguments are sent to the
server, with the caller's actor and each file it names as an absolute path, and what the
server's run of it printed (its stdout, its stderr, its exit code) is printed there byte for
byte as the verb run on the store prints it. So the coordinator's side names no store and holds
no store credentials. The verbs not served, a verb given its own `--redis` (it names its own
store), a verb's help and flags the verb refuses run where they are typed. A waiting read waits
where it is typed and holds the server between none of its reads: `where --watch` sends one plain
`where` a frame and draws it as its own watch does; `inbox --wait` reads the log's tick-end notes
(`log --json` as far back as its timeout and a second) once a second, and at each one that its
last read did not show (and every 15 s while none comes) reads `inbox --json` for what is new
for the coordinator, until something is or the machine stops or its `--timeout` passes, then
sends the plain `inbox` and prints it as `inbox --wait` does (its one line of how it ended, and
`woke` and `new` under `--json`); `--push <dir>` writes its files where it is typed, each group
read whole through the server (`inbox --json --open <id>`). `inbox --wait --read`,
which would wait here and move the cursor the server moves, is refused with a server named
(exit 2, nothing changed): run `inbox --wait`, then `inbox --read`. A server that does not
answer is said in one line with what to do (start `nova-sprint run --listen`), exit 2, and the
verb is not run on a store here instead.
What the arguments say (a help flag, which word is a flag's value, a file flag, a `--`) is read
by the verb's own flags, never by a scan of the words: `add --stream help` is a stream named
help, and in `--brief --rules` the brief is the text `--rules`. Who acts is the caller's actor
alone: the server puts `--actor` with no one before the caller's words, so a verb that names no
actor acts as no one and is refused, never as whoever the server's own environment names. A
worker's verb from this machine names the epoch its worker holds however its words are ordered.
So one process reads and writes the sprint: the server.

With `run --land` the server lands what the readers passed, itself: every two seconds, when a
stream has cards queued to merge, it runs `land` for them as the sprint's coordinator, one
landing at a time, in its own process. Land's reads and its report take the server's line of
control like any other step; its git (the fetch, the merges, the check, the push) runs outside
it, so a tick or a worker's batch never waits on a push. A `land` run by itself beside a
server is a second writer of the merge queue, and is what `--land` replaces. A round prints
what landed and everything land said was wrong (a refused or failed batch, a refusal before any
batch, its remedy); a round that could not read the merge queue prints `LAND FAILED` with why,
since an unreadable queue is not an empty one, and the next round tries again. A failure is
printed once, when it begins: the same failure again prints nothing until it changes or clears.

The address is one address of the coordinator's machine on the fleet's private network; an
address every network can reach is refused. The server checks no credential (the owner: "I am OK
with relying on tailnet as secure"): what can reach the address can run a worker's verb as any
worker, and nothing else.

A worker whose answer was lost sends the verb again with the same operation id (`--op`): a
committed operation returns its recorded result and changes nothing twice; a refusal, or a take
that found nothing, left no operation and is run again. A server that does not answer is, to the
worker, a store that did not answer.

The server is `serve` in cmd/nova-sprint/serve.go, a step with no network in it; the listener is
a shell around it; the wire and the worker's client are internal/sprintwire. Each rule here has a
test in cmd/nova-sprint/serve_test.go and internal/sprintwire/worker_test.go, and none opens a
socket.

## 15. Reminders

The people who work on a sprint each have a goal: a text of what to keep doing,
and a route that reaches them (`goal set <name> --file <path> --to
file:<absolute path>`, `goal show [<name>]`, `goal drop <name>`). While the machine is
RUNNING the tick pushes each person's goal down its route once every five
minutes of running time (RemindEvery), and at once when the goal or its route
is set or the machine starts; nothing is pushed while it is STOPPED. The file
route replaces one file with a header line (`REMINDER <n> to <name> at <time>,
epoch <n>`) and the text, whole, so a watcher of the file sees
one current reminder. A route that fails is one judgment, "a reminder could not
be delivered", closed when a later delivery arrives. `goal show` shows each
person's last push (`where --json` carries it). The people and their goals are the sprint's, not the
epoch's: a clear keeps them and resets their pushes.

## 16. Sentinel cards

A sentinel is a primary of kind sentinel, a stop in its stream, admitted by
`add --stream <s> --sentinel <id>`, at the end of the stream or
`--before`/`--after` a card (its score between its neighbours; refused when no
score lies between; the line is never renumbered), or many at once: `add
--stream <s>[,<s>...] --count <n> --sentinel-every <k> [--sentinel-last]` puts
n cards in each stream named with a sentinel `<s>-gate-<i>` after every k of
them (none after the last unless `--sentinel-last`), all streams in one step.

A sentinel's property belongs to its position in its stream's order, and
nothing about position is stored as a need, on the sentinel or on any card
behind it: stored needs are only what a card names itself (`--needs`), in its
own stream or another. One function reads position for the needs rule, the
tick's resolve, reached, `card`, `check` and the reference model:

- A sentinel waits for every primary of its stream that sorts before it and
  is on the table and not landed (a dropped one is off the line), and for the
  needs it names.
- A primary that sorts after an unlanded sentinel of its stream may not
  leave waiting: it waits behind the latest such sentinel before it (add
  prints `waits behind sentinel <id>`).

So adding a sentinel writes one card at one position, whatever the stream's
length. Inserted in line, the ready cards behind it go back to waiting because
the order now says so, as one set move and one line of the log; a reached
sentinel behind it is no longer reached; the cards in flight are printed as
already past the stop. A card added in front of a reached sentinel un-reaches
it. A rank that moves a card across a sentinel changes what it waits for, by
the same reading. A sentinel is never dealt, read or merged. When what it
waits for has landed, been dropped or been waived, the step that ended the
last of it (or the tick, as the backstop) marks it reached and writes one
judgment. A sentinel with nothing placed before it (no primary of its stream on the
table before it, landed or not, and no need of its own) is not reached while other
work of the sprint is in flight (ready, working, in review or merging): it is
simply next, no judgment is written for it, and it is held by that work; it is
reached when no other work is in flight, by the step that ends the last of it or
the tick (`sprint.Reachable`). Cards placed before it later make it wait for them,
and it is reached when they have landed. Only `release <id> --reason <text>`, by the sprint's coordinator
(`init --coordinator`; `where --json` carries it), lands it, reached or with nothing
before it; the same step moves what
waited behind it, up to the next sentinel, to ready as one set, marks reached
any sentinel now due, and always writes a notification that it landed. A need
it names that is dropped blocks it like any waiting card; ack waives the need.
It counts in the sprint line and in waiting and landed, and the sprint is not
done while one waits. A ready primary whose work card was withdrawn (no member
up) stays ready when a sentinel is inserted in front of it: it has started,
and is past the stop; check's rule 2 holds that the primary of a withdrawn card
is ready.

`add --held` admits every card of the add held (nova-tools#5096 item 15: a
sentinel at the head of an empty stream was reached at once and raised a
judgment): waiting, stamped `held`, whatever its needs. A held sentinel is never
marked reached and raises no judgment; a held card never moves to ready and is
never dealt; ack's waiver leaves a held card waiting; the no-stall rule reads
the hold as the coordinator's (c). So a wave loads behind a held sentinel at the
head of an empty stream, or as held cards, and nothing fires.
`release <id> --reason <text>` lands a held sentinel that waits for nothing as it
lands a reached one (one that still waits is refused, naming what it waits
for), and clears the hold of a held card, which goes to ready in the same step
when it waits for nothing else (`sprint.IsHeld`, `sprint.Release`).

## 17. The log

Each epoch has one log: an append-only record of every change of every card
and every notification, in the order written. A step's lines are written in
the same transaction as its change (the operation's commit; a repair that
finishes an operation writes its lines, and an entry the repair skips has no
line, the skip's judgment saying what it skipped), so no card changes without
its line. Nothing in the log is ever rewritten or trimmed within its epoch: a
second accept, a second ci result and a return each add a line. The inbox's
cursor never hides a line. The log has no hidden lines: every line is every
actor's to read.

A move line has: the time, the epoch, the operation that wrote it, the card,
its primary and its stream, the table, the place before and after
(member:column; none before when the step created it, removed when the step
took it off), its generation after, who acted (the machine, for the tick's own
moves: deal, redeal, level, withdraw, ask, resume, the sentinels), the verb,
the cause in the step's own words, the judgments the step answered on it, the
words given with it (brief, fix, report, finding, reason, return reason, did,
note, ci note), whole, and the other fields it set. A notification's line has
its kind (judgment, happened, decided, acknowledged) and the notification as
written. A note on a card will be a line of its own kind. A set move (many
cards of one step with the same place before and after, the same words and
fields, a score aside) is one line naming its cards: the log grows by the
steps, not by the cards they move.

`log` prints each line in plain words at its local time, the words given
following it as paragraphs; one function renders a line, for `log` and for a
card's timeline. In the whole log's timeline (no `--card`) a cost record a change
set is one short line, `<primary> cost: <kind> <card> by <who>, <end>, ran <n>s,
cost <$ or ->` (`sprint.SplitCost`), and a change that set only cost records prints
no line of its own; `log --card <id>` and `log --json` keep each record whole. At clear, the log stays with its epoch and is read with
`--at-epoch`, as `where` is; the new epoch's log starts empty. Teardown
removes every epoch's log. The log is stored beside the notifications (a
stream of its own in the same transaction), so the inbox's reads never page
through it.


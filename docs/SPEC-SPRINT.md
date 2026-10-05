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

work  | waiting | ready | working | review | merging | landed | cost | per landed
readers | asked | reading | width | ok | broken
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
| friends | friends | sprint cards | who of the friends is here to help, and her cards |

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

After `cost` the text table draws `per landed` (the owner, 2026-10-04: cost
visibility, after a night of $437 for 844 landings whose pro streams landed at
$4.50 to $8.88 a card and flash streams at $0.10 to $0.27): the stream's
dollars per landed card, rounded up to the cent, `-` with nothing landed or
nothing priced, the tick's count when its where record holds one
(`sprint.TierCosts`, cost_view.go) and else the row's cost cell over its landed
count (`sprint.PerLandedOf`); its footer is blank (the sprint's figure is the
dashboard's). `where --json` carries, additively: `tiers` at the top, every
card counted by the tier its brief names (flash when it names none; any word
the briefs carry), on each `tables.work[<stream>]` row `per_landed` (every table
cell stays a string, the shape the dashboard's pull decodes), and `stream_costs`
beside the tables, each stream's `tiers` (its cards by tier) and `cost_by_tier`, its spend by the
tier each attempt and read ran on (the cost records' tier, not the card's
ceiling: a flash card escalated to pro shows both; a record with no tier is
`untiered`), money strings as the cost column shows them. All of it is counted
by the tick from the sprint it reads anyway and kept in the where record
(store.WhereRecord), never read card by card at `where`; before the first tick
of an epoch `tiers` and `cost_by_tier` are absent and `per_landed` is from the
cells (`TestTheWhereRecordCountsTiersAndCostsByTier`,
`TestWhereCarriesTiersAndPerLandedCost`).

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
nova-config no longer has with her beat, and keeping the hold of a
friend that stays; a config that cannot be read or holds no friend row is
refused (exit 3) and changes nothing.

A friend's counts are her sprint cards' (a friend's card, below), read by
`where` from her fleet row `friend.<name>`, never from her working directory: a
card dealt to her is `working`; a `Verdict: LAND` report (with its `Head:` the
tip) finishes it done ok, a `Verdict: HOLD` or `FAIL` (or `FAILED`, `BROKEN`)
report done failed, and a report with no verdict word is done failed too,
never ok. `ready` is never a friend's card's state: the tick deals a card
straight into `working` (`sprint.TickDeal`). A hand-written inbox job that is
no card (an `inbox/<job>/` directory named for no card of the sprint) is
outside the sprint and is shown nowhere in the table; the coordinator's NOW.md
is its pointer. `ready` and `working` count her cards in those states; `width`
is her width, the jobs she works at once: her nova-config friend row's `width`
(`nova-config friend set <friend> --width <n>`, at least 1, 8 by default; the
owner, 2026-10-02: "6/1 seems a bit wrong -- need to setup width for friends?
Start at 8 for each?"), which friend sync writes to her row each pass (a row
whose width is below 1 is refused, exit 1, nothing written), summed in the
footer; `ok` and `failed` count her cards done; `done` is `sum(ok+failed)` and
`ok%` is `pct(ok/ok+failed)`, pooled over the friends in the footer, the fleet
table's own formulas.

A friend says she is there with `friend beat <friend>` (answered `FRIEND-BEAT OK
<friend> at=<t> ... row_mode=<batch|one-shot> row_width=<n>`, her nova-config row
as friend sync last copied it, which is how her daemon reads her delivery mode;
docs/SPEC-FRIEND.md, one-shot lanes), which her own machinery
runs every second (`FriendBeatEvery`) beside her harness (it writes
`friend-beat:<friend>`, the time to the second, with what she reports of her
work: `--running <id>,...`, the cards she is running now, which `friend take`
and `friend down` leave with her, and, as `fleet beat --load` gives a
machine's load, `--working <n>`, `--queue <n>`, `--width <n>` and `--load
<percent>`, her own counts and load as her daemon keeps them, each kept until
the next beat and carried on `where --json`'s `friends` beside the table's
counts, which stay the sprint's (her row's cards) and her width the roster's; a
value of the wrong shape is refused, exit 2; a friend not in the record is
refused, exit 1; `TestFriendBeatTakesHerCountsAndLoadAsFleetBeatTakesALoad`). Her status is the friends' rule (`sprint.FriendStatus`):
`held` while the coordinator holds her (`hold <friend> --reason <text>`,
section 11, and `friend down`; `unhold <friend>` and `friend up` release the
hold), whatever she beats or the coordinator observes; else, once the
coordinator has observed her (`friend health`, below), the observation's word
alone, `up` or `down`, and never her beat again while the observation stands; else `up` while her last beat
is under `FriendDownAfter` (15 s) old; else `down`, and `down` when she has
never beaten (`friend down` holds her and shows `held`, never `down`). An
observation stands until it is removed: `friend health <friend> --clear` (the
seat's holder) and the stall ladder's release (section friend-stall-ladder-r.w1)
remove it, and her status is her beat rule again. A beat
wakes an unobserved friend at once. `friend up` is not a beat: a friend
released with no beat in the last 15 s is `down` until she beats. `friend up
<friend> --width <n>` sets her width (1 to `MaxWidth`), as `fleet up --width`
sets a machine's, until `friend sync` sets her nova-config row's again (a
release without it leaves the width as it is;
`TestFriendUpWidthSetsHerWidthAsFleetUpWidthSetsAMembers`). A friend's
statuses are `up`, `held` and `down`, the same words as the fleet table's, and
nothing else is ever shown (the owner, 2026-10-04 11:42 AM ET: "a friend is up
or down"; "anything but up is down"; "sleeping = down"; held means exactly one
thing, "the coordinator has specifically decided to hold this friend"). A
friend held or down with a reason shows it in her status cell, `down (opus
rate limited, until 6:00 PM)`: `friend down <friend> [--reason <text>]
[--until <RFC3339>]` records why and when the coordinator expects her back
(the owner, 2026-10-04 11:30 AM ET: a friend's model allowance "can run
out"), and an observation carries the same (`friend health --reason --until`);
`where --json` carries the cell as printed. A friend whose harness says it is
out of credits or at a usage limit is down by her own daemon (the owner,
2026-10-04: "stopped on credits means she should automatically be DOWN";
`internal/friend/limit.go`, docs/SPEC-FRIEND.md, a harness at its limit): from
the turn that said it, `nova-friend run` sends no beat, so her row reads
`down`, delivers nothing, and tells the seat (else `--coordinator`) once with
the line that shows why, `friend down <friend> --reason <its words> --until
<the reset>`; after the reset a wake turn must be answered from inside her
session before she beats again (a harness not running at the reset keeps her
down), and the seat is told she is back (`friend up`).
`TestAHarnessOutOfCreditsMakesItsFriendDownUntilTheReset`. Not yet: her
measured utilization on the beat (`friend beat` takes no usage flags), and a
Claude Code friend's `rate_limit_event` (the claude harness has no deliver
command, so no run's output passes through the daemon). A
friend `down` shows `working` 0 in the table, its footer and `where --json`:
her cards stay on her row and count again when she beats, and `ready` and
`done` are as they were (the owner, 2026-10-02 9:48 PM ET: "[a friend] being down,
she automatically is 0/8 working OK?"). The
owner, 2026-10-02 9:44 PM ET, on a friend shown up while she was gone: "two
minutes is too long. 1m", "maybe even 30 secs."; and at 9:46 PM ET: "heartbeat
should ping once every 10sec", then "or every 1sec if you really want, then
after 15 sec. asleep. better."; the word was `asleep` until the owner,
2026-10-03 8:04 AM ET, looking at the friends table: "Please change 'asleep' to
'down' so we have consistency across all tables". The fleet's rule (`MissedBeatsDown` windows of
`BeatDeadline`, down past 45 s, section 5) is separate and stays longer: a
machine down has its cards taken back; a friend holds the cards dealt to
her row (a friend's card, below) and keeps them when she goes down, the
deadline judging them. The
rows are in the fleet table's order (`FleetOrder`): up, then held, then down,
each by name. The table is drawn by `where` from those records when it draws
the frame, never stored as a table: no tick, step, epoch or clear touches it,
`teardown` deletes its records (the roster, each friend's beat), and
the stored view `sprint` has the four tables only. Its footer is the table
layer's: the sums of `ready`, `working`, `width` and `done`, the pooled `ok%`,
and a blank status cell, as the fleet table's; an empty friends table is its
header, its one rule and that footer at zero, as every empty table is.

**Last session activity** (the finding of 2026-10-04: the table said up with 8
working for a friend whose session sat idle from 2:40 to 4:34 PM, and another read
working=0 while she was busy; a daemon pong shows the daemon answers, not that her
session moves). The friends table has one more column after `status`, `active`, text with
no fold: how long ago her session last wrote a file, `4m ago`, `-` when her beat reported
none (an older daemon, or none yet). Her daemon walks her working directory and outbox
(docs/SPEC-FRIEND.md, last session activity) and sends the newest write on her beat
(`friend beat <friend> --active <RFC3339>`); the beat record keeps it as the report's
`active`, `where --json` carries it on her row (`active`), and a beat that reports none
keeps none. The coordinator's view raises an alarm item, `friend idle` (the `friend`
type, next the `friend take` that returns her cards), for a friend that is up, has cards
dealt to her row (ready or working) and whose newest write is older than the setting
`friend_idle` (`set --friend-idle <duration|default>`, 20 minutes by default, the work
table's property, taking effect at the next tick as `dealt_max` does). A friend with no
reported activity raises none of this kind: her silence is the report rule's (15 minutes
without a beat). The column is a field of a table locked on 2026-10-01 and is added by the card
friend-session-liveness.w1 (internal/sprint/TABLES.lock, the 2026-10-04 entry).

**A friend's health** (2026-10-04, with the author of the coordinator's
daemon, nova-friend: "the coordinate daemon is the keepalive SERVER. The
existing sprint server is the authority/table service"). The coordinator's
daemon keeps a keepalive with every friend's daemon (SPEC-FRIEND.md: a ping,
the daemon's pong, the session's pong with the nonce) and writes what it saw
with `friend health <friend> --state up|asleep|down --seen <RFC3339>
--generation <n> [--queue <n>] [--working <n>] [--width <n>] [--reason <text>]
[--until <RFC3339>]`: the word it observed (`up`, her session answered;
`asleep`, her daemon answered and her session did not; `down`), the time the
proof it rests on was seen (the session pong for `up`, the daemon pong for
`asleep`, the judgment for `down`; the daemon's clock), the seat's generation
it read the seat at, and what her pong said. The sprint server is the
authority and the table and nothing more: it validates no nonce (the daemon's
ring does), adds no challenge and no authentication; the existing local trusted
route stays non-cryptographic.

The seat carries a generation (`sprint.SeatChange.Generation`;
`FirstSeatGeneration`, 1, from the first init with no seat record; every
accepted `coordinator` change takes the next, `MoveSeat` on the snapshot's
`SeatGeneration`, read with the coordinator in every step's own read, so two
racing handovers never share one; a replayed `--op` is the recorded result
and no second increment; init and clear never reset it; teardown removes it
with the seat). `nova-sprint seat` is the daemons' read of it every second,
`SEAT holder=<h> epoch=<n> generation=<g>` and `--json` `{holder, epoch,
generation}`, three keys and no table (`store.SeatState`); `handover --json`'s
`seat.generation` and the seat record carry it too.

The fence (`sprint.NotHealth`, applied by the health step `ObserveFriend` on
its own read of the seat, so a seat that moved between the daemon's read and
the write refuses it): the sender (`--actor`) is the seat's holder, the
observation names the seat's generation now, the friend is on the table, and
the proof is dated no later than the server's clock and newer than the row's.
Each refusal is one line, exit 1, nothing
written: `friend health is the seat's: <holder>, not <actor>`; `the seat is
<holder>'s at generation <g>, and this observation names generation <g'>: read
the seat again (nova-sprint seat)`; `the proof is dated <t>, after the server's clock,
<now>: a proof from the future renews nothing` (a review of PR 5305: a
`--seen` ahead held her up until it plus ten seconds, and the order then
refused every real observation before it); `the row holds a proof seen at <t>, and
this one's is not newer, <t'>: an older or repeated proof renews nothing`; `no
friend <name> on the friends table`. The same observation again (the same word,
proof and generation) is answered as recorded, `replayed=true`, and writes
nothing, so a proof never earns a second ten seconds. A->B->A: A's first
seat's generation is refused under A's second. The row (`friend-health:<f>`,
written by the step's commit as the seat record is, `OpRecord.Health`) keeps
the word, `seen`, `generation`, the counts, the reason and the until; `where
--json`'s friends rows carry it as `health`. The answer: `FRIEND-HEALTH OK
<friend> state=<word> seen=<RFC3339> generation=<g> status=<up|held|down>
[replayed=true]`, `--json` `{friend, state, seen, generation, queue, working,
width, status, replayed}`.

The table's word from an observation (`sprint.ObservedStatus`): `up` only when
the observation says `up`, under the seat's generation now, with its proof
under `FriendObservedDownAfter` (10 s) old and not dated after now (a negative
age is no proof); `down` otherwise, at exactly ten
seconds, under any other generation (an old seat's proof never looks up under
a new seat, and no fallback to her beat once observed), and for every finer
word the row keeps (`asleep` is the daemon's, shown as `down`). The first
valid observation makes her `up` at once. Her own `friend beat` stays what it
is, the friend's own beat, and once she is observed it decides nothing: the
observation wins the word. No observation holds a friend: `held` is `friend
down` by the seat alone, lifted by `friend up`. `friend health <friend> --clear`
(the seat's holder alone, refused otherwise with nothing written; `--dry-run`
says what stood and writes nothing; no observation's flag beside it) removes
her observation (`friend-health:<f>`, by the step's commit,
`OpRecord.HealthClear`), and her status falls back to her beat rule: `FRIEND-HEALTH
OK <friend> cleared=true was=<word|none> status=<up|held|down>`, `--json`
`{friend, cleared, was, status}`; a friend with no observation is cleared all
the same (`TestFriendHealthClearFallsBackToHerBeat`). It is the way back for a
friend observed once with no keepalive renewing it, whom her own beat would never
bring up again (2026-10-05: one write by the stall ladder's release held
working friends down). The model is
`tla/SeatHealth.tla` (six reversed witnesses); the tests
`TestAProofDatedAfterTheServersClockIsRefused`,
`TestHealthIsFencedBySeatHolderAndGeneration`,
`TestFirstProofIsUpAndTenSecondsWithoutOneIsDown`,
`TestHealthReplayOrderBeatAndHold`, `TestSeatGenerationFromInitThroughHandovers`,
`TestFriendHealthIsTheSeatsAndFencedByItsGeneration`.

**A friend's card** (the owner, 2026-10-03: "Could we try expressing the work
left for nova-tools-1.1.0 into cards, and doing it via the sprint, but doing
parts on friends where we would normally do friend work."). WHO is a preference (docs/SPEC-CARD-CONTRACT.md, the WHO line; `cardhdr.ReadWho`
is the one parser). A card with no WHO line, or `WHO: -`, is unpinned. `WHO: friend`
is any friend. `WHO: friend <name>` prefers that friend while she is up with room.
`WHO: only friend <name>` is the one hard pin and waits for her alone. `add` and
`brief` refuse any other WHO value, a name that is no row of the friends table, and
`WHO: friend` while the table has no row (exit 2, nothing written). The primary's
field `who` is `friend`, `friend.<name>`, or `only.friend.<name>`, written with its
brief, and `card` prints `who=` on its `CARD OK` line (`--json` `who`).
`nova-sprint unpin <id>... --reason <text>` (or `unpin --stream <s> --reason <text>`)
removes that stored value from a waiting or ready primary that has never been dealt,
or whose first attempt `friend take` returned to ready without starting or ending a
take, including while the machine runs. The returned work card keeps its identity and
is redealt at its next generation. It changes no brief bytes. Each unpin records the
actor, time, reason and removed WHO line. An already-unpinned card is a reported
no-op. `--dry-run` reports the plan without writing it; any refusal, including a
mixed preview, has status `refused` and exit 1 in text and JSON. `WhoPreference.tla`
checks the selection and the hard pin; the reversed only witness permits fallback
and violates `OnlyToItsFriend`. The tick reads the friends' roster before each pump,
because a queued change can make work ready in the same tick. The tick's deal offers
ready work, in the deal's stream turns, to a
friend up (the friends' rule: not held, a beat within 15 s) below her room: in
batch mode (the default, her nova-config row's `mode: batch`), DealAhead (two)
times her friends row's `width`, as the machines' rule fills a member (section
5; the cards on her row, ready and working, count against it; the owner,
2026-10-04: "Do it just like the fleet, you keep people busy by having 2X width
queued up in ready per-friend"): on her row the card is `working` while she has a
lane free (her width less her working cards; dealt and taken at once) and `ready`
behind her working cards otherwise, so her inbox holds her width working and as
many again ready; her finish of a card takes the oldest ready card on her row
into working in the same step (`sprint.Finish`, `friendNext`), no tick between,
as a machine's lane that frees takes its next; the next tick fills her room
again (`TestAFriendAtWidthEightWithThirtyCardsHasSixteenDealt`: width 8 with 30
waiting is 8 working and 8 ready, and a 17th on a landing). In one-shot mode
(`mode: one-shot`), the machine deals one card at a time, straight into
`working` (room 1, lane 1), and the next only after the last one finished
(item 22 of tmp/manual-to-verbs-2026-10-04.md;
`TestDealingRespectsAFriendDeliveryMode`). For either mode, the deal (friends
first, before the machines' deal) offers every ready card to its named friend
whose tiers hold its tier first, then to the friends up whose tiers hold its
tier, an idle lane first,
then the most room free, the first by name among equals, then to the fleet. A
hard pin (`WHO: only friend <name>`) with no room waits ready, held by the
no-stall rule as waiting for her (`sprint.TickDeal`, `sprint.FriendDeal`,
`sprint.OnlyFriend`); a card a friend takes is never held for want of a machine
route of its tier (`TestAReadyCardGoesToAFriendWhenNoMachineRouteServesItsTier`).
The tick reads the friends' records every tick while the roster has a friend.
Its work card, `<primary>.w<attempt>`, is placed on
the friend's own fleet row, `friend.<name>` (a dot, which no member's name
holds, so it is no machine's and no fleet verb names it), straight into
`working` at generation 1, dealt and taken at once (nothing takes it), member
`friend.<name>`, with the primary's fix, finding and why as a machine's deal
carries them; its primary moves ready -> working. The fleet's members are its
rows but the friends' (`Snapshot.Members`): presence, the rebalance, the
level, `fleet sync`, the machines' deal and the shape a clear keeps never
touch a friend's row, so a friend who goes quiet keeps her card (no
take-back by presence; the coordinator takes back what she has not started,
below), the no-stall rule holds it as hers whatever her status, and
the deadline rule judges it as it judges any work card, its working deadline
by friend: the larger of 2 hours from its deal (`DeadlineUnfinished`) and
`FriendDeadlineK` (three) times her median run wall over her last
`FriendDeadlineSamples` (fifty) ok attempts, her run wall being her take to
her report (`sprint.RunWall`), set on the card as `friend_deadline` (seconds)
when it goes into working on her row (her deal, her next on a finish, a level
or a redeal), and absent while she has no ok attempt: nova-tools#5300's member
rule, written here for friends under its own names because #5300 was not on
dev (`TestAFriendsCardsDeadlineIsThreeTimesHerMedianWall`,
`TestAFriendsNextCardsDeadlineFollowsHerRunWall`). The machines' `deal` verb refuses a hard pin (`WHO: only friend`), and
`rework` of a friend's card sends its primary ready with the fix, for the tick to
offer again.

**A friend's card taken back** (the owner, 2026-10-04, on cards dealt to a
friend who would not start them, which could only be dropped and added again:
"sounds bad, we should fix this"; `sprint.FriendTake`). `friend take <friend>
<id>... [--reason <text>]` (the coordinator's) takes back each card named (a
primary or its work card) that is dealt to that friend, ready or working on
her row, and that she has not started: its work card is withdrawn on her row
at its next generation with `taken_back` "taken back by the coordinator:
<reason>" and `taken_from` her row, its primary goes back to ready with its
attempt as it was (a take-back is no attempt and no failure: `take_ended` is
not set, so no redeal of its bound is spent), and its dealt bound runs again
from the take (`untaken_since`, `first_taken` unset). A card she has started
is refused: a push on its branch (origin holds it: one `git ls-remote` a card,
the tip `friend sync` reads; a tip that cannot be read, or a card with no
`REPO:` line, counts as started, so nothing is taken from under her), her
last beat naming it running (`friend beat --running`, by its work card id, its
job or its primary), or finished (in review or later, no longer on her row's
ready or working). A card not dealt to her is refused naming where it is. The
refusals are one line each and, with ids named, all or none.
`--all-unstarted` takes every card of hers she has not started and says each
one she keeps on a NOTE line. A working card taken frees her lane: her oldest
ready card not taken goes into working in the same unit, as her finish takes
it. The friends' deal places the same work card again (`friendRedealUnit`): to
the friend up with room its WHO line allows, never the friend it was taken
from, at its next generation, so on its own branch, and as its own job
(`<card>.g<gen>` from the second generation: a card dealt back to the same
friend after a hold is a new job whose brief names its branch); a hard pin taken
back from the only friend it names waits ready until she has room again, or until
`unpin` releases it. A preference taken back from her is offered to another
eligible friend, then to the fleet. While the machine runs, the take's work-table change waits
for the pump (section 4), so the tick after the next deals it. `friend sync`
writes a card taken back as `taken` in her queue file, so her daemon starts
none of them. `friend down <friend>` holds her as `fleet down` holds a machine
(the owner's ask: a held friend behaves as a held machine): the tick deals her
nothing, and every card of hers she has not started goes back to ready the
same way, `taken_back` naming the hold and no `taken_from`, so her own named
cards come back to her when she is released and beats; what she has started
stays on her row and finishes (`TestTheHoldOfAFriendWithdrawsWhatSheHasNotStartedAndKeepsTheRest`,
`TestFriendDownGivesBackWhatSheHasNotStartedAndKeepsTheRest`).

**Friend level** (the owner, 2026-10-04: "What else is like this? Missing
verbs we need for friends, that machines already have"; `sprint.FriendLevel`).
`friend level` (the coordinator's) evens the friends' ready queues as `fleet
level` evens the members' (section 5), within each class: a friend's class is
the tiers her nova-config row says she can do (`friend sync` copies them to
the roster, sorted and comma joined), and a card moves only between friends of
one class. Among the friends up of a class, while the largest backlog (the
cards on her row, ready and working, less her width) of a friend with a card
that may move and the smallest of the friends below their room (DealAhead
times their width) differ by more than one, the newest card that may move of
the first goes to the second at its next generation (its own branch, and its
own job, `<card>.g<gen>`), into `working` when she has a lane free and `ready`
behind her working cards otherwise. A card may move when its WHO line is
`friend` (any friend), it is ready on her row, and she has not started it (a
push on its branch, her beat naming it running); a card naming her and a
working card stay. Each move lowers the sum of squared backlogs and a card
moves once, so it ends. The first MOVED line ends `moved=N to
<friend>(n),... from <friend>(n),...`. `friend sync` delivers a moved card as a
new job to the friend it went to, and the queue file of the friend it left
marks it `taken` (a queued record of a card now dealt to another row). The tick
does not level the friends (`TestFriendLevelEvensTheReadyQueuesOfAClass`,
`TestFriendLevelMovesAQueuedCardAndTheQueueFilesFollow`).

A card whose read tier, before a frontier card is collapsed onto the tier a
route serves (`readTierOf`), is frontier — a frontier card, or a heavy card
whose read tier is the one above — is asked of a friend of frontier class,
not drawn on a reader machine (`FriendReadAsk`, `FriendReadClose`). The friend
is up, below her room (the same free width a friend's card is dealt within,
`TickDeal`), and her tiers include frontier: the one with the most free
width, the first by name among equals. Each ask takes one of that free width.
The ask writes `inbox/<read-card>/BRIEF.md` in her working directory when it
knows it: the primary's AS A READ section through the next heading, the
attempt's branch (the work card's), its start commit and its head named
separately, `WHO: friend <name>`, and a deadline of two hours on the sprint's
clock. `friend sync` writes that brief when the ask has not, and a
`Verdict: LAND` or a `Verdict: HOLD` with a finding that names a file, a line
or a rule in `outbox/<read>/REPORT.md` retires that read on her fleet row
(the same finding rule as a reader's broken read; a HOLD raises
`a reader found it broken`). The read card is `<primary>.r<attempt>.<friend>`,
placed on her fleet row `friend.<name>` in working while she has a lane,
ready behind her working cards otherwise. The readers table gains no friend
row. With no such friend up with room the read is not asked and the ask
raises the one judgment a read with no reader up already raises
(`fewer than two readers up`), not one note per primary.

`friend sync`, run by the coordinator's own loop where the directories are
(each run once, at the loop's period: 15 s in the coordinator's loop), carries
a friend's card across the inbox/outbox standard
(docs/FRIENDS.md, a sprint card): for each card working or ready on a friend's
row it writes `inbox/<job>/BRIEF.md` when that is not there and tells her so
with one nova-bus message from the coordinator to her, subject `card <card>
dealt: <the FRIEND-CARD DELIVERED line>`, the inbox path in the body (her
daemon pushes it into her session, which the inbox file alone never does; the
store is `NOVA_BUS_REDIS`; `NOVA_BUS_REDIS_USER` names its ACL user and
`NOVA_BUS_REDIS_PASSWORD_ENV` names the variable holding its password, both
independent of the sprint store's login; an unset bus user uses the default
user); the inbox file is the record and the message a
courtesy: a send that fails never fails the delivery, is said on sync's line
(`FRIEND-CARD NOTE friend= card=: the bus message to her was not sent (...)`)
and written on the card's story as one happened note, `a friend was not told
of her card`, with the `nova-bus send` line to tell her by hand, and keeps her
queue file, `inbox/QUEUE.json` (nova-friend's: one record per task, its
state `queued`, `working` or `done`; her daemon's pong reports its counts),
saying which of her cards are `working` and which `queued` (ready behind
them), never touching a record her session marked `done` or one the sprint
does not name (written whole by
`internal/atomicfile`, never over a file there; a later attempt's says it starts from the current
tip of the card's base branch on origin, never an older base, carrying the work of the last
attempt that pushed onto it herself (`git diff origin/<base>...<head>` shows it, redone where it
does not apply), and that the Head she reports must be on that tip: the rule a member's rework is
staged by, docs/SPEC-CARD-CONTRACT.md, where a rework starts; `TestAFriendsReworkStartsFromTheTipOfItsBase`.
A friend has no staged commit, so nothing checks that her Head descends from that tip: the
`ls-remote` tip check below, Head is origin's tip of her branch, is the only guard on her finish),
`<job>` the card's id as the table layer holds it at its
epoch (`sprint.StoredID`: the card id at epoch 0, `<card>~<epoch>` after a
clear, so a card id a clear brings back is another job), and only the
coordinator reaches out; once `outbox/<job>/REPORT.md` is there it finishes
the card as the friend (`finish` at the card's generation and epoch, as
`friend.<name>`): `Verdict: LAND` with `Head: <full sha>` is a worker's ok
finish at origin's tip of the branch BRIEF.md names, read once by one `git
ls-remote` of that branch in the card's `REPO:` repository (bounded at 10 s),
and only when the tip is that Head: what is read and landed is what origin
holds, never the report's word; the card goes to review, its reads and its
landing as any card's. A Head that is not the tip is refused, one line naming
both shas (`FRIEND-CARD REFUSED friend= card=: Head <head> is not origin's tip
of <branch>, <tip>; ...`), as are a branch origin does not hold, a card with no
`REPO:` line and a tip that cannot be read: the card is not finished (the
deadline still judges it), and the next sync reads the report again; `Verdict: HOLD` or `FAIL` (or
`FAILED`, `BROKEN`) is a failed finish, the judgment "work came back failed"
carrying `friend <name> <VERDICT>: <the report's first paragraph>`; a LAND with
no full sha Head, or any other verdict or none, is failed too, its report
saying what it lacks. It collects from a friend whatever her status. It prints
`FRIEND-CARD DELIVERED friend= card= job= branch=`, `FRIEND-CARD FINISHED
friend= card= result=ok|failed head=: <report>`, and `FRIEND-CARD REFUSED
friend= card=: <why>` for a finish the sprint or the tip refused, and for a card whose
id is not a card id or whose `inbox/<job>` is a symlink or a file (checked by
`Lstat`; nothing is written outside her working directory), and its OK line adds
`delivered=<n> finished=<n>` when either is above zero (`--json` `delivered`,
`finished`, `cards`). A hand-written inbox directory that is no card of the
sprint is outside the sprint and shown nowhere in the table: `where` counts her
cards from her fleet row into her friends row (ready, working, done ok and
failed; working 0 while she is down), and draws no friend's row in the fleet
table or its `--json`.
Her row is hidden in the stored fleet table (the table layer's row hide, when
the deal first adds it), so the stored view `sprint` does not draw it either;
as for any hidden row, its counts stay in that table's folded footer there,
where `where` leaves them out.

**Friend reconcile** (the owner, 2026-10-04: "trust but VERIFY"; "Are they actually
doing the work that is shown in the friend table? Really?"; "Mechanical. You know the
drill."; the case: a friend said all 35 of her cards were done while the table showed
34 working). `friend reconcile <friend>` (the coordinator's; `--op`, each collect run
under `<op>.collect.<its args>` and the return under `<op>.return.<its args>`, so a retry
with the same `--op` replays; `--root` as friend sync takes
it, else HOME; `--dry-run`) compares her own account of her cards with the store's and
settles each card working on her row (`sprint.FriendReconcileOf`). Her account is
`<friend>-working/inbox/QUEUE.json`, `{"tasks":[{"id":<id>,"state":<state>}]}`, an id
being the work card's or its job's (`sprint.StoredID`) and a state `queued`, `working`
or `done`, with the time the file was last written; it is read only as a regular file of
at most 1 MiB, and a file that is not there, is not that shape, has a task with no id or
an id twice, is refused, exit 1, nothing changed, as is a friend no row of the friends
table names. For each card: when `outbox/<job>/REPORT.md` is there it is collected, the
very finish friend sync gives it (its LAND read at origin's tip, its refusals the same);
else, when her account says `queued` or `working`, it is kept; else, when it says `done`,
or does not hold the card and was written in a second after the card's deal (her account
is newer than the deal, so she has had it to account for), it is returned; a card her
account does not hold that was dealt in or after the second her account was last
written, a card with no deal stamp, and a state of any other word are kept, each said.
A return (`sprint.FriendReturn`, one step for all of them, all or none) retires the work
card off her row, its record kept (`retired_by` `friend reconcile`, `return_reason` the
line), and moves its primary working -> ready, as a member going down leaves one: no
failed-work judgment, no redeal spent, and nothing counted in her done or ok%; the
tick's friend deal places it again at its next attempt, a new work card. A card that
moved since it was read, is not working on her row, or whose primary is not working on
it is refused. Each card says one line, `FRIEND-CARD FINISHED`/`REFUSED` for a collect
(friend sync's lines), `FRIEND-RECONCILE COLLECT` for a collect under `--dry-run`,
`FRIEND-RECONCILE KEPT`, `FRIEND-RECONCILE RETURNED` and
`FRIEND-RECONCILE REFUSED`; each return is one move line in the log and one happened
note, `a friend's card returned to ready`, carrying the actor and the line. An id of her
account that is no card working on her row is named on a `NOTE` line and never acted
on. It ends `FRIEND-RECONCILE OK friend= collected= kept= returned= refused= strays=`,
exit 0, or `FRIEND-RECONCILE FAILED` with the same counts, exit 1, when a collect or the
return was refused; `--json` carries the counts, the strays and every line. It writes
nothing in her directory, and the server does not run it (it reads the directories of
the machine it runs on).

The frame of `where` and `where --watch` shows work, friends, fleet in that order: the
readers and merge tables are hidden from it (the owner, 2026-10-02: "I feel like
reading and merging is something you can handle now. it seems to work, so please
hide the reader and merge tables."); `where --all` draws every table, work, readers,
merge, friends, fleet, and `where --json` carries every table and every row of it
with the flag or without. The one line under
the title is the word `STOPPED` when the machine is stopped, and the summary
line (landed / all primaries, percent, ETA, with no machine text) when it is
running; a RUNNING machine whose last tick is older than MachineSilence
(15 s) keeps the summary line, with the machine's `running (tick late 16s)`
after it, the whole seconds since that tick, and is never shown `STOPPED`,
which is a stop's alone: the machine's record STOPPED (section 14). The
coordinator measured the old reading at 2:23 PM on 2026-10-04: sampled every 2
to 3 s, `where` said `machine: STOPPED` for about 7 s, then running, over and
over, while the server was RUNNING the whole time, ticking 7 to 16 s apart.
Every count cell is an ordered set.
The summary line shows `held=N` after the percent when cards are held back:
waiting behind a sentinel not released, admitted held (`add --held`), or
waiting on one of those through a need (`sprint.HeldBack`, counted by the
tick into the where record below; `where --json` carries it as `held`). The ETA is
the time until every card on the work table has landed (the owner, 2026-10-02,
at a dashboard reading 347 of 2,846 landed, ETA 1h 36m, 2 cards an hour:
"Please update the ETA on the sprint. It's OBVIOUSLY wrong." and "it's the ETA
to all cards being done, not the cards that are in flight or not blocked"):
the cards left are every primary neither landed nor dropped (waiting, held
behind a sentinel or admitted held, ready, working, review, merging), at the
landing rate. The held cards are shown apart as `held=N` and counted, so
loading a wave in waiting behind sentinels (the owner: "I'd like to really
really load up the sprint in waiting, and stick sentinels in") lengthens the
estimate by the wave; until 2026-10-02 they were left out (nova-tools#5096
item 16), and 56 cards left of 2,499 read 1h36m. The rate
(`sprint.LandingRate`) is the cards landed per hour over the last 60 minutes
of running time (the clock's time less the STOPPED spans; the window of the
dashboard's throughput tile, so the two agree while the machine runs), read
from the landed cards' `landed` stamps as the where record keeps them; with fewer than five landed in that
window it is the whole sprint's average, the cards landed over the running
time since the first start; a read of the stamps that fails leaves that
average, never a failed view. The ETA is a dash, never a number, with fewer
than five landed in all (whatever the rate) and with no rate (no first start
known):
`3/10 30.0% held=4 -> ETA 12m`. From a day on it reads in days and hours, the
hours rounded up, one word as the shorter forms are: `347/2846 12.2%
held=2443 -> ETA 2d15h`. A reading process
shows the largest estimate of the last 10 s (the owner, 2026-10-01: "take
largest ETA in last 10 secs, so it is a stable value"), held over the same
cards to land: an add, a drop or a release changes the primaries on the table
or the held ones, which a landing never does, and makes the held estimate
dirty (the owner, 2026-10-02: "When you add new cards, the ETA needs to be
made dirty and recalculated."; nova-tools#5171), so the first read of `where`,
`where --json` and the dashboard after the tick that drains the change shows
the estimate recomputed over the new count at the rate measured. The verbs' sprint
line, printed after every step, reads no cards, so it has no stamps: its ETA is
over every card left at the whole sprint's average.

`where` reads the tables' cells and one record, never every card: under 1 s
at 3,000 cards is the requirement (2026-10-02 10:13 PM ET, at 2,843 cards with
2,443 held and 347 landed, `where --json` took 5.2 to 7.0 s on the live store,
reading every waiting and every landed card's record each second the dashboard
polled; the owner's rules: one read per tick, and a view is never recomputed
from every row when the table carries the answer). The counts are the work
table's count cells; the whole sprint's average is the landed count over the
running time since the first start. What the cells do not carry is the where
record, one key beside the machine's (`sprint:where`, removed by `teardown`):
the epoch and the work table's revision it was counted at, the held cards
there, and the landing stamps the rate can still count (those from the start
of the last 60 minutes of running time on, oldest first, at most 10,000: the
window only moves forward). Held is not a count a step can keep by adding
one, since one release or one need frees or holds a whole chain, so the tick
counts it whole: at the end of every tick, RUNNING or STOPPED (a verb moves
cards while the machine is stopped), when the work table's revision is not the
record's, the tick brings its twin up to date (it holds every card) and writes
the record; an idle tick reads the work table's shape and the record, two
exchanges, and writes nothing. `where` takes the record when it is of the
epoch it reads and either counted at the work table's revision it read or
kept by the RUNNING machine's loop: its last tick within 15 s and not failed,
and the record counted at or after the work table's revision that tick saw
(the heartbeat's), so a loop that ticks and does not keep the record (a binary
from before it) is never taken at its word, and the record is at most the tick
in flight behind. Between a verb and the tick that drains it, `held=N` may show
the count from before the verb; the ETA's cards left do not lag, as they are
the table's count cells. Otherwise (no record yet, as on a store from before the
record, until its first tick counts it; a clear, until the new epoch's first
tick; a STOPPED machine whose table a verb moved since its last tick; no loop
keeping it) it reads the waiting and landed cards as it did before the record,
and answers the same; a read of the stamps that fails there leaves the whole
sprint's average. The invariant, held
after every tick (`TestTheWhereRecordIsTheCardsAfterEveryTick`, with reversed
witnesses): the record's held count is `sprint.HeldBack` over the cards, and
its landings are the landed cards' stamps in the window, in order. The gate
(`TestWhereReadsTheTableNotEveryCardAtThreeThousandCards`): at 3,000 cards,
2,500 held and 350 landed, `where --json` reads no card's record but the
streams' control cards, in at most 9 round trips (22 on the code before the
record, 7 of them reads of card records; 19 on this code's own read of the
cards when it cannot take the record), and `-tags perf`, which gates a release,
holds it under 200 ms of wall time on the in-memory store. A tick's count costs
2 round trips when idle, with no write, and 8 when the table moved
(`TestTheWhereCountsTripsArePinned`).

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

The dashboard also serves each worker its own view, pulled when the worker wants it (the
owner, 2026-10-03 11:18 AM: "Think from the point of view of the worker. How to get the
current in the dashboard to them efficiently for their own visibility, on request (pull)."
and "This should be part of nova-sprint tool, dashboard, maybe a different URL on the
tailnet that gives them the json or xml or whatever you choose."). `dashboard --pull
<address:port>[,...]` (default `127.0.0.1:7395`; `none` serves none; the same addresses
`--listen` takes, so loopback or the tailnet and never a public one) serves the pull routes
on listeners of their own, so a proxy that publishes the page never fronts them, and
`--listen none` serves no page. Every route answers from the one cached copy the page
reads: `where --json --cards`, read at most once a second however many workers pull (the
owner, 2026-10-03 11:21 AM: "updated once per-second."). `--cards` adds to `where --json`
the work cards dealt to a fleet row and not finished (each card's row, state, since,
deadline and branch, read from the fleet's ready and working cells alone, so the read is
bounded by the fleet's width and never by the sprint's cards) and the open judgments
naming them (id and kind). The routes: `/api/sprint` is the whole copy as the page reads
it; `/api/friend/<name>` is one friend's view as JSON: the sprint line (landed, all, held,
ETA, the machine's state), her friends-table row (status, ready, working, width, done,
ok%), the cards dealt to her row `friend.<name>` (id, stream, state, since, deadline,
branch) and the open judgments naming them; `/friend/<name>` is the same as plain text,
one line an item and no markup, the form an AI pulls with one curl and reads whole (about
a kilobyte for sixteen cards: 1.0 KB to 1.2 KB by the length of the stream names):

```
sprint 352/1205 landed held 770 eta 2d7h machine running at 11:20:00 AM
friend amy up ready 1 working 1/8 done 9 ok 33.3%
ci-03.w2 ci working 16m due 1h10m sprint/ci-03.w2.g1.e15
ci-07.w1 ci ready 2m due 5h58m sprint/ci-07.w1.g1.e15
judgment ci-03-failed.2 work came back failed on ci-03
```

Each card line is the card, its stream, its state, how long it has been in it, the time to
its deadline (`late 5m` past it; the deadline by the clock, as the tick would hold it if
the machine does not stop), and the branch its work is pushed to. `/api/machine/<name>` and
`/machine/<name>` are the same for a fleet row, with its load. `/team` is every friend at
once (the owner, 2026-10-03 11:56 AM: "we need to get the friends working together."):
the sprint line, then for each friend of the friends table, by name, a line of her row
and an indented line per card she holds (id, stream, state, how long in it), so each
friend sees what every other is on with one curl; `/api/team` is the same as JSON
(`friends`: name, row, cards). About 2 KB for six friends holding eight cards each:

```
sprint 352/1205 landed held 770 eta 2d7h machine running at 11:20:00 AM
friend amy up working 1/8 ready 1 done 9 ok 33.3%
  ci-03.w2 ci working 16m
  ci-07.w1 ci ready 2m
friend bob down working 0/8 ready 0 done 0 ok 0.0%
```
 A name is a row of its table
or a 404 of one line (`no friend named "zed" on the friends table`); a name is looked up,
never read as a path. Every answer is `Cache-Control: no-store` and carries the copy's
time in `Sprint-At`; nothing is written, any method but GET and HEAD is refused, and no
key or secret is in the copy (`where --json` carries none). The event streams push each
new copy as it is read instead of waiting to be asked (the owner, 2026-10-03 11:30 AM:
"nova sprint website is not updating once per-second. something is chug."; a page that
polls after each answer sees the answer's latency, ~0.7 s through the public proxy, on top
of the second): `/events` on both listeners, and `/events/friend/<name>` and
`/events/machine/<name>` on the pull listeners, are server-sent events
(`text/event-stream`, no-store), the event `sprint` whose data is the JSON its
`/api/` route answers, the first at once from the copy there is, then one per new copy;
while a stream is open the dashboard reads the sprint each `--every` on its own ticker,
and a `: keepalive` comment goes every 15 s. The page and any client prefer `/events`
(an EventSource reconnects on its own) and, while it is not open, poll `/api/sprint` once
a second on a fixed timer, never after an answer. A friend pulls her view as
docs/FRIENDS.md says.

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
line, never FAILED: the table holds what the step wrote.

### friend-reconcile-every-tick-r.w1: the run loop reconciles every friend each tick

Friend reconcile above ran only when the coordinator typed it, so a phantom working count
stayed until someone looked (2026-10-04: one friend's row read working=4 with nothing
running). After each tick of a RUNNING machine, `run` reconciles every friend of the friends
table with the verb's own plan (`reconcileFriend`, shared by `friend reconcile`:
`sprint.FriendReconcileOf`, the collect of friend sync, `sprint.FriendReturn`), as the
machine, so each fix is the verb's history line and nothing new decides; her directory is
`<HOME>/<friend>-working`, HOME the server's, as the verbs take it with no `--root`. It is
bounded to one stat walk of each friend's directory a tick (the directory, her
`inbox/QUEUE.json`, each working card's `outbox/<job>/REPORT.md`), and a friend whose
directory is not reachable from the server, or whose account cannot be read, is skipped
with one record line, `FRIEND-RECONCILE SKIPPED friend=<name>: <why>`, said once until she
is read again and never an error. Only moves are printed (`FRIEND-CARD FINISHED`/`REFUSED`,
`FRIEND-RECONCILE RETURNED`/`REFUSED`), never a keep. Each card it moves pushes the
coordinator one note naming the card and why: a return's own happened note, `a friend's
card returned to ready`, addressed to the coordinator (`FriendReturnReq.Push`), and for a
collect `a friend's card collected by reconcile`. A friend whose row's working count
still differs from the tasks her QUEUE.json says `working` after the pass is one note to
the coordinator an episode, `a friend's row disagrees with her QUEUE.json`, and one more,
`a friend's row agrees with her QUEUE.json again`, when it ends; the episode is kept on the
fleet table's property `friend_disagree_<friend>` as the idle alarm keeps its own, so it
is one note across ticks and run loops, never one a tick (`sprint.FriendDisagree`). The
cards the tick has just dealt are not yet delivered (friend sync delivers them, in the
coordinator's loop); her account was written before their deal, so the verb's rule keeps
them. `TestRunReconcilesFriendsEveryTick`.

### who-dash-is-fleet.w1

**`WHO: -` is the fleet** (the owner, 2026-10-04: "Keep looking for verbs you
are missing"; an add was refused for a brief whose header said `WHO: -`).
`cardhdr.ReadWho` reads `WHO: -` as no WHO line: the card is a machine's,
dealt to the fleet, and `add` and `brief` take it with no refusal. `WHO: friend`,
`WHO: friend <name>` and `WHO: only friend <name>` read as the preference section
says, and any other value (`WHO: - -`, `WHO: friend -`, `WHO: junk`) is refused
(`TestWhoDashIsTheFleet`).

### friend-take-partial.w1

**friend take of several cards takes the takeable** (the coordinator took eight
cards back from one friend one at a time in a loop because one of the set had
started; `sprint.FriendTake`, `FriendTakeReq.AllOrNothing`). `friend take
<friend> <id>...` takes back every card named that it may take and refuses each
one the friend keeps (one she has started, or one not dealt to her), one
`REFUSED` line each, exit 1 when any is refused; this replaces "with ids named,
all or none" in "A friend's card taken back" above. `--all-or-nothing` keeps the
old behaviour: when any card named is refused it takes none, and each card that
would have been taken is refused too, "not taken: --all-or-nothing, and <n> of
the cards named <was|were> refused"
(`TestFriendTakeTakesTheTakeableAndNamesTheRest`).

### friend-deal-idle-lanes-first.w1

**The friends' deal and level fill idle lanes first, by tier, every tick**
(the owner, 2026-10-05: "You should automatically rebalance queues", "This
should not require you to remember, it should just happen mechanically." and
"The machine should do this."; `sprint.TickDeal`, `sprint.FriendLevel`,
`sprint.TickDeal`). Two failures led here: on 2026-10-04 at 3:57 PM eight
unstarted cards taken back from three full friends for two idle ones were dealt
back to the full friends by the next tick; on 2026-10-05 at 9:40 AM a friend
came up at width 2 with both lanes idle while three friends held 15 ready cards
at full width, `friend level` moved nothing (it levelled within a class, and
hers differed), and two cards taken back were dealt to a friend working 8 of 8
(room 2 x 8 - 11 = 5) over her (room 2 x 2 - 0 = 4). This replaces "the friend
up with the most room free" and "within each class" above, and "the tick does
not level the friends":

- **Eligibility is by tier, never by class.** Every friend deal and every
  level move, whatever the card's WHO line, goes only to a friend up whose
  tiers (her `FriendSeat.Tiers`, else her class's) hold the card's tier; a card
  with no tier is the dealer's default, flash (`cardTierOf`), and a withdrawn
  attempt at its redeal bound below its ceiling is offered at the tier it
  escalates to (`escalating`), a new attempt on that tier. A friend whose row
  names no tier takes none; a named friend without the tier is passed over for
  another friend with it, and a hard pin to her waits
  (`TestAFrontierCardGoesOnlyToAFriendWithFrontier`,
  `TestACardWithTwoProviderFailuresGoesToAFriendNotAnUnfundedRoute`). A card no
  friend up may take is the fleet's (WHO is a preference); a hard pin waits ready.
- **Idle lanes first.** Among the friends it may go to, a friend with an idle
  lane (width - working > 0) is preferred over every friend with none, the most
  idle lanes first, then the most room (DealAhead x width - working - ready),
  then the first by name (`preferredFriend`). A friend at or over DealAhead x
  width is never dealt.
- **Never back to a friend it left.** A work card carries `friends_left`, the
  friends it has left: each the level moved it off, and the one the coordinator
  took it back from (`taken_from`, kept past the deal that places it again).
  Neither the deal nor the level places it on any of them, with one exception,
  the owner's rule that a held or down friend's cards go to the up friends' ready
  queues: a card withdrawn off a friend held or down (or taken back) that no
  friend up it has not left may take is dealt to a friend up with room that the
  level moved it off, never to the friend it was withdrawn on or taken back from
  (`sprint.withdrawnFrom`; `TestACardOffAHeldFriendGoesBackToAFriendTheLevelMovedItOff`;
  found by the chaos suite's hold case, where the level had moved the held
  friend's cards off the only other friend).
- **The level runs inside every tick, after the deal.** The tick reads the
  friends' records whenever the roster has a friend, not only when a friend's
  card is ready, so a friend coming up (friend up, or a hold released) is
  levelled on the same tick. An unstarted ready card that is not a hard pin (no
  WHO line, `WHO: friend`, or one preferring a friend; never `WHO: only friend`,
  `TestTheLevelMovesUnpinnedCardsToAnIdleFriend`) on a friend
  with no idle lane moves to a friend it may go to with an idle lane, into
  working; then backlogs even as before (a card moves from a backlog to one
  smaller by more than one, below her room), across every friend it may go to.
  The friends with no idle lane give first, the largest backlog first, the
  newest card first; the friend it goes to is `preferredFriend`'s. The cards the
  deal placed that tick count against room and lanes and do not move. At most
  `FriendLevelPerTick` (4) cards move a tick, each one MOVED line. "Started" is
  the store's own data, never a read of a branch in the tick: her last beat
  names the card running (`FriendSeat.Running`: the card, its job or its
  primary), or it carries a progress stamp (`progress`); a started card stays.
  `friend level`, the verb, runs the same plan with no bound, and also keeps a
  card it reads pushed on its branch.

`TestAFriendWithAnIdleLaneIsDealtAndLevelledBeforeAFullOne`,
`TestTwinStoreDealsIdleFriendsFirstAndLevelsEveryTick`.

### cycle-time-breakdownb.w1: where a card's wall time goes

**Each card records its stage times on itself** (the owner, 2026-10-04: the
wall-clock lens, the time from add to landed measured per stage, and the waits
removed). The steps that already move a card write the stamps; none is
recomputed from the log. A primary carries `admitted` (added), `ready_at`
(first ready: add, resolve), `dealt_at` and `taken_at` (its final attempt's
work card, the deal and take it ran on), `finished_at` (its last finish),
`rework_s` (the seconds from each finish to the deal of the next attempt,
summed; written at that attempt's finish), `read_asked_at` and `read_done_at`
(the first ask and the last ok of the reads its accept counts), `accepted` and
`landed`; its merge card carries `queued`, stamped at the accept.

`sprint.CycleTimes` reads them from the snapshot: for each stage, the median and
p90 (nearest rank) in seconds over the primaries landed in the last 24 h. The
stages are `needs` (added to ready), `deal` (ready to dealt, a card never
reworked), `take`, `work`, `rework` (a reworked card), `read_wait` (finished to
first ask), `read` (first ask to last ok), `accept` and `merge` (queued to
landed). A stage is sampled for a card only when both its stamps are on it and
in order. The tick keeps the result in the where record (`stage_times`), and
`where --json` carries it as `stage_times`: `all` and `streams.<stream>`, each
stage `{median_s, p90_s, n}`; the dashboard draws one stacked bar, where wall
time goes, from the medians of `all`. A card whose path skips a stamping step
(a friend's deal or take, a sentinel's release or an ack's release, which do not
go through the steps above) has no sample for the stages that need it.
(`TestStageTimesGiveMedianAndP90PerStage`).

## 2. The cards

Layer 1 of the processor, the instruction set, is [SPEC-ISA.md](SPEC-ISA.md): a
card is one instruction, the coordinator is the front end that issues it, and
one `wait` kind whose operand names what it waits for replaces the hold, the
sentinel and the wave. The kinds there are the vocabulary the layer's model
checks.

**Primary.** One unit of work, between an issue and a pull request. One stream
for life. Fields: stream, score, brief, rules (the held rules file the member injects, section 2's rules by reference), who (the friend its brief's WHO line names, section 1, a friend's card), needs, head, attempt, fix, finding and why (a rework's, kept
for the attempt a rework with no member up deals later), work (its live
work card), grade (nova-decide's convergence grade before its first deal: section 5,
the grade), decided:<op> (each attempt decision of its takes: the attempt decision,
below), asked (its readers), readers (the two whose ok it was accepted on),
tier (the tier the coordinator pinned it to, `rework --tier`: its tier and its
ceiling both) and tier_now (the tier it is on: flash at its first deal on a route,
then the tier the machine escalated it to: section 5, flash first; pro from add when
its gate's measured wall is over the flash bound, with gate_wall, the measurement:
section 5, the gate's wall),
counters (failed, reworks, broken_reads, stuck, returns), failure, failure_at and
identical_at (its last failed work's class and attempt, and the attempt that failed the
way the one before did: section 2, the second identical failure), returned_attempt (its
attempt when it was last returned to review, by any return, an orphan merge
card's included), and the last CI observation for its current head (ci,
ci_head) and of any head (ci_run, ci_run_status, ci_source).

**Rules by reference** (the owner, 2026-10-02: "Rules by reference: the member injects
fleet/child-rules.txt once; the card does not carry it; a per-repo rules file for second
repos."; nova-tools#5174 rule 6). A brief is the card's own text; the primary's field `rules`
(`sprint.FieldRules`) names the held rules file the member appends the RULES paragraph of when it
writes the card it hands the child (the packet's `rules`), so the child reads card text + rules,
the shape it read when the card carried them. The member holds the `fleet/child-rules*.txt` of the
build it runs (package `fleet`, embedded); a card that names none gets nothing injected and
carries its own rules, as before; a brief that already quotes every sentence of the file is
handed as it is (`swarm.StagedBrief`); a card naming a file the build does not hold is refused at
the start, naming it. `add` under a rule set the members hold holds each brief to the held file
of the repository its `REPO:` names (`swarm.OwnRulesName`: `fleet/child-rules.txt` for nova-tools,
`fleet/child-rules.<repo>.txt` for another repository that has one, the add's file for a brief
naming none) by reference: it is linted as the member stages it (`swarm.LintCardChildByReference`),
so a rule it does not carry is no finding and a line that contradicts the rules (a `step-` scan, an
unfilled `Libraries considered:` line) still refuses it; the stored brief is the text given, and
the card names the file. A brief whose repository has no held file carries the add's rules, as
before, and names none. The name is each card's, written with its brief (by `add`, `quack`, and
`brief`, whose replacement rewrites it; `move` keeps it), so no later add changes what an earlier
card's child reads. The bytes a card no longer carries are the RULES paragraph of
`fleet/child-rules.txt` and the blank line before it, 2,035 bytes a card (measured 2026-10-02,
`TestAddStoresTheCardTextAloneAndNamesItsRules`). **Rollout:** the members are released before the
coordinator's `nova-sprint`: a coordinator with rules by reference stores cards without their
rules, and a member older than it injects none, so its children would read no rules.

**Work card** (consumer). What a child with a worktree is handed: the brief and
the place to work, and on a later attempt the fix, the finding of the broken read that
caused it and why the attempt before ended. Identity `<primary>.w<attempt>`.
Fields: primary, stream, kind=work, attempt, fix, finding, why, member, tier (the tier
its route was drawn from: section 5, flash first), decide_attempt_no_result,
decide_attempt_nothing_to_do, decided and decided_used (the attempt decision's two bars
and the decision its last finish carried: the attempt decision, below), gen (its assignment
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
with a fix or a drop (below its tier's ceiling the deal escalates it instead,
a new attempt on the next tier with no judgment: section 5, flash first); so the card is dealt again after each of its first three
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

**The second identical failure** (the owner, 2026-10-02, nova-tools#5174: "Escalate
on the second identical failure, not the third."). A card whose second try fails the
way its first did is not tried a third time on its tier: the judgment "a card reached
its bound" is raised at once, and its answer is a rework with a fix (on the next tier)
or a drop. Identical is one function, `sprint.SameFailure` over `sprint.FailureClass`
(`TestSameFailureIsTheOneDefinitionOfAnIdenticalFailure`): a take whose child left no
result is the class `no result` whatever its line; any other end is its reason, the
first line of the report up to the member's `; ` (`budget: no RESULT.md shape`,
`push refused: <git's line>`, `nothing to do: <why>`), cut to 200 bytes; a verdict's
reason is broad, so its class keeps the first three words of the child's line too
(`verdict not-done; tests red in`, after the member's `pushed=<sha> to <branch>: `); a
take the provider failed, a launch refused at staging or at launch (`launch refused:
<why>`, the member's) and an end with no line (a member down) are never the card's,
have no class, and are never identical. Two places count tries:

- the takes of one attempt's work card that ended with no work to judge: when the
  last two ended takes' records (provider_take_<n>) are the same failure, the card is
  at its bound whatever its redeals count (`redealBound`): it stays withdrawn, the deal
  refuses it, and the judgment says `its last two takes ended the same way (<class>),
  the second identical failure: not dealt a third time on its tier`;
- the attempts of a primary whose work came back failed: the failed finish writes on
  the primary `failure` (its class) and `failure_at` (its attempt), and when the
  attempt before failed with the same class, `identical_at` (this attempt) and the
  bound's judgment (`attempts <n-1> and <n> failed the same way (<class>), the second
  identical failure`) in place of "work came back failed"; the tick holds it while the
  primary stays in review at that attempt (`AtIdenticalFailure`), and a rework or a
  drop closes it (`TestASecondIdenticalFailureRaisesTheBoundAtOnce`).

**The brief is wrong, not the worker** (the owner, 2026-10-03, after two gating cards
were reworked to attempts 262 and 17 with the same reader finding every time, by hand
and by `answer`: "These two gating cards getting rejected, should have been escalated to
you, the coordinator, way sooner than this"). The same finding twice means the brief is
wrong, not the worker, and no further attempt is possible without changing the brief.
The decision is one pure function over the primary, `sprint.AtBriefBound`
(brief_bound.go): the primary carries the finding its last rework sent back and that
attempt (`finding`, `finding_attempt`), and a card is at its brief's bound when its
readers' finding at its current attempt is the same finding (`sprint.SameFinding` over
`sprint.FindingClass`: the whole finding, whitespace collapsed and case folded, and a
finding of files outside the card's PATHS one class however worded) as that one, both
since the brief last changed, or when it has made the attempt cap's attempts since the
brief last changed (`brief_attempt`, written by `brief`; absent is attempt 0). **The
attempt cap** (the owner, 2026-10-04, after a night in which only a repeated finding
bounded a card and one that failed differently each time was reworked 262 times):
`sprint.AttemptsCap` is the stream's control card's `attempts` (`stream set <s>
--attempts <n>`), else the work table's property `attempts` (`set --attempts <n>`,
`init --attempts <n>`), else 4; whatever each attempt found, after that many attempts
on one brief the card is not dealt again (its redeal bound is final whatever its
ceiling, the second identical failure escalates no more, the tick's "a card reached
its bound" offers brief and drop) and it goes to the coordinator as one judgment,
`<id>: brief defect after <n> attempts, 501x> spent; the brief is wrong, not the
worker; findings: attempt 1: <first sentence>; attempt 2: ...` (`501x>` the card's
charged total to the cent, rounded up, or `nothing priced`; the findings the primary's
`findings`, one line an attempt, appended by `rework` with the attempt's readers'
finding, else its failed report, else its bound's class, the latest kept under the
8 KiB bound), decisions brief and drop
(`TestTheAttemptCapIsOneJudgmentWithEveryFindingAndTheSpend`,
`TestFailedWorkReachesTheAttemptCapToo`, `TestTheAttemptCapIsASettingOfTheSprintAndTheStream`).
**The attempt cap's default answer is a friend card** (`sprint.AttemptCapDeal`, the
pump's part `cap deal` before the deal, `sprint.TickCapDeal`, decided in the reference
model as the duty `cap deal`;
`TestTheAttemptCapJudgmentsDefaultAnswerDealsAFriendCard`,
`TestTwoCappedCardsDoNotExceedAFriendsWidth`): a machine's primary ready and past its cap
(`sprint.AtBriefBound` asked with `sprint.AttemptsCap`), in a stream not held, is dealt
as a friend card (section 1) to a frontier or heavy-class friend up with room (her
seat's class), the friend up with the most free width (her width less the cards she
holds), the first by name among equals. The plan counts that free width as it deals,
decremented per card and picked again, so one friend's width takes one such card; the
deal after it reads the cards it gave her. The card keeps its work and
findings, its brief gains `WHO: friend <name>` for the friend chosen (the fields the
brief edit writes, so the cap count resets as a replaced brief does), its next
attempt's work card is created on her row in working, and a brief-defect judgment open
on it closes. With no such friend up with room the card is the deal's as before: at its
redeal bound it is not dealt again and the tick raises the cap's judgment above (brief
and drop), which closes once a friend takes it; below its bound its attempt is dealt
again to a machine. The tick reads the friends' seats when a friend's card or a card
past its cap is ready. Then `rework` is refused, nothing written, one line: `<id> has failed the
same way twice (attempts <n> and <m>: <the finding's first sentence>); the brief is
wrong, not the worker; run: nova-sprint brief <id> --brief-file <path> (a waiting card)
or drop <id> and add it again with the brief corrected` (or the cap's line, then the
same `run:`); a `--fix` changes the brief not
at all, so it lifts nothing; a replaced brief resets the count
(`TestReworkRefusesACardWhoseLastTwoFindingsMatch`). The judgment raised for such a
card is "a card has reached its bound: the brief is wrong, not the worker", in place of
"a reader found it broken" at the read and of "work came back failed" at the finish; it
names the two findings and their attempts, and its decisions are brief and drop, never
rework (`TestTheBoundJudgmentNamesTheBriefNotTheWorker`); the tick's "a card reached its
bound" on a card past the count says the same and offers the same. `answer` applies
only a verb the judgment prints, so it cannot rework such a card
(`TestTheAnswerPathCannotReworkACardAtTheBriefBound`), and the verb refuses it besides.

**A card replaced by its twin** (the coordinator, 2026-10-04, measured at 1:30 PM: the fleet ran 4
of 68 slots while 311 cards sat behind 21 judgments "a primary is blocked on something
dropped", up to 1h50m old; each was raised because a card had been re-cut as a twin, its
old id dropped and a new id added, `lint-pkg-cairn-tb` for `lint-pkg-cairn-t`, and every
card that needed the old id waited for a person's ack). A twin takes over its old card's
edges: every waiting card whose needs name the old id, and did not waive it, needs the new
id instead, in the same place of its needs, each id once; nothing else of it changes (its
column, its score, its other needs, its waivers), and the card records the relink
(`relinked`: `<old,...> -> <new> <time> by <who>`), which `card` shows with the new need.
Three verbs make it (internal/sprint/twins.go and recut.go; the model is tla/SprintRules.tla,
`TwinsInherit` and `NoDanglingNeed`):

- `add ... --replaces <old-id>[,<old-id>]` admits one card, the twin, and in the same step
  re-points every such edge to it, drops each old card still on the table with the reason
  `replaced by <new>` (the drop plans on the state with the edges already re-pointed, so it
  raises no blocked judgment), answers the blocked (or missing) judgments that named only
  the old ids (`replaced by <new>: <card> needs <new> in place of <old>`), and writes the
  weights once for the state after (the twin carries what waits on it). An old card dropped
  before is replaced the same way: its edges and its judgments. It means `--one`. Refused
  whole, nothing written, for an add of more or less than one card or of a sentinel, an old
  id that is the new one, no card, a sentinel or landed, a cycle the new edges would close,
  or any refusal of the add or the drop (`TestAddReplacesTakesOverEveryEdgeOfTheOldCard`,
  `TestAddReplacesADroppedCardClosesItsBlockedJudgments`,
  `TestAddReplacesIsRefusedWholeWhenItCannotHold`).
- `relink <old-id>[,<old-id>] <new-id> [--reason <text>]`, the coordinator's alone, is the
  one-shot repair of the edges a drop and an add made apart: the same re-pointing and the
  same answers, the twin a card on the table. Refused whole for a twin not on the table or a
  sentinel, an old id that is the twin, landed, or that nothing waits on, and a cycle
  (`TestRelinkRepairsTheEdgesOfADropAndAnAdd`,
  `TestRelinkIsTheCoordinatorsAndRefusesWhatCannotHold`).
- `recut <id> (--tier <t> | --brief-file <path> [--rules <file>]) [--new <id>]`, the
  coordinator's alone, re-cuts a card for another tier or another scope: it is `add
  --replaces <id>` of a twin in the old card's stream, in front of it, with the old card's
  needs (less those it waived) and the new brief's `DEPENDS-ON:` needs, held if it was held,
  its brief and rules unless `--brief-file` names a new brief (held to the card lint as
  `brief` holds one), pinned (`tier`) to `--tier` or else to the old card's pin, from its
  first attempt. The twin's id is `--new`, else the old id with the next letter: `b` for a
  card never re-cut (`lint-pkg-cairn-t` -> `lint-pkg-cairn-tb`), and for a twin that
  replaced its id less one letter, that id with the letter after (`-tb` -> `-tc`). Refused
  whole, nothing written, for no tier and no brief, a tier that is no class or the one the
  card is pinned to with no brief, a card not on the table, a sentinel or landed, a model
  the brief pins with a tier, and any refusal of the replace
  (`TestRecutKeepsIdLineageViaReplaces`,
  `TestRecutIsRefusedWholeWhenItChangesNothingOrCannotHold`, `TestRecutFromTheCommandLine`).

A twin made by `add --replaces` or `recut` records the ids it replaced (`replaces`), the
other end of the old card's reason `replaced by <new>`: the card's lineage, which `card
<id> --fields` shows.

A blocked judgment that names a need not replaced stays open, and its ack waives what it
names, as before.

**The attempt decision** (nova-decide's layer 2; the owner, 2026-10-02, the agreed
plan: "result classification after each attempt (done / nothing to do / wrong scope /
no result / needs pro)"; 2026-10-03: "Please push Jev wide."; docs/SPEC-NOVA-DECIDE.md
section 10). The deal writes the sprint row's two attempt bars (nova-config) on every work
card it cuts or deals again, each by its own field: `decide_attempt_no_result` and
`decide_attempt_nothing_to_do` (a redeal writes the bars the row holds then, and unsets
one it no longer holds). Both are empty by default (the coordinator, 2026-10-03: nothing
routes on a decision until a review round labels cards independently, as for layers 4
and 6): every take is decided, recorded and shown, and no decision routes a finish; 0.7
is the starting point, with the calibration beside it in docs/SPEC-NOVA-DECIDE.md section
9 (class=no-result AUC 0.883; class=nothing-to-do 0.618). When
any take ends, a member whose environment holds the key, in the end's long work beside the push and never in its pass, asks
the attempt decision over the card's brief, the child's RESULT.md as the member read
it (`member.ResultText`) and the finish's own report, through Jev with the key
`JEV_API_KEY` its loop's nova-secrets keys hold (the member's own: native and the child
are never handed it), and the finish carries the decision, one JSON record line
(`finish --decision`; one card a finish): its class (done, nothing-to-do, wrong-scope,
no-result, needs-pro, provider-failure), its p, and its op id `<card>@<attempt>.<12 hex
of its state>`, per attempt and state: two takes of one attempt that ended with identical
states (the same report and RESULT.md) share one decision. A decision that cannot be made (no key, a backend that fails) is one
`NOTE attempt <card> not decided` line, and the finish goes by its report alone. The
server holds the decision to the attempt schema (`decide.ParseAttempt`, else the finish
is refused) and to the take: its op must name the work card's primary and attempt, else
the finish is refused in one line naming both (`the attempt decision names <card>@<n>
(op=<op>), not this take's <primary>@<attempt> (<work card>)`; `sprint.decidedFor`,
`TestAFinishCarryingAnotherTakesDecisionIsRefused`), so a finish is only ever routed by
a decision of its own take. It writes it on the work card (`decided`: `<class> p=<p> op=<op>`; `card`
prints `decided=<class>:<p>` on the ATTEMPT line, `decided_used=yes` when it routed the
finish) and on the primary (`decided:<op>`: `<class> p=<p> used=<yes|no>`, set once,
the outcome's key), and hands it to the decide lane, which records it in
`<dir>/attempt.jsonl`. Two classes route a failed finish, each when its p is at or above
its own bar on the card, where the report's prefix routed it (`sprint.finishKind`;
`TestAFailedFinishGoesByItsAttemptDecisionAtItsClassBar`):

| class, at or above its bar | the failed finish |
| --- | --- |
| no-result (`decide_attempt_no_result`) | an ended take with no result, as a `no result:` report is: redealt on another route, counted against the bound and by the route's rest, its record's line `no result: <report>` |
| nothing-to-do (`decide_attempt_nothing_to_do`) | failed work, its class `decided nothing-to-do`, so two attempts the decision classes so are the second identical failure whatever their reports say |

No other class has a bar in this layer (docs/SPEC-NOVA-DECIDE.md section 10: needs-pro had
four positives in the calibration): a decided needs-pro, wrong-scope, provider-failure or
done is recorded and shown, and never routes. A report native ended as a provider failure
is never overridden by a decision, into failed work or into a take with no result: it is
the provider's ended take as before. Under its bar, with its bar empty, and for a launch
refused at staging or at launch (no take ran, and no decision is asked) the report's
prefix routes the finish as before; an ok finish is recorded and never routed. The decide lane attaches each decision's outcome
when its card lands or is dropped (`sprint.DecideDue`): `landed` for an attempt decided
at the attempt that landed, `later-<tier>` for one decided earlier (the tier that landed
it), `dropped`; a grade's outcome is that tier, or `dropped`.

**Read card.** One reader's read of one primary at one attempt. Identity
`<primary>.r<attempt>.<reader>`. Fields: primary, stream, kind=read, reader,
attempt, head, asked and begun (clock times), verdict, finding, usage. It takes its
primary's score. `queue` shows each card's times. A read card
exists because a member has one place per table and a pro card has two readers
at once (a flash card one; section 6).

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
or reader, route, model, tier (the tier its route was drawn from: flash first,
section 5), end, the tokens, wait, run, predicted, actual and
`actual_by`) and a COST TOTAL line (`predicted_of=<n>/<consumers>`,
`actual_by=harness`, `charged_usd`, and `cut=<n>` when the list was cut);
`--json` carries the same value as `cost`. No reader or member removed, no read
card retired and no consumer record cleaned up can lose cost: the record is
already in the card. A figure not known prints `-`, never 0.

**Every run's cost, whatever its end.** A take or a read records its cost in the step
that ends it, whatever the end (done, failed, no result, the provider's failure, a refusal
at staging, a read of any verdict or none): the consumer record carries its route, model,
tier, card, kind (work or read) and end, and its charged figure is the harness's own cost
where it reported one, else its tokens at the route's prices (`costRecord`); a run that
reported no token is counted unpriced, never as a zero. A launch's child appends one row
per attempt to its job's `usage.tsv` before the summary line that carries the launch's
spend; a launch stopped between the two still reports what the rows say
(`member.ReceiptUsage`): every attempt's tokens summed, the model, and the harness's cost
only when every attempt that reported tokens reported one. A launch the member reaps
because its claim moved (the card redealt or dropped under it) reports nothing, and its
spend reaches the dashboard only on the unreconciled line below; so does the record of a
card that leaves the work table.

**The reconciliation** (`internal/sprint/cost_reconcile.go`, `sprint.CostReconcile`). Its
step takes each provider's own count of the dollars its key used on a UTC day (openrouter:
`GET /api/v1/key`, `data.usage_daily`) and sets it beside the sprint's records of that
provider for the same UTC day: every consumer record on every primary whose route (else
reported provider/model) is that provider's and whose end stamp falls on that day, at its
charged figure. A day is compared with the same day, never with all time. The read is written
to the fleet table's property `cost_reconcile_<provider>`, the last read of each day kept for
62 days. A gap over 5% of the provider's figure, and of at least $1.00, opens ONE judgment on
the provider (`a provider's usage and the sprint's cost records disagree`, filed under
`provider:<p>`, decisions ack and wait), never a second while it is open; a read back within
the bound closes it. A provider with no usage endpoint (opencode) or no key is recorded
unknown with why, and changes nothing. **`nova-sprint cost reconcile [--dry-run] [--json]`** runs it
once: each provider the routes name is read through the seat's key in its own environment
(`provbalance.ReadUsage`, today's UTC day), the reads go to the step (`store.CostReconcileStep`),
and one line per provider is printed, `COST provider=<p> day=<d> provider_usd=<$> records=<$>
gap=<$> share=<n>%` or `COST provider=<p> unknown: <why>`, then `COST RECONCILE OK
providers=<n> notes=<n>` (with `--json`, the providers' records and the notes written; with
no provider named by a route, `providers=0` and nothing written; with `--dry-run`, the same
lines from the step's plan, `COST RECONCILE DRY-RUN ...: nothing was written`); the
release's spend check calls it (`TestCostReconcileSetsEachProvidersDayBesideTheRecords`).
**Not yet run by the loop:** the read when `nova-sprint run` begins and every hour after
(outside every tick, as the balance poll does), with the judgment's entry in `Decisions` and
its line in the help, is owed; until it lands a read is written only when the verb runs.

**The dashboard's cost** is the complete total: every take and read of every card on the
work table in any column, landed or not (`total_cost` on each stream's `stream_costs`), plus
what is unreconciled, on a line of its own beside the count of runs unpriced. Unreconciled is,
over the days since the epoch began, each day's last provider figure beyond the sprint's
records of that day, summed over the providers (`unreconciled`, the sprint's, the same on
every stream's record). The cost per card is that recorded total over the cards landed, so
every attempt and read behind them counts, those of cards not landed included.

**A card is a tree of steps** (`internal/cardtree`; nova-tools#5174 rule 7). The owner,
2026-10-02: "any card can be a tree"; "a batch card is just nomenclature"; a script step is "a
script card, when it is anything that is not an LLM", "preference: lisp, or golang obv.", a
regex at simplest. The failed-step rule is the coordinator's, 2026-10-02: a failed step n lands
steps 1..n-1, and steps n.. become a new card. The machine sees one card: one id, one slot, one
deal, one finish, one push, one pull request, its reads; dependencies stay at the card level
(`Needs:`), and inside a card the tree is the order.

- *The grammar.* A step's number may be dotted: `STEP 3.1.` is the first child of `STEP 3.`, the
  children of one parent numbered 1, 2, 3 with no gap (`steps-nested`). A step carrying `COMMIT:`
  is a work step and carries its own `PATHS:` and `VERDICT:`; each glob is a relative path inside
  the checkout (no `..`, not absolute) and one of the card's `PATHS:` or `NEW:` (`tree-step`). A
  step field is upper case as written; an indented `verdict:` is prose. A work step with `SCRIPT:
  regex|go|lisp`, its program in one fenced block under it and at least one `POST: sha256 <path>
  <64 hex>` (a path inside the checkout) or `POST: exit0 <command>` line, is a script step; bash,
  sh and python are refused as a language, and an exit0 command whose first word is bash, sh,
  zsh, python, python3, perl or env is refused (`script-step`). A card with a script step is
  script steps only: a model step beside one is refused (`script-step`), and the model steps go
  in a card of their own with `Needs:` between the two. A card with no dotted step and no step
  field is flat, and nothing here applies to it. `nova-swarm lint` and `nova-sprint add` hold a
  tree to the three rules.
- *The walk.* Depth first in card order, one commit per work step with its COMMIT: line as the
  message, from the header's `From: STEP <n>` when the card names one. A model card's child
  walks it. A script card is walked by the executor, `nova-swarm step`, with no model: native
  runs it in place of the harness, outside the child's wall (a wall does not nest), with no
  credential in its environment. Each command of a step runs in the step's own wall, tighter
  than a child's: `--net-deny` (refused where the wall cannot enforce it), no variable whose name
  carries KEY, TOKEN, SECRET, AUTH, PASSWORD, PASSWD or CREDENTIAL and none whose value holds
  a URL's `user:password@` (a denylist: under native the executor's environment is already the
  child's allowlist), HOME a private one, Go's build cache a private one in the temp
  (`GOCACHE=<temp>/go-build`, `GOFLAGS=-mod=readonly`, `GOPROXY=off`: no step reads or writes
  the bench's shared cache), and the checkout and a private temp the only writes; reads are the
  system, the built programs, the bench toolchain, the module cache GOMODCACHE names and the
  checkout's borrowed objects, the last two never executable (no data home, no auth copy). The
  wall binary is named by an absolute path, since each command runs from the checkout. `regex` runs
  in process over the step's PATHS through the checkout's `os.Root`, so a link out of it is
  never followed; `go` is built by the toolchain from its one file (no cgo, no module fetched)
  and the binary runs in the wall; `lisp` runs under `sbcl --script` in the wall; each POST
  command and git's add and commit run in the wall too. Every path holds this: the executor
  with no wall binary refuses unless `--no-wall` is given, and then says the programs run
  unconfined. The walk stops at the first step that is not ok.
- *The verdict per step.* The result's body carries one line per work step, `step <n>: <ok|
  broken|not-done|skipped> <commit sha|-> <one line>`, the commit `-` or 7 to 40 hex, a shorter
  sha a prefix resolved to the full sha on the pushed branch and a full sha checked the same way;
  an ambiguous prefix is not-done (`the step line's commit <prefix> is ambiguous on <branch>`) and
  an unknown prefix is not-done (`the step line's commit <prefix> is on no commit of <branch>`);
  a line whose commit is any other word is a defect, read as not-done. The first work step that is
  not ok (a step with no line is not-done) is the failed step; a body with no step line at all
  keeps the result's own verdict. A failed first step is a failed finish whose reason is `step <n>
  <verdict>: <why>`. A later failed step n: the member pushes the commit of the last ok step
  before it and finishes ok there, so steps 1..n-1 are read and land on the unchanged path, with
  the report `remainder=<id>-r<n> step <n> <verdict>: <why>`, which the "work came back ok" note
  carries beside its `pushed=<land>`. The remainder card `<id>-r<n>` (a dotted step's dots as
  dashes, `c1-r3-2`, an id the sprint takes) is the brief staged at the land commit: line 1's
  `sha=`, `BASE: <ref>@<sha>` and `base-sha:` rewritten to it (one added when the card names no
  base), with `From: STEP <n>` and `Needs: <id>` added after line 1. `nova-swarm step --card
  <brief> --remainder <id> --from <n> --land <pushed sha>` prints it; the coordinator adds it and
  the deal deals it as any card. A member adds no card: `add` is the coordinator's.

### recut-widen-r.w1: a HOLD's PATHS-PROPOSED line widens the twin

A card held for PATHS too narrow is re-cut by the tool, not by hand: `recut <id> --widen
[--repo-dir <clone>] [--new <id>]` reads the PATHS-PROPOSED line of the report of the card's
latest attempt (docs/SPEC-CARD-CONTRACT.md section 4), and re-cuts the card as its twin with
the old brief, every `PATHS:` line of it the union of the old globs and the proposed ones (the
old first, each once), and a `CARRY: <id> attempt <n> head=<sha>` header line naming that
attempt's pushed head, where the twin's first attempt starts (the member's packet takes it as
`base_head` when no attempt of the twin pushed one); the verb says `NEXT <twin> starts from <id>
attempt <n> head=<sha>`. The base's files and the head's are read in `--repo-dir`, else land's
clone of the card's `REPO:`, the head's branch and the base fetched from origin when the clone
lacks them. Refused, exit 1, nothing written, naming every problem: a card with no attempt, a
report with no PATHS-PROPOSED line, an attempt with no pushed head, no clone, a glob that
climbs out with `..` or is absolute, and a glob that names no file at the base nor at the head;
and as `recut` is refused otherwise (`TestRecutWidenAppliesPathsProposed`).

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
| working -> ready | its work card was withdrawn because no fleet member is up, or, still ready, because its route rests (its note names the route and the reason) | mechanical, notifies |
| review -> merging | accept: the readers it needs said ok at this head (one for a flash card, two different readers for a pro card; section 6) | mechanical (the tick), unless its CI is red at its head or it was returned to review at its attempt; the coordinator's verb takes those; refused without them |
| review -> working | rework with a fix: the next attempt is delegated at once | the coordinator's verb |
| review -> ready | rework with a fix when no fleet member is up or none is below its width; the tick deals it when one has room | the coordinator's verb |
| merging -> review | the stream's CI went red and the coordinator sent it back, or return | the coordinator's verb |
| merging -> working | redo a conflicted card with the tip's rework: delegated at once to an up member | the coordinator's verb |
| merging -> ready | redo a conflicted card when no fleet member is up: start delegates it later | the coordinator's verb |
| merging -> landed | its batch, green on the stream branch, merged to the development branch | mechanical |
| any open state -> off the table | drop, with the reason | the coordinator's verb |
| waiting -> landed | release of a sentinel that is reached, waits for nothing, or waits only for cards under way (landed, dropped, or in flight: taken, in review, merging), and only that | the coordinator's verb |
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
  default 64; `fleet up m1 --width 0` drains it: its width cell is `0`, so no
  deal, redeal or level places a card on it and a take by count takes none,
  its untaken ready cards are levelled away and its working cards finish where
  they are, unlike `fleet down`, which deals them again elsewhere; `fleet up
  m1 --width <n>` ends the drain, and `fleet sync` writes the inventory's width
  back; `init --members m1:0` stays refused). It holds up to DealAhead (two) times its width, ready and
  working together: its width working and as many again ready behind them, so
  a lane that frees takes its next card at once (the owner, 2026-10-01: "The
  WHOLE POINT of nova-sprint is to feed the fleet at width and keep it working
  at that width until done."; a friend's row is fed the same way, her finish taking her next ready
  card itself, section 1; "deal at most 2X width ahead per-machine in
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
  its tier, `tier: flash|pro|heavy|frontier` (none is flash), and a primary's `tier` field,
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
  index, the fleet table's `route_index_<tier>` (`route_index_flash`, `route_index_pro`, `route_index_heavy`), a uint64
  counter modulo the array's length as the member rule's `deal_index` is, and
  moves the index by one for each card dealt, written in the deal's batch with
  its cards (tla/RouteIndex.tla, RouteIndexAdvancesOncePerCard, RouteFair). A
  route with `first` set is drawn before the others of its tier, and a read
  drawn from the tier walks the same way: from that same index the walk takes
  the first served entry whose `first` is set, and an entry without it only
  when none such remains drawable, the index still moved by every entry walked
  to reach the one taken (internal/sprint/route.go, preferFirst). RouteFair is
  the walk when no route of the tier has `first` set. A
  redeal or a later attempt takes the next entry whose route was not taken for
  the card while another remains, the index moved past the entries it skipped
  (ExcludedNeverDrawn), and an entry that names no enabled route of the tier is
  skipped the same way. The work card keeps `route`, `model`, `tokens`, `usd` (the route's dollar budget, empty for none) and `deadline` (its
  packet hands them to the member) and the primary `routes`, every route taken
  for it. **The deadline is by machine** (the owner, 2026-10-04: the route's
  deadline was one number for the fleet, and a machine whose median run wall
  was twice the others' timed out twice as often, every timeout a whole
  attempt's spend lost): the deadline a dealt card gets is the larger of the
  card's own (its route's or its pin's, kept as `deadline_own`) and
  `sprint.DeadlineK` (3) times the member's median run wall in seconds over its
  last `sprint.DeadlineSamples` (50) ok attempts, the usage walls of its ok work
  cards newest first, as `stats` measures it (`sprint.MemberMedianWall`); a card
  dealt again to another member (a member down, the level) gets that member's
  from the same start. The fleet row may pin it: `fleet up <m> --deadline <d>`
  writes the member's control card's `deadline` in seconds and every card dealt
  to it gets that, whatever the card's; `--deadline default` takes the pin off
  (`TestADealtCardsDeadlineIsThreeTimesItsMembersMedianWall`,
  `TestARedealtCardsDeadlineIsItsNewMembersToo`,
  `TestTheMedianWallIsOverTheLastFiftyOkAttempts`). The median is measured
  once a member for the done-ok cell the fleet table holds, and again only when
  a card put on the table makes a new cell, never once a card dealt: the deal
  costs the cards it deals, not those times the member's history
  (`TestTheMedianWallIsMeasuredOnceACellAndAgainAfterAPut`; the tick gate under
  load, `TestTheTickGateHoldsUnderLoad`). A card no route serves stays ready: the deal refuses it naming the
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
- Flash first on every card (the owner, 2026-10-02, cost rule 1 of
  nova-tools#5174, agreed after "The cost of the sprint at $5,400 seems
  excessive.": "Flash first on every card; pro only on escalation"). The tier
  line 1 names is the card's ceiling, what it likely needs, never its first
  deal: every card is dealt on flash, and the deal draws from the tier the
  card is on, the primary's `tier_now` (flash when unset), which every deal on
  a route writes. The tier a card is on (its reads, its read count, its
  escalation) is `tier_now`; before its first deal on a route, and always in a
  store with no route (where its member runs its own model and no deal draws a
  tier), it is its ceiling. An attempt that
  reaches its bound on a tier below its ceiling is escalated by the machine,
  raising no judgment. At the take bound (the redeal bound, section 2, the work
  card's redeals, or its last two takes ending the same way, the second
  identical failure) the deal retires the work card at its bound
  (`retired_by=escalation`), writes the next tier of the ladder flash, pro on
  the primary (`tier_now`), and deals a new attempt on it, the brief and fix the
  card's own, its work card's `why` `escalated from flash to pro: attempt <n>
  reached its bound on flash (redealt 3 times)`, or `(its last two takes ended
  the same way: <class>)`. At the attempt bound (two attempts whose work came
  back failed the same way) the failed finish writes `tier_now` and the why
  (`escalated from flash to pro: attempts <n-1> and <n> failed the same way
  (<class>)`) on the primary and returns it to ready, as a rework with no
  member up does, and the tick's deal cuts the new attempt on the next tier. The
  failed finish reads the routes whether or not it reports usage (a native run
  that printed no final line sends no `--usage`), so the escalation never waits
  on a usage line (`TestASecondIdenticalFailureWithNoUsageStillEscalates`). A
  tier no route serves is the tick's judgment of that tier, the card held by it. At its ceiling the bound is the judgment "a card
  reached its bound" as before. The ladder has flash and pro: a frontier card
  is the coordinator's and is never dealt, a pinned model runs on its pin (read
  on the tier line 1 names), and a tier the coordinator pinned with `rework
  --tier` is the card's tier and its ceiling both, never escalated. A store with
  no route escalates nothing (its member runs its own model whatever the tier).
  Each work card records `tier`, the tier its route was drawn from, which its
  packet hands the child and its JOB.md names; `card` prints it on each ATTEMPT
  and COST line and the tier now and the ceiling on its CARD OK line
  (`tier=<t> ceiling=<t>`; `--json` `tier`, `ceiling`). One function decides
  the next tier for every bound that escalates (`Snapshot.NextTier`,
  `internal/sprint/route.go`): the take bound and the attempt bound.
  **The grade** (nova-decide's layer 2; the owner, 2026-10-02, the agreed plan:
  "convergence grade and route choice before the deal"; docs/SPEC-NOVA-DECIDE.md
  section 11). Before a card's first deal the server's decide lane (`run
  --decide`, section 12) grades its convergence from its brief alone, script,
  flash or pro with a p each, records the decision in `<dir>/grade.jsonl`
  under `<card>@grade.<12 hex>` and writes it on the primary, `grade` (`<grade>
  p=<p> op=<op>`; `sprint.Grade`, written only on a card never dealt and still
  ungraded, waiting or ready, never a sentinel; a brief replaced clears it).
  `card` prints it on its CARD OK line (`grade=<g>:<p>`; `--json` `grade`).
  The grade is a hint while the sprint row's `decide_grade` is empty (its
  default, nova-config: nothing routes on a decision until a review round labels
  cards independently; 0.7 is the starting point, the calibration beside it in
  docs/SPEC-NOVA-DECIDE.md section 11: grade=pro AUC 0.930 over the store's
  landed cards, 0.547 over the mechanical set, part of it the brief's own `tier:`
  word). The grade decides no tier: a brief's tier on line 1 is the card's
  starting tier, not only its ceiling (the owner, 2026-10-03, after seven of
  seven first flash attempts of pro cards died at the budget or the deadline
  with no result, each needing `rework --tier pro` after: "tier: pro remains
  only a ceiling" was the defect). Flash first (section 5) applies to a brief
  that says flash or says no tier; a card that says pro starts on a pro route
  (`Snapshot.startTier`: the deal draws its first route from pro and writes
  `tier_now` pro) and is never dealt below it; `rework --tier` stays the pin it
  is (`TestABriefThatSaysProStartsOnPro`). A card dealt on flash below a pro
  ceiling (dealt before this rule) still escalates at its bound as before.
  **The gate's wall** (the coordinator, 2026-10-04: a card whose gate cannot run in
  15 minutes on a flash member is not dealt flash; a 40-minute deadline was missed on
  flash before pro did the card twice). `add` measures each card's gate: the package
  its TEST line names, its wall the median wall of the ok work takes the sprint
  record holds for cards naming that package (each primary's cost records, kind work,
  end ok, with a usage wall; a take's wall holds the work and the gate both, so it
  bounds the gate's from above). A card whose measured wall is over the flash bound,
  15 minutes (`FlashGateBound`), is admitted on pro: `add` writes `tier_now` pro and
  `gate_wall` (`<median> n=<takes> over <bound>`), its line says `admitted pro: gate
  <package> measured <median> (median of <n> ok takes) over the flash bound <bound>`,
  and its first deal draws pro, whatever its ceiling; pro is the top of the ladder, so
  the machine escalates it no further. A card never measured, measured at or under the
  bound, with `TEST: none`, a frontier card and a pinned model are admitted as before
  (`sprint.gateTier`, `internal/sprint/gate_wall.go`;
  `TestTierFollowsTheGatesMeasuredWall`).
- The gate verdict (docs/SPEC-NOVA-DECIDE.md section 12; the owner, 2026-10-02,
  layer 3 of the nova-decide plan: "gate verdict: flaky vs caused vs
  pre-existing"). Every deal of a work card (a first deal, a redeal, a rework's)
  writes the sprint row's gate bars that are set on it, `decide_gate_flaky` and
  `decide_gate_preexisting` (nova-config, `sprint set`), read with the routes in
  the same round trip (a store with no route reads none). **Both are empty by
  default** (the coordinator's rule for every nova-decide layer, 2026-10-03):
  every gate decision is made, recorded and shown, and nothing is rerun or
  reclassified until the owner sets a bar; 0.8 each is the starting point
  (docs/SPEC-NOVA-DECIDE.md section 12: with the base run, p(flaky) AUC 0.716,
  p(caused) 0.914, p(pre-existing) 0.907; without it, p(pre-existing) 0.522), and it is not routed
  yet because at 0.8 24 of the calibration's 39 flaky failures would have been
  reported pre-existing. The packet hands the bars to the member, and
  the member hands native the key `JEV_API_KEY` its loop's nova-secrets keys
  hold (native's own: no child is handed it). When the child ends `verdict:
  not-done` and the gate output it names (its result's `output:` file, inside the
  job or the child's temp, else its body) holds go test failures, native, before
  the member reports the take: runs the failing tests once at the work's start, in
  a worktree, in the child's own wall and environment, bounded by `gateRunWait`
  (three minutes; are they red at the base? a run past it is asked `not run`);
  asks the gate decision of each failure over its lines, the base's result, the
  gate's other failures, the card's PATHS and the diff's summary, recorded in the
  machine's `<root>/decide/gate.jsonl` under `<primary>@<attempt>@gate/<pkg>.<Test>`;
  reruns at the head, once and bounded the same, every failure flaky at or above
  the flaky bar when it is set, and attaches each rerun's result as its decision's
  outcome; and prints one line, `NATIVE GATE label=<l> op=<op>
  route=<green|pre-existing|caused> tests=<names> classes=<Test>:<class>:<p>,...`,
  every decision shown whatever the route:
  - `green` (every failure flaky, and the rerun passed): the member reads the
    child's verdict as ok, its report beginning `gate: <tests> flaky, green on the
    rerun; `, and the take is judged as any ok take (its push), so the readers
    read it;
  - `pre-existing` (every failure that stayed red was classed pre-existing at or
    above its bar): the take is failed `pre-existing: <tests>` (`member.Judge`,
    `cardhdr.EndPreExisting`), the base's or the member's and never the card's:
    `FailureClass` gives it no class, so it is never the second identical failure
    and spends no bound (section 2), and its judgment is the failed work's as
    before;
  - `caused` (one failure caused, a rerun red again, or the bars unset): the take
    as the child reported it.

  A gate decision that cannot be made (no key, bars it cannot read, a backend that
  fails) is one `NATIVE NOTE` line and the take is the child's; a base that cannot
  be run is asked `not run`; a rerun that cannot run is red again. A script card's
  steps are their own gate and are never decided. `tla/CardContract.tla`'s
  `JudgeOf` is unchanged: the decision changes the verdict Judge reads (green) or
  the reason of a failed finish (pre-existing), never the finish kinds. The
  calibration of 2026-10-03 (60 failing tests of the coordinator bench's CI runs,
  labelled by git and the other runs): with the base run, p(caused) AUC 0.914,
  p(pre-existing) 0.907, p(flaky) 0.716, and at the starting bars no caused failure
  was routed flaky or pre-existing (internal/decide, `TestTheGateCalibrationRecordsSupportTheBars`).
- The bound holds across attempts (the coordinator's finding, 2026-10-03, not the
  owner's words: "So the bound is per attempt, and a rework resets it."; an answer
  loop reworked ci-03 231 times and docsd-03 244 times in one night, every rework a
  new attempt whose redeals began again at 0). An attempt retired by a rework at its
  redeal bound writes the primary's record of its failed work as a failed finish
  does: `failure` (the class its bound ended with, `sprint.BoundClass`: its last two
  takes' class when they ended the same way, else its last take's; a take the
  provider failed, which has no class inside an attempt, is `provider failure` with
  the provider's class word across attempts, `provider failure: class=out-of-credit`),
  `failure_at`, `failure_tier` (the tier the card was on; a failed finish writes it
  too, and a new tier counts its own failures) and `failure_bound` (yes; a failed
  finish clears it). Then, at a bound, on the ladder flash, pro, heavy, frontier:
  - `rework --tier` never names a tier below the one the card is on: refused, one
    line, `--tier <t> is below the tier it is on (<on>): a rework at its bound never
    lowers its tier ...`;
  - when the attempt before also ended at its bound on the card's tier, whatever its
    class, `rework` is refused unless `--tier` names a tier above it, one line:
    `attempt <n> reached its bound on tier <t> as attempt <n-1> did (<class>), and is
    not reworked on <t> again: rework it with a fix and --tier <the dealt tiers above>
    (a tier above <t>; the ladder: flash, pro, heavy, frontier, and frontier is never
    dealt), or drop it (nova-sprint drop <id> --reason <why>)`, the rework offered only
    when a dealt tier is above (from pro, drop alone); nothing is written, and `deal`
    of it says the same; the judgment "a card reached its bound" names it (`; a
    second bound on tier <t>: not reworked on it again`);
  - the provider back lifts it once per tier per card: when the attempt's bound was
    a provider failure, a take on one of the providers its failed takes ran on (the
    provider of a take is its route's, the part of its model id before the `/`), of
    any card, that finished ok after the attempt's last take ended lets one rework on
    that tier, and the rework writes the tier in `failure_back` on the primary, which
    nothing clears; once it is there, a held bound on that tier is not lifted again,
    however often the provider comes back or other cards succeed. A take of another
    provider is not the provider back, nor is any take on the tier: a fleet at width
    finishes ok takes on every tier every minute (the second cold read of PR 5202: a
    card whose takes all ended `class=out-of-credit` had 12 of 12 same-tier reworks
    accepted when any flash take finishing ok lifted the bound). `wait` is offered
    only where it can lead somewhere: on a bound that was a provider failure, and on a
    held one only while its tier's lift is unspent; so the refusal offers `or wait`,
    and the judgment "a card reached its bound" on a held card lists `rework with a
    fix on a higher tier` (when a dealt tier is above), drop, and wait, only then; on
    a first bound that left no result, or whose member went down, it lists rework
    with a fix and drop. When the provider is back, the held judgment closes and the
    bound's plain one opens, so the wait ends in a judgment. A rested route is not
    the provider back: rule 3 never rests a route for a provider failure (it counts
    takes with no result), so that test would lift the bound at once. Nor is
    `funded`: a take refused for credit or for its key rests its provider first
    (nova-tools#5199, below), the card waits ready under the rest, a rework onto a
    tier whose every route rests is refused, and after `funded` the attempt is dealt
    again; so an out-of-credit attempt reaches its bound across `funded`, the rule
    is unchanged, and the lift still needs a take on the provider finishing ok
    (`TestAnOutOfCreditBoundRestsThenLiftsOnce`).
  So a bound-to-rework loop is stopped by the store, whoever answers and however often
  other cards succeed: an answer with no `--tier` or the same tier is refused at the
  tier's second bound (third, once, when the provider came back between), one
  alternating tiers is refused at its second turn as a lower tier, and one that
  climbs makes at most three attempts per dealt tier of the ladder that end at a bound
  (frontier is the coordinator's and never dealt), so at most five reworks at a bound
  in a row on flash and pro (`TestASecondReworkAtTheSameBoundIsRefused`,
  `TestAWithdrawnTakeCountsTowardTheIdenticalFailure`; `tla/DirtyTick.tla`,
  `ReworksBounded`, unconditional, reversed by W29, W30 and W31). Two cases this
  leaves to drop, said plainly:
  - an outage on a card's second pro attempt (its first ended at its bound with no
    result, its second at its bound with `provider failure: class=out-of-credit`):
    the rework is refused, offering drop and wait; when a take on that provider
    finishes ok, one rework on pro is accepted; if that attempt ends at its bound
    too, whatever its class, drop is the only exit;
  - an outage on its first pro attempt, then one real no-result on its second: the
    second bound on pro is refused with drop alone, though the card had one real try
    there. That is accepted: the class does not gate a repeated bound (bounds
    alternating a 402 and no result must not loop). Since PR 5205 a 402 at launch is a
    provider failure, never `no result`, so the store can tell the outage from a real
    try; leaving a provider-failure bound out of the tier's count is an option the
    capped lift makes safe, and it is not taken here.
- A route whose children end without a result rests (the owner, 2026-10-02,
  nova-tools#5174: "A route whose children end without a result three times is
  rested by the machine, never redealt on."). The tick's deal counts each route's
  ended takes over a sliding window, the last RouteRestWindow (10) takes on it that
  ended after its last rest began, in the order they ended: each take the provider
  failed or that left no result (the work card's provider_take_<n> records) and each
  work card's own finish; a take that ended with its member down keeps no record and
  is not counted. When RouteRestAfter (3) of the window left no result, the tick
  rests the route for RouteRestFor (30 minutes, the clock's): it writes that route's
  line into the fleet table's one property per provider, `rule3_rest_<provider>`
  (one line per route, `<route> <began> <ends> <card,card,card> no-result`, RFC3339,
  the lines in route-name order, the whole value guarded on the value read), in the
  deal's batch, and a happened note to the
  coordinator, "a route rested: its children ended with no result", naming the
  route, when the rest ends and the cards. While it rests the deal draws no work
  card on it, a first deal, a redeal (even when it is the tier's only route, where
  the exclusion of the routes drawn lapses) or a rework's, as it draws none on a
  disabled one; when every route of a tier rests, the tier's judgment `no route
  serves the tier` says so and names when each rest ends. The rest ends by itself at
  its time, and the window begins again after it, so the ends that rested it never
  rest it twice. Reads are drawn as before. The rests are settled once per tick part
  or verb that draws work routes or asks why a card is not dealt (the tick's deal,
  `deal`, `rework`, and the held rule of the tick's check), from one scan of the fleet
  table, and each draw reads a map: at 984 ready primaries and 3,000 work cards each
  such part scans once (`TestTheTicksCheckSettlesTheRestsOnceAtScale`, its
  `TICK-COST` lines; a scan per card was 1,969 scans and 3.82 s a check). The rest is
  the sprint's, in the store, never nova-config's: enabled stays the coordinator's (docs/SPRINT-COORDINATOR.md).
  `routes` prints `rested_until=<RFC3339>` while a route rests (`-` when not)
  (`sprint.RestsDue`, `TestRestsDueCountsNoResultEndsInTheRoutesWindow`,
  `TestARouteWhoseChildrenEndWithNoResultThreeTimesRests`,
  `TestARestingRouteServesNoDealWhileAnotherServes`).
  The property is one per provider, never one per route, so the table's properties
  (64, `ntable.LimitTableProps`) grow with the providers. At 100 routes over two
  providers, every route rested, the fleet table holds 4 properties
  (`TestTwoRoutesOfOneProviderAreOneProperty`,
  `TestAHundredRoutesRestedByRule3StayUnderThePropertyCap`). A property
  `route_rest_<route>` is ignored (`TestAnOldPerRouteRestPropertyIsIgnored`): a
  property stays until the epoch ends, so reading one into `rule3_rest_<provider>`
  cannot free its slot, and the route's rest is only its line in
  `rule3_rest_<provider>`.
- A provider out of funds is never a mystery failure (nova-tools#5199). The owner, 2026-10-03,
  8:03 AM ET: "provider out of funds should never be a mystery failure." And at 8:18 AM ET:
  "you'll need to detect when a provider runs out of credits, and exclude that provider moving
  forward, and let me know. then if all providers are out, then you stop the sprint." On
  2026-10-03 two providers ran dry overnight and nothing landed: the store's redeal bound held,
  and ci-03's repeated attempts were reworks from the coordinator's own answer loop.
  - A provider rests as one: its rest is ONE fleet table property, `provider_rest_<provider>`
    (`<began> <ends|open> <card|-> <cause> [balance=<x>] <words>`), never a copy on each
    route, so a rest is one write and the table's properties (64, `ntable.LimitTableProps`)
    grow with the providers and never with the routes (`TestTheFleetPropertiesAtAHundredRoutesStayUnderTheBound`). A
    route rests while its rule-3 line or its provider's rest holds, the one that ends later
    deciding (`sprint.RouteRests`); `routes` prints each route's `rested_until=`.
  - A provider has two rests of its funds, and only one stops the sprint. Each holds with no
    time (`open`, said `until paid`) and ends on what is listed with it, or on `funded`:
    - OUT OF CREDIT (cause `out-of-credit`): a take the provider refused for want of credit
      (its line `provider: class=out-of-credit ...`, docs/SPEC-SWARM.md), or a balance the poll
      reads at or under zero. The provider has no money. It counts toward stopping the sprint.
      - Begun by a refused take, it ends ONLY on `funded` or on a payment the poll sees: a
        balance read strictly higher than the read before it, or than the balance at the
        refusal (the rest keeps it, `balance=<x>`). That read then decides as any other: over
        the hour of spend the rest ends, over zero but not over it the provider is low on
        funds, at or under zero it stays out of credit. A balance over zero that is not higher
        never ends it: OpenRouter refuses with 402 a request whose estimated cost the balance
        cannot cover, so a provider that refuses can still read a small balance over zero. It
        stays out of credit, with its balance beside it on the providers table, and counts
        toward the stop (`TestARefusedProviderReadingASmallBalanceRestsOnceBesideAServingOne`,
        `TestARefusedProviderAloneStopsTheMachineOnceUntilAPayment`,
        `TestAReadHigherThanTheReadBeforeItEndsARefusedTakesRest`).
      - Begun by a balance at or under zero, it ends on a balance read over zero, after an
        unknown read too (the rest names no card, so it is no refused take's): over the hour of
        spend the rest ends, not over it the provider is low on funds from then
        (`TestABalanceAtZerosRestEndsOnAReadOverZeroAfterAnUnknownOne`).
    - LOW ON FUNDS (cause `balance`): a balance the poll reads over zero but not over one hour
      of the provider's spend. The provider is excluded before it runs dry, and still has
      money: it NEVER counts toward stopping the sprint. It ends on a balance read over the
      hour of spend measured before the rest began; at or under zero it is out of credit.
    A take refused for the provider's key (`class=auth`) rests it for RouteRestFor, ends at
    that time, and stops nothing.
  - A refused take rests the provider in the tick that sees it, over rule 3's rest of its
    routes; the rest's note and the tier's `no route serves the tier` name the cause and the
    provider's words. A refusal is attributed to the rest window its child launched in (the
    take's record keeps when it was taken): a take launched before the provider's last rest
    ended (in flight when the rest began, or launched while it held) starts no new rest
    however late its refusal arrives, so a rest ended by `funded` or by a balance is not undone
    by it; a take launched after the end and refused rests the provider again
    (`sprint.RestsDue`, `providerRestsDue`; `TestAnOutOfCreditTakeRestsEveryRouteOfItsProvider`,
    `TestProviderRestsDueRestEveryRouteOfTheRefusedProvider`).
  - A card dealt on a route before its rest began is never taken there. `take` reads the
    rest itself (its provider's property, for a rest the balance poll wrote between two ticks;
    a route's own rest is written only by the tick, which withdraws its cards in the same
    step): a take by id of a ready card on a resting route is refused, naming the rest, and a
    take by count passes over it. The tick withdraws every ready card on a route resting then,
    by the one path a member going down takes (`withdrawCard`): no take ended, so no redeal is
    spent, its primary goes back to ready for the deal to place on a route that serves, and
    the primary's timeline notes why (`a card withdrawn from a resting route: taken back: its
    route <route> rests (<reason>)`) (`sprint.restWithdrawals`, `cardRest`; `TestACardReadyOnARestingProvidersRouteIsWithdrawnNeverTakenAndRefused`,
    `TestATakeOfACardOnARestingRouteIsRefused`).
  - The balance poll. `run` reads each provider's balance when it begins and every 10 minutes
    after (`sprint.BalancePollEvery`), outside every tick, through the seat's key in its own
    environment (`nova-secrets exec --only OPENROUTER_API_KEY -- nova-sprint run ...`;
    `internal/provbalance`): openrouter's `GET /api/v1/credits`, the balance `total_credits`
    less `total_usage`; opencode publishes none (Zen has no balance endpoint,
    anomalyco/opencode#44189, and its CLI reads none), so its balance is recorded `unknown` and
    why, as any provider's with no endpoint or no key. One step writes each read to the fleet
    table's property `provider_balance_<provider>` (`<balance|unknown> <at> <spend/hour>
    <used|-> <note>`), the spend an hour measured from the provider's count used between two
    reads. While the provider rests for its funds the step keeps the spend measured before the
    rest began (a resting provider spends next to nothing), so a rest never lifts because its
    own spend fell. The step writes, changes or ends the provider's rest as above; a read that
    ends a rest of its funds writes a happened note, "a provider's routes serve again: its
    balance is back". An unknown balance
    writes and ends no rest. Each poll prints one `BALANCE` line naming the balances, never a
    key (`sprint.Balance`; `TestTheBalancePollRestsAProviderUnderAnHourOfItsSpend`,
    `TestLowOnFundsNeverStopsTheSprintAndOutOfCreditDoes`, `TestAnUnknownBalanceChangesNoRest`,
    `TestAPolledBalanceAtZeroExcludesTheProviderUntilABalanceReturns`).
  - `funded <provider> --reason <text>` is the coordinator's word that a provider was paid: it
    ends the provider's rest of its funds now, for a provider whose balance no poll can read
    (opencode) as for any; it is refused when the provider does not rest for its funds.
  - One judgment of the provider, never one per card, while it rests: `a provider is out of
    funds` (`provider <p> is out of funds (balance $x): a payment is the owner's; it is
    excluded: its routes ... rest until paid (...)`), `a provider is low on funds`
    (its words say it is not out of credit and the sprint does not stop for it), or `a provider
    refuses its key`. Its subject is `stream:provider:<p>`; the funds judgments' decisions are
    `funded <p>`, ack and wait, never a rework (a payment is not the card's to fix, and is the
    owner's, never the machine's). It closes when the rest ends.
  - Every provider out stops the sprint. When every enabled route of every tier rests because
    its provider is out of credit, the tick's deal plans the stop and the binding STOPS the
    machine as the step commits: its record's cause `every provider is out of credit`, the
    machine line `machine: STOPPED (every provider is out of credit)`, and one judgment, `every
    provider is out of credit`, naming the providers. A provider low on funds keeps the machine
    running: its tier's `no route serves the tier` says why nothing deals. The line says what
    counts as paid: `the sprint is STOPPED until a provider is paid: a balance over zero the
    poll reads higher than the one before or than the balance at the refusal, or nova-sprint
    funded <provider>`. `start` is refused
    with the same line (exit 1) while every provider stays out; once one is paid (a read that
    ends its rest, or `funded`), the coordinator starts it and the judgment closes
    (`TestEveryProviderOutOfCreditStopsTheMachine`, `TestEveryProviderOutOfCreditStopsTheSprint`).
  - `where --json` carries `providers`, the providers table: a row for each provider the
    routes name, `name`, `balance` (dollars and cents rounded up, a negative one `-$0.51`, or
    `unknown`), `balance_at`, `spend_hour`, `state` (`serving`; `resting until <end> (<cause>:
    <words>)`; `serving; resting <routes> ...` when some rest) and `note` (why a balance is
    unknown), read from the routes and the fleet table's properties, no card. The text frame
    draws no new table (the sprint tables are locked); the dashboard's view of it is its own
    change. `routes` prints each route's provider balance (`balance=`; `--json` `balance`,
    `balance_at`, `rested_for`) (`TestTheRunLoopPollsBalancesAndWhereShowsTheProvidersTable`).
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
- A card's bench: a brief whose header carries `BENCH: <member>`, or a
  comma-separated list of members, names the members that have what the card
  needs (a tool one machine alone holds), and the card is dealt only to them.
  `add` refuses a BENCH that does not read and a name that is no fleet member,
  with the members there are (exit 2, nothing written); the primary's field
  `bench` is the line's names, written with its brief by `add` and `brief`. A
  card with no BENCH line is dealt round the fleet as every card before it was.
  Every placement of its work cards honours the line: the deal, a redeal, an
  escalation and a rework draw from the members of its bench that are up alone
  (`round.next` over them, its room DealAhead times its width as any member's),
  a member going down or held withdraws its bench's card instead of dealing it
  to another member, and the level never moves it off the bench it was dealt to
  (the members its line does not name are avoided as a member that refused it at
  staging is). While no member of its bench is up (down, or held) the card waits
  in `ready`: the tick's deal passes it by, no judgment is written, and the
  no-stall rule holds it as waiting for its bench, the line `nova-sprint card
  <id>` prints (`sprint.BenchOfBrief` and `sprint.Bench`, `bench_deal.go`;
  `TestDealHonoursACardsBenchLine`).
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
  going down sends them, or withdrawn when none is up. The one exception is the
  coordinator's hold with no `--return` (section 11, hold): a member held to
  finish keeps its working cards (`held_finish` on its control card) until they
  finish or their deadline judges them, and its ready cards go.- done and ok% are computed by the table from the member's `ok` and `failed`
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
  sent again, three tries in all (internal/sprintwire). `hold <member> --reason
  <text>` holds a member (section 11, hold; `fleet down <member>` is `hold
  <member> --return` in the old words, for one release) and takes it down;
  `unhold <member>` or `fleet up <member>` releases the hold; `fleet up` counts as a
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
A take by selection (`--as` and `--max`) takes the live generation; a finish
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
  coordinator's `hold <reader> --reason <text>` holds it whatever it beats
  (state `held`; section 11, hold), and `unhold <reader>` releases the hold;
  `reader away <reader>` (`hold --return`) and `reader up <reader>` (`unhold`)
  are the old words, for one release; `reader retire <reader>` holds it
  away for good (state `retired`, its beat written no more, its row and read
  cards kept) until `reader up`. A held reader is asked nothing, its reads
  asked and not begun are asked of another, and its reads begun finish (with
  `--return` they are taken back at the hold, where a reader up is free to read
  them); a reader away or down has every read, begun or not, taken back. The readers table has no `status`
  column and `where` shows no reader's state; the state is never stored in the
  table. The state is read, never typed: the tick reads it once, with its first
  read, and every part plans on that reading.
- The readers' rebalance runs once at the start of every tick, before any
  table's update (the owner, 2026-10-01: "just once before tick, rebalance each
  table."): first its safety ("if ever there are cards on a held or down
  machine, rebalance moves them away"): every read asked or reading of a reader
  that is not up is taken back (retired by `away`) while a reader up without a
  card at its attempt could take it, and the tick's ask asks it again; with
  none, it stays and is judged as below. Then the level: a reader's room is
  its width less its load (asked and reading), compared as a share of its
  width, and asked reads (not begun) move from the reader with the least share
  (over its width when below zero) to the reader up with the greatest share
  that has free room, ties round the readers from the `ask_index`, while the
  target's share after the move stays at or above the source's, so no reader
  up has free lanes while another holds a backlog, no move fills a reader past
  its width (a reader at width is given nothing), and the ask's placement is
  the level's fixed point (`sprint.levelReads`, `round.levelToRoom`). A moved read is retired (by `level`) and asked of the other reader
  at the same attempt and head, its route kept, as a fresh ask (not returned,
  reasked 0); a pro card's two reads stay with two different readers, and no
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
  sprint knows a reader's width the same way, derived from its fleet row
  (`sprint.ReaderWidth`; the readers table holds no width column, and `where`
  shows each reader's width beside reading, "-" for a reader with no row), so
  reads are asked and levelled by room, never by count alone (the owner,
  2026-10-03: with widths 4/8/16/16/24, count-levelling queued seven reads on
  the two narrow machines while 31 reader slots sat idle); a reader named for
  no fleet row keeps unbounded room, so such readers order by load alone. No
  reader is bounded at DealAhead times a width: its room is its width.
- A card's reads are counted by its tier (the owner, 2026-10-02, cost rule 4,
  nova-tools#5174: "Reads: one cold read per flash card on a flash route; two
  per pro card; readers still equal workers per machine"): a flash card needs
  ONE read, a pro card (or a heavy card) TWO (a frontier card is asked of one frontier friend, not drawn on a route; a friend's card, above), from two
  different readers (`sprint.ReadsNeeded`). The tier is the card's own, the tier
  it is on (section 5, flash first: flash at its first deal on a route, then the
  tier it escalated to, or the tier a rework recorded; its ceiling, line 1's
  tier, in a store with no route), never a setting, so a card in merging or landed is
  held to the count it was accepted on; a read tier set for the stream or the
  sprint (below) raises the route its reads are drawn on and never their
  count. The reads per machine are unchanged: a reader still runs at its
  machine's width.
- A reader row carries the tiers it reads (`readers.tiers`, text, no fold;
  an empty cell means every tier). `reader add <reader>... [--tiers
  flash[,pro,heavy,frontier]]` and `reader set <reader>... --tiers ...` write
  them; omitted, `all` and `default` store empty, which is today's behaviour.
  The ask (`freeReaders`, `enoughReadersUp`, the level, and a returned read
  asked again in place) counts a reader only for a primary whose read tier
  (`readTierOf`) the cell names, and never asks a reader a read outside that
  tier. A card with fewer readers of its tier up than `ReadsNeeded` raises the
  one existing judgment `fewer than two readers up` and asks nothing of a
  reader outside the tier. `where` and the reader verbs print the tiers
  (`all` when the cell is empty). A table created before the column gains it
  at `init` or at the next `reader set` (and at `reader add` when `--tiers`
  is given). The model is `tla/ReaderTiers.tla`: no read is asked of a reader
  outside the primary's tier. `tla/ReadsByRoom.tla` and `tla/DirtyTick.tla`
  do not name reader tiers.
- ask deals every primary in review that wants a read to as many different
  readers UP as it wants now, in work order. **Reads are asked one at a time,
  each to a reader with room** (`sprint.ReadsWanted` says how many,
  `sprint.askFinders` and `sprint.askPicks` say where; the owner,
  2026-10-04, after a night of 3,513 reads for 844 landings, 4.2 a landing
  against a design of 2, because a pro card's two reads were asked together
  and the second was spent on work the first reader had already found
  broken): a card's first read is asked alone;
  while a read is outstanding nothing more is asked; a broken read goes to its
  judgment and the rework with no second read; once every read that stands
  came back ok the rest the card needs are asked (ReadsNeeded: one for a flash
  card, TWO DIFFERENT readers for a pro card). A read taken back from a reader
  away or handed back with no verdict was wanted when it was placed and is
  asked again whatever stands; `ask --another` adds one more reader at any
  time. A pro card whose first read fails once costs three reads to land, not
  four. Each read wanted goes to the reader with the greatest share of room
  (its free room, width less asked and reading, as a part of its width;
  widths 4 and 24 with ten reads: the 24 takes eight) that has free room and
  no read card at the attempt, placed or retired, ties the next round the
  readers from the readers' `ask_index`; the reads one ask places come off the
  room as it goes. A reader at width is given nothing, and a primary whose
  reads wanted now find too few DIFFERENT readers with room waits for the
  next tick, due, with no judgment; the ask's `cannot ask` judgment is for a
  primary that no readers could read to the count it needs, whatever their
  room (so not even its first read is asked when no second reader could ever
  read it). One read card per reader. The two rules meet in one pin,
  `TestSequentialReadsAtReaderWidthKeepThreeReadsPerLanding`: pro cards whose
  first read fails once land on three reads each while no reader holds more
  reads than its machine's width (with `TestAProCardsReadsAreAskedOneAtATime`
  and the reader-width tests of cmd/nova-sprint). **The reader who found the
  defect checks the fix** (the owner, 2026-10-04): `rework` writes on the
  primary the reader whose finding it sends back (`finding_reader`, the first
  broken read's reader in reader row order, the readers table's declaration
  order and never name order, when ask --another left two broken reads of one
  attempt: `sprint.finderOf`, refmodel `Rework`,
  `TestTheFinderIsTheFirstBrokenReadInReaderRowOrder`; beside
  `finding_attempt`), and the
  next attempt's first read is asked of that reader, out of turn, when it is
  up, has no read card at the attempt and has free room under its machine's
  width (a reader named for no fleet row has room), so the check is against
  the finding and not a fresh opinion (`sprint.finderFirst`); the second
  reader stays fresh, the reader with the most room. The finder's read
  carries `finder`, the readers' level leaves it where it is, and the
  readers' index does not move for it; the finders' reads are placed first in
  a step and come off their room like any read, so the room, not a turn
  count, keeps the readers' loads even and the ask's placement stays the
  level's fixed point
  (`TestTheReaderWhoFoundTheDefectChecksTheFix`); a finder away or without
  room is not preferred (`TestTheFinderIsNotPreferredWhenAwayOrWithoutRoom`).
  Reworked work is otherwise asked by the same room: a read is a fresh child
  on a freshly drawn route, so the readers of an earlier attempt are not
  preferred, and a busy reader is not asked again only to have the next
  tick's level move the read (the owner, 2026-10-01, deleting the preference
  for the readers kept on the primary: "yes on the decision."). The model is
  tla/ReadsByRoom.tla (one read at a time, the finder first, every read on a
  reader with room under its machine's width, three reads for a pro card
  found broken once) and the reference model (internal/sprint/refmodel,
  AskChoice: the finder first, then the least loaded reader).
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
  free with room, of the same reader again, in place, which takes no room (its
  reader holds the card already); either way on a route drawn
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
  rework, drop; tla/DirtyTick.tla, ReasksBounded and StrandingIsJudged). A
  reader is asked an attempt once: its read card at the attempt is one read
  per reader per attempt (`<primary>.r<attempt>.<reader>`), so a reader whose
  read was taken back (away, levelled, returned) holds the card retired and is
  not asked that attempt again, where the model leaves such a reader out of
  `seen`; the next attempt is read on new cards, by every reader. The tick's
  `cannot ask` is one judgment per tick, `no eligible reader for <ids>`, every
  such primary a subject of it (the night of 2026-10-03: five cards whose reads
  were taken back from five readers in turn, five judgments). Its
  member does not begin a read it returned again before
  `member.ReadStageRetry`. A return of a read the caller does not hold is
  refused, and so is a second return of a read returned and not begun since:
  a return is counted once. A primary that needs more readers than are up
  is not asked (with one reader up the tick asks the flash cards and the pro
  cards wait): the tick raises one judgment, `fewer than two readers up:
  <readers and their states>`, for the sprint (not one for each primary),
  closed when enough are up or no such primary waits; `reader up` and
  `reader add` answer it.
  The machine's tick asks for every such primary; `ask` is the coordinator's
  own. Each read card the ask creates carries a route as a work card does
  (`route`, `model`, `tokens`, `usd`, `deadline`), and `tier`, the tier it is drawn
  from: the tier of the card it reads, the tier the deal draws that card's
  work from (flash first, the tier it escalated to after; a card that pins a
  model is read on line 1's tier, flash when it names none; a heavy card, heavy; a frontier card, a
  tier no route serves, is asked of a frontier friend, not drawn on a route (section 1, a friend's
  card), and a route named for it is heavy, the strongest tier a route serves (a read on pro
  would be weaker than the writer); the owner, 2026-10-01: "i think readers being
  conservatively the same tier as the work being done seems fine?"), raised
  to the read tier set for its stream (`stream set <s> --read-tier <tier>`, the
  stream's control card's `read_tier`) or else for the sprint (`set --read-tier
  <tier>`, the work table's `read_tier` property) when that is stronger, and
  never lowered (nova-tools#5096 item 27: a pro card's reads run on a tier at
  least as strong as the writer's). **The floor is per attempt** (the owner,
  2026-10-04): a flash-first card whose third attempt runs on pro gets pro
  reads whatever its stream's read tier, which is a floor over the attempt's
  tier and never a cap, and the default read tier is the attempt's own; a
  stream's read tier is never set below its work tier, the strongest tier its
  open cards' briefs name (`sprint.StreamWorkTier`): `stream set <s>
  --read-tier <t>` raises it and refuses to lower it with one line, `<s>'s
  work tier is pro: its read tier is never below it; run: nova-sprint stream
  set <s> --read-tier pro`; a stream read tier of heavy raises every read in
  the stream (`TestTheReadTierFloorIsPerAttemptAndAStreamSettingOnlyRaises`).
  **The escalation** (readtier.go): when a stream's landed card is returned by
  dev or an audit (`promoted --sha <sha> --returned <ids>` marks each with
  `returned_by_dev`, the tier its reads ran on and the sha), when two readers
  at the stream's read tier disagree on one attempt (an ok and a broken read
  standing at it), or when a card alternates broken and ok across attempts (a
  reader passed an earlier attempt, `passed_head`, and the current one is
  found broken), the tick raises one judgment per stream, `raise the read tier
  of <s> to <next>? <why>`, with the decisions raise (the default: `stream set
  <s> --read-tier <next> --reason '<why>' --answers <note>`, which records the
  reason on the stream's control card, `read_tier_reason`, and closes the
  judgment) and keep (`ack`, which holds it quiet); it is one per stream
  whatever the cards, kept in place while a cause holds, closed when the
  stream's read tier for the card has risen, and it never lowers; at the top
  tier nothing is asked (`TestReadersDisagreeingRaiseOneJudgmentPerStream`,
  `TestALandedCardReturnedByDevAsksToRaiseTheReadTierAndKeepHoldsIt`,
  `TestACardAlternatingBrokenAndOkAsksToRaiseTheReadTier`); its packet hands the reader that tier and
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
  attempt only (the primary's `asked` field still names the readers the
  attempt was asked of, and after a rework the readers its tier needs are
  asked round the readers);
  before
  the first ask of its attempt it is refused, naming `ask` and the tick as
  what asks first.
- **The decide read.** The first read of a flash card is a decide read (the
  owner, 2026-10-02: "i'd really like to start using jev to do cheap
  reads/evals/scoring of work"; "the nova-decide is both sides"). The ask writes
  the sprint row's two bars, `decide_bounce` and `decide_review` (nova-config,
  `sprint set --decide_bounce <p> --decide_review <p>`; 0.5 and 0.3 by default,
  the calibration of 2026-10-02 over 234 reviewed cards, AUC 0.869), on the first
  read card it places at a flash card's attempt (`steps_review.go`, decideFields:
  drawn on flash, not `ask --another` or `--instead`, and no read that stays at
  the attempt is one); the packet hands them to the reader, and a read the level
  moves keeps them. Every other read is a strings read: a read drawn on pro
  always is, and both bars empty in the sprint row turns the decide read off. Its reader's native, once the checkout is staged
  and before any child, asks nova-decide's read decision (docs/SPEC-NOVA-DECIDE.md
  section 8) over the work card's brief alone (the sprint's mechanics and the
  worker's report cut off, the state the bars were calibrated on; the E1 rule the
  calibration added for docs and diary cards is not sent yet) and the work's
  diff, start..HEAD, through the Jev
  backend with the key `JEV_API_KEY` the reader loop's nova-secrets keys hold
  (native's own: no child is handed it), and routes the read by p(defect) alone,
  never by the decision's verdict or its inside_paths answer:
  - at or above `decide_bounce`: a broken read, its finding
    `decide: p(defect)=0.xx at or above the bounce bar ...` with the files the
    diff changes and the five answers; no child runs;
  - below `decide_review`: an ok read, its report the same line; no child runs,
    and the card goes on to accept as any ok read takes it;
  - between the two: the strings read runs as before, and its verdict (ok is
    LAND, broken is BOUNCE) is attached to the decision as its outcome.

  Every decision is appended to the reader machine's record,
  `<root>/decide/read.jsonl`, under the read card's id at the head it read
  (`<card>@<head 12>`), so a read returned and asked again replays it with no
  call; a review round's label is attached later with `nova-decide outcome`, and
  `nova-decide calibrate` reads the bars the record supports. The record is
  loaded whole on every read and not yet rotated: a cap or a rotation is owed
  before it grows past the spec's thousand reads. A decide read that
  cannot be made (no key, a backend that fails, bars it cannot read) is said on
  one `NATIVE NOTE` line and the strings read runs.
- A reader moves its own read cards: asked -> reading -> ok | broken, with the finding.
  A report on a card still asked is accepted: it is the begin and the report in
  one step, and `begun` is stamped with it.
  A read whose stage fails (the head could not be checked out) is never a verdict: its member
  runs the read again once, after `member.ReadStageRetry`; a second stage failure is returned
  (`read --as <reader> --return <card> --reason <the stage's reason>`), and the next tick asks
  it again as above.
- The read that completes the ok reads a primary needs at its head (one
  reader's for a flash card, two different readers' for a pro card) writes
  the judgment ready to accept; accept, rework and drop close it.
- The machine's tick accepts every acceptable primary in review whose work did
  not fail, except one whose CI is red at its head ("ci red on a primary" is
  the coordinator's: a green at its head, or the coordinator's accept, takes
  it) and one the coordinator returned to review at its attempt ("returned to
  review" decides it: accept, rework, drop; its reads stand, and a rework's new
  attempt with its own reads is the tick's to accept again). An
  acceptable primary the tick does not accept is told as ready to accept when
  no open judgment on it offers accept.
- A primary is acceptable when as many different readers as it needs have an
  ok read card at its current attempt and head: one for a flash card, two for a
  pro card. For a pro card one reader's ok alone is never enough, whoever the
  reader. A reader counts once, and a read card counts only when the row it
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
  finish wakes, asks the readers its tier needs (one for a flash card, two
  different readers for a pro card) round the readers, at the new head, on new read cards of the new attempt, each with the route it draws
  (one path asks). A report against a retired
  read card is refused, naming the retirement.
- A rework of a primary a reader passed (an ok read at its head when it is sent
  back) keeps that head (`passed_head`). When the next attempt's worker finds
  nothing to do or commits nothing (its failed finish begins `nothing to do:` or
  `no commit:`, and pushed no head), the card was right: it is no failed work and
  no judgment; the primary goes back to review at the passed head, and the tick
  asks the readers its tier needs at the new attempt (`TestNothingToDoAtAHeadAReaderPassedIsBackInReview`).
  With no pass at the head it is failed work for the coordinator, as before.

### reader-ignores-attribution.w5

- Attribution is never a finding and never decides a verdict. The By: line, the
  Co-Authored-By trailer and the model or harness a worker names are its own
  honest account of what ran, which may differ from what a brief guessed, so a
  reader's packet carries that one standing sentence. The verb holds it too:
  `read --broken` whose finding is attribution-only (every sentence is about the
  trailer, the By: line, the model or harness named, or a request to amend them;
  `sprint.AttributionOnly`) is refused, `read REFUSED: attribution is never a
  finding (docs/SPEC-SPRINT.md); read --ok, or name the defect in the work`,
  and nothing is written; a finding with a real defect and an attribution remark
  passes (`TestAttributionOnlyFindingsNeverBounce`).

### restart-keeps-reads-r.w1

- A server restart keeps every read in flight whose lease is live and only takes back
  reads whose lease has lapsed (retired by `lapsed`: `sprint.RestartReads` / `sprint.ServerRestart`,
  `store.ServerRestartStep`, `tla/ServerLanes.tla` Restart, `LiveLeaseNeverTakenBack`,
  `EveryLapsedReadTakenBack`). A read asked of a reader is held by a lease: started by
  `read --begin` (`begun` + `DefaultReadLease`, 10 minutes), and renewed by the reader's beat
  (`queue --as <reader>`, setting `lease` to the current time + `DefaultReadLease`).
  On server start, `store.ServerRestartStep()` is run once before the first tick in `run` and
  in `listen` (`run --listen`), both through `serverStart`, so in-flight reads with live
  leases survive a server restart while lapsed reads are retired and re-asked.

## 7. Merging

1. In work order, never random: the head of the stream's queued cell first.
2. In batches onto the sprint branch, the branch every stream lands on; the
   batch is proved there. Promotion alone takes the sprint branch to the
   development branch, dev (the sprint branch, below).
3. When a merge is not possible or not easy the stream stops and the
   coordinator is told. The coordinator may act on the card, the stream, or
   several streams together.

**The sprint branch.** Every stream lands on the sprint branch, and promotion
alone reaches dev: a card cut on dev is merged by the lander straight onto it and
ejects the merge queue's promotion run (found 2026-10-04, when cards cut with
`BASE: dev` restarted that run at every landing). So `add` refuses, per card and
all or none, nothing written, a card whose brief names `BASE: dev` (a pinned
`dev@<sha>` too, as the lander reads the line) in a stream that is not the
promotion stream, its remedy the sprint branch: re-cut the card with `BASE:` the
branch its stream lands on; `quack` is held to the same rule, its default base
the test repository's sprint branch, `sprint/quack`. The promotion stream is the
stream whose control card carries the mark `land_protected`, written only by the
coordinator's `nova-sprint stream set <s> --land-protected <owner/name,...|any>`
and taken off by `--land-protected default` (`sprint.IsPromotionStream`). A card
naming no `BASE:` lands on the lander's `--base` (`sprint.SprintBranchWhy`). The
lander holds the same line from the other side: it refuses, in `placeWhy`, before
any git, a batch whose effective base (the card's `BASE:`, else `--base`) is dev
in a stream that is not the promotion stream, naming the sprint branch and the
mark (the protected branches, below).

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

The record of a landing names its cards: `merge --stream <s> --landed <id>@<head>[,...]
--repo <dir> --base-ref <ref>`. For each card the caller runs one `git merge-base
--is-ancestor <head> <ref>` in the clone against the fetched base tip (never the tick),
and passes the answer as a fact; the step then refuses every card, naming it and why, and
writes nothing, unless each is queued in the stream and merging in work, at exactly the head
given, with that head an ancestor of the base tip. A card returned, queued again or re-cut
between the push and the record fails the head check, and the cards that were not pushed
are never recorded in its place. It lands exactly the named cards. The position form
(`--batch n`) is the lander's selection of a batch and the selection a fact (`--red`,
`--rejected`) is about; land's own report names its cards by id, head and attempt
(tla/Land.tla, idguard), and `merge --landed` is the same record for a caller without
land (tests TestMergeRecordsLandOnlyTheCardWhoseHeadWasPushed).

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
when it cannot; it adds no state of its own. The base is the sprint branch:
`land` refuses, before any git, a batch whose base is dev (or main) in a stream
not marked for promotion, its remedy the sprint branch (re-cut the card with
`BASE: <the sprint branch>`, or `--base <the sprint branch>` for a card naming no
`BASE:`) or, for the promotion stream, the mark (the protected branches, below).

**The protected branches.** The lander never lands on a protected branch of a
repository, dev or main, unless the card's stream is marked for that repository: a
stream lands on its sprint branch, and promotion to dev is the marked stream's work
(a card cut on dev lands straight onto it and ejects the merge queue's promotion
run). The mark is the stream's control card's `land_protected`,
the repositories (owner/name, any spelling of its clone URL, comma separated) or `any`
for every repository, a card naming no `REPO:` line among them, written by the
coordinator's `stream set <s> --land-protected <owner/name,...|any|default>` (`default`
takes it off). A batch whose base is a protected branch in a stream not marked for its
repository is refused before any git, `land` and its dry run alike, nothing pushed or
recorded and the cards left queued, its remedy the sprint branch (every stream lands
on it, and promotion alone reaches dev: a card is re-cut with that `BASE:` line, or
landed with `--base` on it when it names none) or, for the promotion stream, the mark
(`sprint.ProtectedLandWhy`).

**Weight** (the owner, 2026-10-04: "these critical blockers should have some elevated
priority ... they should be at front of queue"; one night the root of about 140 schema
cards was dealt, read and reworked like any other card). A card's weight is the number of
cards transitively waiting on it through their needs (`sprint.Weights`,
internal/sprint/weight.go, walked backwards over every card on the table not landed); every
needs change (`add`, `drop`) writes it on the primaries it changes as `behind`, exact, so no
tick recounts it. The inbox lists judgment groups by the heaviest of their cards, after the
marked ones, and a group of a card with 10 or more behind it, `sprint.CriticalBehind`, prints
`CRITICAL <n> behind:` before its line. The deal, the ask and the lander's batch keep the
modelled order (stream turns, work order; `tla/SprintTables.tla`, the differential test
holds the store to it) until the model orders by weight too: owed, with the model. A
critical card starts on a pro route from its first deal whatever its brief's tier says, with
pro's deadline and budget, and is never dealt below it (`ceilingTier`; a pinned model or tier
and a frontier card keep their own). `where` names the five heaviest under the summary, from
the tick's where record: `critical: <id> <n> behind, <state>; ...`
(`TestACriticalRootIsDealtReadAndJudgedFirst`, `TestACriticalCardStartsOnPro`).

**Dev is behind** (the owner, 2026-10-04: "You should regularly, mechanically be reminded
merges to dev are dirty, and should be done"; "We must merge into dev continually, at least
in bursts"). The tick counts the primaries landed (sentinels aside, by their `landed` stamp)
since the last promotion the store records (`promoted_at`, `promoted_sha`, the work table's
properties, written by `nova-sprint promoted --sha <merge sha>` (with `--returned <ids>`, the landed cards dev or an audit returned, each marked for the read tier's escalation, section 6), the coordinator's; none
recorded counts every landing), and raises the judgment "dev is behind" when the count
reaches 25 (`sprint.PromoteCards`) or the oldest of them landed 30 minutes ago
(`sprint.PromoteAge`), whichever comes first (`sprint.DevBehind`, internal/sprint/promotion.go,
one pure decision): `dev is behind: <n> cards landed on <branch> since the last promotion at
<time> (<sha>); promote: merge origin/dev into the sprint branch, open the PR to dev, run the
functional tier, queue it; then: nova-sprint promoted --sha <merge sha>` (the branch is the
base the most of them name; with no promotion recorded, `since no promotion recorded`),
decisions promoted and wait 30m, updated in place while it holds and closed when `promoted`
is recorded; `promoted --answers <note>` is held as every answer is (`answered`): an answer
naming no open judgment refuses the whole step, nothing written
(`TestTheTickRaisesDevBehindAtTwentyFiveLandingsOrThirtyMinutes`, `TestPromotedHoldsItsAnswers`).

**Dev sync every cycle.** On 2026-10-04 the base and the development branch drifted for an
afternoon while hundreds of cards landed on each; folding them took 105 conflicts and an evening
(the owner: "promote every cycle or drift causes a big fuckup"). Each land cycle in which a sync
is due (`sprint.DevSyncDue`: `sprint.DevSyncEveryLandings` landed since the last sync, counted
from the store by `sprint.LandedSinceSync`; `sprint.DevSyncAge` since it with nothing landed, as
dev moves on its own; none recorded; or a conflict open), the land round merges the development
branch into the base in its clone (`sprint.LandCycleSync`, `sprint.RunDevSync`): fetched, the
drift counted, merged on a detached base so no local branch ever holds an ungated merge; a clean
merge goes through the round's own tree gate (`DevSyncReq.Check`, required: a sync with no gate
refuses) and is pushed onto the base like a batch; a red gate or a refused push pushes nothing.
The facts are recorded by one pure step, `sprint.DevSynced`, on the step's own snapshot (git runs
once, outside the plan, which a retry runs again): the drift (commits each side lacks, the last
sync and its sha) on the merge table's properties and on every stream's control card, the
dashboard's merge row, and `sprint.DevDriftOf` reads it with the minutes since. A conflict stops
every stream (cause `dev sync conflict`, its files in `conflict_paths`) with ONE judgment naming
the files while they are few (`sprint.NDevSyncConflict`); each cycle tries again while it is open,
bringing its text up to date and raising no second one; the cycle that finds the base holding dev
(merged by hand, or cleanly) closes it and resumes only the streams it stopped; `sprint.CanLand`
is false meanwhile (`TestTheBaseTakesTheDevelopmentBranchEveryCycle`, on the twin store and a twin
repository; `TestADevSyncConflictStopsEveryStreamWithOneJudgment`). Owed, outside this card's
paths: the call in the land round itself (`nova-sprint land`, before its first batch, with the
round's tree gate as `Check`), and the drift on `where --json`.

**The lander's checks.** Each head `land` merges is checked by script, no model,
before the batch's check runs (`internal/diffcheck`), the two checks the decide
read's calibration of 2026-10-02 found a model read does not make: the merge's
own diff changes no file outside the brief's `PATHS` globs (E12: p(inside_paths)
was 0.98 on the card that left its PATHS), the class ledgers excepted (under
`internal/ci/testdata/`, a list file as internal/ci spells its lists or a `.txt`
shard of a counted ledger, never the class tests' fixtures beside them); a rename
holds both sides: it moves from a file the card names, to one it names or within
the directory the file was in (a name card's rename in place); and it leaves no
stranded fragment in prose, a Go comment or a Markdown or text line (E4): a
change that takes away backquotes of one parity and puts back the other, or a
line that ends mid-sentence (on a letter, a digit or a comma, and not on a word
that opens a sentence) whose old text went on with a word that opens no sentence
and whose new text goes on with one that does (a capital and lower case after
it). Over the 234 reviewed cards the fragment rule found four cards, each one the
review called wrong for a fragment, and no other; over the land merges of 215 of
them it finds those four and one more, diaryr-37, a lead-in left without its end
the review passed. On those land merges the PATHS rule flags negd-42 and three
cards of its shape (negd-17, -32, -41: a file the card edited after a name card
renamed it from the name its PATHS gives), which the review passed. A head that fails is taken off
the batch branch and ends the batch as a head in conflict does: the conflict fact
on it names every failure, `<file>:<line>` and the rule.

**The tree gate.** Every tip of the batch branch passes the tree gate before the
next head is merged onto it, in a clone that holds a `go.mod`: the module builds
and vets (`go build ./...`, `go vet ./...`), and when the head's merge changes a
document or a test file (a `.md`, a `_test.go`) the packages that test the tree
itself pass (`go test ./internal/docs/ ./internal/ci/`, those the clone has). The
base's tip is gated once a batch, the tree tests included, before any head is
merged: a base that is red refuses the batch, nothing pushed or reported and no
card blamed, the reason naming the base and the run, and the remedy is to fix
the base. A head whose merged tree is red is taken off the batch branch and ends
the batch as a head that does not merge does, the conflict fact's note the run,
how it ended and its output on one line (the finding; the heads before it land).
A merge that made no commit is not gated. Every go run the lander makes in the
clone, the gate's and the update runs below, is under `GOFLAGS=-mod=readonly`:
no run writes `go.mod` or `go.sum` (under a caller's `-mod=mod` the update runs
of 2026-10-03 rewrote `go.mod` and every resolution was refused for it), and a
module that needs them changed fails the run, which is the card's finding.
`--check` is the caller's own command on top, once a batch, as before.

**The scope amendment.** A file outside the brief's `PATHS` that is the test, the fixture or
the doc of the same change is allowed by rule, never by a message to the coordinator
(`sprint.ScopeAmended`): the change also changes a file of its own (in `PATHS`), and the
file is a Go test file (`_test.go`) in the directory of one of those files, a file under
that directory's `testdata/`, or a Markdown file under `docs/` or in that directory. Each
one is recorded on the batch's line, `scope=<card>:<file>,...` (and `scope` under
`--json`). Any other file outside `PATHS` is refused as E12 says; a change with no file
of its own amends nothing.

**The generated ledgers.** A merge that stops only on generated ledgers lands.
The generated ledgers are the class ledgers the checks above name (a `.txt` shard
of a counted-ledger directory or a list file, never a directory, a Go file or a
symlink), narrowed to a family owned by named tests: today the generality family,
the `.txt` shards under `internal/ci/testdata/generality-text/` and
`internal/ci/testdata/generality/` and the generality text fixtures allowlist,
owned by `TestGeneralityGuardrail` and `TestGeneralityText`. Such a ledger is
shrink-only and a function of the tree: two cards that both delete rows and both
move its ceiling line conflict line by line, and regenerating it at the merged
tree gives the one answer. When every unmerged path of a head's merge is a
generated ledger, `land` takes the tip's side of each, runs the owning tests'
update mode (`NOVA_CI_UPDATE=1`) in the clone until a run writes nothing (at most
four runs; the update drops the fixtures allowlist rows whose files have no
finding any more), and commits the merge as `land <id> (sprint stream <s>)` with
a body naming the ledgers and the tests. An update run that fails, one still
writing at the fourth run, and one that changes or adds any file outside the
ledgers (`git status`, untracked files included) refuse the card with the
conflict fact, its words in the note, the merge ended and what the runs wrote
taken back. Before any update run every path the update writes (each family's
directories and allowlist and every tracked file it owns) and every directory on
the way to it is held to be no symlink, in the tree (`git ls-files -s`) and on
disk (`lstat`), whichever paths conflicted, and a link refuses the card the same
way. Any unmerged path outside the family is refused as any conflict is. The
landing writes the resolution on the card's merge card (`note`), and the
card's timeline tells it on the line of its merge.

**land-e12-catalog-rows.** A card that adds a directory (a file under a
directory that holds no tracked file before the merge) that its PATHS name
owns the catalog row that directory costs and the maps `make map` writes.
`internal/docs/catalog.go` is the card's when its change is added lines only,
each a row naming one of those new directories; every `AGENTS.md` map
`tools/agentsmap` writes is the card's. Any other change to `catalog.go`, or a
map change from a card that adds no directory, stays outside PATHS (E12). The
maps are a second generated-ledger family, owned by
`TestCommittedMapMatchesTree`, its update run `go run ./tools/agentsmap` under
`GOFLAGS=-mod=readonly`. A merge whose unmerged paths are only those maps, or
only those maps plus `internal/docs/catalog.go` where both sides only add
rows, resolves: the union of the rows, the tip's first, then the map family
until a run writes nothing, committed as the generality family is. The
resolution is told as a shrink-only union is: one land log line and the card's
note. A conflicting `catalog.go` line that is not an added row is refused as
any conflict is.

**The shrink-only ledgers.** A merge that stops in a shrink-only ledger lands
without a stop. The shrink-only ledgers are the lists whose class test in
internal/ci says they only shrink, named in one place, `shrinkonly.ShrinkOnly`
(`internal/ci/shrinkonly/shrinkonly.go`): every top-level list file under
`internal/ci/testdata/` but the lists that grow (the deleted-tests log, which
is appended to, and `compared_examples.txt` and `namedpaths_allowlist.txt`,
which gain rows by hand), and the unit tier's two lists under `internal/ci/`
(`sleeps-skips_allowlist.txt`, `slow-tests_allowlist.txt`); the counted-ledger
shards below the testdata directories are not: they are the generality family's,
regenerated above. Two cards that
each remove a row of the same ledger conflict when the rows are adjacent, and
the one right answer is the base with both removals gone: for each such
unmerged path `land` reads the merge's three stages (the merge base, the tip's
side, the card's) and resolves the file as the base less what either side took
(`unionRemovals`, a function of the three sides' bytes): a plain row is held or
gone; a counted row (`key N ...`, the dead code ledger's) holds its count less
what each side lowered it by, so a row both sides lowered is lowered by both
(left + right - base), and goes at zero; the `# ceiling: N` line, when the base
has one and a side lowered it, is at the lower of the two sides' values (each is
at least its side's rows, so the lower is at least the union's). A side that
adds a line the base does not hold (a new row, a changed row, a reordering) or
raises a count or the ceiling is refused, and the result is held to be the
base's lines less some before it is written. The resolved files are
staged; when nothing else is unmerged the merge is committed as `land <id>
(sprint stream <s>)` with a body naming the ledgers and the removals, and when
the rest is a generated-ledger family's, that family's regeneration (above)
follows on the staged tree. A refusal stops the stream as any conflict does, its
reason naming the ledger and the line, the merge aborted. The land log carries
one line per resolved ledger, `ledger <path>: resolved as the union of removals
(-n left, -m right)` (the batch's `NOTE`), and the card's merge card and timeline
say the ledgers were resolved as the union of both sides' removals.

**The landed score.** Once every stream of the run has landed what it could,
`land` scores each landed card's merge diff (the diff its checks read) against
the card's brief with nova-decide's score decision (docs/SPEC-NOVA-DECIDE.md
section 9: the read's five questions and one per escalation class of landed
work), recorded in `decide/score.jsonl` under the land root as
`<card>@landed@<head>`, and reports each batch's scores in one store step
(`score`, sprint.RecordScores): each landed card carries `landed_class`, its top
class, `landed_p`, that class's p, and `landed_op`, the decision's id; the cards
whose `landed_p` meets the sprint row's `decide_score_bar` (nova-config) are
listed, the highest first, in one judgment for the batch, "landed work scored
low", whose decisions are to add a repair card (`add`, then `ack` naming it) or to
accept the landing as it is (`ack`). The step writes nothing for a card already
carrying the same decision id, so a replay raises no second judgment. The bar is
empty by default: the scores are recorded, written on the cards and clustered,
and no judgment is raised, because the calibration of 2026-10-03 flagged 54 of
157 clean cards at 0.5 (the top class's AUC against the clean cards of the same
streams was 0.716); 0.7 is the starting point once a review round labels cards
independently. The bars are read with the routes, so a store whose route set
names no route reads no bar and raises no judgment. A score never holds a landing
back: the scoring runs after the whole land pass, under one deadline for all of
it (a minute) and the land loop's context, so the loop's shutdown ends it; the
first backend failure, or the deadline, ends the pass, and each batch's `NOTE`
names every card not scored and why. The batch's line carries `scored=<n>`, and
`judged=yes` when the judgment was raised. `nova-decide findings` over the record
clusters the classes into the material for new finder rules.

**The lander's gate.** A batch whose `--check` is red on go test failures has each
failure classified by the gate decision (docs/SPEC-NOVA-DECIDE.md section 12; section 5,
the gate verdict) at the sprint row's bars, read once a land run, over its lines, the
batch's PATHS and its diff from the base; the base is not run. Each decision is recorded
and shown in a red batch's reason, `(the gate decision, <op>: <Test>:<class>:<p>,...;
recorded; the flaky bar is unset, so nothing is rerun)` while the bar is empty, its
default. When the flaky bar is set, no failure is caused and one is flaky at or above it,
the check runs once more: green, the batch
lands as any green batch; red, it is red as before, the reason ending `(run once more:
the gate decision classed <tests> flaky, op <op>)`. The rerun's result is each flaky
decision's outcome (`flaky`, or `red-again`). Every decision is in
`<land root>/decide/gate.jsonl` under `land/<stream>@<tip 12>@gate/<pkg>.<Test>`. A gate
red otherwise is red as before, its reason naming the decision's route; no key, or bars it
cannot read, is red as before with why no decision was made. Asked with the base not run,
the calibration of 2026-10-03 gave p(caused) AUC 0.8 and p(pre-existing) 0.522, so the
lander acts on flaky alone.

**The lander's pause.** `land` does not land onto a branch while a merge window is
open, nor while that branch's merge queue on the forge holds a group: a direct push
under a group being checked moves the base the group was cut from (windows were
negotiated by message before this was a verb). `merge-window open --for <duration>
--reason <text>` (the coordinator's) opens the window from the step's clock: its end, RFC
3339 in UTC, and its reason are the merge table's properties `merge_window_until` and
`merge_window_reason`, written guarded on the values read and replacing a window open
before; a clear starts the next epoch with neither. While it is open every batch is
refused, its reason `paused: a merge window is open until <end> (<reason>); the cards stay
queued and land when it closes`, and no forge is asked. Otherwise the merge queue of the
batch's base is asked (through an interface: a GitHub repository's through `gh api
graphql`, its entries counted, each answer kept 20 s so the land loop's rounds do not ask
every round; a repository on no forge with a merge queue, a path or a bare clone, has none,
and tests give a fake): a group held refuses the batch, `paused: the merge queue of <base>
holds a group; ...`, and a queue that cannot be read refuses it too, naming why: an
unreadable queue is not an empty one. The pause is checked before any git and again just
before the push. A paused batch records nothing, pushes nothing and stops no stream; its
cards stay queued and land on a run after the pause ends. A dry run reads the store only:
it shows the window and asks no forge. An end that cannot be read pauses, naming it, until
the window is opened again (`sprint.LandPause`, `sprint.MergeWindowOpen`).

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

After a conflict, `resume` puts the card back in the queue at its head, and the
next `land` merges that head again, regenerating the generated ledgers where they
conflict (above). A conflict outside the ledgers is answered by rework or drop,
never by a resolution on the card's branch, so no change a reader has not read
lands. The conflict judgment's "resolve and resume" decision says so in its
`--did` text.

#### land-clone-self-heals-r.w1

The clone land keeps under its root is the lander's own: it computed the path and made the
clone, so a pass cut short there is the lander's to undo (found by hand, 2026-10-04: a hand
land beside the server's lander in the same kept clone left modified files, and every later
pass refused all 11 streams, the clone is not clean). Before each batch, when its kept clone
is not clean (a tracked change, or a merge in progress), land aborts the merge, fetches the
batch's base, resets the clone to it and removes its untracked files, inside that clone only,
and says so on one line before the batch's, `LAND CLEANED stream= dir= files=<n>
paths=<a,b,...>` (`--json`: the batch item's `cleaned`); a clone it cannot restore refuses
the batch with the step that failed. A clone the caller gives with `--repo-dir` is the
caller's: it is never cleaned, and a dirty one is refused as before
(`TestLanderRestoresItsOwnDirtyCacheClone`, cmd/nova-sprint/land_clean_clone_test.go).

## 8. Notifications

One stream of notifications, written by the same step as the move that caused
it and visible at that step's logical commit (section 10), never before; read
from the coordinator's cursor. Two kinds.

**happened**: no decision. stream started merging; batch landed; stream landed;
work came back ok; fleet member up or down; cards returned to ready because no
member is up; ci green on a primary; an operation was abandoned; a sentinel
landed, released by the coordinator; the machine started or stopped; a stream
resumed because the card it needed landed; a route rested because its children
ended with no result (section 5); the fleet is idle, and working again (section 14, the
fleet is idle, addressed to the coordinator).

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
| a card has reached its bound: the brief is wrong, not the worker | brief (a waiting card; else drop and add again), drop | no |
| stream stopped: conflict on a card | resume (resolved), rework, drop | no |
| stream stopped: stream branch red | return the suspect and resume, rework the suspect | no |
| stream stopped: needs a card of another stream first | rank that card first (the tick resumes when it lands), wait, card (look at both), return, drop | no |
| stream stopped: the merge queue rejected | resume, return, drop | no |
| stream stopped: the base fails its tree gate (land's base-gate rule, its third failure; section 8, answered by rule) | resume (the base passes again), wait | no |
| ci red on a primary | rework (with a fix), return, drop, card (look), ack (looked, nothing to do) | yes |
| a primary came back a second time for the same cause | card (stop and look) | no |
| a primary is blocked on something dropped | drop, ack (waives the dropped need); `relink <old> <new>` answers it when the dropped card has a twin (section 2, a card replaced by its twin) | yes |
| a primary is blocked on something missing | drop, ack (waives the named missing need) | yes |
| reads exhausted | ask --another, rework, drop | no |
| ready to accept | accept, rework, drop | no |
| returned to review | rework, accept (while its reads stand at its head), drop | no |
| stranded in review | rework, drop (and ask when never asked) | no |
| sentinel reached | release, add --before (do more before going on), drop | no |
| repair skipped changes the store refused as recorded | card (look), return, drop, rework, ack | yes |
| an operation was stuck | check, ack | yes |
| a reminder could not be delivered | goal set (a new route), goal drop, ack | yes |
| cannot ask (enough readers are up, and a primary has fewer readers with no read card at its attempt, placed or retired, than its tier needs: one for a flash card, two for a pro card; one judgment per tick, `no eligible reader for <ids>`, every such primary a subject of it) | reader add, rework (a new attempt every reader may read), drop, wait | no |
| fewer than two readers up | reader up, reader add, wait | no |
| no fleet member is up (when every member that beats is held, it says so and offers only fleet up and wait) | fleet beat (on a machine), fleet up (releases a hold), wait | no |
| the fleet is starving (ready, sentinels aside, is under twice the up members' width while a wave is held: `the fleet is starving: ready <n> is under twice the width <2w>; release a wave: nova-sprint release <sentinel> --reason '<why>'`, raised once and updated in place every tick while it holds, naming the first held sentinel in work order; closed when no wave is held; `TestTheTickRaisesStarvingWhileReadyIsUnderTwiceTheWidth`) | release (the wave's sentinel; never a single card), wait | no |
| a member is overloaded (the owner, 2026-10-03: "the overload is defined as -- cards are timing out. not any CPU%": within the last 15 minutes, `sprint.OverloadWindow`, a member up has had three or more cards, `sprint.OverloadTimeouts`, end on a timeout of any kind, counted from the finishes it reported: a launch refused at staging on `stage-timeout` (the work card's staging take, on whatever row the card sits now), a failed finish `deadline: ...`, or one the budget rule ended because `the usage source stopped answering`; `sprint.TimeoutKind`, `sprint.MemberTimeouts`, `sprint.Overloaded` in internal/sprint/overload.go, one pure decision the tick and the seat check both read; no load number is in it, the beat's load stays a fact for the table): `<m> is overloaded: <k> cards ended on a timeout in the last 15m0s: <card> (<kind>), ...; halve its width: nova-sprint fleet up <m> --width <half>, or wait 15m`, one per member, updated in place every tick while it holds and closed when the window has no three (`TestTheTickRaisesOverloadedOnThreeTimeoutsInTheWindow`) | fleet up <m> --width <half of its width>, wait 15m | no |
| the readers are behind (the owner, 2026-10-03: "This is another type of thing that should be escalated to you mechanically"; one night review held 75 cards while five readers read 44, their widths kept from before their machines were widened): a read has sat asked and not begun on a reader up for `sprint.ReadersWindow`, 10 minutes (`sprint.ReadersBehind` in internal/sprint/readers_behind.go, one pure decision beside `Overloaded`; a reader's width is its machine row's, `Snapshot.ReaderWidth`, 0 for a reader named for no row): `the readers are behind: review <n>, reads asked and not begun past 10m0s; the readers read <k> of width <w> (<reader> reads <k> of width <w>, <a> waiting past the window[: it reads under its width, restart its loop (nova-config loop show <reader>)][: away, run: nova-sprint reader up <reader>]; ...)`, one for the sprint, updated in place every tick while it holds and closed when no read has waited the window (`TestTheTickRaisesReadersBehindWhenReadsWaitTheWindow`) | reader up <r> (a reader not up holding reads), restart <r> (it reads under its width while reads wait: the loop record of its name in nova-config), wait 10m | no |
| raise the read tier of the stream? (readtier.go: a landed card of the stream returned by dev or an audit, `promoted --returned`; two readers at the stream's read tier disagreeing on one attempt; a card alternating broken and ok across attempts; one judgment per stream, `raise the read tier of <s> to <next>? <why>`, updated in place while a cause holds and closed by the raise; section 6) | raise (`stream set <s> --read-tier <next> --reason '<why>'`, the default), keep (`ack`) | no |
| a card reached its bound (at its ceiling tier, flash first, section 5: an attempt's work card redealt MaxRedeals, 3, times after takes that ended, the provider's failures among them, and a take of it ended again; the judgment names the provider and the last error line when the provider failed that take; or the second identical failure, section 2: two takes of the card, or two attempts of its primary, failed the same way, and the judgment names the class) | rework with a fix (a new attempt, on the next tier), drop, wait (at a redeal bound, wait only when the bound was a provider failure); when the attempt before also ended at its bound on the card's tier (section 5, the bound holds across attempts): rework with a fix on a higher tier (`--tier`, when the ladder has one), drop, and wait only when the bound was a provider failure and the provider's return has not yet lifted a bound on that tier | no |
| a work card is past its deadline | fleet down (the member, only when it has held the card its own whole deadline: never the member a late card was just redealt to, nor one it was withdrawn from), wait, drop | no |
| a read card is past its deadline | ask --another, wait, drop | no |
| a stream has had no merge step past its deadline | merge --stream, card (look), wait | no |
| an invariant is broken | card (look at the card), repair, wait | no |
| a judgment has waited past its due time (overdue) | a decision of the judgment, wait | as the judgment |
| a stream has made no progress past its deadline (stale) | where, queue (look), wait | no |
| stalled: nothing holds a card (rule 12) | the decisions its place allows and that would be accepted (ask --another for a primary asked already, never ask), else look at the card; drop; wait | no, while the stall stands |
| review above its alarm, merging above its alarm, nothing ready while cards wait, the fleet works below its alarm (the backlog alarms, below) | ack (seen: quiet until the episode ends), wait | yes |

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
merge step past its deadline, an invariant is broken, a stall (one
judgment for each card nothing holds, or for the stall a chain of waiting
cards ends at; the stall judgment itself holds nothing), and the backlog alarms
(below).

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
whom and when. The ack is how a chain behind a dropped card is mended: the
waiver is written on the card (`waived`, `waived_by`, `waived_at`), and the card
moves to ready in the ack's own step when nothing else holds it (on a RUNNING
machine, at the next tick, which applies the step), so nothing down the chain is
dropped and added again; a sentinel is reached, and a held card or held sentinel
keeps its hold. The coordinator who acks owns the risk that the dependent runs
without the dropped card's change. An add counts only valid candidate IDs as proposed dependencies;
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

A primary in review is never silent. With the ok reads it needs at its head
(one reader's for a flash card, two different readers' for a pro card), some open judgment on it offers accept (ready to accept, or
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
given a selection instead (`--stream`, `--col`, `--max`, `--read-ok`) moves
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

### Backlog alarms

Four conditions of the whole sprint the tick keeps (its deadlines part plans them
with the deadlines), each off until the coordinator sets its threshold with `set`,
the work table's properties `alarm_review`, `alarm_merging`, `alarm_fleet` and `alarm_ready`
(a clear starts the next epoch with none):

- review above its alarm: more primaries in review than `--alarm-review <n>`;
- merging above its alarm: more primaries merging than `--alarm-merging <n>`;
- nothing ready while cards wait: with `--alarm-ready on`, no primary ready and one or
  more waiting;
- the fleet works below its alarm: the members up working fewer work cards than
  `--alarm-fleet <percent>` (1 to 100) of their width, while a primary is ready or
  waiting.

An alarm is an episode: one judgment of the sprint (no primaries) written when its
condition starts, never again while it stands, whatever its counts do (it is keyed by
its type), and closed once when it ends, in the same step as one happened note to the
coordinator, "an alarm cleared", whose text opens with the alarm's type and a colon
and says the count now. The judgment and that note are what `inbox --wait --push`
pushes, so an episode is pushed once when it starts and once when it ends. `ack` keeps
it quiet until the episode ends (the next is raised again); `wait --for` until that
much running time has passed, when one that still stands is raised again; `off` takes
an alarm off, and an open one clears.

### The coordinator's pass

The owner, 2026-10-05: "everything I described above needs to be mechanical, so you
have a reminder to do it (notification) coming from the machine every 10 minutes.
Otherwise, you will eventually drift and forget." The night before, one friend's
session was deaf from about midnight to 8:41 AM and finished no card while his row read
8/8 working, and reader findings and failed attempts waited on the coordinator for four
hours. The tick's overdue part runs the pass (internal/sprint coordinator_pass.go,
`TickCoordinatorPass`), three conditions the tick keeps, each a judgment:

- **a friend's session is deaf** (`a friend's session is deaf`), one on each friend not
  held whose beat carries a session pong (`friend beat --pong <RFC3339>`, her daemon's
  `last_pong`) older than 10 minutes of running time (`FriendDeafAfter`); it names her,
  her pong age and the remedy: a wake note (`nova-friend ping --to <friend> --wake`),
  then the debug steps of docs/SPEC-FRIEND.md (Presence, The harness check). A beat that
  carries no pong judges nothing: deafness is read only from the session's own answer.
  **Not yet live:** the nova-friend daemon's beat (cmd/nova-friend main.go, `beat`)
  sends only `--active` and not `--pong` yet, and nothing else in the tree sends a
  pong, so on the running machine no beat carries one and this judgment stays silent
  until the daemon passes `--pong <status last_pong>` (a follow-up card, PATHS
  cmd/nova-friend/**). Until then a deaf session shows only as the idle judgment below,
  30 minutes on, and only if she holds working cards.
- **a friend holds working cards and finishes none** (`a friend holds working cards and
  finishes none`), one on each friend not held holding working cards on her row when
  neither her last working-to-done finish (`finished` of her done cards, ok or failed) nor
  her oldest working card's take is within the friend-finish window (`set
  --friend-finish <duration|default>`, the work table's property `friend_finish`, default
  30m); it names her cards and the age of her last finish. Only her work cards count: a
  read is not card work.
- **judgments wait on the coordinator past their deadline** (`judgments wait on the
  coordinator past their deadline`), one about the sprint while any open judgment is
  overdue (its review time, else `DeadlineJudgment`, as the overdue line has it) and its
  overdue line was written at least 10 minutes of running time ago, naming them by type
  and count. The overdue line is a late judgment's first reminder, at its deadline; the
  pass is the next, 10 minutes on, and every 10 minutes after while it holds, so the
  coordinator is never pushed twice in one tick for one late judgment. The pass's own
  judgments are not counted: each is raised again on its own (and, like every judgment,
  gets its one overdue line).

Each is an episode, keyed by its type and subject: written once when its condition
starts, raised again in place every 10 minutes of running time while it holds
(`PassEvery`; the k-th raise again is due k times that after it was written, and the
judgment's `before` counts them), and closed when it stops holding. A raise again
rewrites the judgment with the latest facts and writes one happened note to the
coordinator, `a judgment still holds: raised again`, so each tick that raises one ends
with a tick-end note and `inbox --wait` wakes on it: a coordinator who missed one is
woken again. `ack` (deaf and idle list it) keeps one quiet until its episode ends, and
`wait` until its review time; a friend the coordinator holds (`friend down`, `hold`) is
judged neither deaf nor idle. The model is tla/CoordinatorPass.tla: one judgment an
episode (`OneJudgmentAnEpisode`), never a whole window unraised (`PushedEveryWindow`),
closed when it stops holding (`ClosedWhenCleared`), each with a reversed witness TLC
catches. Pinned by `TestTheMachineRemindsTheCoordinatorOfADeafOrIdleFriendEveryTenMinutes`
on the twin store with a fake clock.

### Answered by nova-decide

`answer` answers the routine judgments by the judgment decision
(docs/SPEC-NOVA-DECIDE.md section 13; the owner, 2026-10-03: "Please push Jev
wide"). The tick's rules answer the mechanical ones first (answered by rule, below):
`answer` reads what they leave. The routine kinds are the ones a coordinator's own loop answered by the
printed lines: a reader found it broken, work came back failed, a primary is
blocked on something dropped, stalled, stream stopped: conflict on a card, a
work card is past its deadline, cannot ask, ready to accept, and a card reached
its bound. Every other judgment, a sentinel reached among them, is left, and
listed `left`, its kind `other` and its type in the why. A judgment the coordinator set a wait on is the coordinator's
until it comes due, and is not read.

Each card of a judgment is decided by itself: a grouped judgment (several cards
in one note, or several notes in one group) is several decisions, each over its
own card's state, recorded under the note's id (`<note id>:<card>` for a note of
several cards). The verbs allowed are the decisions the judgment prints; the
lines applied are the lines the inbox prints for that card (a judgment of one card:
its group's lines; one card of several: the lines `inbox` prints for a group of
that card alone, the card named where a group's form would name `--group <note>
--expect 1`), with the placeholders filled from the decision: `'<fix>'` the fix's
text, `'<why nothing is to be done>'` the ack's reason. A note's own verb (`wait`,
`ack`) runs once a pass, and an ack is applied only to a note of one card.

The verb is applied when its probability is at or above the bar: `--bar`, else
nova-config's sprint row `decide_judgment_bar` (applied to `sprint:decide_judgment_bar`
and read with the routes; `routes --json` carries it). The row ships it empty (the
owner, 2026-10-03: "same rule as the other layers"): with no bar, nothing is applied;
each decision is asked and recorded (`act` listed) and its row says what a bar would
apply, so the record trains before anything acts; once a bar is set or given, a
recorded decision is applied from the record without asking again. 0.8 is a starting point measured on 100 of the coordinator's own judgments
(docs/SPEC-NOVA-DECIDE.md section 13), not an independent calibration. A drop is never
applied, whatever its probability: it is listed with the reason chosen. A card
whose judgment text or last ten log lines carry a provider's refusal for want of
payment (HTTP 402, out of credit) is never asked: it is listed, "a payment is the
owner's". A card decided before is answered from the record and asked nothing; one
applied before whose judgment is still open is listed, never applied twice; and a
card the decision reworked, or began to, within the last hour (the record's applied
and applying lines say when, a decision applied from the record among them) is
listed, not reworked again, so no loop reworks a card round and round under a dead
provider.

The record is the guard against applying a decision twice. Before the first verb of
a decision runs, the record says `applying` with the decision's op id,
`decide.<decision id>` (a `~` written `_`; `<op>.<n>` for the n-th of several lines),
on the decision's own line when it is new and as an act line when it was recorded
before; every verb carries that id as its `--op`; after them the record says
`applied` or `refused`. A pass stopped between the two leaves `applying`: the next pass
that finds the judgment open finishes the decision through the same ids, past the
bar and the hour guard it passed when it began, and a verb that ran replays its
recorded result and changes nothing. The verb's own check is not skipped: a drop, a
release, or a verb the judgment does not print is never applied, so a decision that
says `applying` on one (a record answer did not write) is that card's `refused` row
with the reason, and the record says `refused` after it. A judgment the verb answered is closed, so its decision stays
`applying`, which the hour guard counts.

One ask of the backend may take `--timeout` (60s by default; the Jev client is bounded
by it too): an ask past it, a backend error, an answer out of shape, or no key is
that card's one `failed` row, escaped to one line; nothing is applied or recorded for
it, the pass goes on to the next card, and it exits 1. A failure goes to stdout only:
the record holds decisions, and an ask that failed made none.

At the start of each pass the outcome of earlier decisions is attached from the
card's state (at most 25 a pass, newest first): landed, dropped (off the table),
or came back (another judgment open on it). Each pass prints one table, a row a
card (`judgment`, `card`, `kind`, `verb`, `p`, `act`: applied, would-apply,
listed, refused, failed or left, and `why`: the lines applied, or why it is
listed), an `OUTCOME <decision> card=<c> label=<l>` line per outcome attached, and
`ANSWER OK rows=<n> applied=<n> would_apply=<n> listed=<n> refused=<n> failed=<n>
left=<n> outcomes=<n> bar=<p, or - for none> record=<file>; run: nova-sprint inbox`; `--json` is the same as one object.
answer takes none of the shared `--op`, `--epoch` or `--max`: each verb it applies
carries its decision's own op, and a pass lists every row; given one, it is refused
as a flag answer does not define.
`--dry-run` asks the decision and prints `would-apply`, and writes neither the
sprint nor the record (`ANSWER DRY-RUN`). `--every <d>` is the coordinator seat's
loop: a pass every `<d>` until a pass finds the machine STOPPED (or DONE), then
`ANSWER STOPPED <state>: the loop ends`, exit 0. Exit 1 is a pass with an applied
line refused or a decision whose backend failed; exit 2 a usage, an actor other
than the coordinator, or a sprint that did not answer.

answer is a client of the sprint as the coordinator's shell is: every read and
every answer is a verb, sent to the server when `NOVA_SPRINT_SERVER` names one
(the server never runs answer itself), else run on the store `--redis` names.
The backend is Jev (`--backend jev`, the key from `JEV_API_KEY`, which
`nova-secrets exec --only JEV_API_KEY` sets; with no key every ask fails, naming
that command) or a fixed file (`--backend fixed --answers <file>`). The record is
`--record`, default `~/nova-sprint/decide/judgment.jsonl`: the decide layers keep their
records under one `decide/` directory (a member's reads in `<root>/decide/read.jsonl`,
the brief's in `~/nova-sprint/decide/brief.jsonl`), and answer makes that directory
0700, or tightens it to 0700 when it was made before, as the records hold the sprint's
state.

#### decision-record.w1: the coordinator's answers are recorded

With `run --decide <dir>`, the verbs that answer a judgment (accept, rework, return, drop and
ask with `--answers`, and `ack` and `wait`) append one `judgment-answer` record per judgment
and card to `<dir>/judgment-answer.jsonl` as the verb succeeds, whoever answers (the
coordinator or its inbox agent); the decide lane attaches the outcome when the card lands,
leaves the table or is bounced again (its next read broken, its next finish failed). A record
that cannot be written is a NOTE under the verb's summary line, never its failure
(docs/SPEC-NOVA-DECIDE.md section 13, the coordinator's own answers).

### Answered by rule

The machine answers the mechanical judgments itself, by rule, without the coordinator (the
owner, 2026-10-04, at 1:36 PM: "I want this sort of oh no fleet is idle, do judgement,
release more cards thing -- i want this more automated."; that afternoon 49 judgments of
these kinds were open, some 3h39m old). One pure function decides
(`sprint.RuleAnswers`, internal/sprint/rules.go): for every open judgment and subject, the
rule that answers it and its act, or `left` (it needs a mind) or `off` (its rule is turned
off), with why. The tick applies it in its end, after the checks and the deadlines and
before the overdue part, as five parts, each a step on a fresh read (`rule return`, `rule
resume`, `rule rework`, `rule late`, `rule brief`), so a judgment the end raises is
answered in its own tick, and a conflict's three moves are made in one. Each answer is a
verb the judgment's decisions name, applied as the machine; it closes the judgment with
the decided note `answered by rule <name>: <act>: <why>` (the log and the inbox's decided
list), and writes `rule_answer` (`<name>: <act> at <time>`) on the card it moved. `nova-sprint
rules` prints the same answers, read-only: one `RULE` line per judgment and subject, and
`RULES OK judgments= acting= left= off= by=<rule>_<act>=<n>,...`.

| rule | judgment | answer |
|---|---|---|
| `failed` | work came back failed (a take with no result is redealt by the machine, section 5, and reaches here as its bound) | the next attempt (rework, the report its fix) on the next route of its tier, the routes it drew left out; the `RuleAttemptCap`-th (2) failure on one tier (`rule_tier`, `rule_fails` on the primary) a new attempt one tier up (`rework --tier`: flash to pro to heavy, the first tier above that a route serves); past heavy, a friend's card (`who=friend`: the friends' deal gives it to a friend up with room) |
| `bound` | a card reached its bound (its redeal bound at its ceiling, or the second identical failure) | a new attempt one tier up, as `failed`'s climb; past heavy, a friend's card. A card every member up refused at staging, or at its brief's bound, is left |
| `late` | a work card is past its deadline | with progress in the last 10 minutes (the work card's `progress` stamp: the server's time of its holder's last `progress` verb, which the member sends every 3 minutes while its child prints and the friend daemon while a lane's turn on the card prints; a stamp from before the card's take is another holder's and counts as none) a wait of 30 minutes, once a generation (`rule_waited`). The default is wait only: a working card whose holder has stamped no progress since its take is held 30 minutes at a time and never returned by this rule, so a member that does not stamp never loses an honest long child to it (`tla/SprintRules.tla`, `NeverStampedNeverReturned`). A card whose holder stamped and then went silent past the 10 minutes, or whose one wait is spent, is returned and dealt again once its holder has had its own whole deadline (withdrawn, the take ended: it spends a redeal, so a card late again and again reaches its bound and climbs); a card just dealt again is held until its holder's own deadline. Each answer keeps a hold on the condition until the time it names, and the tick raises it again then if it still holds. A friend's card is left: a friend keeps her cards |
| `conflict` | stream stopped: conflict on a card, where the lander refused a head one of three ways (`sprint.RefusalWay`): its paths that did not merge are files no generated ledger owns (`conflict_kind=file`, `conflict_paths` on the stream's control card, from `merge --conflict-kind --conflict-path`, which land reports), it fails the lander's checks (files outside its PATHS, E12, or another check), or its merged tree fails the tree gate | in one tick: the card returned to review (`rule_redo` its attempt, `rule_refused` the way, `rule_refusal` the lander's words, `tier_now=flash`), the stream resumed, so the rest of its batch lands on the next landing, and the card reworked at flash, staged on the base's tip, with the fix `redo the same change on the current tip` (a PATHS, checks or gate refusal adds `; the lander refused attempt <n>: <its words>`). The same card refused the same way as the refusal it was last returned on is a brief defect: the card marked (`brief_defect`), the judgment's text prefixed `brief defect: `, the stream left stopped for a mind. A conflict in a ledger the lander could not resolve, one whose files the lander did not say, and a head that is no commit or that origin does not hold are left |
| `brief-defect` | a card has reached its bound: the brief is wrong, not the worker (the same finding twice, section 2) | the card marked (`brief_defect`), the judgment's text prefixed `brief defect: `, once; the judgment stays open (brief or drop) and no rule moves the card |
| `base-gate` | (no judgment: the lander's) the base fails its tree gate at its tip | land gates that base commit again after 2 minutes and again after 5 (`sprint.BaseGateRetries`), each landing in between refused with the finding and when it is gated again; the third failure stops every stream that lands on it, `stream stopped: the base fails its tree gate` (`merge --base-red`), the judgment carrying the error; a green base is cached for its commit. With the rule off, a red base is cached for its commit as before (every landing refused until the base moves) |

A failure many cards share is the fleet's, not the card's: when `RuleSameFailureCards` (3) or
more cards hold the same failure class now (in review with their work failed that way, or
ready at their redeal bound with that class), `failed` and `bound` leave each to a mind
(`the same failure on <n> cards (<class>): the fleet's, not the card's`) rather than raise
every card a tier for a failure no tier changes, a toolchain a machine cannot run or a
provider down (`TestTheSameFailureOnManyCardsIsLeftToAMind`). A reader found it broken stays a judgment: a finding needs a mind (the same finding on the
same card twice is a brief defect, above). So does every other type. `run` and `tick` answer
by rule unless `--answer-rules=false` (a `tick` by hand only with `--answer-rules`); nova-config's sprint row `answer_rules_off` (a list of
`base-gate, bound, brief-defect, conflict, failed, late`, applied to
`sprint:answer_rules_off` and read with the routes) turns single rules off
(`TestEachRuleHasAnOffSwitch`). The tests are internal/sprint/store/rule_answers_test.go,
internal/sprint/rules_conflict_test.go and cmd/nova-sprint/base_gate_rule_test.go; the model is tla/SprintRules.tla (`RuleAnswersBounded`,
`LadderClimbs`, `WaitOnce`, `BaseStopsOnThird`).

#### v11-conflict-rule-in-tick-now.w1

A refused head is the tick's, not the coordinator's (the owner: "the machine keeps itself
fed"; "a hand step is a missing instruction"). On 2026-10-04 every head that did not merge,
changed files outside its PATHS, or failed the tree gate stopped its whole stream until the
coordinator returned it, reworked it on the tip and resumed the stream: 19 streams sat
stopped and the coordinator answered them by hand every two minutes, because the lander
records a PATHS or gate refusal as a conflict with no `conflict_kind`, and the conflict rule
left every such stop to a mind. The rule now places all three (`sprint.RefusalWay`:
`conflict`, `paths`, `checks`, `gate`) and answers each in the tick that sees it: the card
returned with its way and words on it and its next attempt at flash, the stream resumed so
the rest of its batch lands, the card reworked on the base's tip with the refusal as its fix.
Only the same card refused the same way a second time (a brief defect) keeps its stream
stopped, with one judgment, prefixed `brief defect: `, for a mind. A refusal of another way
is redone again; the way compared is the one the card was last returned on.
`TestAConflictingHeadIsRedoneOnTheTipAndItsStreamKeepsLanding` drives each way on the twin
store: the stream merging again after one tick, the next card landing, the redo at flash
with its fix, and the repeat stopping the stream with the mark.

#### land-base-gate-stops-stream

The base-gate count is the store's, never only the lander's memory: a hand land starts every
run with none, and the server every start, so a base red at its tip was refused round after
round and no stream stopped (found by hand, 2026-10-04: 62 refusals of one
stream from 12:04 PM, no judgment, `where` showing it merging, `resume` refusing it as not
stopped). Each landing refused because the lander ran the base's tree gate and it was red
(not one refused inside a retry's wait, nor with the rule off) is counted through the merge
step (`MergeReq.BaseRefused`, `Base`) on the stream's control card per stream and base
(`base_gate_refused`, `base_gate_base`, `base_gate_first`; a count on another base starts again
at one). The `sprint.BaseGateStops`-th (3) refusal, or the lander's own third failure, stops
the stream with one judgment, `stream stopped: the base fails its tree gate`, its text naming
the base, the gate, the refusals and the first refusal's time, every card where it is; `resume`
then moves the stream and clears the count, as do a pass that merges and every other stop
(`TestBaseGateRefusedThreeTimesStopsTheStreamWithAJudgment`, cmd/nova-sprint/land_basegate_count_test.go).

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
6. No primary enters merging without ok read cards from as many different readers at its head as its tier needs (one for a flash card, two for a pro card; section 6).
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
the rule that every copy has its primary's score. `check` reports a pending operation: in flight while it is younger than
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
take that orphan card off (into returned) in the same step, so the rule that merge queued + stuck equal work merging holds
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

Each takes a set and is one step. A set is ids, a stream, a column, `--max n`,
or an inbox group (`--group <id>`; a group number is refused, naming the ids).
Every verb taking `--group` takes `--expect <n>`: when the group's members now
number otherwise the verb is refused, changes nothing, and names the size now
and the members added or gone; a verb given `--group` prints how many it acted
on. Each prints what moved, what did not and why,
and the summary line. A verb run on a store with no sprint in it yet (a table it reads is
not there) says `no sprint here yet: init makes its tables; run: nova-sprint init
--coordinator <name>`, never the table layer's words. Every judgment verb takes `--answers <notification>`, each an id as inbox prints it or its alias `j<n>` (the comfort list of 2026-10-03, item 10): the store's commit numbers every judgment and acknowledgement of the epoch in the order written, under the fence, keeps the alias on the note (`alias`) and in an alias index, so an alias is stable for the life of the judgment and names it after it is answered too; inbox prints it beside each judgment (`alias=j<n>`, `--json` `alias`), and `--answers`, `--group`, `wait`, `ack` and `inbox --open` take it in the id's place, read back to the id before the step (an alias naming no judgment of the epoch is refused naming it; the core sees ids alone). An `--answers` id that is no longer open because the machine answered it since the inbox was read (a tick closed it: a lateness judgment whose attempt finished, a condition that cleared) is a stale id, not a refusal: the step runs and says so on a NOTE line, `--answers <id>: the machine answered it already; nothing more to answer` (`--json` `said`); the store records who answered each judgment as it closes (the step's actor, under the fence), and the step reads that record for the ids it names before its first read of the tables (the comfort list of 2026-10-03, item 11); an id another verb answered, or one never a judgment, is refused as before, naming it, the whole step and nothing written.
Every store verb takes `--redis`, `--actor`,
`--op <id>` (the same id again, for the same verb with the same arguments, returns the recorded
result; recorded for another verb or other arguments it is a conflict and is
refused), `--json` and `--max` (`--limit` is `--max` for one release). `--actor` has no
default: it is `--actor`, else
NOVA_SPRINT_ACTOR, and a verb that writes with neither is refused. Every verb
has one class of who may run it. The coordinator's verbs (init, add, quack, release,
resolve, start, stop, ask, accept, rework, return, drop, rank, brief, move, resume, land, hold, unhold, fleet
up, fleet down, fleet level, fleet sync, friend sync, friend reconcile, friend down, friend up, friend take, friend level, friend health, reader add, reader away, reader up, reader remove, stream remove, merge-window open, wait, ack, answer, clear, teardown, repair,
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
(queue, inbox, card, needs, held, sentinels, check, where, view, dashboard, goal show, seat) need no actor, except `inbox
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
| init | creates the four tables and the view; `--readers`, `--members`, `--attempts <n>` (the sprint's attempt cap, as `set --attempts` writes it; section 2), `--coordinator` (the one actor who releases sentinels; default the actor; the seat then moves by `coordinator`), `--owner` (who may give the seat and whose name a take carries; set once, kept by a clear, removed by teardown), `--rules <file>` (the child rules file every brief is held to, one required sentence per line; its absolute path is recorded as the key `sprint:rules`, kept by a clear and removed by teardown; refused at once when the file cannot be read or holds no rule; this repository's is `fleet/child-rules.txt`); its line names every reader of the sprint (`readers=`), and on a twin a NOTE says a member it adds is up after the next tick |
| add | admits primaries into a stream: waiting if they need something, else ready; `--count n` generates ids; `--sentinel <id>`, `--before`/`--after <id>` (section 16); `--brief <text>` or `--brief-file <path>` gives the brief (the file's bytes as they are, its one trailing newline cut, the whole file read so the lint and the size refusal see all of it, a file over 1 MiB refused naming that cap and its true size; both together, or a file that cannot be read, is refused with exit 2), and every packet carries it whole; a brief is a child's whole brief, so `add` holds every brief to the card lint's child rules (`swarm.LintCardChildWith`, in process, the rules of `nova-swarm lint --card --child-rules`) and refuses one that fails with the lint's own `LINT DRIFT brief <check>: <line>: <excerpt> remedy=...` lines on stderr, exit 2, nothing written; a `--count` card and a sentinel with no brief are not linted, and an add of cards with no brief says so on a NOTE line naming `brief`, the verb that gives one, and `--rules <file>` names the rule file, read at add time, for this add (over the one `init --rules` recorded, over the built-in general rules; a file the members hold, `fleet/child-rules*.txt` of the build, is rules by reference, section 2: a brief on a repository with a held file need not carry it, and its card names that file; a file of a held name whose text is not the build's copy is refused, since the members inject theirs; a `--rules` with no brief is refused, and a file that cannot be read is refused naming its absolute path); `nova-swarm template --name card` prints a card that passes the general rules; `--brief-dir <dir>` adds one card per `*.md` file in the directory in byte order of file name, and `--brief-file <path>` given alone (no ids, `--count` or `--sentinel`) or again adds one card per named file in the order given (with either form no positional id; the card id is the file's base name without `.md`, which add says on a NOTE line under its ADD line, refused naming the file when not one; one `--brief-file` with no ids, `--count` or `--sentinel` is that form too, one card named by its file, nova-tools#5096 item 19, and with ids, `--count` or `--sentinel` it is the brief of the cards they name); each brief's `Needs:` line (the first `Needs:` header line, else the `DEPENDS-ON:` line of its typed header block; ids comma separated, text after an opening parenthesis cut, so `none` or `-` is no needs, and an `owner/repo#n` reference is no need) becomes that card's needs, a need naming no primary on the table or in this add refused naming the file and the id, `--needs <a,b>` on the line adds to every card's (a need named by both is stored once), and `--sentinel <id>` with either form admits one stop after the cards, `--before`/`--after`/`--score` applying to every card; one failing brief refuses the whole call, every failing file named with its findings, exit 2, nothing written; two cards of one many-brief add that name one file in their `PATHS:` header lines (commas or blanks between the files), neither needing the other through the add's needs, are refused, exit 2, nothing written, naming the file and the two cards, unless both briefs declare the file on a `SHARED:` header line (a ledger every card of the add touches; a pair that shares only declared files is admitted, and a shared file one of them does not declare keeps the refusal) or `--allow-shared-paths` is given (nova-tools#5096 item 17); the one-brief form reads the brief's `Needs:` or `DEPENDS-ON:` line as the cards' needs when `--needs` is not given; `--held` admits every card of the add held (section 16); under `JEV_API_KEY` add asks nova-decide's brief decision of every card it names with a brief, after its own checks and before it writes, one deadline (a minute) for the whole batch, and with a server where it is typed, sending the server each card's op (`--brief-op`, refused when typed on an add no server runs; the server takes only `<id>@brief-...`) (docs/SPEC-NOVA-DECIDE.md section 14): one `BRIEF card=<id> op=<card>@brief-<hex> p_converges= minutes= failed= uncalibrated=true recorded=` line per card (under `--json` the object's `brief` field), recorded in the coordinator's `<root>/decide/brief.jsonl` (root `~/nova-sprint`) or `--decide-record <file>`, and each card stores its op and record (`brief_op`, `brief_record`); with nova-config's sprint row `decide_brief_bar` set a card under it refuses the whole add, exit 2, nothing written (empty, the default, reports only, and stays empty while the decision is uncalibrated; a decision that cannot be made is a `NOTE brief:` line and the add goes on); land attaches `landed` (attempt 1) or `reworked` and drop attaches `dropped` to the decision the card stores, by its exact op; cards are admitted in waves (the owner, 2026-10-03: "BATCH EVERYTHING"): one positional id, `--count 1` on one stream, or one `--brief-file` alone, is refused unless `--one` says a single card is meant, exit 2, nothing written, `one card at a time is the mistake; put the briefs in a directory and run: nova-sprint add --stream <s> --brief-dir <dir>; or say --one for a single card` (`TestAddRefusesASingleCardWithoutOne`); `--brief-dir`, `--count` of two or more (or on several streams: one card each), and several `--brief-file` are unaffected; a card whose `BASE:` is dev is refused outside the promotion stream, naming the sprint branch (section 7, the sprint branch) |
| quack | cuts quack cards, the sprint's end-to-end test cards, into a running store: `quack --streams <a,b,...> --count <n> --repo <clone url> [--tiers <t,...>] [--base <branch>]` adds n cards to each stream (a stream new to the sprint is made), the tiers (default `flash,pro`) taken in turn down each stream, the base default `sprint/quack`, the test repository's sprint branch (a `--base dev` is refused outside the promotion stream as `add` refuses it, section 7, the sprint branch); each card's id is `quack-<stamp>-<stream>-<nnn>` and its brief asks for the one file `quacks/<id>.txt` holding the line `quack`, the stamp twelve hex digits drawn once per call (two passes share one with a chance of about one in 2^48, so a pass's files are new to the repository's history, which a clear does not empty), every call drawing its own; under `--op` the call is held to the caller's arguments (streams, count, tiers, repository, base), never to the cards its stamp makes, so the same call retried, or two of it overlapping, replays the store's recorded result while other arguments under that op are refused, and an op this store has no record of (after a teardown, or in another store) adds new cards under a fresh stamp; each brief is a child's whole brief (line 1 its tier, `BASE:`, `REPO:`, what to do, the known answer, the finish and the read) closing with the RULES paragraph of the rule set `add` would hold it to, and is held to the card lint as `add` holds a brief; every card of every stream is checked before anything is written (a stream long enough to make an id over 128 characters is refused, naming the stream and the length), one step adds every stream's cards or none (several streams' named cards are all or none, as one stream's are), and a refusal names every missing or bad input at once, exit 2, nothing written |
| release | lands reached sentinels, held ones (`add --held`) that wait for nothing, and sentinels not yet reached whose waits are each landed, dropped or in flight (section 16); clears the hold of a held card, which goes to ready when it waits for nothing else; the coordinator's alone, with `--reason` |
| resolve | waiting -> ready where needs have landed (the tick does it; by hand for a stuck case) |
| start, stop | set the machine RUNNING or STOPPED (section 14); `stop --reason <text> --until <time or duration>`, both wanted (`sprint.StopArgs`), names who stopped it, why, and when the tick starts it again |
| run | ticks on every line of the log (at most every 100 ms) and once a second while the log is quiet; before each tick it reads its own binary's file, and when a new build was installed under it since it began it stops (`RUN STOP the binary this loop runs was replaced ...`, exit 3) so its supervisor starts the new one: a loop never ticks the store with older code than the verbs run |
| tick | one tick by hand |
| take | a worker moves work cards fleet ready -> working; `--as <member>`, `<card>@<gen>`. The width is hard: a member's working cards never pass its fleet row's width, held here whatever is asked; a take by count is cut to the room, a take by id past it is refused; a take by count that took fewer than asked says why on a NOTE line (the member at its width, its ready queue empty, or not up); a card on a route that rests is never taken: refused by id, naming the rest, and passed over by count (the tick withdraws it) |
| finish | work cards done ok or failed; primaries to review; `--as <member>`, `<card>@<gen>`; `--usage <text>` (what the run spent) is kept on the attempt's record, timed and priced (section 2, What a card cost); `--decision <json>` (the take's attempt decision, one card a finish, its op naming that take's card and attempt, else refused) is kept on the card, recorded by the server's decide lane, and routes a failed finish when its class is no-result or nothing-to-do at or above that class's bar on the card (section 2, the attempt decision) |
| progress | a holder stamps progress on the work cards it works: `progress --as <worker> <card>[@<gen>]... --epoch <n>` sets the card's `progress` field to the server's time and nothing else; refused for a card that is not working, one another row holds (only the holder stamps), and one named at a generation that is not its live one. The member sends it every 3 minutes for each card whose child printed since its last stamp, and the friend daemon for each card whose lane turn did; a child that prints nothing stamps nothing, and the late rule (section 8) reads that silence |
| ask | deals primaries in review the reads each wants now, one at a time (the first read alone, the second of a pro card once the first came back ok; a broken first read is reworked with no second read; section 6); `--another`; `ask <primary> --instead <reader>` takes that reader's read, asked or reading, of the primary at its attempt back (`retired_by: coordinator`, a later report of it refused naming that) and asks one other reader in the same step, chosen and routed as `--another` (the owner, 2026-10-01: "get the verbs in man."); refused, nothing changed, when the primary is not in review, the reader holds no live read of it, no other reader is free, or with `--another`, `--group` or more than one primary |
| queue | a reader's read cards or a member's work cards, oldest first (`--as`; `--json` carries the worker's `width`: a member's fleet row's, a reader's its machine's, `reader-<m>`), or a stream's merge queue (`--stream`) |
| routes | each route of the store (nova-config's `route` kind) with what its attempts did: attempts, ok, failed, provider failures, mean wall from take to finish, and `rested_until`, when its rest ends while the machine rests it for children that ended with no result (section 5; `-` when it does not rest); `TIERS flash=<n> pro=<n>` first, the enabled routes per tier; `--json` (`rested_until` only while it rests) |
| stats | the epoch's pass in seconds, each as median, max and count, from one read of the work, fleet and readers tables (every primary's work and read cards of every attempt, retired ones too, in read sets; `sprint.Stats`, pure): the stages (deal wait: admitted to the first work card's `first_dealt`; finish to two reads: the last ok take's `finished` to `accepted`; accept to land; total: admitted to landed), each member's work cards (cards, failed, take wait `dealt` to `taken`, run wall the usage's `wall`, or for a friend's card, which reports no usage, `taken` to `reported` (her REPORT.md's time, which `friend sync` keeps on the card, no later than the finish; `TestStatsTimesAFriendsRunFromHerReport`), report lag `finished` - `taken` - wall), each reader's read cards (cards asked, begin wait `asked` to `begun`, run wall, report lag `read` - `begun` - wall), and each route's takes from the primaries' cost records (takes; ok: a work take finished ok or a read with its verdict; provider: provider failure or no result; failed: every other end; a launch refused at staging is no take; a read whose record names no route counts on its card's route; run wall); members, readers and routes in name order; changes nothing; `--json`. `--routes --since <time>` prints the route table from the log over that window instead (`sprint.RouteTable`): takes, ok, failed, no-result, provider failures, landed, first-take rate, wrong at review, dollars per take, dollars per landing, median wall, imputed; a finish before the window is left out, and the provider takes on that finish with it; a deal, a take, an accept and a read before the window stay, as the route, the wall and the review of a finish inside it; a provider take whose error begins `no result:` is a no-result and every other provider take is a provider failure; `actual_usd` counts when the usage writes it; an accept is the verb `accept` or `tick accept` |
| read | a reader records ok or broken with the finding; `--as <reader>`, `--begin`; `--usage <text>` (what the read spent) is kept on the read card, timed and priced (section 2, What a card cost) |
| accept | review -> merging and into merge queued; refused without the ok reads it needs (one reader for a flash card, two different readers for a pro card); named ids all or nothing, a selection moves the eligible |
| rework | delegates the next attempt at once with a fix (the member stages it at the tip of the card's base branch, the last pushed attempt's work carried on top where it applies cleanly, docs/SPEC-CARD-CONTRACT.md, where a rework starts), and writes on that attempt's work card `fix`, `finding` (its broken reads' findings, each once) and `why` (how the attempt before ended: failed with its report, finished and found broken, or sent back), and on the primary `finding_reader`, the reader whose finding it sends back, who checks the fix (section 6), each cut to MaxCardTextBytes with a trailing `...` and never refused for its size, and kept on the primary too for a rework that deals later; its packet carries them to the child's JOB.md and `card` prints per attempt; ready when no member is up; a primary at its redeal bound (ready, its work card withdrawn) is reworked too, with `--fix`, its withdrawn card taken off and its bound's class written as the primary's record of its failed work; `--tier`: at a redeal bound it never names a lower tier, and when the attempt before also ended at its bound on the card's tier the rework is refused unless it names a tier above (flash, pro, heavy, frontier) or the provider its takes failed on is back, which lifts it once per tier per card; the refusal is one line naming that attempt, the class and the tiers above (section 5, the bound holds across attempts); without `--fix` each primary's fix is the finding of its broken read, else the report of its failed work, and a primary with neither is refused by name; `--tier <flash|pro|heavy|frontier>` writes the tier on the primary (`tier`), and this attempt's deal and every later deal and read of the card draw from it over its brief's line 1, so a flash card that failed twice is reworked on pro, the same card and brief; it pins the card: the tier is its ceiling too and the machine never escalates it (section 5, flash first); a word that names no tier is usage (exit 2), and a card whose brief pins a model is refused by name, since it runs on its pin whatever its tier; one id while the inbox holds a judgment group of several naming it is refused unless `--one`: `the inbox holds a group of <n> for this card; answer the group; run: nova-sprint rework --group <id> --expect <n>; or say --one` (`TestReworkAndDropRefuseOneCardOfAGroupWithoutOne`; the group is read from the open notes alone, `store.OpenGroups`, never the whole inbox: `TestOpenGroupsAreTheInboxsJudgmentGroupsFromTheOpenNotesAlone`) |
| return | merging -> review, off the merge queue |
| redo | atomically executes return, rework with fix "redo the same change on the current tip", and resumes the stopped stream in one step with one history line; refused when the card is not in a conflict |
| drop | off the table with the reason; one id while the inbox holds a judgment group of several naming it is refused unless `--one`, as rework's is |
| rank | changes a score and every copy: `--score <n>` (the first id's; the rest follow it), `--first` (ahead of every primary), or `--before <id>` (in line in front of a primary of the cards' own stream, in the order named, placed as `add --before` places cards: between the card before the anchor and the anchor, the line never renumbered; a card of another stream, the anchor itself, or no primary refuses the whole step, nothing written) |
| brief | replaces the brief of a primary that has not started (the owner, 2026-10-01: "What other things should you be able to do to mutate a stopped sprint" / "Are there other verbs you need as you work with sprints?" / "I don't want you manually hopping in and working around it and doing manual stuff."; 2026-10-04: "what is manual? what needs new verbs in nova-sprint?"): `brief <id> (--brief <text> \| --brief-file <path>) [--rules <file>]`, or `brief --dir <dir> [--rules <file>]`; the new brief is held to the card lint and the size bound as `add --brief` holds one (the same function, refused exit 2, nothing written, with the lint's own lines); on a RUNNING machine as on a STOPPED one, since only a card dealt or in flight holds its brief: on a RUNNING machine the change queues for the next tick's pump like every coordinator verb's, and the pump deals no card a queued change names before the change drains, so the card is dealt with its new brief; refused (exit 1, nothing written) for a card that is no primary or has started: only a primary waiting or ready with no work card ever dealt (attempt 0) takes one, a card dealt, working, in review, merging or landed keeps its brief, its state named, and is refused so whatever the machine's state, with what changes it instead: from review `rework <id> --fix`, the next attempt's change (from merging after a `return`); from any open state a `drop` and the new brief added as a new card; once landed, a new card. `--dir` replaces one brief per `*.md` file of the directory, in byte order of file name, read as `add --brief-dir` reads them, the card the file's base name without `.md` (refused naming the file when not one; not with an id, `--brief` or `--brief-file`); every brief is held to the bound and the lint before anything is written, one failing file refusing the whole call naming it with its findings, exit 2; one step replaces them all or none, a card refused or named twice refusing the call, exit 1, and its `BRIEF OK moved=<n>` is the count replaced, one MOVED line per card. The card keeps its id, stream, score and needs; before this verb the coordinator dropped the card and added it again, which changed its id and place (`sprint.Brief`). A brief that differs from the card's in its `DEPENDS-ON:` line alone is taken in any state, on a RUNNING machine (applied by the next tick, as every work-table change is while it runs) and for a card dealt (it applies to the next attempt), and the card's needs become the line's, read as `add` reads it: re-pointing a card's needs after a drop is no change of its task (the comfort list of 2026-10-03, item 2); each need is a primary on the table and not the card itself, and a ready card takes no need that has not landed (the deal would run it first), each refused by name, nothing written; the brief decision and the grade stay (`sprint.Brief`, `briefDepends`); `brief <id> --tier <flash\|pro\|heavy\|frontier>` re-tiers the card instead (the owner, 2026-10-04: "If there are pro cards that are really heavy, then let's mark them as heavy"): the tier is pinned on the primary as `rework --tier` pins it (`tier`: every later deal and read draws from it, never escalated past it), taken in any state, on a RUNNING machine and for a card dealt, where it applies to the next attempt; refused for a card landed, a sentinel, a brief that pins a model, a word that is no class, and the tier it is pinned to already; not with --brief, --brief-file, --dir or --rules (`sprint.Brief`, `briefTier`) |
| move | moves primaries that have not started to another stream (the owner, 2026-10-01: "What other things should you be able to do to mutate a stopped sprint" / "Are there other verbs you need as you work with sprints?" / "I don't want you manually hopping in and working around it and doing manual stuff."): `move <id>... --stream <s> [--before <id> \| --after <id> \| --score <n>]`, one step, all or none for the ids named; refused (exit 1, nothing written) on a RUNNING machine (`nova-sprint stop` first), for a card that is no primary or has started (only a primary waiting or ready with no work card ever dealt moves; a card dealt, working, in review, merging or landed keeps its stream, its state named), and for a card of the destination already (`rank` changes a place in line). The destination is placed exactly as `add` places cards (the same plan, on the sprint without the moved cards): a stream new to the sprint is made as `add --stream` makes one, the cards go in line by `--before`/`--after`/`--score`, else at the end in the order named, waiting or ready by their needs and the stream's sentinels, a reached sentinel behind them no longer reached, a cycle of needs refused naming it, and a ready card the destination would put behind a sentinel refused by the lifecycle (ready -> waiting is only the effect of inserting a sentinel; `--before` the sentinel moves it). The card is the same card moved: its id, brief, needs and admission stay, and a need naming it still holds (a need is by id) (`sprint.MoveCards`) |
| merge | one mechanical merge step for a stream, a store write: the record of a landing by name, `--landed <id>@<head>... --repo <dir> --base-ref <ref>` (each card merging in the stream at that head, the head an ancestor of the base tip, else all refused and nothing written); `--batch n` selects the batch a fact is about; `--red [--suspect <id>...]` |
| land | the coordinator's landing step as one command, an external delivery (a git push) and a store write (the merge step): for each stream named (`--stream`, again for more; default every stream with cards queued and not stopped), in stream order, the merge queue up to its first stuck card, in work order, cut into batches of consecutive cards whose briefs name one repository and one base (`REPO:` and `BASE:`, read as staging reads them; `--base` for a card naming none); each batch's heads merged `--no-ff` with the message `land <id> (sprint stream <s>)` onto a branch cut from the base's tip on origin, in a clone (`--repo-dir`, else a clone kept under the directory each line names, its name the readable repository and a hash of it; every clone reused has its origin's fetch URL and its one push URL held to the repository the cards name before any git, the host compared without case and the path with it); a caller's `--epoch` the sprint has left refused before any git; `--check <command>` run once per batch in the clone before the push; the queue head, its heads and attempts, and the epoch read again just before each push; the push plain, never forced, and on a rejection the base fetched and the batch rebuilt on its new tip once; then the batch reported by the merge step `merge --stream s --batch n` runs, fenced to the epoch land read and guarded in the same store step to plan only while the queue still starts with the batch's cards at the heads and attempts land read and pushed (a rework keeps a card's id and epoch, not its head); the pins are the step's arguments, so an `--op` replay returns only that batch's receipt. A head that is not a commit on origin or whose merge stops on unmerged paths (unless every one is a generated ledger, which land regenerates, or a shrink-only ledger, which land resolves as the union of both sides' removals, section 7) ends its batch before it, the cards before it land, and it is reported with `--conflict` and git's words as the note; git failing for any other reason (an identity, a hook, the disk, the network) blames no card: nothing is pushed or reported and the batch is refused; a head whose merged tree fails the tree gate (`go build ./...`, `go vet ./...`, and the tree's own test packages when it changes a document or a test file; section 7) ends its batch before it as a conflict, the run's output the note, and a base whose tip fails it refuses the batch before any merge; a check that fails, with `--red`, nothing pushed; a second rejected push, with `--rejected`. A push that landed and a report that did not (a clear, a card accepted ahead of the batch, a return, between the two) is `LAND FAILED`, exit 2, and the one remedy named is to run land again, which rereads the queue and lets its own checks decide: a card as it was is recorded with no new push (its merges and push are no-ops), a card reworked since is merged at its new head or meets a real conflict, and after a clear there is nothing to report (tla/Land.tla). A bare `merge --batch n` is never offered: after a rework the queue starts with the same ids at a head the base does not hold, and the merge step alone would record it. One line per batch, `LAND OK|REFUSED|FAILED stream= cards= base= tip= ids=<first>..<last>` (a batch whose git ran also says each step's seconds, `fetch= merge= check= queue= push= report=`, and its `--json` item `times`),
 then `LAND DONE batches= cards= refused=`; `--dry-run` reads the store only and changes nothing, and refuses what land refuses before its git, in land's words: a batch whose card names no base (and no `--base`) or no repository (and no `--repo-dir`) is refused, land and dry run alike, naming every problem at once, each cause on its own line with its one next command (the
first on the `LAND REFUSED` line, each other on a `NOTE` line and in the `--json` item's
`also`: each head of the batch that is not a commit id, with its return), and on a twin,
which has no git, a `NOTE` that `merge --stream <s> --batch <n>` records the landing in
land's place; a head that is not a commit id stops the dry run where land stops, the cards before it a batch, that card refused with the conflict fact land would record and nothing recorded; `--json`. A batch landed and reported tags the branches its cards' work cards of every attempt record (`branches_queued=<n>` on its line, `prune` on its item; never the base, an empty name, an option-like name or one not under `sprint/`, each said on a NOTE and counted as `branches_kept=<n>`), and the cleanup deletes only canonical successful-attempt branches from origin later, many in one push, each with an explicit lease against its recorded head; advanced or recreated tips, unowned branches and all recorded stream bases stay on origin, and a retry keeps the original lease; then removes the clone's remote-tracking refs of branches origin no longer holds, never while a landing builds or pushes: the one-shot land once after every stream, the land loop (`run --land`) between rounds when a round finds nothing queued or 256 branches wait, a line per clone `PRUNE OK|FAILED branches= refs= dir= took=` (`--json` `prune`); a failed cleanup fails no landing, and the loop keeps its branches and tries again after a minute; the queue is the process's memory, so a crash or a stop loses it and those branches stay on origin; a dry run queues and deletes nothing and says how many it would queue |
| promote | the machine's promotion, on a schedule, of a frozen branch cut from the sprint tip (`promo/<YYYY-MM-DD>-<n>`, `git branch --no-track` at that commit), never the live sprint branch: a queued pull request whose head is the live branch blocks the lander's pushes (GH006, found 2026-10-04). `--every <duration>` (default 1h) is the clock; `--landings <n>` also promotes once that many `land <id> (sprint stream <s>)` commits have landed since the last cut, looked for once a minute until the clock elapses; `--branch` (default the checkout's branch), `--repo-dir`, `--base` (default dev), `--check <command>` the tree gate run on the frozen commit before anything is pushed. The pull request body is the landed card ids since `refs/promoted/last`, else since the base, oldest first. Admission is the `enqueuePullRequest` mutation, which carries no merge strategy (the queue refuses one; `gh pr merge` and a flag named auto are the spelling the class test refuses), then a query confirms `mergeQueueEntry`. A merge prints `promoted --sha <sha>` and moves `refs/promoted/last`. A failed merge-group run raises one judgment, the failing check's log tail, decisions `fix-and-recut` and `skip`, and does not record the sha; a later pass of the same branch does not raise a second one. `--dry-run` prints the branch and the cards and cuts nothing. The land loop calls the same step only when promotion is armed; `run --land` does not arm it. The step is `(*promoter).step` (cmd/nova-sprint/promote.go), which cites this section |
| resume | a stopped stream moves again, with what was done; refused while a cause is unresolved |
| fleet | `up|down <member>`, `level` (down is `hold <member> --return` in the old words, for one release); `up <member> --deadline <duration|default>` pins the deadline every card dealt to the member gets, or takes the pin off (section 5, the deadline by machine); down and up say on the member's MOVED line where its cards went (nova-tools#5096 item 21): down `moved=N to m2(n),m3(n); stayed=K withdrawn: <primaries>` (a card no member up has room for, or at its redeal bound, is withdrawn), up `moved=N to <member>(n) from m2(n),...` when the level moves cards onto it; a member going down in the tick's presence part says the same |
| hold | `hold <name>... --reason <text> [--return]`: the coordinator's hold of fleet members, readers, friends and streams, one verb for the four (the owner, 2026-10-04 1:50 PM: "there should be a hold verb in nova-sprint"; 1:51 PM: the same for friends, hold and unhold). Each name is a fleet member, a reader, a friend (nova-config's friend rows) or a stream, resolved first: a name of none, or of more than one, refuses the whole call, exit 1, nothing written; a hold wants `--reason` (exit 2 without one). One step (`sprint.HoldNames`). A name held takes no new cards: a member is dealt none and its takes are refused, a reader is asked nothing, a friend is dealt none (her cards wait ready), a stream's ready primaries are dealt to no machine and no friend. What is dealt and not begun is handed back now: a member's ready cards are dealt round the fleet as a member going down sends them, a reader's reads asked and not begun are asked of another at the next tick, a stream's work cards ready on members are withdrawn (no redeal spent). What is begun finishes (the default): a member's working cards stay on it (the sweep leaves them, section 5; the deadline judges them), a reader's reads begun stay with it, a friend keeps her cards, a stream's working cards finish. `--return` hands the work begun back now too: a member's working cards dealt round the fleet (a redeal counted, as fleet down always did), a reader's reads asked or begun taken back where a reader up is free to read them, a friend's cards and a stream's working cards withdrawn to ready. Its status reads `held`: the fleet and friends tables' status, the readers' state, the merge table's state cell (and the stream clocks' state; a stopped stream reads `stopped`); the reason is on the member's and the stream's control card (`held_reason`) and in the reader's and friend's hold records, and `where --json --cards` (the dashboard's read) carries every hold as `holds` (kind, name, reason, by, at, return). Each name held writes a happened note (`held by the coordinator`), the reason in it, so the log holds every hold; handover shows it among the decisions. `fleet down <member>` is `hold <member> --return`, `reader away <reader>...` is `hold <reader>... --return`, and `friend down <friend>` holds her as `hold <friend>` does and takes back the cards she has not started (`--until` and her started cards' NOTE lines are its own), each with its old words' output, kept for one release with their help naming the pair; a member down because it stopped beating is the machine's `down`, never a hold |
| unhold | `unhold <name>... [--reason <text>]`: releases the holds of the names (resolved as hold resolves them, all or none), the reason in its note (`released from a hold`): a member that beats is up at once and is dealt again, otherwise down until it beats; a reader's and a friend's state is then their beat's; a stream's primaries are dealt again. `reader up` and `friend up` are its old words, for one release; `fleet up` still releases a member's hold and adds a member or sets a width |
| merge-window open | `merge-window open --for <duration> --reason <text>`: landing pauses from now for the duration, the reason on every batch it pauses (section 7, the lander's pause); a store write of the merge table's properties `merge_window_until` and `merge_window_reason`, replacing a window open before; the coordinator's; refused whole, nothing written, for a duration that is none or not above zero, no reason, a reason over 8 KiB, or another actor |
| friend sync | the friends table's rows made nova-config's friend rows (section 1); `--every <d>` loops as the seat each pass, and `friend sync install --every <d>` / `friend sync uninstall` put that loop in place as this machine's service ("Handing over the seat") |
| friend reconcile | a friend's own account of her cards, `<friend>-working/inbox/QUEUE.json`, and her outbox compared with the cards working on her row, each collected, kept or returned to ready (section 1, friend reconcile): `friend reconcile <friend> [--root <dir>] [--dry-run]`; never run by the server |
| friend clean | the retention rule of the friends' working directories (docs/FRIENDS.md; ideas#833), run nightly from a loop row on the machine that holds them, never by the server and never on the store: `friend clean [--pg <dsn>] [--root <dir>] [--days <n>] [--dry-run]`. For each friend row of nova-config (as `friend sync` reads them; the coordinator is one), `<root>/<friend>-working` (`--root`, else HOME); a friend with no directory there is said and skipped. A job is `inbox/<job>/` or `jobs/<job>/`, done when `outbox/<job>/REPORT.md` is a regular file, its age that file's. Inside a done job at least `--days` old (default 3) a clone (a directory holding `.git`) is removed when `git status --porcelain` is empty, it holds no stash and no commit of `HEAD` or a local branch is missing from every remote-tracking ref (`git log HEAD --branches --not --remotes`, no network); build output (`node_modules`, `target`, `gocache`, `gocache-*`, `.gocache`, `go-build`, `wt-*`) that is no clone is removed. A clone that fails the check, or whose git fails, is dirty: listed each run, `FRIENDS-CLEAN DIRTY friend= path= age=<d>d why=`, and removed once its job is 14 days old whatever its state, its line saying `dirty=<why>`. Nothing else is touched: the brief and any text of the job, `outbox/`, every file outside `inbox/` and `jobs/`; a link is never followed; a job under `jobs/` that is itself a clone is one target, one under `inbox/` is never removed (a NOTE). Every removal is `safepath.RemoveUnderRoots` under the job's directory. The friend's one build cache, `<friend>-working/.cache/go-build`, is held under 20 GiB by the member's trim (`internal/gocache`). `--dry-run` says `WOULD-REMOVE` in place of `REMOVED` with the bytes and removes nothing. Lines `FRIENDS-CLEAN REMOVED\|WOULD-REMOVE friend= path= bytes= age=<d>d kind=clone\|build[ dirty=<why>]`, `FRIENDS-CLEAN CACHE ...`, `FRIENDS-CLEAN FRIEND <f> dir= jobs= done= freed= listed=` (or `absent`), `FRIENDS-CLEAN FAILED friend= path=: <why>`, and last `FRIENDS-CLEAN OK freed=<bytes> listed=<n>` (a dry run adds `dry-run: nothing was removed`), or `FRIENDS-CLEAN INCOMPLETE ... failed=<n>`, exit 1; a config that cannot be read or holds no friend row, exit 3, nothing removed; `--json` one object with the lines |
| friend beat | a friend's beat, `friend beat <friend> [--working <n>] [--queue <n>] [--width <n>] [--running <id>,...] [--load <percent>] [--active <RFC3339>]`, run by its own machinery every second; through the sprint's server it is `friend beat <friend>` and its report's flags, each once with its value, and nothing more |
| friend down, friend up | `hold <friend>` and `unhold <friend>` in the old words, for one release: hold a friend (status `held`, whatever she beats or the coordinator observes; every card dealt to her she has not started goes back to ready and a card she has started finishes; `--reason <text>` and `--until <RFC3339>` shown in her status cell) and release the hold (not a beat: `down` until she beats or is observed up; `--width <n>` sets her width) |
| friend take | take back cards dealt to a friend that she has not started (`<id>...` or `--all-unstarted`), each back to ready for the friends' deal |
| friend level | even the ready queues of the friends up, idle lanes first and by the card's tier, as fleet level evens the members'; the tick runs it too (friend-deal-idle-lanes-first.w1) |
| friend health | the coordinator's observation of a friend, `friend health <friend> --state up\|asleep\|down --seen <RFC3339> --generation <n> [--queue <n>] [--working <n>] [--width <n>] [--reason <text>] [--until <RFC3339>]`, written by the coordinator's daemon from its keepalive (section 1, a friend's health): the seat's holder alone, at the seat's generation now, with a proof newer than the row's; refused otherwise with nothing written; the same observation again is the recorded answer; `friend health <friend> --clear [--dry-run]` removes her observation, so her status falls back to her beat rule |
| seat | the seat as the daemons read it every second: `SEAT holder= epoch= generation=`, `--json`; three keys, no table (section 1, a friend's health) |
| lane take | `lane take go --machine <m> --as <worker> [--wait <duration>]`: a worker's take of one of the machine's Go lanes (section 18); exit 0 granted, exit 1 queued with its place and the next command; --wait asks again every 5 s until granted or the wait is over; through the sprint's server it is `lane take <kind> --machine <m> --as <worker>` and nothing more |
| lane give | `lane give go --machine <m> --as <worker>`: the worker's lane, or its place in the queue, given back; the head of the queue is granted |
| lane list | every machine's holders and queue of each lane kind, the timeouts applied; where --json --cards carries the same rows as `lanes` |
| reader add | declares readers |
| reader away | `hold <reader>... --return` in the old words, for one release: holds readers whatever they beat (state `held`): no read is asked of them, a read asked and not begun is asked of another at the next tick, and a read begun is taken back where a reader up is free to read it |
| reader up | `unhold <reader>...` in the old words, for one release: releases the hold; the reader's state is then its beat's |
| reader remove | takes readers off the readers table; refused (exit 1, nothing written) when a named reader is no row or holds a read card, asked, reading, ok or broken, naming the reader and its read cards |
| reader retire | retires readers and keeps their history (the comfort list of 2026-10-03, item 6: `reader remove` refuses a reader that holds read cards, so the second readers could not be retired without losing the record): each named reader, a row of the readers table, is held away for good, its state `retired`: no read is asked of it and a read asked and not begun is asked of another at the next tick (as `reader away`); a read it is reading is taken back at the next tick and asked of a reader up with no card at that attempt, and it stays when none can take it, the few-readers judgment names it no more, its own `queue --as` writes no beat and answers `reader: false` (its loop stops as for a name with no row), while its row and its read cards stay on the table, counted on their cards and in `where`; `reader up` brings it back; a named reader with no row refuses the whole call, nothing written; `--dry-run` prints `READER-RETIRE DRY-RUN readers=<names>; nothing was changed` and writes nothing; `reader remove` is as it was |
| stream set | `stream set <s>... [--read-tier <flash|pro|heavy|default>] [--land-protected <owner/name,...|any|default>] [--release <name>] [--attempts <n|default>] [--reason <why>] [--answers <note>]`: `--attempts` sets the streams' attempt cap, their control cards' `attempts`, over the sprint's (section 2); `--reason` records why the read tier is set as `read_tier_reason`, and `--answers` answers the judgment `raise the read tier of the stream?`; the read tier of the streams named, their control cards' `read_tier`, over the sprint's (`set`), and their protected-branch mark, `land_protected` (section 7, the protected branches); `default` takes a stream's off; `--release <name>` records the release on each stream's control card, `default` or `none` takes it off; the coordinator's; refused whole, nothing written, for a stream that is no row, a tier that is not flash, pro or heavy, a mark that names no repository, or another actor |
| set | `set [--read-tier <flash|pro|heavy|default>] [--dealt-max <duration|default>] [--go-lanes <n|default>] [--alarm-review <n|off>] [--alarm-merging <n|off>] [--alarm-fleet <percent|off>] [--alarm-ready <on|off>] [--friend-idle <duration|default>] [--attempts <n|default>]`: also `friend_idle` (how long a friend holding cards may show no file write before it is an alarm, default 20 minutes) and `attempts` (the attempt cap: how many attempts one brief may run before the card is the coordinator's as a brief defect; default 4; section 2); the sprint's settings, the work table's properties `read_tier` (every card's reads raised to it, never lowered), `dealt_max` (how long a work card may wait dealt and never taken before it is a judgment; default 3 times the take deadline, 6 hours), `go_lanes` (the Go lanes of every machine, section 18; default 1) and the backlog alarms' thresholds `alarm_review`, `alarm_merging`, `alarm_fleet` and `alarm_ready` (section 8, off by default); the coordinator's; refused whole, nothing written, for a tier that is not flash, pro or heavy, a bound that is not a duration above zero, a lane count under 1, a threshold its alarm does not take, nothing to set, or another actor; a clear starts the next epoch with none of them |
| stream remove | takes streams off the work and merge tables (the owner, 2026-10-01: "remove work streams a/b/c" / "you should have a verb to remove work streams" / "they should only succeed on a STOPPED sprint machine"): each stream's row of both tables, with the stream's control card, the one card `add` made for it, which the merge row's delete takes off the table (its record kept); refused (exit 1, nothing written) on a RUNNING machine (`nova-sprint stop` first), for a stream that is no row of either table, named, and for a stream that holds a card (a primary or a sentinel placed in any column of its work row, landed included, or a merge card in its merge row), naming how many of each and the remedy (`nova-sprint clear --confirm sprint`, or `drop`); all or none for the streams named. A clear keeps the streams and does not bring a removed one back. The table layer never places a removed member again within an epoch, so `add --stream <s>` of a stream removed in this epoch is refused, naming the clear, and adds it fresh after the next clear (`sprint.StreamRemove`, `sprint.RemovedStream`) |
| ci | records a CI observation for primaries in any state |
| wait | sets a judgment's next review time |
| ack | closes a judgment the coordinator looked at, with the reason |
| answer | each card of each routine judgment answered by the judgment decision, the verb chosen applied at or above `decide_judgment_bar` and the rest listed, every decision recorded with its outcome ("Answered by nova-decide", section 8); `--dry-run`, `--bar <p>`, `--every <d>` (until STOPPED), `--backend jev\|fixed`, `--answers <file>`, `--record <file>`, `--timeout <d>` (one ask, 60s); each verb applied carries the decision's `--op`, recorded applying before and applied after; run where typed, its verbs sent to the server |
| inbox | every open judgment and the notifications since the cursor, grouped, judgment first, each judgment with its alias (`alias=j<n>`, section 11); `--open <id>` (or the alias), `--read`; `--json` carries `judgments` (each with `id`, `kind`, `type`, `what`, `stream`, `size`, `cards` whole, `notes`, and `answers`: every decision with the exact command lines that make it, in order), `happened` (the notifications since the cursor, grouped), `done` (the machine has stopped because the sprint is done) and `groups`, every group in the order the text prints; `--wait --timeout <d>` blocks until the inbox holds a judgment, or a note addressed to the coordinator, that was not in it when the wait began (by note id: a held or waited judgment is open already and never wakes it; the owner, 2026-10-02: "push notifications for inbox from nova-sprint so she doesn't have to poll"), or the machine stops having run, or `<d>` passes; it sleeps on the tick-end notes and looks at the inbox at each one and every 15 s while nothing ticks; then it says how it ended on one line (`inbox --wait: new=<group id,...>`, `inbox --wait: the machine stopped`, `inbox --wait: nothing new in <d>`) and shows the inbox; `--json` carries `woke` and `new` (the new groups' ids), the line only for a wait that found nothing, on stderr (stdout stays one JSON object); `--wait --push <dir>` keeps running until it is interrupted: each new judgment and note to the coordinator is written once as `<dir>/<note id>.md` (the group as `inbox --open` prints it, then `clock: <RFC3339>`; a `:` in a read-time group's id becomes `-`), said as `INBOX OK pushed=<id> file=<path>` (`--json`: one object a file), and the files there are its cursor: a note with a file is never written again, so a restart pushes nothing twice and misses nothing; a timeout is quiet and the loop goes on, the machine stopping is its line and the loop goes on; a local write, into the coordinator's own inbox directory; `--push seat` is the holder's inbox, and follows the seat ("Handing over the seat"); `--push` takes no `--read`, `--open` or `--at-epoch`; `--json` carries `coordinator`, the seat's holder |
| card | one primary's story, told from the log: for a card in flight, first what holds it now (each open judgment with the commands that answer it, or the actor and its deadline); its place in its stream's line; its brief, and the fix its attempt was given; its timeline in local time, an attempt at a time ("attempt 2, because attempt 1 failed"), one line per event a person would name (two readers asked, a merge and its batch, a step and its answer are one line each), a finish and a read with the first line of their words; the reports, findings and fixes whole as paragraphs; a card that has ended says so in one line; a COST line per consumer that ended and the COST TOTAL (section 2, What a card cost); the CARD OK line ends with the tier it is on and its ceiling (`tier=<t> ceiling=<t>`, section 5, flash first); `--fields` prints every field of the primary and its cards instead; `--brief` prints the brief alone, nothing before or after it, so a child gets a brief's text out in one call (a card with no brief is refused, exit 1, naming `brief`; not with `--fields`; `--json` is one object, `id` and `brief`); `--json` carries both, the timeline's events with the log lines each tells, and the cost |
| needs | the dependency graph of the waiting cards, a read (needs no actor, writes nothing): for each stream (`--stream <s>`, else every stream) its waiting cards in chain order (a card after every card it needs; within a depth, work order), one line a card naming its unmet needs and each need's state (a column name, `dropped` for a kept record whose outcome is dropped, `absent` for no record at all), the roots marked `ROOT` (the waiting cards none of whose unmet needs is itself waiting), each card's depth (a root is 0; a card is one more than its deepest waiting need), a `WIDTH` line a stream giving the width at each depth (`depth 0: n, depth 1: n, ...`), and a line a stream and one for the sprint counting the cards whose needs name a dropped or absent id; it reads the work table once with the off-table needs of the waiting cards read too, and every need's state comes from the same read the card view uses; a cycle through needs is printed as such, never followed; `--roots` prints only the roots and the width lines; `--json` one object with the same facts |
| held | the held cards and what each waits on, a read (needs no actor, writes nothing): every waiting primary admitted held (`add --held`, until `release`) or behind a sentinel by its place in line (the cards `where`'s held count counts), one line a card, `HELD <id> stream= held=yes|- behind=<sentinel,...|-> needs=<needs not landed|->`, then `HELD OK cards= held= behind=`, from one read of the work table; `--stream <s>` keeps one stream's; `--json` one object, `cards` (the comfort list of 2026-10-03, item 1) |
| sentinels | the sentinels on the table and what each gates, a read (needs no actor, writes nothing): every sentinel not landed, one line each, `SENTINEL <id> stream= reached=yes|- behind=<n> needs=<needs not landed|->`, behind the waiting cards its release lets go (section 16), then `SENTINELS OK sentinels=`, from one read of the work table; `--stream <s>` keeps one stream's; `--json` one object, `sentinels` |
| view | the role views, reads for a model: `view coordinator` (what needs the seat, ranked by the cards behind each, each with its command) and `view worker --as <member\|friend>` (its cards, its next step, its results not landed); `--json` (schema 1), `--since <cursor>`, `--all` (below, "Role views") |
| queue --as, take | a member's or a reader's cards (a reader's `queue --as` is its beat), each with its packet: what it is handed so that it needs no other read to learn its task (the card, its epoch and generation, the brief, this attempt's fix, the notes on it, for a rework the finding of the read that found the attempt before broken and why that attempt ended (the work card's own words: the primary's are written at the next tick's drain, after a member may have taken the card), and for a work card the branch to work on, `sprint/<card>.g<gen>.e<epoch>` (the epoch makes it one per epoch, a card id coming back after a clear, and the generation one per launch, a card dealt again within an epoch, withdrawn from a member or redealt after a staging or provider failure, being another launch whose push must not meet the first's), and the one to start from, the attempt before's branch for a rework, with `base_head`, the head that attempt finished ok at, the work a rework carries (docs/SPEC-CARD-CONTRACT.md: never a branch name alone, which may never have reached origin): a rework is staged at the tip of its base branch on origin when the member stages it, with that head's work carried on top as one commit where it applies cleanly, and the bare tip where it does not, its JOB.md then saying the work must be redone; the finish counts the child's commits from that staged commit, and its report says it (`stage: staged=<sha> tip=<sha> of <base> carry=<carried|held|conflict|none>`); for a read card the work it reads: the worker, its head, branch and base, and the worker's report), and the command that reports it (a work card's names `--head <commit>`: a finish without `--head` records the card's id as its head, which `land` refuses as not a commit id); `queue --as <w> --packets <n> [--have <id,...>]` hands only the packets the worker asks for: the first n cards it may start (asked, ready) and every card in flight (reading, working), each not named in `--have`; every other card is listed with its id, column, attempt and gen, and the answer's epoch, which are its claim, and no packet (a reader of width 8 holding 150 asked reads with 2.5 KB briefs: 445,525 bytes without the flag, 51,623 asking for 8; a recorded fleet load test measured 579,181 bytes a pass); without `--packets` every card carries its packet; take prints the packets of the cards it took, `--json` as `packets`; finish takes `--branch` and `--base`, which the work card keeps and the reader's packet and card show; a fleet member (`nova-swarm member`) pushes the child's commit to origin's `sprint/<card>.g<gen>.e<epoch>` before its finish, so the finish's `--head` is the pushed sha the merge queue carries and the merge reads the work from origin; a finish is ok only with the result's shape, its verdict ok and a pushed commit, and every other is a `--failed` finish naming no head and no branch, its report starting with the reason (`no RESULT.md shape`, `nothing to do: <why>`, `verdict <word>`, `no commit: <why>`, `push refused: <git's line>`), so it opens the failed-work judgment and never goes to review with nothing to read (docs/SPEC-CARD-CONTRACT.md section 4) |
| log | the epoch's log, every line in order: --card (a primary with its work, read and merge cards; a set move the card is in is printed as the card's own line with the set's size, `(in a set of n)`, never the first card's words with the rest listed; `--json` keeps the set line whole), --stream, --member, --since, --at-epoch, --json (section 17); a line's words are printed under it, a brief by its size and the card that shows it (`card <id>`), never whole (`--json` carries it) |
| check, repair | section 9 and section 10 |
| seat check | the machinery under the sprint: server, store, loop, beats, readers, dashboard, installed versions, merge queue; prints one line per check and an exit code (0 if all OK, 1 if any check is DOWN); `machinery` is an alias (The seat check, below) |
| where | the view, once or `--watch` (redrawn in place, section 1): work, friends and fleet; `--json --cards` also carries `merging`, every merging primary with its stream, head and attempt, the queue with heads in one call; `--json --rows` carries every primary's row of the work table in one call (`rows`: id, stream, state, score and its fields but the brief, in work order; `card <id> --brief` prints the brief), so a child reads every card at once and never loops `card` calls (the comfort list of 2026-10-03, item 8; `--cards`, the dashboard's, stays bounded by the fleet's width); `--all` draws the readers and merge tables too (hidden from the default frame; the owner, 2026-10-02: "please hide the reader and merge tables"); `--release [<name>]` prints count of cards left per release from stream rows (`RELEASE <name> cards=<n>`, `cards=0` when no streams match); its title line names the seat's holder (`SPRINT TABLE  coordinator friend-b`, with `(taken 5:21 PM)` after a take until the next handover is given); `--json` carries every table and the pending operation, the stalled streams, `streams` (each stream row with its `release`), `releases` (cards left per release), the people, the coordinator and `seat`, its last change |
| coordinator | moves the seat: `coordinator <name> --reason <text>`, given by its holder or the owner; `--take --approved-by <owner>`, taken by `<name>` itself; prints the handover after (below) |
| handover | what the next seat needs, from the store, in one screen (below); `--json` |
| seat install, seat uninstall | the seat's push loop as a service of this machine ("Handing over the seat"): `seat install` writes it and loads it, `seat uninstall` unloads it and removes its file; `--dir`, `--dry-run`, `--json`; run where they are typed, never by the server |
| play | plays the world outside the table through these verbs, seeded (section 12); refused while no machine is running |
| goal | `set`, `show`, `drop`: each person's goal and route, pushed by the tick (section 15) |
| selftest land | lands a canned card on a scratch clone with this binary; green on a good binary, red on a broken lander |
| server switch | `<binary> [--rollback]`: switches the server binary on disk, keeping the previous binary; with `--rollback`, rolls back if a land fails within the window; `--rollback` alone restores the previous binary |
| clear | stops the sprint and clears all work in it: a new epoch (section 13); `--confirm sprint` |
| teardown | drops the tables, the view and every key of the sprint, of every epoch; `--confirm sprint` |
| selftest | the install gate, one verb (`selftest [--dir <d>] [--keep]`, the machine's, needing no actor): a build installed for a fleet is gated by running the binary itself before it lands anything, because a build of 2026-10-04 refused every landing for nine minutes ("the base dev fails the tree gate": go build said "package os is not in std") while its unit tests were green, and the install was gated by hand with the help's walkthrough. It makes a fresh directory (under `--dir`, else the system's temporary directory), a bare repository origin.git standing for the forge and a clone work whose base commit holds a go.mod (module selftest, the go directive of nova-sprint's go.mod, from the toolchain that built the binary) and a main.go importing fmt and os, so the lander's tree gate (section 7) really builds, then runs the walkthrough's card flow (realSteps) in process on a twin file in that directory — init with two readers and one member, one card, start, ticks, take, a commit pushed to the card's branch, finish with `--head`, a read ok, `land --repo-dir work --base main`, a tick — and checks origin's main holds the landing. It prints one line: `SELFTEST OK landed=1 land=<duration> gate=<duration> go=<go version> dir=<d>` at exit 0 (land the landing step took, gate the whole selftest, go the toolchain that built the binary), or `SELFTEST FAILED step=<name> why=<one line> dir=<d>` at exit 1, naming the step and its own reason — the lander's for a red gate, so the gate's go output is printed ("package os is not in std" would be). The directory is removed unless `--keep` or a failure (a failure keeps it and names it); a removal that cannot run is said on a NOTE line and keeps the exit. It opens no store of the caller's, no Redis and no network |

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

### Promotion

`nova-sprint promote [--every <duration>] [--landings <n>] [--branch <name>] [--repo-dir <clone>] [--base <branch>] [--check <command>] [--dry-run]` cuts a frozen branch `promo/<YYYY-MM-DD>-<n>` from the sprint tip and opens its pull request to dev (section 11, promote). The pull request head is that branch, never the live sprint branch. The tree gate runs on the frozen commit before the push, and the only ref pushed is `refs/heads/<promo>`. The body lists the card ids of `land <id> (sprint stream <s>)` commits since the last promotion. Admission to the merge queue is the `enqueuePullRequest` mutation with no strategy flag; a GraphQL query of `mergeQueueEntry` confirms the entry. The class test refuses `gh pr merge` and a flag named auto, so this verb does not spell either: the mutation is the admission that test names. When the pull request merges, the verb prints `promoted --sha <40-hex>` and records it at `refs/promoted/last`. A merge-group run that fails raises one judgment, with the failing check's log tail and the decisions `fix-and-recut` and `skip`, and does not record the sha. The verb repeats every `--every` (default 1h). `--landings <n>` also cuts once that many cards have landed since the last cut, and until the clock elapses the verb looks once a minute. `--dry-run` is one pass and changes nothing. `run --land` does not arm this step: the land loop calls it only when `promoteArmed` is set, so a server does not open a pull request from its working directory. The step is `(*promoter).step` in cmd/nova-sprint/promote.go.

### store-snapshot-verb

`nova-sprint snapshot --dir <d> [--keep <n>] [--every <duration>]` (the coordinator's) asks the store for a snapshot: BGSAVE, waited for until LASTSAVE moves with the save reported ok, then the RDB the server wrote is copied into `<d>` as `snapshot-<UTC time>.rdb` with `<name>.sha256` beside it. The copy is read back and checked against its SHA-256, loaded into a twin, and its counts compared with the store's at the save; a snapshot that fails either check is removed and the older ones stay, so the directory holds only verified snapshots. Only after a newer snapshot verifies is the directory pruned to the newest `--keep` (default 7). With `--every` the verb takes one now and another each interval until interrupted; a failed take is reported on stderr and the loop goes on. `nova-sprint snapshot --restore-drill <file>` checks the file against its checksum, loads it into a twin and prints its counts; it opens no store. The logic is `store.Snapshotter` and `store.RestoreDrill` (internal/sprint/store/snapshot.go), with the source and the twin as interfaces so the drill test (`TestSnapshotRestoreDrill`) runs on a fake source and a twin, with an injected clock and no socket. The RDB twin (`store.RDBTwin`) checks the REDIS header, the version and the CRC-64 trailer; it reports keys and cards as unknown, which are not compared, so a card-level load of the RDB is owed. The RDB is copied from the path CONFIG GET names, so the verb runs on the store's host.

### sprint-backup-verb

`nova-sprint backup --file <path>` (the machine's, like `snapshot`) is the sprint backup as one verb, where it was a hand procedure run by a child. It asks the store for its bytes (the same source as `snapshot`: BGSAVE and the RDB the server wrote on a Redis, the document of a `mem:` twin), writes them to `<path>` (a file that does not exist: an existing file is refused, never overwritten; written whole or not at all, owner-only), reads the file back against its SHA-256, restores it into a twin (`store.RDBTwin` on a Redis, `store.MemTwin` on a twin store) and compares: the restored counts with the store's, and on a twin store that the restore loses nothing the document holds (the restored store's document, restored and written once more, is the same document). It then scans the file line by line for secrets with the rules of `log.Redact`, the rules that keep a secret out of a log. A file that fails any step is removed, so the path holds only a verified, secret-free backup, and the failure names the lines (never the value) and what to do. The success line is `BACKUP OK file=<path> sha256=<hex> bytes=<n> keys=<n> cards=<n> restored=twin compared=<document+counts|counts> secrets=none`. The verb reads the store and writes only the file; it uses no clock and no socket of its own, so its test (`TestBackupWritesRestoresComparesAndScansATwinStore`, with `TestBackupRefusesAndRemovesAFileHoldingASecret`, `TestBackupRefusesWhatItCannotDoSafely` and `TestBackupFailsAndRemovesTheFileWhenTheRestoreDiffers`) runs on a twin store. The logic is `runBackup` (cmd/nova-sprint/backup.go). On a Redis the RDB is copied from the path CONFIG GET names, so the verb runs on the store's host, and a card-level load of the RDB is owed as for `snapshot`.

### preflight

`nova-sprint preflight --brief-dir <dir> [--json] [--repo-dir <dir>]` reads a
batch of briefs together, before any of them is added, and writes nothing: no
card, no field, no store key changes. One brief is each `*.md` file in the
directory in byte order of file name, its card id the file's base name without
`.md`, read as `add --brief-dir` reads it (`decide.CardFilePaths`).

A brief is held to the same card lint `add` holds it to (the same
`lintBriefReads`: the rules of the sprint's recorded `--rules` file, else the
built-in general rules). Every `DEPENDS-ON` id is a primary of the work table or
a card of the directory, and none of them is dropped. No `PATHS` glob of a brief
overlaps a `PATHS` glob of a ready, working or review card or of another brief
of the batch, as `hygiene.MatchGlob` matches one glob against a path, so a broad
`docs/*.md` overlaps a file below it that the glob matches. `TEST` names a test
`git grep` finds at `BASE` in `--repo-dir`, and `BASE` exists there; with no
`--repo-dir` both are unchecked. It prints one `PREFLIGHT <id> FAIL <check>:
<why>` line per defect, `PREFLIGHT <id> OK` for a clean brief, and a last line
`PREFLIGHT OK briefs=<n>` or `PREFLIGHT FAIL briefs=<n> failed=<m>`, exit 0 when
every brief is clean and 1 otherwise. `--json` prints one object.

### Handing over the seat

The holder (or the owner, `init --owner`, else NOVA_SPRINT_OWNER) gives the seat: `coordinator <name> --reason <text>`; with the holder away, `<name>` takes it with the owner's name, `coordinator <name> --take --approved-by <owner> --reason <text>`, refused without that name or with another (the owner, 2026-10-02: "you can be given coordinator status, or you can take it (with my permission only)").
Either is one commit of the coordinator, the seat's record (with the seat's next generation: section 1, a friend's health) and a happened note, its log line `seat: <from> -> <to>: <reason>, by <actor>` or `seat TAKEN: <from> -> <to>, approved by <owner>: <reason>`; a take's note is addressed to the old holder; every coordinator verb then takes the new name and refuses the old.
`handover` prints the holder and since when, the machine and the progress, each stream's counts, the sentinels held with what waits behind each, every open judgment with its answer lines, the members held or down and by whom, the routes disabled, the last ten decisions (release, drop, fleet down, rework with a fix, seat) with their reasons, and the lines the next seat runs first; `coordinator` prints it after the change, the leaving seat's receipt.
`inbox --wait --push seat` writes to the holder's inbox, `~/<holder>-working/inbox/sprint-judgments/` (refused when `~/<holder>-working/inbox` is not there), reads the holder at every look, and after a seat change pushes every open judgment into the new holder's inbox; a note addressed to someone with an inbox there goes to theirs. It is installed as a loop record kept alive on the coordinator machine (`nova-config loop add seat-push`, docs/FRIENDS.md, "The coordinator's loops").
`seat install` runs that loop as the tool's own service of the machine it is typed on (the owner, 2026-10-04: what is manual needs a verb): a launchd agent, `nova-sprint.seat-push`, in `~/Library/LaunchAgents` on macOS (its lines to `~/Library/Logs/nova-sprint-seat-push.log`, or `--log`), a systemd user unit, `nova-sprint-seat-push.service`, in `~/.config/systemd/user` on Linux (its lines in the journal), or `--dir`; it runs this binary by its absolute path as `inbox --wait --push seat` on the store the verb was given (`--redis <addr>`, or the sprint's server, NOVA_SPRINT_SERVER, carried in the unit's environment, never a secret), kept alive and started at login. The unit is written whole and renamed into place, kept as it is when its text is the same, and loaded either way (launchctl bootstrap after a bootout; systemctl --user enable and restart), so a second install is the first again; `SEAT INSTALL OK unit=<path> written=<bool> loaded=true`. It is refused on another OS, for the in-memory twin (no machine to wait on), and with no store named; a unit that does not load stays written and the verb fails naming the load. `seat uninstall` unloads it and removes the file (`removed=false` when there is none). `--dry-run` prints the unit and writes and loads nothing. The loop's push over nova-bus to the seat, and the backlog alarms it would carry, are not in it yet.
`friend sync install --every <d>` runs the friend sync loop, `friend sync --every <d>`, as the tool's own service of the machine it is typed on, the way `seat install` runs the push loop (the owner, 2026-10-05: "We need to get away from these one shot shell scripts"; it replaces the zsh `while` loop that read the seat with `where --json | jq .coordinator` and ran `friend sync --actor <seat>`): a launchd agent, `nova-sprint.friend-sync`, in `~/Library/LaunchAgents` on macOS (its lines to `~/Library/Logs/nova-sprint-friend-sync.log`, or `--log`), a systemd user unit, `nova-sprint-friend-sync.service`, in `~/.config/systemd/user` on Linux, or `--dir`. The unit runs this binary by its absolute path as `friend sync --every <d> --redis <addr>` with `--pg <dsn>` (`--pg`, else NOVA_PG_DSN) and `--root <dir>` when given, and carries the variables that name the stores' passwords and the bus as they were set where it was typed (NOVA_PG_PASSWORD_ENV, NOVA_SPRINT_REDIS_USER, NOVA_SPRINT_REDIS_PASSWORD_ENV, NOVA_BUS_REDIS, NOVA_BUS_REDIS_USER, NOVA_BUS_REDIS_PASSWORD_ENV), never a password: the passwords they name are the service's environment to give. It names no actor: each pass reads the seat and acts as it (`sprint.FriendSyncUnit`; a pass under a stale actor is refused a coordinator's verb), says `FRIEND-SYNC FAILING` once when it starts failing and `FRIEND-SYNC OK again after <n> failing passes` once when it is ok (docs/FRIENDS.md, "The friend sync loop"). Written, kept and loaded as `seat install`'s unit is (`FRIEND-SYNC INSTALL OK unit=<path> written=<bool> loaded=true`); refused with no `--every`, with `--actor`, with no Redis store (friend sync is never the server's), for the in-memory twin, for a `--pg` that carries a password (quoted nowhere), and on another OS. `friend sync uninstall` unloads it and removes the file (`removed=false` when there is none); `--dry-run` and `--json` as `seat install`'s. Both are words of `friend sync` and run where they are typed (`TestFriendSyncEveryFollowsTheSeatAndSaysEachChangeOnce`).
The next seat runs first: `nova-sprint where`, `nova-sprint inbox --wait --push seat`, then reads this section.
The handover's first rule, printed above the lines it runs first (`RULE ...`; the owner, 2026-10-03: "BATCH EVERYTHING"): cards are admitted and released in waves of at least the fleet's width: add takes a directory, release names a wave, rework and drop answer a group; a single-card verb outside a judgment is the sign of doing it wrong.

#### seat-key-follows-record.w5

The coordinator key follows the seat's record (found by hand: the server's unit kept `NOVA_SPRINT_ACTOR=<other>` after the seat went to a new holder, approved by the owner; each restart left the key saying the other actor while the record said the holder, the holder's coordinator verbs were refused and the inbox stalled until the key was set by hand). Only init and the seat's own steps write the key, and from the record: init writes the record's holder when the seat has a record, else the first coordinator (`--coordinator`, else its actor) (`store.InitSeat`); `coordinator` writes the key with the record in one commit; the run loop never writes it, whatever actor it runs as. The run loop records the actor it runs as (`store.SetServerActor`: the server's record, written before its first tick and every 30 s, for 2 minutes on Redis; read as the server's while no older than that). `seat` goes on to print `record=<the record's holder> server=<the server's actor or ->` once the seat has a record or a server is recorded, and when the key, the record and the server's actor disagree (`sprint.SeatDrift`: the key is not the record's holder, or the server runs as a named actor other than the holder; the server running as `machine` is no drift) it ends the line `DRIFT <why, with the line to run or change>` and exits 1 (`--json`: `record`, `server`, `drift`, `"status":"drift"`, `"exit":1`). `seat --repair --reason <text>` (the record's holder or the owner; an actor is required) writes the key from the record (`store.SeatRepairStep`, `sprint.RepairSeat`: the record written back unchanged, fenced on its generation, with a happened note, its log line `seat: repaired: the key said <key>, the record <holder>: <reason>, by <actor>`), prints `SEAT REPAIRED key=<holder> was=<key> by=<actor>`, and is refused with nothing written with no reason, by anyone else, with no record (init's key is its record), or when the key already says what the record does. `handover` prints `SERVER <the server's NOVA_SPRINT_ACTOR line to change>` under its first line when the server runs as a named actor other than the holder (`--json` `server`: `actor`, `change`). Test: `internal/sprint/store/seat_key_test.go`, `TestServerStartLeavesTheSeatKeyAndSeatRepairRestoresIt`.

#### fsck-seat-agreement-r.w2

fsck's check `seat-agreement` holds four values to one coordinator: the store's coordinator key, the seat record's holder, the actor the running server was started with (the server's record, written by the run loop that serves, `store.SetServerActor`) and the nova-config sprint row's `coordinator` (found by hand: a server's unit kept an old actor across a handover and each restart left the key naming it against the record, and `nova-config apply` moved the seat because the sprint row still named the previous coordinator). The holder is the record's, else the key's while the seat has no record; a value that names no one (no record, no fresh server's record, an empty row) and a server running as `machine` are no disagreement. The key, the record and the server are read as `seat` reads them (`store.SeatCheck`, `sprint.SeatDrift`), the row with nova-config's library at `--pg` (else NOVA_PG_DSN; an injected reader, `sprint.ConfigCoordinator`) (`sprint.CheckSeatAgreement`, `sprint.FsckSeat`). The check names each fix and runs none: `nova-sprint seat --repair --reason <text>` for the key, the server's `NOVA_SPRINT_ACTOR` line and a restart for the server, `nova-config sprint set --coordinator <holder>` for the row. It prints `FSCK OK check=seat-agreement key=<k> record=<r> server=<s> config=<c>` and exits 0 when they agree, `FSCK DRIFT check=seat-agreement key=<k> record=<r> server=<s> config=<c> <each disagreement with its fix>` and exits 1 when they do not, and exits 2 with `fsck FAILED: the nova-config sprint row was not read: <error>` when the row could not be read (`--json`: `checks` with `check`, `key`, `record`, `server`, `config`, `holder`, `drift`). The command's entry is `cmdFsckSeat` (`cmd/nova-sprint/fsck_seat.go`); the `fsck` verb's row is the fsck verb's own (fsck-held-without-beat), which this check joins. Tests: `internal/sprint/fsck_seat_test.go`, `TestFsckFindsTheSeatKeyAgainstTheRecord`; `cmd/nova-sprint/fsck_seat_test.go`, `TestFsckNamesTheFourSeatValues`.

### Role views

The owner, 2026-10-04: "i'd rather you hit this vs. hitting my dashboard which is for human
eyes", and "it will save $$$ if the data is served to you better" (ideas#852).
A role view is one document of what one role must act on now, for a model that reads it every
few minutes. Both are reads (no actor, nothing written), `--json` one object on one line (`<`,
`>` and `&` unescaped), else a short text whose first line is the summary. Each JSON document
carries `"view"` (its role) and `"schema": 1`; a change that renames or removes a field or
changes what one means is a new schema. They cost few tokens by shape: short keys, only the
items that need action (every row with `--all`), counts where a count is enough, the summary
first, and `--since <cursor>`.

`nova-sprint view coordinator [--all] [--since <cursor>] [--json]` is everything that needs
the seat, from one read of the work, merge and fleet tables, the inbox, the friends' rows, the
machines' beats and the machine's record, at one epoch. Its fields: `sum` (one line: the seat,
the machine, the open judgments and the heaviest's cards behind, the count of each item type,
landed of all and landed in the last 30 minutes, the work table's counts, the up machines'
cards working of their width), `at`, `epoch`, `seat`, `cursor`, `n` (the counts: `landed`,
`l30`, `all`, `wait`, `ready`, `work`, `review`, `merge` of the primaries, sentinels aside;
`held`; `width` and `busy`, the up machines' width and their cards working; `j`, the open
judgments) and `items`, ranked by the cards behind each (`b`), then by type in the order
below, then oldest first. An item is `k` (its key, stable while it stands), `t` (its type, one
letter), `w` (what kind), `b`, `n` (the cards a judgment names), `age`, `od` (overdue), `d` (a
judgment's decisions, `|` separated), `s` (one line, at most 160 bytes) and `next`, the exact
command that acts on it:

| t | item | next |
|---|---|---|
| j | an open judgment of the inbox, `k` `j:<group id>`, `b` its cards behind (weight.go) | its first decision's command lines joined by ` && `; longer than 300 bytes (a group's `--answers`), `nova-sprint inbox --open <id>` |
| r | the notes addressed to the coordinator (`Note.To`), one item a type, `k` `r:<type>`, `n` how many, `s` the newest's words, `age` the oldest's | the newest's hint when it is a command, else `nova-sprint inbox --read` (which moves the cursor past them) |
| a | an alarm, an effect on the cards: `a:stopped`, the machine STOPPED with cards not landed (by whom, why); `a:idle`, the up machines working under half their width while the machine runs; `a:dry`, nothing ready while cards wait; `a:review` and `a:merging`, a result waiting there 30 minutes or more (the count, the oldest's age and stream); `a:stopped:<stream>`, a stream stopped with no judgment open on it (its cause) | `nova-sprint start`; the release of the first sentinel reached, else of the first sentinel, else `nova-sprint needs --roots` (or `where --all` with ready at width); `nova-sprint ask --stream <s>`, `nova-sprint land --stream <s>`; `nova-sprint resume --stream <s>` (`--did` for a red branch) |
| s | a sentinel reached (its needs have landed) with no judgment open on it, `b` the cards behind it | `nova-sprint release <id> --reason '...'` |
| f | a friend holding cards who is down, has never reported, or has not reported for 15 minutes | `nova-sprint friend take <name> --all-unstarted --reason '...'` |
| m | a machine with a width that is down and not held | `nova-sprint log --member <m> --since 1h` |

A silent run loop, failing ticks and a stalled stream are the inbox's judgments already and
come as `j` items. With `--all`, `rows` carries every machine's and friend's row: `k`
(`m:<machine>`, `f:<friend>`), `st`, `r` and `w` (ready and working on the row), `wd` (its
width), `f30` (finished in the last 30 minutes) and `rep` (since its last beat, or `never`).
The text form prints `VIEW coordinator <sum>`, then an item a line (`<T> <b> <age> <k>: <s> ->
<next>`), at most 20 lines with a `+<n> more` line when there are more, then `cursor=`.

`nova-sprint view worker --as <member|friend> [--since <cursor>] [--json]` is one worker's
cards, from one read of the cards dealt and not finished, their packets, and the cards it
finished ok. Its fields: `sum`, `at`, `epoch`, `as`, `kind` (`member` or `friend`), `cursor`,
`next` (the next step of its first card: a member's `take` or `finish` line with its gen and
epoch; a friend's brief to start, or the push and the REPORT.md that finish her working card),
`cards` (working first, then in the order dealt: `id`, `p` its primary, `st`, `brief` (a
friend's `~/<name>-working/inbox/<job>/BRIEF.md`, a member's `nova-sprint card <id> --brief`),
`base` and `paths` (the brief's BASE: and PATHS:), `dl` (when its deadline falls by the clock),
`att`, `gen`, `br` (its branch), `notes` (the coordinator's words on it: why the attempt
exists, the reader's finding, the fix, its notes) and `j` (the kinds of the judgments open on
it)), and `wait`, its results not landed: each card it finished ok whose primary waits in
review or merging (`id`, `p`, `st`, `age`), at most 20, with `nwait` the count when there are
more. A name that is no fleet member and no friend is refused, exit 1, naming `queue --as` for
a reader. The sprint records no lanes and no note addressed to a worker: a friend's lanes are
the bus's.

`--since <cursor>`: each view prints a cursor, `1.` and the base64url of three bytes an item,
row, card or result it showed (the low 24 bits of FNV-1a of its key and of what makes it another thing to act on:
a judgment's weight, size, decisions and command; a note's words; a sentinel's cards behind; an
alarm's or a friend's or a machine's standing and command; a row's counts; a card whole; never
an age). Given back, the view leaves out every item whose digest the cursor holds, and says how
many (`same`) and how many of the cursor's digests no item has now (`gone`: answered, landed,
cleared, or changed and shown again). The cursor is the reader's: the sprint keeps nothing
between reads. A cursor of another shape is refused, exit 2.

The sprint's server serves both read-only (section 14, the server): `GET
/api/view/coordinator[?all=1][&since=<cursor>]` and `GET
/api/view/worker?as=<name>[&since=<cursor>]`, the verb's JSON as it prints it.

#### view-coordinator-needs.w1

`nova-sprint view coordinator --needs [--json]` lists every decision waiting on the
coordinator, ranked by the cards blocked behind each, and takes neither `--all` nor
`--since`. It is `sprint.NeedsRank` (internal/sprint/needs_rank.go) over one read of the work
and merge tables and the open judgments at one epoch: no git, no other read. A need is one
of: an open judgment (`judgment`, one need a judgment however many cards it names, `id` the
judgment's id); a sentinel reached or held that waits for its release (`sentinel`); a stopped
stream with no judgment open on it (`stream`, `type` its cause); a card admitted held that
waits for its release (`held`). A sentinel, card or stream an open judgment names has its
need in that judgment only.

`behind` is the cards not landed that wait on the need's cards, transitively through needs and
stream order (the waits of a card: its unmet needs and the sentinel before it, as `WaitsFor`
reads them); the need's own cards and sentinels are not counted. The list is ordered by
`behind`, then by age (the older first: a judgment's since it was raised, a sentinel's since
reached, a held card's since held, a stream's since it stopped), then by kind in the order
above, then by id. Each need carries `cards` (the cards it is about), `age_ns`, `what` and
`paths`: the evidence paths the judgment's words name (a word with a slash in it, or ending
in `.md`, `.json`, `.txt` or `.log`), reports and findings. The JSON is `view`
`coordinator-needs`, `schema` 1, `at`, `epoch`, `total` and `needs`; the text is
`VIEW coordinator --needs total=<n>`, then a line a need (`<behind> behind <age> <kind> <id>:
cards <ids>; evidence <paths>`), at most 20 lines with a `+<n> more` line.

#### coordinator-wake-verb.w1

`nova-sprint watch --wake [--every <d>] [--state <file>] [thresholds]` is the coordinator's wake as a
verb. It blocks until the first of what the coordinator would otherwise look at by hand, prints
one line, `WAKE <kind> <RFC3339 time> <evidence>`, and exits 0; the session runs it again. It
looks every `--every` (20 s) at the store and at the coordinator's bus stream (`NOVA_BUS_REDIS`;
unset, bus messages do not wake and stderr says so). A look that is due more than one wake gives
the first of these, and the others wake the next runs:

- `bus`: a message to the coordinator that is not the coordinator's own. By its `kind` when it has
  one: `request`, `blocker` and `report` wake, `status` and `ack` do not. With no kind, by its
  subject: no ping, pong, `card <id> dealt`, `card <id> finished`, land or landed, `width <n>`,
  `RESULT:`, `HOLD`, `DONE`, `ACK` or acknowledged notice.
- `judgment`: a judgment open now that no judgment wake has named, at most one wake in
  `--judgment-every` (20 m); one held back by that limit wakes when it ends.
- `stop`: the machine reads STOPPED (not DONE) and the coordinator's own stop verb did not record
  it (the machine record's `who` is not the seat's holder, or the machine stopped itself), once per stop.
- `friend`: a friend with status `down` at two looks in a row, once per time she goes down; a friend
  held on purpose (`friend down`, status `held`) never counts.
- `merge`: merging over `--merge-over` (30), or merging with no land pass for `--land-after` (15 m);
  at most one wake in `--merge-every` (10 m).
- `backlog`: the fleet's working cards under half the width of the members up, review over
  `--review-over` (40), merging over `--merging-over` (60), or no card ready with cards waiting; at
  most one wake in `--backlog-every` (30 m). `merge` and `backlog` are silent while the machine is
  stopped by the coordinator's stop verb.
- `check`: `--check` (10 m) since the last wake of any kind.

`--state` names the file that keeps the cursors between runs (default: one a store, under the user
cache directory): the bus entry id last consumed, the judgments seen, the stop woken, each friend's
count of looks down and whether she was told, and the time of the last wake of each kind. It is
written whole, by rename, before the line is printed. The first run, with no file, starts from now:
the bus from the current instant, the judgments open now seen, the check counted from now. So an
event that arrives between two runs is woken by the second, and none is woken twice. A look that
fails is tried again; five in a row end the verb (exit 2). An interrupt before a wake ends it (exit
1) and the state keeps its cursors. The model is `tla/CoordinatorWake.tla` (`AtMostOnce`, `NoLapse`,
and two reversed witnesses: a run that does not keep its cursor wakes an event twice, one that
keeps it past events not woken loses them); the code is `cmd/nova-sprint/watchwake.go`. It is a
read of the store and a write of the state file, never run by the sprint's server.

### The seat check

(the owner, 2026-10-04 1:52 PM: "what is manual needs a verb: nova-sprint seat check").

`nova-sprint seat check` (alias `machinery`) measures the machinery under the sprint:
server, store, loop, beats (fleet and friends), readers, dashboard, installed versions, merge queue.
It prints one line per check and an exit code: 0 if all OK, 1 if any check is DOWN.
Each line has the format `MACHINERY <thing> OK|DOWN <facts> [remedy="<command>"]`.
A summary line `MACHINERY OK n=<total>` or `MACHINERY DOWN n=<down> of=<total>` concludes the output.
`--json` prints one JSON object with `at`, `lines`, `down`, `exit_code`, and `measures`.

### The seat's store login

(the owner, 2026-10-05: "We need to get away from these one shot shell scripts"; card
seat-store-login-built-in.w1, which replaces the shell wrapper every store verb went through.)

The coordinator's login to the store is a setting of nova-sprint itself, never a wrapper that runs
it under `nova-secrets exec` with the `NOVA_SPRINT_REDIS*` variables set:

```
nova-sprint seat login --store <secrets dir> --as <seat> --key <keyfile> --secret <NAME> --user <redis user> --redis <addr> [--sops <path>]
nova-sprint seat login --check
nova-sprint seat logout
```

`seat login` records the address, the ACL user and where the user's password is in nova-secrets
(store, seat, key, sops, the secret's NAME; never the password) in the per-user config file
`$XDG_CONFIG_HOME/nova-sprint/login.json`, else `~/.config/nova-sprint/login.json`, mode 0600,
written whole by a rename. The paths are recorded absolute and `--sops` left off is the `sops` on
`PATH`. A login is recorded only when its secret resolves; the twin (`mem:<file>`) takes none.

Every verb after it opens the store as follows. The address is `--redis`, else
`NOVA_SPRINT_REDIS`, else `NOVA_REDIS_ADDR`, else the recorded address. The login is the
environment's when `NOVA_SPRINT_REDIS_USER` is set (that user, the password in the variable
`NOVA_SPRINT_REDIS_PASSWORD_ENV` names), which wins; else the recorded user, when the store opened
is the recorded address (an explicit other address is never sent the recorded password); else the
store's default user. The recorded password is read in the verb's own process through
`secrets.ReadLogin` (internal/secrets/login.go), which takes the path `nova-secrets exec` takes
(`OpenSeatFile`: the store's invariants, the key's mode, the sops version, then the decrypt), and
is handed to the connection through the getenv redisconn reads it by. It is never printed, never
written, and never in the process's environment, so no child of the verb inherits it. A recorded
secret that does not resolve, or a login file that is not a whole login, is a refusal naming the
file and the remedy (`nova-sprint seat login --check`, then `seat login` again or `seat logout`),
never a login without a password. `seat login --check` prints the recorded login, which login wins
when the environment names one (`wins=env:NOVA_SPRINT_REDIS_USER`, `redis-wins=env:<addr>`), and
`resolves=yes|no` (exit 1 on no); `seat logout` removes the file (`was=recorded|none`). The code is
`cmd/nova-sprint/storelogin.go`; `TestABareVerbOpensTheStoreWithTheSeatLoginFromSecrets` measures
it on the in-memory store with a fake secrets reader.

### bases-view-r.w2

Cards sat on a base nobody watched (2026-10-04: a stream of cards on the coordinator's own branch, its gate red from 12:04 PM), so what the coordinator looked up by hand is a verb. `nova-sprint bases [--json]` (read) prints one row per base a card not landed or dropped names (a placed primary, not a sentinel, its brief's `BASE:` line), keyed by base and `REPO:`, in base order: `BASES <base> repo=<repo> cards=<n> waiting=<n> ready=<n> working=<n> review=<n> merging=<n> ahead=<n> behind=<n> gate=<green|red|-> gated=<RFC3339|->`, then a `NOTE` line per thing not known, then `BASES OK bases=<n> cards=<n>`; `--json` is one object (`bases`, each with its card ids by state; `cards`; `notes`). Ahead and behind are counted against `origin/dev` by `git rev-list --left-right --count` in land's kept clone of the repository (section 7, the clone land keeps under its root), after one fetch of dev and every base named per clone a call; when that fetch fails, one `ls-remote` finds the bases origin does not hold (each a `NOTE`, its counts `-`) and the rest are fetched again. bases clones nothing: a repository land keeps no clone of has its counts `-` and a `NOTE`. The gate is the lander's last record at the base's tip, read from the store: green when a card on the base landed (land gates the tip before it merges), red at the stop of a stream stopped on its base gate (section 8's base-gate rule, its third failure, `cause=base`: its `since`, for the base of each card it holds merging; a refusal before the third is the lander's memory, not the store's); the latest record wins, red on a tie, and `-` when the base was never gated. `add` refuses a card whose `BASE:` is a personal branch, `<name>/*` for the sprint's coordinator, its owner (`init --owner`) or any row of the friends table, naming every such base and `--allow-personal-base`, with nothing written; with the flag it is admitted. The names are read from the store, never written in the code. The code is `cmd/nova-sprint/bases.go` (`basesInUse`, `basesAhead`, `holdBase`, `personalNames`); the test is `TestBasesListsEveryBaseInUseAndAddRefusesAPersonalOne`.

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
pending operation, or abandons it at the epoch it started at, then advances the epoch
once, atomically, recording when and the shape to restore. It deletes nothing.
At the new epoch every table is empty with the same rows (streams, readers,
members), every stream waiting, every member with its status and no work
counted; these are written at the new epoch in the same verb, and a clear cut
before they are is finished by the next clear. The old epoch stays where it is
and readable (`where`, `card` and `inbox --at-epoch <n>`), and every writer
still holding it is refused as stale: nothing of that epoch lands in the
new one. Operation ids and notification ids carry their epoch (`~<n>` after
the first): a caller's operation id recorded at an earlier epoch is refused,
naming the epoch, and never run again as new work; `ack` and `wait` of a
judgment of another epoch are refused, naming it; and a step given
`--answers` naming a judgment of another epoch is refused whole: nothing
moves, and the refusal names the id's epoch and when the sprint was cleared.
Every command builds its store pinned to the sprint's epoch, so no verb after
a clear touches the epoch before the advance; a writer caught mid-step by a clear is told the
sprint was cleared. clear reads the shape it restores after the advance, and a
restore the last clear owes is performed first by the next step that reads
the sprint. The machine's records (its state, its STOPPED spans, the heartbeat)
and the coordinator are the sprint's, not the epoch's: a clear keeps them. A
tick in flight at a clear holds the epoch of its read: its next part is refused
as stale, writes nothing, and the tick stops there; `run` goes on at the new
epoch, where the machine is STOPPED until `start`. clear prints the epoch
before and after, what the epoch before the advance held as counts, the machine's state
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
and nothing after the word but a late tick or a STOPPED machine's why (below): when the state is RUNNING and
nothing has ticked for 15 s (MachineSilence), on a store a run loop ticks, it
says `machine: running (tick late 16s)`, the whole seconds since the last
tick; a late tick is never `machine: STOPPED`, which is a stop's alone, the
record STOPPED. A twin (`mem:<file>`) is ticked by hand and nothing ticks
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

A stop by hand carries why and until when: `stop --reason <text> --until <time
or duration>`, both wanted, a stop missing either refused (exit 2) naming each
missing one; `--until` is a duration from now (`90m`), a clock time (`2:04 PM`,
`14:04`: today's, or tomorrow's once today's has passed) or an RFC 3339 time,
after now (`sprint.StopArgs`). The record keeps the stop's actor, reason and
time, and the machine line says them: `machine: STOPPED by <actor>: <reason>,
back by 2:04 PM` (a time on another day with its date), in `inbox`, the header
of `where`, its JSON (the dashboard's machine line) and every verb's sprint
line (`sprint.StoppedText`). At `--until` the next tick starts the machine
itself, recorded as the machine's start with the stop it ends, and runs it
(`store.backAt`); a stop of the STOPPED machine before then replaces the reason
and the time, and the span goes on; a clear takes them off, so nothing starts
a cleared sprint; when every provider is out of credit at `--until` the
machine stays STOPPED for that cause, as a start is refused then. The
machine's own stops carry their cause instead (below).

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
ask (T2: the reads each primary in review with work not failed wants now, one
at a time: its first read alone, the rest it needs once the first came back ok,
one for a flash card and two for a pro card; a read asked of a
reader that is not up is taken back first, section 6), check (T6: section 9, and the
no-stall rule), deadlines (and the backlog alarms, section 8), overdue, done
(the sprint done: the machine stops). Each part is
bounded per tick (200 moves, 50 notes): the rest are due, the next ticks
catch up, and the machine line says so. A card made ready is dealt in the same
tick. Running a tick twice in a row changes nothing the second time.

The tick writes a judgment once while its condition holds and closes it when
the condition clears (closing a primary's last judgment in review, it writes
the judgment the primary needs next, as every step that leaves one in review
does): cannot ask (enough readers are up and fewer different readers than its
tier needs, one for a flash card and two for a pro card, are free for a
primary with no read card at its attempt, placed or retired: a reader is
asked an attempt once, whether it read it or its read was taken back;
one condition per primary whatever its count of free readers, and the
primaries of one tick one judgment, `no eligible reader for <ids>`, the ids
those no open judgment of the type names yet, each its own subject, closed
when it is asked),
fewer than two readers up (the sprint's, one whatever the primaries waiting:
the ask asks no primary that needs more readers than are up while it stands,
section 6),
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
and the cards), stalled (what nothing holds, and why). Deadlines count running time: time spent STOPPED does not
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

### The fleet is idle

The coordinator, 2026-10-04, measured at 1:30 PM: the fleet ran 4 of 68 slots with 561 cards held, 311
of them behind 21 judgments "a primary is blocked on something dropped", and nothing said so
until a person looked. The tick's idle alarm (`sprint.TickIdle`, internal/sprint/idle.go; run
`--idle-alarm`, on by default; a tick by hand only with `--idle-alarm`) is its last part but
the done part. An episode begins when the fleet works under half its width (the work cards
working on the machines up, against their widths' sum: `sprint.FleetWorking`) while a
primary waits; its start is the fleet table's property `idle_since`. Past `IdleWindow` (5
minutes) of running time, one note goes to the coordinator, `the fleet is idle`, a happened
note addressed to them (the tick end wakes them, and `inbox --push` carries it to the bus):
`fleet <working>/<width>: <roots>`, and `idle_said` marks the episode said. The roots are
`sprint.TraceIdle`: every waiting card traced through its needs and its place in line
(memoized, so the counts are the table's, not a sample's) to where its chain ends, the
kind that needs a person first: a need dropped (`<n> behind <k> drop-blocked judgments (oldest
<age>)`), a need missing, a card in flight held by an open judgment (`<n> behind <card>
(<judgment type>, <age>)`), a sentinel held or reached and not released, a held card, a stream
stopped, a tier no route serves, else work in flight; grouped and named by the cards behind
each, the most first, at most `IdleRoots` (8). When the fleet works at half its width again,
or no primary waits, the episode ends: both properties are cleared, and when its note was
pushed a second note says `the fleet is working again` with how long it lasted. One note an
episode, a clear only after a note (tla/SprintRules.tla, `AlarmOncePerEpisode`,
`ClearFollowsAlarm`; `TestTheIdleAlarmNamesTheRootsOnceAnEpisode`,
`TestTheIdleTraceNamesACardAtItsBound`).

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
in order. A write runs on the server's one line of control: one batch's writes, and one tick, at
a time, neither during the other, each waiting its turn in the order it came. A friend's beat and
a read do not take the line (the owner, 2026-10-04: "a verb is answered within 1 s whatever the
tick or the lander is doing"; "a beat is a small write"). A friend's beat (`friend beat <friend>` and its report)
writes one record outside every table, the friend's beat, and runs on the beat lane, beside the
line and beside every other beat. A read (`where`, `card`, `log`, `check`, `routes`, `stats`,
`needs`, `goal show`, `handover`, and `inbox` without `--read`) writes nothing and runs on the
read lane: one read at a time on the lane's own process state, beside the line, as a client
reading the store directly always has. `queue` records a reader's beat, `fleet beat` can write
the fleet table and `inbox --read` moves the coordinator's cursor: each takes the line. A batch
waits for the line only while its caller waits for the answer: a caller that has gone (its
request ended: a client's deadline, a dropped connection) has the verbs of its batch not yet run
answered exit 2, not run, and nothing is changed by them. On 2026-10-04 every verb took the line
and a batch was run whenever its turn came: from 2:06 PM the line held the run loop's tick 10 to
50 s, from 2:14 PM 36 to 151 s while each tick took 0.5 to 6 s, every friend's beat (two a second
a friend) timed out at its 10 s deadline and was still run later, and the line never drained. On a
twin file (`mem:<file>`), which the server writes whole after every verb, every verb takes the
line. The server says what its batches cost once a minute when it answered any: `SERVE
batches=<n> beat-lane=<n> read-lane=<n> on-line=<n> gone=<n> wait-max=<d> held-max=<d>
held-by=<verb> over=<d>`. The model is `tla/ServerLanes.tla`. The server keeps
nothing between requests.

The tick's turn. The batches, the lanes beside the tick (land's reads and report, decide,
balance) and the tick take one line of control. The batches and the lanes take it in the order
they asked; the tick does not queue behind them. A tick that asks takes the line as soon as no
batch waits; else its turn is due once the batches have had the line, since the tick before
ended, for as long as that tick held it (100 ms at least, 5 s at most), and then the tick takes
the line next, after the holder in flight and before every batch still waiting. So the tick
waits at most its turn and one batch, however many batches wait, two ticks of a loaded server
begin less than 15 s apart while a tick and a batch each take less than 5 s, and the batches
keep at least half the line while the ticks take at most 5 s. A tick that waited more than
TickEvery for the line prints `LINE the tick waited <d>`. A tick queued behind every batch
waiting is the stall this prevents: the ticks run in a second or two and begin tens of seconds
apart, and a RUNNING machine reads STOPPED with no stop given. `TestNoTickStepExceedsItsBound`
holds the bound.

The server runs the workers' verbs only: `take`, `finish`, `progress`, `read` and `queue`, each beginning
`<verb> --as <worker>` with one worker's name, `fleet beat <member> --load <percent>` and
nothing more, `friend beat <friend>` with its report's flags (`--running`, `--working`, `--queue`, `--width`, `--load`), each once with its value, and nothing more, and `lane take` or `lane give` `<kind>
--machine <m> --as <worker>` and nothing more (a take's `--wait` asks again from the worker's side). No later word of a verb, wherever it stands, is a flag named `as`, `redis` or
`actor`: the server gives the store and the actor, and puts them before the worker's words. A
`take`, a `finish`, a `progress` and a `read` name the epoch their worker holds (`--epoch`). A `queue`'s
`--packets` is a count from 0 to 1024 and its `--have` card ids, each given once: a worker asks
for the packets it can use this pass and no others (a reader of width 8 with 150 asked reads:
445,525 bytes an answer before, 51,623 after; a member with 32 cards working and 32 ready:
184,003 before, 10,637 after). A verb the server does not run is answered exit 2, saying
nothing was changed, and the batch goes on.

The coordinator's verbs go to the server too. The server listens a second time on the
loopback address at the same port, and there it runs any verb of the command but the ones it
runs for nobody: itself (`run`, `tick`), `land` and `play`, which work outside the store for
seconds or minutes, `fleet sync` and `friend sync`, which read the config store with their
caller's own credentials, and `friend clean` and `friend reconcile`, which work on the directories of the machine they
run on, nor a read that waits for the sprint to move (`where --watch`, `inbox --wait`): the
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

The server serves the role views (section 11, "Role views") on both listeners, read-only, with
no batch: `GET /api/view/coordinator` (`all=1` for every row) and `GET
/api/view/worker?as=<name>`, each with `since=<cursor>`. Each runs `view <role> --json` on the
line of control as any verb the server runs (never during a tick) and answers its JSON, gzipped
for a client that takes it, `Cache-Control: no-store`. A name, a cursor or an `all` of the
wrong shape is a 400 and nothing is run; a name that is no fleet member and no friend is a 404;
a store that did not answer is a 503; any other method is a 405; each with the verb's line. The
access control is the fleet's private network, as for the workers' queue.

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

With `server switch <binary> [--rollback]` the coordinator or an install switches the server's
binary file on disk, keeping the previous binary (`<target>.prev`). With `--rollback`, if a
land fails within the rollback window (default 15 minutes, `--window`), the failed landing
in `run --land` rolls back the binary on disk to the previous binary, causing the running loop
to stop (`RUN STOP the binary this loop runs was replaced...`, exit 3) so its supervisor restarts
it with the previous binary. `server switch --rollback` without a binary immediately restores
the previous binary.

`nova-sprint selftest land` lands a canned card on a scratch clone with this binary, run by
any install before switching: green on a good binary and red on a broken lander (item 14).

With `run --decide <dir>` the server keeps the record of nova-decide's layer 2 (section 2,
the attempt decision; section 5, the grade): `<dir>/attempt.jsonl` and `<dir>/grade.jsonl`,
of which it is the one writer. Its decide lane runs a round every five seconds
(`DecideEvery`), apart from the line of control but for a read of the work table and a
grade's write: it records the attempt decisions the finishes carried, grades every card
never dealt and ungraded through Jev (with `JEV_API_KEY` from the run loop's own
environment, its loop row's nova-secrets keys; with no key it grades nothing and says so
when it starts), eight at a time, each ask bounded by the lane's wait (`GradeWait`, one
minute, given to the lane when it is made, so a backend that never answers ends the
round at the wait; `TestTheDecideLaneCannotStallTheTickOnAHangingBackend`: the line of
control stays free and a tick and a worker's finish run while the asks hang), each
recorded and its grade written on the card
(`store.GradeStep`, the machine's step), and attaches the outcome of each decision of a
card that landed or was dropped, once (a primary with a decision is read unplaced after
its drop). A round prints one `DECIDE recorded= graded= written= attached=` line when it
did something and nothing when it did not; a failure is `DECIDE FAILED <why>`, once until
it changes. A card graded is not asked again by the server process; a grade whose ask
failed (the backend's error, the wait run out) is asked again on the next round, at most
once a round per card, until it is answered or the card is dealt. The record is loaded
whole on each write, as every record of nova-decide is, and its rotation is owed with the
read's. A member's decision that reaches a server with no `--decide` is kept on the card
(and routes the finish when its class's bar on the card is set), and recorded nowhere.

The address is one address of the coordinator's machine on the fleet's private network; an
address every network can reach is refused. A name, a public address, a link-local address and
an unspecified address are refused before a socket is opened. Loopback, a private address and
the tailnet address are the ones that listen, and the coordinator's verbs listen on loopback at
the same port. The server checks no credential (the owner: "I am OK
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

#### install-canary-shadow-tick-r.w1: a shadow tick before every server swap

A release build broke the lander for 13 minutes, and a cold server crash-looped from 4:28 to
4:31 PM; a tick of the new binary, planned before the swap, would have shown both. `server
switch <binary>` therefore runs `<binary> tick --shadow --json` against the store `--redis`
names (else the switch's environment) before it changes anything on disk. The shadow tick plans
and applies nothing (plan and apply are separate: the store holds every step's plan before it
applies it, section 3): it opens the store through a read-only store user (`store.ReadOnly`), a
backend on which every write of the Backend and KV interfaces is a refusal
(`store.ErrReadOnly`) and no optional writer is reachable; it writes no beat, heartbeat, repair
of a pending operation or restore a clear owes (an operation pending past its tries, or an owed
restore, fails the shadow, never repaired); on one fenced read it plans every part of the
tick's start, its tables' updates in order and its end, as the tick's first pass does, whether
the machine is RUNNING or STOPPED, and prints each part with something to do and the plan's
size (units, notes, rows, closes and updates) and time (`store.ShadowTick`). A part that panics
is not recovered: the shadow is the canary of a crash too. The switch refuses (`server switch
REFUSED: the shadow tick of <binary> <why>`, exit 1, nothing on disk changed, the old server
running as it was) when the shadow exits non-zero, panics, does not end within
`--tick-deadline` (default the run loop's `TickDeadline`, 10s; the process is killed), or
prints no plan; a binary that predates `--shadow` is refused for that. On a pass it prints
`SHADOW TICK OK ... size= took= wall=`, switches as above, and records the shadow (binary,
time, plan, its size, the plan's time and the process's) at `<target>.shadow.json`, beside the
switch record. `server switch --rollback` with no binary restores the previous binary and runs
no shadow. Tested on the twin store with this binary as a working candidate and broken
candidates that error, panic, hang past the deadline and print no plan
(`TestServerSwitchRunsAShadowTickAndRefusesABrokenBinary`), the store byte for byte unchanged
by a shadow (`TestShadowTickPlansOnTheStoreAndWritesNothing`), and every write of the read-only
store refused (`TestShadowTickStoreRefusesEveryWrite`).

#### store-latency-row-r.w2: where shows the store round trip the server measures

The store latency was measured by hand with redis-cli (20 pings, then 500 on one connection) on
2026-10-04 at 3:26 PM, while landing was slow; the server measures it instead. Every 10 s
(`store.StoreRTTEvery`) `run` times one round trip to the store, the read of the store round
trip record, by the injected clock, and writes the record (`store.MeasureStoreRTT`): the samples
of the last minute (`store.StoreRTTWindow`) and their p50 and p99 in milliseconds, nearest
rank, to the microsecond. `where` reads the record in the exchange it already makes for the
machine's records; `where --json` carries `store_rtt_p50_ms` and `store_rtt_p99_ms`, and
`where` prints them on its store line, `store: rtt p50=<ms>ms p99=<ms>ms`, under the tables.
With no record, or one whose last sample is older than the window (a server that stopped
measuring), both fields and the line are left out. Tested on the twin store with the harness's
clock, never the wall clock (`TestWhereReportsTheStoreRoundTrip`).

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
(`init --coordinator`; `where --json` carries it), lands it: reached, with nothing
before it, or not yet reached when each card it waits for is under way, that is has
landed, was dropped, or is in flight (working with its work card taken by a member, in
review, or merging). One not reached is refused while any card it waits for has not
started (waiting, ready, or working with its work card dealt and not taken), with one
line naming the first such card and its state, so a starving fleet frees the cards
behind work in flight in one step (nova-tools#5096 item c13). Released so, what it still
waited for is waived on it (`waived`, `waived_by`, `waived_at`) and named in its
notification, beside the coordinator's reason (`release_reason`); that work lands as it
would have. The same step moves what
waited behind it, up to the next sentinel, to ready as one set, marks reached
any sentinel now due, and always writes a notification that it landed. A need
it names that is dropped blocks it like any waiting card; ack waives the need.
It counts in the sprint line and in waiting and landed, and the sprint is not
done while one waits. A ready primary whose work card was withdrawn (no member
up) stays ready when a sentinel is inserted in front of it: it has started,
and is past the stop; check's bijection rule holds that the primary of a withdrawn card
is ready.

`add --held` admits every card of the add held (nova-tools#5096 item 15: a
sentinel at the head of an empty stream was reached at once and raised a
judgment): waiting, stamped `held`, whatever its needs. A held sentinel is never
marked reached and raises no judgment; a held card never moves to ready and is
never dealt; ack's waiver leaves a held card waiting; the no-stall rule reads
the hold as the coordinator's (c). So a wave loads behind a held sentinel at the
head of an empty stream, or as held cards, and nothing fires.
`release <id> --reason <text>` lands a held sentinel that waits for nothing as it
lands a reached one (one that still waits for a card not started is refused, naming
it), and clears the hold of a held card, which goes to ready in the same step
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


## 18. Lanes

The one-Go-test-stream-per-machine rule (docs/STANDARD.md, "one test stream per
machine") is a lock the machine grants, not the coordinator: a worker about to run a
Go build or test takes one of the machine's Go lanes, runs, and gives it back. The
lanes are a per-machine semaphore (`internal/sprint/lane.go`), one record per lane kind
outside the tables and the fence (`lanes:<kind>`), so a take or a give works while the
machine is RUNNING or STOPPED and moves no card. The server runs the workers' takes and
gives one at a time beside the store, so no two steps of the record interleave.

- **The width.** Each machine has as many Go lanes as the sprint's `go_lanes` setting
  (`set --go-lanes <n|default>`, the work table's property), 1 when none is set.
- **The grant.** A take is granted while the machine's holders are fewer than the
  width and nobody waits ahead of it; otherwise it joins the back of the machine's
  queue and is told its place (exit 1, with the command to wait). The queue is fair:
  a give, or a release, grants the head, never a later asker. A narrower width takes
  no lane back and grants none until the holders are under it.
- **Asking again.** A waiter keeps its place while it asks again within one minute
  (`lane take --wait` asks every 5 s); a waiter that stops asking for a minute leaves
  the queue. A holder renews its hold by taking again.
- **Release.** `lane give` when the run exits, or a timeout: a holder that has not taken
  again for 20 minutes (past a whole `go test -timeout 600s` gate with its build) is
  released, and a grant made while its waiter was not asking is released when the
  waiter has not claimed it within a minute. Each release grants the head of the queue.
- **Shown.** `lane list` prints every machine with a holder or a queue;
  `where --json --cards` carries the same rows as `lanes`, which the dashboard's
  copy of the sprint holds (it reads the sprint that way); the page does not draw
  them.


## friend-stall-ladder-r.w1

**The friend stall ladder** (docs/SPEC-SPRINT.md, `internal/sprint/friend_stall.go`;
the model is `tla/StallLadder.tla`). When a friend stalls while holding dealt sprint cards,
recovery is fully mechanical as a tick part (`PartFriendStall = "friend-stall"`) in the
fleet update pass, after presence (`TickTables`, `TickParts`), with no step needing the
coordinator; the reference model decides it as the duty `friend-stall`
(`internal/sprint/refmodel`).

A friend holding dealt cards (`Ready` or `Working` on her row) is stalled when neither
activity of hers nor any card progress stamp (`FieldProgress`) is newer than
`friend_stall_after` (default 20 minutes, configurable via `nova-sprint set --friend-stall-after`).
Her activity is any of: her session activity (`FriendReport.Active`, her daemon's report of
the newest write under her working directory and outbox); a beat whose running list is not
empty (`friend beat --running`), at the beat's time, since a one-shot lane friend and a
friend whose cards run in child agents move no session while they work; and a finish of a
card on her row (`DoneOK` or `DoneFailed`, its `finished` stamp), so a finish within
`friend_stall_after` holds her at rung 0 (2026-10-05: working friends were stalled and
marked down on session activity alone). A running beat is activity only while it is her
beat: nothing remembers it once her beat names nothing running.

While stalled, the ladder climbs one rung per `friend_stall_step` (default 5 minutes,
configurable via `nova-sprint set --friend-stall-step`):
1. **Wake turn 1**: a bus message to her (`wakeFriendStall`, the store's `WakeFriend` that
   `tick` and `run` set) pushed into her daemon as a turn; a message not sent is said on
   stderr and on the tick's result, and the rung climbs the same. The part only plans the
   wake: the store's tick (`tickRun.parts`, `internal/sprint/store/tick.go`) hands it a
   `TickReq.WakeFriend` that keeps the wakes of the plan being made, and sends the last
   plan's once the part's step commits. A plan made to see whether the part has work, or
   made again after a commit lost to another writer, wakes no one, so each wake rung wakes
   her once (2026-10-05: the plan made to see whether the part had work sent the wake too,
   and every wake went twice; `TestATickWakesAStalledFriendOnceAtEachWakeRung`).
2. **Wake turn 2**: a second wake bus message.
3. **Coordinator note**: a pushed judgment (`Kind: Judgment`, `Type: NStalled`,
   `"friend <f> stalled <d>: two wakes unanswered"`).
4. **Unstarted cards taken back**: unstarted cards on her row are taken back (`FriendTake`
   with `All: true`), each withdrawn on her row with its primary back to `Ready` for the next
   deal; any started card (one her beat names running or with `FieldProgress` stamped) stays
   with her and finishes.
5. **Friend marked down**: she is marked down with reason `"stalled"` (`p.Health` with
   `State: Down`, `Reason: "stalled"`, and status `down` on her fleet row). She is released to
   `up` by the tick itself at her first activity after it (session, running beat or
   finish; never card progress alone): the release clears the stall properties, sets her
   fleet row `up`, and removes the coordinator's observation of her (`p.HealthClear`, the
   same removal as `friend health --clear`), writing none, so her status falls back to her
   beat rule (`sprint.FriendStatus`). It once wrote an `up` observation: that stood for
   `FriendObservedDownAfter` (10 s) and then held her `down` for good, since nothing renews
   an observation every ten seconds and her own beat never brings an observed friend up.

Every rung emits a happened note (`Kind: Happened`, `Type: "friend stall"`). Any activity
or card progress resets her to rung 0.

The TLA+ specification `tla/StallLadder.tla` verifies five invariants:
- `NoCardHeldPastBound`: no unstarted card is held by a stalled friend for more than the bound
  (`friend_stall_after + 4 * friend_stall_step`).
- `NoStartedRedealt`: no started card is taken back or redealt; started cards stay and finish.
- `ReleasedOnlyByActivity`: a friend marked down for stall is released to `up` only by her
  activity (session activity, a beat naming running cards, or a finish on her row), never by
  card progress alone. Its one activity action (`FriendActivity`) stands for all three.
- `WokenAtEveryWakeRung`: a friend past wake rung n (n = 1, 2) was sent wake n (reversed
  witness `nowake`: the wake planned and never sent, as `friend-stall-ladder-r` left it).
- `NoWakeWithoutRung`: a wake is sent only at a rung the ladder climbed, and once
  (`PlanUncommitted`, a plan the tick makes and does not commit; reversed witness
  `wakeinplan`: the planner sends the wake as it plans).

# Reads as cards: one width per unit

Status: a design for the owner's decision. Nothing here is built. The code follows on
the card's next attempt, and only after the owner says yes to this note.

## The problem

A read and a work card are both one child run by one unit, but today they are dealt by
two machines with two widths:

- **Work** is dealt to a unit's row (a fleet member's, or a friend's `friend.<name>`
  row), against that unit's width, `DealAhead` times its width counting ready and
  working (`width.go`, `friend_deal.go`).
- **Reads** are asked of a second kind of unit, a reader, on the readers table. A
  machine's reader `reader-<m>` runs beside the member on `m` at the member's width
  again ("readers equal workers"), so the machine runs up to twice its width of
  children. A bud's reader `reader-<friend>` runs her daemon's read slots
  (`row_read_slots`, `internal/friend/read_lanes.go`), a width of its own beside her
  work width. A friend's read asked through `FriendReadAsk` sits on her fleet row and
  does count against her one width: that path is already the design below.

The reader widths are invisible beside the friends and fleet tables. A reader has
its own presence (beats, `ReaderBeatBound`, up/away/down), its own rebalance
(`levelReads`), its own judgments (`fewer than two readers up`, `no eligible reader`,
`readers are behind`) and its own verbs (`reader add/set/up/away/retire/remove`).
On 2026-10-06 reads backed up for hours behind three bud readers at their read slots
while friends with room took new work, because nothing in the deal knew that the
units taking work were the same units that could have read.

The owner, 2026-10-06 10:56: put reads ahead of regular work as high-priority cards,
so they are "just a type of consumer card" with the one width, and their priority
"would naturally STOP other regular tasks from coming in, until the reads are
finished." "Less is more."

## The design in one sentence

A read is a consumer card in a group whose level is above every stream's, dealt by
the one deal to any unit (a fleet member or a friend) with room under that unit's one
width and tiers; it is taken, run, finished and recorded as work is; its verdict
closes the read as today.

## What goes

| goes | what replaces it |
|---|---|
| the readers table (`readers` rows, its `asked/reading/width/ok/broken` view) | read cards on the units' own rows in the fleet and friends tables |
| reader rows, `init --readers`, `reader add/set/up/away/retire/remove` | nothing: a unit that is up and has room is a reader |
| reader presence: `queue --as reader-<m>` beats, `ReaderBeatBound`, `ReaderState`, `UpReaders`, the reader's `held/retired` states | the unit's presence (the member's beat, the friend's seat); `hold`/`unhold` of the unit |
| reader widths: `ReaderWidth` (derived from the fleet row), the bud's read slots (`row_read_slots`, `DefaultReadSlots`) | the unit's one width |
| the readers' rebalance (`TickLevelReads`, `levelReads`, `readerRooms`, `FieldLeveled`, retired by `level`) | the fleet's level of ready cards, which moves a ready read like any ready card |
| the readers' tiers cell (`readers.tiers`, `reader set --tiers`) | the unit's tiers: a friend's seat tiers; a member serves the tiers its routes draw (`readerServesTier` today) |
| the separate machine reader loop (`member --reader`, `queue --as reader-<m>`) and the bud's read-slot pass in its daemon | the unit's one loop takes read cards from its own row as it takes work cards (a read's packet, `Kind: read`, is already one the member runs) |
| judgments: `fewer than two readers up`, `no eligible reader for <ids>`, `the readers are behind` | one judgment, `no unit can read <ids>`: no unit up may read the card to the count it needs (its tier, not its worker, two different units for a pro card) |
| `FriendReadAsk`'s separate chooser and `machineReaderHasRoom` | the one deal |
| `tla/ReadsByRoom.tla`, `tla/ReaderTiers.tla` and their cases | folded into `tla/Deal.tla` (below) |

## What stays

- **What a read is.** A read card per (primary, attempt, unit), its id
  `<primary>.r<attempt>.<unit>`; its packet; the verdicts ok and broken with a finding;
  `read --return` with its reasons and `MaxReadReasks`; the lease and its renewal by
  the unit's beat; a broken read names its defect; attribution is never a finding.
- **How many reads.** `ReadsNeeded`: one for a flash card, two different units for a
  pro or heavy card. Reads are asked one at a time (`ReadsWanted`): the first alone,
  the second only after the first came back ok. The decide read on a flash card's
  first read.
- **The finder checks the fix.** A rework's first read goes to the unit whose finding
  sent it back (`finding_reader` now names a unit) when it is up and has room.
- **The read tier.** Per attempt, raised and never lowered by the stream's or the
  sprint's read tier; its escalation judgment; the route drawn at the tier's rolling
  index for a member; a friend brings her own model.
- **Acceptance.** A primary is acceptable on its `ReadsNeeded` ok reads at its head
  from different units; the coordinator's heavy read (`accept --heavy`) stays one ok.
- **Reads kept by head** (`read_memo`) where it has landed: the key's reader becomes
  the unit.

## How a read is dealt

1. **The group.** Reads are one group, `reads`, at a level above every stream's
   (`ReadLevel`, one more than the highest stream level, so `priority set` can never
   put work ahead of reads). It needs the deal's levels of the card
   `priority-is-a-group-the-deal-honors-first` (`dealOrder`, `byLevel`,
   `tla/Deal.tla`); this card lands after it.
2. **What is ready in the group.** A primary in review that wants a read now
   (`ReadsWanted` > 0, its work not failed) contributes one ready read. Inside the
   group, the reads go in work order (the order the ask uses today).
3. **Who may take it.** A unit up, not held, whose tiers hold the read tier, that did
   not work this attempt, that has no read card at this attempt (placed or retired),
   and that has room under its one width. The finder first when it qualifies; else
   the unit the deal's chooser picks (an idle lane first, then the most room as a
   share of width, then by name: `preferredFriend`'s rule, used for members too).
4. **Room.** A read counts against the unit exactly as a work card does: ready plus
   working, under `DealAhead` times its width; working under its width. No unit runs
   more than its width of children, reads and work together.
5. **Draining.** Because the deal offers the highest level first, a unit with room is
   given every read it may take before any new work. On the unit, its loop starts its
   ready cards in level order, so a read dealt into its ready column starts at its next
   free lane ahead of the work queued there. Nothing pre-empts a running child.
6. **Taken back.** A read on a unit that goes down or is held with `--return` is taken
   back with the unit's other ready cards, and dealt again at the same attempt to
   another unit (`<id>.g1` as today when it must return to the same unit).

## Invariants (the model's, and the tests')

- **OneWidth**: on every unit, reads and work together never exceed its width working,
  nor `DealAhead` times its width held.
- **ReadsFirst**: no work card is dealt to a unit while a read it may take is ready (the
  deal's `LevelFirst` with reads as the top level).
- **NeverOwnWork**: no read of an attempt is dealt to the unit that worked that attempt.
- **TwoUnitsOnOneHead**: a pro card's ok reads at one head are by two different units,
  and no unit holds two read cards of one attempt.
- **OneAtATime**, **FinderFirst**, **ReadsPerLanding** (three reads for a pro card
  found broken once): kept from `ReadsByRoom.tla`.
- **TierHeld**: no read is dealt to a unit whose tiers do not hold its read tier (from
  `ReaderTiers.tla`).
- **NeverSilent** (liveness, fair deal): a primary in review that wants a read and has a
  unit that may read it is dealt one; one that has none carries the judgment.

`tla/Deal.tla` gains a second kind of card (a read of a primary at an attempt, with a
worker and a tier), the `Finish` of a read with its verdict, and the invariants above;
the reversed witnesses: reads at stream level (breaks `ReadsFirst`), a separate reader
width (breaks `OneWidth`), the worker allowed to read (breaks `NeverOwnWork`), both
reads of a pro card on one unit (breaks `TwoUnitsOnOneHead`), the pair asked at once
(breaks `OneAtATime`). `ReadsByRoom.tla`, `ReaderTiers.tla` and their cases, records
and coverage rows are deleted with it.

## What the tables show

- The text table loses the `readers` line. The `fleet` and `friends` lines show, per
  unit, `ready`, `working` and `width` as now, and a `reads` cell: the unit's read
  cards among them (`3/8` working, `1` of them a read). A row says what it holds.
- The dashboard's fleet and friends rows show reads beside work, by the same cells;
  the readers panel goes.
- `where --json` drops `tables.readers` and adds `reads` to each unit's row.

## Migration

- At the first tick of the new code, every readers-table row is retired with one
  happened note, `reader <name> retired: reads are dealt to units`, naming the unit it
  was named for where there is one (`reader-<m>` to member `m`, `reader-<friend>` to
  `friend.<friend>`).
- A read card asked and not begun on a reader row is retired (by `migrated`) and dealt
  again by the deal at the same attempt. A read begun finishes where it is and its
  verdict records as today; the retired row stays readable until the epoch clears.
- `nova-sprint reader ...` answers, for one release, `REFUSED: reads are dealt to units;
  run: nova-sprint where` and then goes.
- A bud's `row_read_slots` is ignored and named in one NOTE of its beat's answer.

## Consequences the owner should weigh

1. **Fewer children per machine.** Today a machine runs its width of work and its width
   of reads. With one width it runs its width in all. Keeping today's throughput means
   raising the machines' widths; keeping today's widths means fewer concurrent children
   and less memory and CPU contention. This note keeps the widths and leaves a raise to
   the owner.
2. **Work waits while reads are ready.** That is the point, and it is total: with reads
   ready that a unit may take, it is given no new work. A long review queue holds the
   whole fleet on reads until it is drained.
3. **Never read your own work is stricter.** Today `reader-<m>` may read work done on
   `m`. Under this rule a pro card needs two units other than its worker that hold its
   tier, so a sprint with two such units cannot land a pro card worked on one of them;
   `no unit can read` says so.
4. **The member's loop.** A member's loop runs reads already (`Kind: read`); taking
   them from its own row instead of a second loop is a change in `internal/member`, and
   the bud's read lanes become its daemon's ordinary lanes, a change in
   `internal/friend` beyond `read*.go`.

## Paths the code attempt needs beyond this card's

This card's PATHS cover `internal/sprint/**`, `cmd/nova-sprint/**`,
`internal/sprintdash/**`, `internal/friend/read*.go`, the two docs, `docs/design/**` and
`tla/**`. The code attempt also needs:

- `internal/member/**` (the member's queue takes reads from its own row; the
  `--reader` mode goes);
- `internal/friend/**` (the daemon's read slots fold into its lanes);
- `internal/docs/catalog.go` and `docs/AGENTS.md` (the `docs/design` directory's row,
  which the map guard requires; see the report of this attempt).

## The test that pins it

`TestAReadIsDealtToAUnitAgainstItsOneWidthAheadOfWork` (`./internal/sprint`, pure, a
fake clock): two units of width 1 and 2 with one free lane between them, a ready work
card of a stream at the highest stream level and a primary in review that wants a
read. The tick deals the read, not the work, to the unit with room; the unit that
worked the attempt is never chosen; the unit's ready and working, reads included,
never pass its width; with no readers-table row anywhere the read is still dealt.

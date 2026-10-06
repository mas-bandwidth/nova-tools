# Glossary: nova-sprint 1.0.0

Every term the nova-sprint spec and help use, one line each: what it means, the section of
[SPEC-SPRINT.md](../SPEC-SPRINT.md) that defines it, and the words it replaced where it
replaced any. This file moves to nova-sprint's `docs/` at the split
([SPLIT-NOVA-SPRINT.md](../SPLIT-NOVA-SPRINT.md)); until then the links reach back into
nova-tools. The building blocks' terms are in [../GLOSSARY.md](../GLOSSARY.md), the rules
of naming and the retired words in [../TERMINOLOGY.md](../TERMINOLOGY.md). A word used in the
spec or the help and missing here is a gap: file it.

Each row is `term` — definition. *Defined:* section. *Replaces:* what it was called before.

- **accept** — the move from review to merging once the readers it needs said ok at the
  card's head: one reader for a flash card, two different readers for a pro card.
  *Defined:* section 3, The lifecycle of a primary; section 6.
- **card** — one unit of work, one instruction: a primary for the work itself, work cards on
  the fleet, read cards for the readers, a merge card for the lander. *Defined:* section 2, The cards.
- **clear** — `nova-sprint clear`: stops the sprint and advances the epoch, so every table is
  empty with the same rows; it deletes nothing. *Defined:* section 13, Epochs and clear.
- **coordinator** — the friend who decides: the system moves cards without mistakes and tells
  the coordinator what needs a decision. *Defined:* the opening of the spec; the seat is section 11.
- **cost** — the work table's last column: the sum of each landed card's total cost for the
  stream, in dollars and cents rounded up. *Defined:* section 1, The tables.
- **deal** — the tick's move of a ready card to a fleet member with room, as a work card
  (ready -> working). *Defined:* section 3.
- **down** — the word of the friends table for a friend whose beat has lapsed, and of the
  fleet for a member that is not up. *Defined:* section 1 (the friends table).
  *Replaces:* asleep.
- **epoch** — the generation of the sprint every table, key, judgment and cursor is bound to;
  `clear` advances it, and a card id is used again in a later epoch. *Defined:* section 13.
- **fleet** — the table of members (machines) and their work cards, the swarm across machines.
  *Defined:* section 5, The fleet.
- **friend (in the sprint)** — a friend who helps with cards, with a row in the friends table
  (ready, working, width, done, ok%, status) copied from `nova-config`'s friend rows.
  *Defined:* section 1, The tables.
- **hold / unhold** — the coordinator's verbs that hold a reader or friend whatever it beats
  (state `held`) and release it. *Defined:* section 11, hold. *Replaces:* `reader away` and
  `reader up`, kept for one release as the old words.
- **judgment** — a point where the machine stops and notifies the coordinator for a decision;
  a card of a judgment is decided once. *Defined:* section 8, Notifications.
- **lander** — the machine step that merges a stream's batches onto the sprint branch, proved
  there; promotion alone takes the sprint branch to the development branch. *Defined:* section 7, Merging.
- **lane** — a per-machine Go lane: a lock the machine grants so one Go build or test stream
  runs per machine; a worker takes one, runs, and gives it back. *Defined:* section 18, Lanes.
- **landed** — a primary's final state: its code is on the development branch. *Defined:* section 3.
- **member** — a fleet machine with a width. *Defined:* section 5, The fleet.
- **primary** — one unit of work between an issue and a pull request, one stream for life.
  *Defined:* section 2, The cards.
- **promotion** — the move of the sprint branch to the development branch, the promotion
  stream's alone. *Defined:* section 7, The sprint branch.
- **rank** — the coordinator's receipted verb that changes a score; nothing else does.
  *Defined:* section 4, Order.
- **reader** — a row that reads a primary in review and says ok or not; up, away, down, held
  or retired. *Defined:* section 6, The readers.
- **score** — a primary's place in line, given once at admission and copied to its work, read
  and merge cards. *Defined:* section 4, Order.
- **seat** — the coordinator's place: who holds it is recorded, and the seat's verbs check
  the actor is the holder. *Defined:* section 11, Handing over the seat.
- **sentinel** — a primary of kind sentinel: a stop in its stream that waits for the cards
  before it. *Defined:* section 16, Sentinel cards.
- **stream** — a line of primaries that lands in order, one stream for a primary's life.
  *Defined:* section 1, The tables.
- **tick** — one pass of the machine that `run` makes whenever a line comes on the log (at
  most every 100 ms after the last, and every second on a quiet log), making the mechanical
  moves. *Defined:* section 14, The machine.
- **tier** — the class of model a card runs on (flash first, pro when pinned or measured
  heavy); a cost record that names none is counted under no tier. *Defined:* section 2, The
  cards (tier, tier_now). *Replaces:* untiered.
- **twin** — a card re-cut to replace an old one: it takes over the old card's edges, so every
  waiting card that needed the old id needs the twin instead. *Defined:* section 2, A card
  replaced by its twin.
- **width** — the most work cards a member runs at once; it holds up to two times its width,
  ready and working together. *Defined:* section 5, The fleet.
- **work card** — the live card on the fleet a primary in working has, exactly one each.
  *Defined:* section 9, What is always true.

# Glossary: nova-sprint 1.0.0

Every term the sprint spec and help use, one line each: what it means, the
section of [SPEC-SPRINT.md](../SPEC-SPRINT.md) that defines it, and the words it
replaced where it replaced any. This file moves to nova-sprint's `docs/` at the
split ([SPLIT-NOVA-SPRINT.md](../SPLIT-NOVA-SPRINT.md)). The rules of naming are
[TERMINOLOGY.md](../TERMINOLOGY.md); the nova-tools terms are in
[GLOSSARY.md](../GLOSSARY.md).

- **card** — a unit of work, a primary or one of its work or read cards.
  (SPEC-SPRINT.md, section 2, The cards)
- **coordinator** — the friend who decides; the system moves cards and tells her
  what needs her. (SPEC-SPRINT.md, opening)
- **deal** — the tick cuts a work card for a ready primary and gives it to a
  fleet member. (SPEC-SPRINT.md, section 3, The lifecycle of a primary)
- **down** — a friend or machine that has not proved it is there; the one word
  every table shows. (SPEC-SPRINT.md, section 1, a friend's health) Replaces:
  asleep, the daemon's intermediate word, now shown as down.
- **epoch** — the span the four tables are bound to; a card id is used again in
  a later epoch. (SPEC-SPRINT.md, section 13, Epochs and clear)
- **fleet** — the table of machines running work cards, each with a width and a
  status. (SPEC-SPRINT.md, section 5, The fleet)
- **friend health** — the coordinator's observation of a friend, up, held or
  down, written by the seat's holder alone. (SPEC-SPRINT.md, section 11, Verbs)
- **held** — a friend the coordinator's judgment holds down; her beat does not
  make her up. (SPEC-SPRINT.md, section 1)
- **judgment** — a notification that needs the coordinator, naming the
  decisions open to it. (SPEC-SPRINT.md, section 8, Notifications)
- **lander** — the part of the machine that merges a stream's batch, green on
  the stream branch, to the development branch. (SPEC-SPRINT.md, section 7, Merging)
- **landed** — the final state of a primary: its code is on the development
  branch. (SPEC-SPRINT.md, section 3)
- **lane** — a per-machine Go build or test lock a worker takes and gives back.
  (SPEC-SPRINT.md, section 18, Lanes)
- **primary** — a unit of work driven through waiting, ready, working, review,
  merging and landed. (SPEC-SPRINT.md, section 3)
- **rank** — the coordinator's receipted decision that changes a score; a score
  is otherwise given once, at admission. (SPEC-SPRINT.md, section 4, Order)
- **reader** — a member that reads a primary in review and says ok or not at
  its head. (SPEC-SPRINT.md, section 6, The readers)
- **seat** — the coordinator's place, held by one friend at a generation; only
  the holder writes what the seat guards. (SPEC-SPRINT.md, section 11, Handing over the seat)
- **sentinel** — a primary of kind sentinel: a stop in its stream, released by
  the coordinator. (SPEC-SPRINT.md, section 16, Sentinel cards)
- **stream** — an ordered line of primaries that merges as one batch.
  (SPEC-SPRINT.md, section 1, The tables)
- **stall ladder** — the mechanical recovery when a friend stalls holding dealt
  cards. (SPEC-SPRINT.md, the friend stall ladder)
- **tick** — one pass of the machine: it reads once and plans every move its
  dirty rows name. (SPEC-SPRINT.md, section 14, The machine)
- **tier** — the class of model a card names, flash or pro among the four; the
  cost view counts spend by it. (SPEC-SPRINT.md, section 1) Replaces: untiered,
  the label of a cost record with no tier, now no tier.
- **up** — a friend whose last observation or beat is recent enough.
  (SPEC-SPRINT.md, section 1, a friend's health)
- **width** — how many cards a member or friend runs at once under its ceiling.
  (SPEC-SPRINT.md, section 5)

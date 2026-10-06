# Glossary: nova-sprint 1.0.0

The words the nova-sprint spec and help use, one line each: what it means, the
section that defines it, and the words it replaced where it replaced any. The
words of the building blocks are in [../GLOSSARY.md](../GLOSSARY.md) (nova-tools
1.2.0). This file moves to nova-sprint's `docs/` at the split
([../SPLIT-NOVA-SPRINT.md](../SPLIT-NOVA-SPRINT.md)). The rules of naming are
in [../TERMINOLOGY.md](../TERMINOLOGY.md).

An entry reads `**term** — definition. Defined: [where](link). Replaces: words.`
The `Replaces:` clause is the one place a retired word may stand in a glossary.

- **attempt** — one run of a primary's work card; a failed attempt is reworked or escalated.
  Defined: [SPEC-SPRINT.md, The cards](../SPEC-SPRINT.md#2-the-cards).
- **coordinator** — the friend who holds the seat and decides; the machine moves cards, the coordinator judges.
  Defined: [SPEC-SPRINT.md, Notifications](../SPEC-SPRINT.md#8-notifications).
- **dealer** — the tick step that cuts a work card for a ready primary and gives it to a fleet member or friend.
  Defined: [SPEC-SPRINT.md, The fleet](../SPEC-SPRINT.md#5-the-fleet).
- **down** — the table word for a friend, reader or machine that is not answering; the daemon's
  finer observation is kept on the row. Defined: [SPEC-SPRINT.md, The tables](../SPEC-SPRINT.md#1-the-tables). Replaces: asleep.
- **epoch** — one run of the four tables; every key is named at its epoch and `clear` starts the next.
  Defined: [SPEC-SPRINT.md, Epochs and clear](../SPEC-SPRINT.md#13-epochs-and-clear).
- **fleet** — the machines that run work cards, each a member with a beat and a width.
  Defined: [SPEC-SPRINT.md, The fleet](../SPEC-SPRINT.md#5-the-fleet).
- **friend health** — the coordinator's observation of a friend: up, held or down, with the time it was seen.
  Defined: [SPEC-SPRINT.md, The tables](../SPEC-SPRINT.md#1-the-tables). Replaces: asleep.
- **held** — a friend or reader the coordinator has put on hold, whatever it beats.
  Defined: [SPEC-SPRINT.md, The readers](../SPEC-SPRINT.md#6-the-readers).
- **landed** — the final state of a primary: its code is on the development branch.
  Defined: [SPEC-SPRINT.md, The lifecycle of a primary](../SPEC-SPRINT.md#3-the-lifecycle-of-a-primary).
- **lander** — the merger that lands a primary's pull request onto the sprint branch in batches.
  Defined: [SPEC-SPRINT.md, Merging](../SPEC-SPRINT.md#7-merging).
- **lane** — a per-machine Go lane a worker takes before a build or test and gives back.
  Defined: [SPEC-SPRINT.md, Lanes](../SPEC-SPRINT.md#18-lanes).
- **no tier** — a cost record that names no tier, counted under its own heading in `cost_by_tier`.
  Defined: [SPEC-SPRINT.md, The tables](../SPEC-SPRINT.md#1-the-tables). Replaces: untiered.
- **primary** — one unit of work between an issue and a pull request, one stream for life.
  Defined: [SPEC-SPRINT.md, The cards](../SPEC-SPRINT.md#2-the-cards).
- **promotion** — the one step that takes the sprint branch to the development branch.
  Defined: [SPEC-SPRINT.md, Merging](../SPEC-SPRINT.md#7-merging).
- **reader** — a friend that reads a primary in review and says ok or not; a pro card needs two.
  Defined: [SPEC-SPRINT.md, The readers](../SPEC-SPRINT.md#6-the-readers).
- **seat** — the coordinator's place, held by one friend at a time and handed over by a verb.
  Defined: [SPEC-SPRINT.md, Verbs](../SPEC-SPRINT.md#11-verbs).
- **sentinel** — a primary of kind sentinel: a stop in its stream that holds the cards behind it.
  Defined: [SPEC-SPRINT.md, Sentinel cards](../SPEC-SPRINT.md#16-sentinel-cards).
- **sprint branch** — the branch every stream lands on; promotion alone takes it to dev.
  Defined: [SPEC-SPRINT.md, Merging](../SPEC-SPRINT.md#7-merging).
- **stream** — an ordered line of primaries that land one after another.
  Defined: [SPEC-SPRINT.md, The tables](../SPEC-SPRINT.md#1-the-tables).
- **tick** — one pass of the machine over every table: resolve, deal, accept, merge.
  Defined: [SPEC-SPRINT.md, The machine](../SPEC-SPRINT.md#14-the-machine).
- **tier** — the strength class a card runs at (flash, pro, frontier); a card with none is dealt at flash.
  Defined: [SPEC-SPRINT.md, The fleet](../SPEC-SPRINT.md#5-the-fleet).
- **work card** — the live card cut from a primary and dealt to a member for one attempt.
  Defined: [SPEC-SPRINT.md, The cards](../SPEC-SPRINT.md#2-the-cards).

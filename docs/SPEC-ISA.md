# SPEC-ISA: the instruction set of nova-sprint

nova-sprint is a processor (the owner, 2026-10-04). A card is one instruction.
The coordinator is the front end: it issues work (`add`, `release`, `rework`,
`drop`) and handles exceptions (the judgments and their answers), and it never
executes an instruction. This page is layer 1 of the eight layers the design is
built in, bottom up: the vocabulary every later layer speaks. Its first job is
to remove concepts, so the simplicity test gates it: a kind enters only if a
current card on the table uses it.

The kinds are the vocabulary `tla/CardISA.tla` models. No kind is added that no
current card uses; the wait mechanisms of today become one kind.

## The kinds

The instruction kind is a column of the one list, `pkg/hygiene/kinds.txt`:
the `KIND:` line the brief already carries names the instruction, so the spec
adds no second vocabulary. One line, one list.

Candidates: script, think, verify, merge, wait, fence, vector.

| kind | operands | results | today's card line | KIND: line | current card |
|---|---|---|---|---|---|
| `think` | `BASE`, `PATHS`, `DEPENDS-ON`, `TEST`, `tier` | `head`, `verdict` | a primary's `tier`, its work card, and the finish's `head` and verdict | the model work kinds: `fix-red`, `transcript-test`, `rebase`, `sweep`, `mutation-kill`, `guard`, `ledger` | a work card (`kind=work`) |
| `verify` | `BASE`, `PATHS`, `TEST`, `DEPENDS-ON` | `verdict` (`ok`, `broken`) | a primary's `readers`, its read card, and the read's verdict | the read kinds: `read`, `probe`, `text`, `tone`, `report` | a read card (`kind=read`) |
| `script` | `BASE`, `PATHS`, `DEPENDS-ON`, `TEST`, `SCRIPT` | `head`, `verdict` | a work card's `SCRIPT:` steps and their `POST:` lines | none of its own: the step's `SCRIPT:` line, the brief still carries one work kind | a work card whose every work step is a script step (`pkg/cardtree`) |
| `merge` | `BASE`, `REPO` | `head`, `verdict` | the merge table's place and the stream's `state` | none: a merge card carries no brief and no child | a merge card (`kind=merge`) |
| `wait` | `DEPENDS-ON` (its operand names what it waits for) | none | a primary's `needs` and `held`, and a sentinel's `kind` and `reached` | none: a waiting primary is not dealt and runs nothing | a waiting primary, a sentinel, or a held card |

`think` and `verify` are the one list already: their kinds are its rows. `script`
is a form of the steps, not a second list. `merge` and `wait` are machine
instructions: they carry no child and no `KIND:` line, so they add no row to the
list.

The operands and results, field by field, are today's card lines:

| instruction field | today's card line | today's field |
|---|---|---|
| `BASE` | `BASE:` | the ref and sha the work starts from |
| `PATHS` | `PATHS:` | the globs the change touches; a step's own `PATHS:` in a tree |
| `DEPENDS-ON` | `DEPENDS-ON:` (or `Needs:`) | the primary's `needs` |
| `TEST` | `TEST:` | the gate the finish runs |
| `SCRIPT` | `SCRIPT:` | a work step's language and program |
| `head` | the finish's `head:` | the primary's `head` |
| `verdict` | the finish's `verdict:` | the primary's result; a read's verdict on its read card |
| `KIND` | `KIND:` | one row of `pkg/hygiene/kinds.txt` |

## The one wait kind

The one `wait` kind is one path in the code, `WaitOf`
(`internal/sprint/held.go`): admitted held (`add --held`), a sentinel (`add
--sentinel`), the wave behind a held sentinel and `DEPENDS-ON` between cards
are one wait, read once. The operand is the
`DEPENDS-ON` line the brief already carries, read in four forms:

- `DEPENDS-ON: <card id>` waits for that card to land. Today's `needs`.
- `DEPENDS-ON: release` waits for the coordinator's release. Today's held.
- `DEPENDS-ON: line` waits for every primary of its stream that sorts before it. Today's sentinel.
- `DEPENDS-ON: external:<condition>` waits for an external condition. The proposed external wait.

| today | the one `wait` kind |
|---|---|
| admitted held (`add --held`, `FieldHeld`, `release`) | `wait`, operand `release` |
| sentinel (`add --sentinel`, `kind=sentinel`, `release`) | `wait`, operand `line` |
| wave (`heldWave`, a wave behind a held sentinel) | `wait`, operand `release`, many cards at once |
| `DEPENDS-ON` / `needs` | `wait`, operand `<card id>` |
| external wait (proposed) | `wait`, operand `external:<condition>` |

A wave is not a fourth mechanism: it is many `wait` cards behind one sentinel
operand, whose own `wait` operand is `release`. `sprint.WaitOf` is the one code
path that reports these operands and why they apply; `release` keeps its spelling
and serves both the hold and the sentinel. The wave is ready when that same
operand is satisfied.

## Folded and reserved

- `fence` folds into `wait`. A barrier a wait cannot express is not shown by a
  current card: the merge queue's barrier is a rule of the merge step
  (`internal/sprint/steps_merge.go`), not a card's wait, so `fence` adds no line.
- `vector` is reserved for layer 7. It has no line and no mapping row until a
  card uses it.

## What changes

Every concept the one `wait` kind removes, every line it renames and every line
it adds:

| # | before | after |
|---|---|---|
| 1 | admitted held (`add --held`, `FieldHeld`) | `wait`, operand `release` |
| 2 | sentinel (`add --sentinel`, `kind=sentinel`) | `wait`, operand `line` |
| 3 | wave (`heldWave`) | `wait`, operand `release`, many cards |
| 4 | `DEPENDS-ON` / `needs` | `wait`, operand `<card id>` |
| 5 | external wait (proposed) | `wait`, operand `external:<condition>` |

| line | change |
|---|---|
| `held` (`FieldHeld`) | removed; the operand `release` |
| `kind=sentinel` | removed; the operand `line` |
| `needs` | renamed `DEPENDS-ON` |
| `WAIT:` | not added: the operand rides the `DEPENDS-ON` line |
| `KIND:` | unchanged: one list, one line |

concepts before: 5
concepts after: 1

## Sources

- `internal/sprint/held.go`: `WaitOf`, `CardWait`, the one wait hold, sentinel
  and wave read through; `FieldHeld`, `IsHeld`, `heldWave`, `HeldBack`, and the
  no-stall rule's hold.
- `internal/sprint/steps_sentinel.go`: `IsSentinel`, `WaitsFor`, `Reachable`,
  `Release`, the sentinel's release, which is the same wait.
- `pkg/hygiene/kinds.txt`: the one list of work kinds.
- `pkg/cardtree/tree.go`: the script step, which runs with no model.
- `docs/SPEC-SPRINT.md` section 2 (the cards), section 5 (the fleet, for the wave) and section 16 (sentinel cards).

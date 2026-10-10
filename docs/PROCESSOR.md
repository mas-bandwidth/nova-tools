# nova-sprint as a processor

nova-sprint is a processor. Cards are its instructions. The coordinator is the front end that issues work and handles exceptions, never executes it. This page is the processor view: a way of thinking, and the home of everything that is only metaphor. A metaphor is written here and nowhere in code.

The instruction set page is [docs/SPEC-ISA.md](SPEC-ISA.md). The card `isa-spec` is that layer. The instruction format that runs today is the card contract.

The machine is built in layers, bottom up, each secured before the next starts. A layer that is only metaphor does not start.

## The simplicity test

A feature enters code only if it (a) removes concepts or code that exist today, (b) shows a measured gain in cost, wall clock or reliability from the counters, or (c) makes the machine easier to model and check in TLA+. Anything that is only metaphor goes in docs/PROCESSOR.md, not in code.

## Mapping

Each row is MECHANISM and names the file or card that implements it, or METAPHOR and names no code path.

| concept | mark | names |
| --- | --- | --- |
| ISA | MECHANISM | `docs/SPEC-CARD-CONTRACT.md`, `pkg/cardhdr/cardhdr.go`; card `isa-spec` |
| assembler | MECHANISM | `internal/cardgen/cardgen.go` |
| out-of-order issue | MECHANISM | `internal/sprint/steps_tick.go` |
| speculation | METAPHOR | none |
| prediction | METAPHOR | none |
| renaming | METAPHOR | none |
| reorder buffer and retire | MECHANISM | `internal/sprint/steps_merge.go`, `cmd/nova-sprint/land.go`, nova-sprint's `Land.tla` |
| heterogeneous cores | MECHANISM | `internal/sprint/route.go` |
| SIMD | METAPHOR | none |
| caches | METAPHOR | none |
| interrupts | MECHANISM | `internal/sprint/held.go`, `internal/sprint/steps_tick.go` |
| counters | MECHANISM | `internal/sprint/stats.go`, `internal/sprint/store/stats.go` |
| power | METAPHOR | none |
| debugger | MECHANISM | `cmd/nova-sprint/verbs.go`, nova-sprint's `SPRINT-COORDINATOR.md` |

ISA is the card: its header is the opcode (`pkg/cardhdr/cardhdr.go`) and its contract is the instruction format (`docs/SPEC-CARD-CONTRACT.md`). The assembler is `internal/cardgen/cardgen.go`: a structured source becomes briefs `nova-sprint add` admits. Out-of-order issue is the tick in `internal/sprint/steps_tick.go`: a card is admitted while its operands are not ready, and the tick dispatches it when they are. The reorder buffer is the merge queue (`internal/sprint/steps_merge.go`); retire is land, in order (`cmd/nova-sprint/land.go`, `tla/Land.tla`). Heterogeneous cores are the tiers and routes (`internal/sprint/route.go`): flash, pro, heavy and frontier are not one core. An interrupt is a judgment a stall raises (`internal/sprint/held.go`); the tick writes it. Counters are the pass's numbers (`internal/sprint/stats.go`) and the tick's store counters (`internal/sprint/store/stats.go`). The debugger is the inspection the coordinator already runs: `where`, `inbox` and `card` (`cmd/nova-sprint/verbs.go`, `docs/SPRINT-COORDINATOR.md`).

Speculation has no code. A card does not run down a path whose operand has not held. Filling a ready queue ahead of a free core is issue, not speculation. Prediction has no code. A route's predicted price (`pkg/cardcost/predict.go`) is a cost figure, not a guess of which way a card goes, and it is not this row. Renaming has no code. A card keeps its id. A replace re-points needs (`internal/sprint/twins.go`); that is a new card, not a renamed register. SIMD has no code. One card is one instruction with one result. A count of cards is many instructions, not one instruction over many data. Caches have no code. The build cache and the where record are not a memory hierarchy the machine consults on a miss, and this metaphor adds none. Power has no code. Cost and wall clock are counter readings, not a power state that gates a core.

## OUT OF ORDER

Reservation stations are DEPENDS-ON plus external operands: a card is issued early and dispatches when its operands are ready. Issue is add. The card sits waiting. Its station is the needs on its DEPENDS-ON line, plus any operand that is not another card. Dispatch is the tick. `TickResolve` in `internal/sprint/steps_tick.go` moves a waiting primary to ready when every need has landed, and `TickDeal` hands a ready primary to a core that has room. A card whose operand has not held is not executed early. That would be speculation, and speculation is a metaphor.

I/O stalls are a card waiting on a PR, a branch, a time. A PR is a primary in review or merging: the reads, or the land, have not arrived. A branch is a base the lander has not accepted, or a conflict that stops the stream. A time is a deadline, or the late rule's one wait. None of these is a need on DEPENDS-ON. Each holds the card until the outside operand arrives.

Interrupt-driven release is the target: the tick releases a wait when its operand holds. Polling is a coordinator re-checking by hand. Interrupt-driven release is the target, and polling is not, because a coordinator re-checking by hand repeats a read the tick already made. The tick reads the sprint once. The release is part of that pass (`internal/sprint/steps_tick.go`). A stall the pass does not move raises its own interrupt, a judgment (`internal/sprint/held.go`), instead of waiting for someone to look.

The coordinator is the front end that issues work and handles exceptions, never executes it. A member executes a work card. A friend executes a friend's card. A reader executes a read. The coordinator adds work and answers the exceptions the machine does not.

The exception escalation vector is rule, bud, coordinator, owner. A rule answers a mechanical exception (`internal/sprint/rules.go`): failed work, a bound, a late card, a conflict, a base gate, a brief defect. What a rule does not answer climbs to a bud, then to the coordinator. What the coordinator does not answer is the owner's. That last step has no code path. It is not a package.

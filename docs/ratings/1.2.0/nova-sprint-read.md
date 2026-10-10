# nova-sprint READ and USE rating, nova-tools 1.2.0

Rater: a cold rater,
build: 17ec8d256a04
READ: 5/10
USE: 5/10

## Reasons

READ. The tool has no README. The primary documentation is docs/SPEC-SPRINT.md which describes four tables (work, readers, merge, fleet) and the mechanical moves between them.

What holds READ at 5: the spec runs 4000+ lines and the opening shows a table drawing but doesn't explain what the columns mean. The contract references SPR-INT-COORDINATOR.md for the coordinator's day but doesn't summarize what decisions the coordinator makes. There is no install section and no first-run example.

USE. The spec describes verbs but the CLI reference at docs/CLI.md:984+ is needed to see flags and exit codes. The spec says "The coordinator decides; the system moves cards without mistakes" but doesn't show how a human starts the system or what the first command is.

What holds USE at 5: there is no transcript showing a complete workflow from "I want to sprint" to "a card is landed". The friend loop (how friends receive cards) and the fleet sync (how machines cooperate) are described but not illustrated with commands.

## Findings

| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | docs/SPEC-SPRINT.md:0 | No README exists; the spec is 4000+ lines with no high-level summary | Add a README.md with one-sentence purpose, install, and first-run example | L |
| 2 | docs/SPEC-SPRINT.md:9 | The table drawing shows columns (work, waiting, ready, working, review, merging, landed) but doesn't explain what each means | Add a column glossary or link to a section that explains the states | M |
| 3 | docs/SPEC-SPRINT.md:2-4 | The contract mentions SPR-INT-COORDINATOR.md but doesn't summarize the coordinator's decisions | Add a one-paragraph summary of what the coordinator decides | S |
| 4 | docs/SPEC-SPRINT.md:0 | No install section or first-run example exists | Add install and First run blocks | M |

## Good, keep

The spec shows the four tables and their column headers. The contract states that "The coordinator decides; the system moves cards without mistakes."

## Compared with earlier ratings

| earlier | now | evidence |
|---|---|---|
| 1.1.0: nova-sprint existed, rating at docs/ratings/1.1.0/johnny/sprint-read.md | NEW | 1.2.0 re-rate |

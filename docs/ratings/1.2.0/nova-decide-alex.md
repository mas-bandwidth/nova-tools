# nova-decide READ and USE rating, nova-tools 1.2.0

Rater: Freddy (inception/mercury-2.5) via opencode, a sprint worker on a friends re-rate card
Build: bc9ee29ed2132
READ: 8/10
USE: 8/10

## Reasons

READ. The banner says what the tool is in one line, lists all 14 verbs, gives a working example block with five example lines that run as printed, and states the exit codes once. Every verb answers -h with its flags and an effect line. --max is the one cap flag across listings. An unknown verb lists the verbs; an unknown flag lists that verb flags flags and says what each flag wants.

What keeps READ at 8. The "how it works" paragraph is a compressed pseudo-run (state R?, id=f) instead of a real printout from the first example. The jev backend is only glossed in the key refusal, not in the banner. Default flag values (calibrate --bars, findings --bar) are not shown in -h. Briefs minutes question options are not listed in help. The spec at docs/SPEC-NOVA-DECIDE.md is titled "trained from its own record" but the export and training verbs are not built. The records TLA+ model is still owed.

USE. Used for real on a throwaway directory in ~/freddy-bench/rerate-alex-decide-bb.w2~15.g3/trial. Created a schema, a state, and a fixed answers file. ASK worked, recorded the decision, and returned recorded=existing on replay with the same --op. --dry-run did not touch the record. CALIBRATE refused when there were no outcomes attached, which is correct behavior. Refusals name every missing flag in one turn, with remedies. Unknown flags suggest near matches.

What keeps USE at 8. The default id includes a timestamp hash, so the same ask run a second apart records twice instead of replaying (gate default default op already hashes content only). Remedy lines say run: nova-decide help instead of run: nova-decide verb -h. The minutes refusal does not list the six options. An import glob that matches nothing is IMPORT OK with every count 0.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | cmd/nova-decide/main.go:751 | Default id hashes RFC 3339 timestamp so same ask a second apart records twice | Hash schema and state only, like gate does | S |
| 2 | every deciding verb refusal | Remedy says run: nova-decide help instead of run: nova-decide verb -h | Point at the verb own own help | S |
| 3 | cmd/nova-decide/main.go:191 | Minutes question options not listed in help brief or in the not one of its options refusal | List the six options (under-10, 10-20, 20-45, 45-90, 90-180, over-180) | S |
| 4 | internal/decide/decide.go:111 | Problems is a switch so only one problem per question is reported | Test each rule separately and append every problem | S |
| 5 | internal/decide/import.go:136 | Import glob that matches nothing is IMPORT OK with count=0 | Refuse source glob that matches no file, or print matched=0 | S |
| 6 | docs/SPEC-NOVA-DECIDE.md:1 | Title says trained from its own record but train verbs are not built | Title it calibrated from its own record | S |
| 7 | docs/SPEC-NOVA-DECIDE.md:101 | Record TLA+ model is still owed | Land the module with Ask, Replay, Attach, Act | M |

## Good, keep
Refusals that name every missing flag in one turn with what each wants. The key refusal that prints the exact nova-secrets exec line. --op replay (recorded=existing). Record under file lock (concurrent asks work). Calibrate refuses a one-sided record. effect: on every verb. --json carrying refusal why and remedy.

## Compared with earlier ratings
| earlier | now | evidence |
|---|---|---|
| default id includes timestamp | STILL THERE | cmd/nova-decide/main.go:751 |
| banner how paragraph is pseudo-run | STILL THERE | cmd/nova-decide/main.go:59 |
| spec title claims training | STILL THERE | docs/SPEC-NOVA-DECIDE.md:1 |
| record model owed | STILL THERE | docs/SPEC-NOVA-DECIDE.md:101 |
| minutes options not listed | STILL THERE | cmd/nova-decide/main.go:191 |
| remedy is run: help | STILL THERE | ASK REFUSED output |

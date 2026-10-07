# nova-decide READ and USE rating, nova-tools 1.2.0

Rater: z-ai/glm-5.3-flashx in opencode, a sprint worker on a friend re-rate card
Build: 9a9f1412792d
READ: 8/10
USE: 8/10

No v1.2.0 tag exists on the forge yet, so this rates the release candidate at the head of sprint/mechanical-2026-10-02 carried on this card's branch. `nova-decide version` prints `nova-decide v1.0.1-0.20261007224944-9a9f1412792d linux/amd64 go1.26.6`. Built and run on a Linux bench, cold, in a throwaway directory inside the job directory; no live store, no server, no key, no network, the fixed backend.

## Reasons

READ. `nova-decide help` and all fourteen verbs' `-h` were read cold, then docs/SPEC-NOVA-DECIDE.md and the nova-decide section of docs/CLI.md. The banner's first line is the CLI row's sentence. The usage lists all fourteen verbs (twelve deciding, version, help) with full flags. The `example:` block is ten lines and every one ran as printed from a checkout root; the gate example rewrote its tracked testdata output byte-identical, leaving the tree clean. The exit codes are stated once and every verb quotes the table; every verb's `-h` gives its usage, an example, what each flag wants, and an `effect:` line that names delivery, local write or inspection. An unknown verb lists the verbs; an unknown flag lists that verb's flags, answers a misspelt one with its near miss and the remedy `run: nova-decide ask -h`.

What keeps READ at 8. The how-it-works paragraph ends in a compressed pseudo-run (`state R?`, `id=f`, cmd/nova-decide/main.go:59) instead of the line the tool really prints (`ASK OK id=t1 decision=ship backend=fixed verdict=LAND p=0.8 tokens_in=0 tokens_out=0 recorded=new`). The banner says "jev or fixed answers:" and only the key refusal says jev is a hosted model wanting `JEV_API_KEY`. Defaults the code sets are not stated in `-h`: calibrate `--bars` is 0.5,0.7,0.9 (main.go:240) and findings `--bar` is 0.5 (main.go:317), while `--since` and `--max` state theirs. Brief's `minutes` question names none of its six options (main.go:191, internal/decide/brief.go:82). The spec is still titled "trained from its own record" (docs/SPEC-NOVA-DECIDE.md:1) while section 1 says the export and training verbs are not built, and the record's TLA+ model is still owed (docs/SPEC-NOVA-DECIDE.md:107; tla/ holds no DecideRecord.tla).

USE. Used for real on a throwaway directory: my own schema (one noul, one choice), a state, and a fixed answers file. Ask recorded and printed one ANSWER line per question with every option's p; the same `--op` replayed as `recorded=existing` changing nothing; `--dry-run` wrote nothing (the record stayed two lines); `--state -` read stdin. Outcome attached labels, repeated the same one, and refused a conflicting label at exit 1 naming both. Calibrate refused a one-sided record, then printed auc, BAR and CATCH-ALL lines, and `--json` gave the same value as one object. Gate read a red go-test output into per-failure decisions with class p and a route; brief on a directory of three cards with `--max 2` gave two BRIEF CARD lines and a MORE line with shown/total; findings clustered the testdata record's classes; import `--dry-run` counted three verdicts and a second import added nothing. About a dozen refusals provoked: every missing flag in one turn, the unknown flag and verb, a one-sided answers file, jev with no key (the exact `nova-secrets exec` line), `--since yesterday`. Every one exited 2 and wrote nothing.

What keeps USE at 8. The default id hashes the RFC 3339 timestamp (cmd/nova-decide/main.go:753), so the same ask run twice without `--op`, a second apart, recorded twice (`recorded=new` both times); gate's content-only id shows the fix. The required-flag and record refusals point at `run: nova-decide help` instead of the verb's own `-h`, which the unknown-flag refusal already names. Brief's minutes options are in no help and no refusal. An import glob that matches nothing is `IMPORT OK` with every count 0.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | cmd/nova-decide/main.go:753 | Default id hashes the RFC 3339 timestamp so the same ask a second apart records twice without --op | Hash the schema and state only, as gate's id does | S |
| 2 | every deciding verb refusal | Required-flag and record refusals say run: nova-decide help, not the verb's own -h (the unknown-flag refusal already says ask -h) | Point at the verb's -h | S |
| 3 | cmd/nova-decide/main.go:191 | Brief's minutes options (under-10, 10-20, 20-45, 45-90, 90-180, over-180) are in no help and no refusal | List the six in the detail and in the choice refusal | S |
| 4 | internal/decide/decide.go:111 | Problems is a switch, so a question with two problems names one | Test each rule separately and append every problem | S |
| 5 | internal/decide/import.go:136 | An import glob that matches nothing is IMPORT OK with every count 0 | Refuse a source glob matching no file, or print matched=0 | S |
| 6 | docs/SPEC-NOVA-DECIDE.md:1 | Title says trained from its own record but the train verbs are not built | Title it calibrated against its outcome, the banner's words | S |
| 7 | docs/SPEC-NOVA-DECIDE.md:107 | The record's TLA+ model is still owed | Land tla/DecideRecord.tla over Ask, Replay, Attach, Act | M |

## Good, keep
Refusals that name every missing flag in one turn with what each wants. The jev key refusal that prints the exact `nova-secrets exec` line and says the key is never a flag or a file. `--op` replay (`recorded=existing`, nothing changes). `--dry-run` from the same code path writing nothing. Calibrate refusing a one-sided record, then AUC, BAR and CATCH-ALL. `effect:` on every verb naming delivery, local write or inspection. `--json` carrying the same value as one object. The MORE line with shown, total and the widening flag. Unknown flags answered with the near miss.

## Compared with earlier ratings
| earlier | now | evidence |
|---|---|---|
| default id includes timestamp | STILL THERE | cmd/nova-decide/main.go:753 |
| banner how paragraph is a pseudo-run | STILL THERE | cmd/nova-decide/main.go:59 |
| spec title claims training | STILL THERE | docs/SPEC-NOVA-DECIDE.md:1 |
| record model owed | STILL THERE | docs/SPEC-NOVA-DECIDE.md:107 |
| minutes options not listed | STILL THERE | cmd/nova-decide/main.go:191 |
| remedy is run: help | STILL THERE | ASK REFUSED output, ask with no flags |
| the other 1.2.0 rating of this tree, READ 7.5 USE 8 | READ 8 USE 8 | the ten examples and the refusals ran as that rater saw; the pseudo-run and the silent defaults keep it at 8 |
| the third 1.2.0 rating of this tree, READ 7 USE 7 | READ 8 USE 8 | same tree; the timestamp id that rater hit is finding 1, the rest of the distance is the jev gloss and the minutes options |

# nova-decide READ and USE rating, nova-tools 1.2.0

Rater: OpenAI GPT-5.6 Luna (OpenCode harness)
Build: 65416146b5bb
READ: 7/10
USE: 7/10

## Reasons

READ. I read `nova-decide help`, every verb's `-h`, and docs/SPEC-NOVA-DECIDE.md sections 1 through 7 cold. The help is unusually complete: it names the typed schema, gives runnable examples, describes the record and replay, lists exit codes, and every verb has an effect line. The fixed backend makes a real first run possible without a key, network, store, or server. The first confusion is the banner's pseudo-transcript: `state R?` and `id=f` are not a real invocation or complete output, and the line omits the `tokens_in` and `tokens_out` fields that the binary prints. The second is that `jev` is not glossed in the banner as the hosted TypeSafe backend needing `JEV_API_KEY`; a cold reader must reach the spec or provoke the refusal. The spec title still says trained even though export and training verbs are explicitly not built. Help also omits calibrate and findings defaults, leaves brief's minutes options unnamed, and gives grade a usage line that omits flags documented below it.

USE. I built the binary and used it for real in a throwaway directory with the fixed backend and no live store or server. Ask, read, score, attempt, grade, brief, outcome, findings and the refusal paths for gate, calibrate, import and score-grades were exercised. Ask replay with an explicit `--op` returned `recorded=existing` with the same answers; read produced a LAND result and answers; score, attempt and grade produced typed results; brief recorded a card; outcome attached a label and a dry-run conflicting label incorrectly reported success. The biggest practical stumble was gate: its documented output path had to be created first, then it refused an empty file because it contained no failing test. Brief has no `--op`, despite the shared help saying every write verb has the replay shape and the other deciding verbs accepting it. Refusals generally point to the generic `nova-decide help`, not the verb-specific help or a command that fixes the input. Import reports success for a glob matching no files, which can silently import nothing. Calibrate correctly refused a one-sided record, but its defaults are not printed in `-h`.

A 10 would make the banner a real copy-paste transcript, explain jev at the first screen, align the spec title with the verbs that exist, make every refusal's remedy specific and runnable, create gate output files or state that they must exist, give brief a stable operation id, make outcome dry-run enforce the same label conflict as a real run, and refuse empty or unmatched import sources instead of returning green counts.

## Findings

| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | cmd/nova-decide/main.go:56-60 | The banner's how-it-works block uses the pseudo-run `state R?`, `id=f` and a shortened ASK line that the binary does not print, so the first example teaches output that cannot be reproduced. | Replace it with the actual fixture command and its real ASK OK and ANSWER output, including token fields. | S |
| 2 | cmd/nova-decide/main.go:57; docs/SPEC-NOVA-DECIDE.md:52 | The banner says `jev or fixed answers` without saying that jev is the hosted TypeSafe backend or that it needs JEV_API_KEY; the cold reader learns this only from the spec or a key refusal. | Gloss jev in the banner and name the nova-secrets command needed to provide its key. | S |
| 3 | docs/SPEC-NOVA-DECIDE.md:1 | The spec calls the system trained from its own record while sections 1 and 4 say export and training are not built, creating a false promise about the shipped tool. | Title the current system calibrated from its own record until a training/export verb exists. | S |
| 4 | nova-decide gate --output <file> | Gate refuses when its required output path does not already exist, even though `--output` is described as the gate's output file and other callers naturally provide a new path. | Create the output file as part of the gate input path, or document and enforce that the file must pre-exist before the run. | S |
| 5 | nova-decide brief -h; docs/SPEC-NOVA-DECIDE.md:194-197 | Brief rejects `--op` while the shared help says every verb takes the standard replay flags and every other deciding write accepts it; a brief retry therefore has no caller-chosen id. | Accept `--op` for brief and use it in the recorded decision id, or remove the shared claim and explain why batch brief is different. | S |
| 6 | nova-decide outcome --dry-run | A recorded `ok` outcome followed by `outcome --label wrong --dry-run` prints `OUTCOME OK ... recorded=no` with exit 0, while the real call exits 1 for the same conflict. | Check the existing label during dry-run and return the same conflict and exit 1 without writing. | S |
| 7 | cmd/nova-decide/main.go refusal paths | Input refusals commonly end with `run: nova-decide help`, which sends an AI back to the full banner instead of the verb's flags or a command that changes the bad input. | Make each refusal name `nova-decide <verb> -h` or a concrete corrected command as its remedy. | S |
| 8 | nova-decide import --verdicts '<glob with no matches>' | A source glob matching no files prints `IMPORT OK` with all counts zero and exit 0, so a typo in a source can silently train nothing. | Refuse an unmatched source glob or print a non-success result that explicitly names matched=0 for that source. | S |
| 9 | nova-decide calibrate -h; nova-decide findings -h | The binary uses calibrate bars 0.5,0.7,0.9 and findings bar 0.5, but neither help page states those defaults; the reader cannot know the scoring threshold without source or trial. | Print the actual defaults in the relevant flag descriptions. | S |
| 10 | nova-decide brief -h; internal/decide/brief.go:81 | Brief names the minutes question but does not list its legal options, so a fixed answers file can fail with an option error that does not tell the AI what values to try. | List under-10, 10-20, 20-45, 45-90, 90-180 and over-180 in help and the refusal. | S |

## Good, keep

The fixed backend is a strong no-infrastructure path for a cold AI. The help has runnable examples, a useful exit table, typed answer lines and explicit effects. Required flags are collected in one refusal, unknown flags offer a near-name suggestion, and the jev key refusal gives the exact `nova-secrets exec --only JEV_API_KEY` shape. Explicit `--op` replay is exact and idempotent, outcome attach-once behavior is clear in the normal path, and dry-run writes nothing for the exercised decision verbs. Broken records and schema-mismatched answers are refused rather than guessed.

## Compared with earlier ratings

| earlier | now | evidence |
|---|---|---|
| The banner's how-it-works block is a pseudo-run (1.1.0 READ) | STILL THERE | The current help still prints `state R?`, `id=f` and a shortened ASK line. |
| No-`--op` replay folds the clock into the id (1.1.0 USE) | NOT RECHECKED | This run used explicit `--op` for ask and confirmed replay; the no-`--op` identity behavior was not relied on for the rating. |
| The spec title claims training while training verbs are absent (1.1.0 READ) | STILL THERE | docs/SPEC-NOVA-DECIDE.md:1 and sections 1/4 still make that distinction. |
| Refusals use the generic `run: nova-decide help` remedy (1.1.0 USE) | STILL THERE | Gate, calibrate, score-grades and the tested input refusals all point to the generic help. |
| Fixed-backend effects were classed as delivery (1.1.0 USE) | STILL THERE | Every deciding verb's help still says `effect: delivery`, although fixed writes only the local record. |
| Calibrate's scoring direction is undocumented (1.1.0 READ) | CHANGED | Calibrate help now explains the scored probability and the positive labels. |

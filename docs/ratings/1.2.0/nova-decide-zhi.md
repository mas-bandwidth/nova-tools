# nova-decide READ and USE rating, nova-tools 1.2.0

Rater: Claude Opus 5.5 in Claude Code, a sprint worker on a friend's re-rate card
Build: 88bcd46a21ad
READ: 7/10
USE: 7/10

No v1.2.0 tag exists on the forge yet, so this rates the release candidate at the head of sprint/mechanical-2026-10-02. `nova-decide version` prints `nova-decide v1.0.1-0.20261006151533-88bcd46a21ad linux/amd64 go1.26.6`. Built and run on a Linux bench machine in a scratch directory holding a copy of cmd/nova-decide/testdata; no live store, no server, no model called. Every verb was run with the fixed backend; jev was run only as far as its key refusal and its `--dry-run`.

## Reasons

READ. The banner says what the tool is in one line, glosses noul and choice, and its ten examples run as printed from a checkout root. Every verb answers `-h` and `help <verb>` with its banner excerpt, a paragraph on what the decision asks, flags with a required marker, the exit codes and an `effect:` line that names what is sent beyond the machine and what is appended. Unknown verbs and flags list the full set and point at `-h`. The jev key refusal is the best line in the tool: it names the variable, the one command that sets it, and that the key is never a flag or a file.

What keeps READ at 7. The banner's how-it-works is still the 1.1.0 pseudo-run (cmd/nova-decide/main.go:56): a schema no fixture holds, a state written `R?`, and an `ASK OK id=f ... recorded=new` line the tool does not print (it prints `tokens_in=0 tokens_out=0` too). The help never says what jev is; only the spec does (docs/SPEC-NOVA-DECIDE.md:52, "TypeSafe's System One model"). The banner promises "A verb that lists takes --max" (pkg/tool/tool.go:512) and findings, which lists FINDING lines, refuses `--max`. grade's usage line (cmd/nova-decide/main.go:137) omits the `--examples`, `--held-out` and `--seed` its `-h` documents. import and score-grades carry no example. import's help has a garbled clause ("one nothing answers is counted as unanswered", cmd/nova-decide/main.go:257). The spec's output section (docs/SPEC-NOVA-DECIDE.md:181) says a refusal is one line and a backend failure is `<VERB> FAIL`; the binary prints one REFUSED line per problem and `FAILED`, and the spec's `--dry-run` list omits score and import, which take it. The record's model is still owed (docs/SPEC-NOVA-DECIDE.md:107, `tla/DecideRecord.tla` absent), so the one-writer, one-outcome claims the tool's trust rests on are unchecked.

USE. The first run is clean: ask, read, score, attempt, grade, gate, brief, outcome, calibrate and findings all exit 0 on the fixtures with typed lines, and `--json` gives the same result as one object. `--op` makes a repeat `recorded=existing` with the same answers; the same op id over a different decision refuses. outcome is attach-once: the same label is `changed=false`, another label is `OUTCOME FAILED ...; an outcome is attached once`, exit 1. `--dry-run` writes nothing (the record compared byte for byte). A record with a bad line refuses naming `./c.jsonl:13`. Answers outside [0, 1], an unknown option, a missing answer, a bad schema type, an empty schema and an empty state are all refused or failed with the problem named. The record is created 0600. import is idempotent by source path and content. gate's `--bars` checks that the two bars cannot both be met.

What keeps USE at 7. Things that should fail pass. `outcome --dry-run` on a labelled decision with a different label prints `OUTCOME OK ... recorded=no dry_run=true`, exit 0, where the real run is exit 1 (cmd/nova-decide/main.go:847). A choice's probabilities are not checked as a distribution: `question:0.9,report:0.9,request:0.9` is recorded, and a choice of `report` with `p=question:0.5,report:0.1` is recorded with the value disagreeing with its own highest p (pkg/decide/decide.go:138). `read --diff /dev/null` is `READ OK verdict=LAND`, recorded, while ask refuses an empty state. A missing record is read as empty (pkg/decide/record.go:82), so `findings --record ./missing.jsonl` is `FINDINGS OK scored=0` exit 0, and calibrate on it says "the record holds no decision named read" rather than that the file is absent; `import --verdicts 'nomatch/*'` is `IMPORT OK` with every count 0. The remedies are weak: every verb's refusal ends `run: nova-decide help`, never `help <verb>`; a backend failure for an empty state or an out-of-range p ends `run: make --answers answer every question of the schema` (cmd/nova-decide/main.go:778), which is not a command and not the fix; gate's bar refusal names internal keys `decide_gate_flaky` and `decide_gate_preexisting` (pkg/decide/gate.go:321); score-grades on a bad log prints a Go type, `struct { Lines []decide.LogLine "json:\"lines\"" }`. The four 1.1.0 USE carry-overs checked are still there.

A 10 would make the banner's how lines the fixture's own two printed lines, say in the help what jev is, make every refusal's `run:` a runnable `nova-decide help <verb>`, check a dry-run outcome against the recorded label, refuse a choice whose probabilities do not sum to about 1 or whose value is not its highest p, refuse an empty diff, refuse a named record or glob that does not exist, give findings `--max`, bring the spec's output grammar to the printed lines, and land the record's TLA+ model.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | `nova-decide outcome --record r.jsonl --id card-1 --label wrong --dry-run` | card-1 is labelled ok; the dry run prints `OUTCOME OK id=card-1 label=wrong recorded=no dry_run=true`, exit 0; the real run is `OUTCOME FAILED`, exit 1 (cmd/nova-decide/main.go:847 checks only that the id exists). | Check the recorded label in the dry run and return the same conflict and exit 1. | S |
| 2 | `nova-decide ask ... --answers bad4.json` | A choice answer `p=question:0.9,report:0.9,request:0.9` is recorded; pkg/decide/decide.go:162 checks each p in [0, 1] and never the sum. | Refuse a choice whose probabilities do not sum to 1 within a stated tolerance. | S |
| 3 | `nova-decide ask ... --answers bad2.json` | A choice of `report` with `p=question:0.5,report:0.1,request:0.4` is recorded and printed `value=report`; the value is not its own highest p, so calibrate and the printed value disagree. | Refuse a choice whose value is not the option with the highest p, or name the rule in the spec. | S |
| 4 | `nova-decide read --card card.md --diff /dev/null ...` | An empty diff is `READ OK verdict=LAND`, recorded; ask refuses an empty state ("a decision is made over evidence", pkg/decide/decide.go:184). | Refuse an empty --diff in read, score and gate as ask refuses an empty state. | S |
| 5 | `nova-decide findings --record ./missing.jsonl` | `FINDINGS OK scored=0 classes=0` exit 0 for a file that does not exist; Load reads absent as empty (pkg/decide/record.go:82). | Refuse a named --record that does not exist in the reading verbs (findings, calibrate, score-grades); keep create-if-absent for the writers. | S |
| 6 | `nova-decide calibrate --record ./missing.jsonl --decision read ...` | Refuses "the record holds no decision named read"; the file is absent and the line does not say so. | Name the missing file, as finding 5. | S |
| 7 | `nova-decide import --record i.jsonl --verdicts 'nomatch/*'` | `IMPORT OK` with every count 0, exit 0; a glob matching nothing is a green over nothing. | Refuse, exit 2, when a given glob or directory matches no file. | S |
| 8 | cmd/nova-decide/main.go:778 | An empty state, an out-of-range p and an unknown option all fail with `run: make --answers answer every question of the schema`: not a command, and for an empty state not the fix. | Give a remedy per cause; for the fixed backend `run: nova-decide ask -h` with the answers shape. | S |
| 9 | every verb's refusal | Each ends `run: nova-decide help`, the whole banner, never `nova-decide help <verb>`; unknown flags alone point at `<verb> -h`. Spec and 1.1.0 asked for the verb. | Make the default remedy `nova-decide help <verb>`. | S |
| 10 | pkg/decide/gate.go:321 | `--bars 0.3,0.3` refuses naming `decide_gate_flaky` and `decide_gate_preexisting`, sprint-row keys the gate's help never mentions. | Name the flag's two parts, the flaky and the pre-existing bar. | S |
| 11 | pkg/decide/gradescore.go:55 | `score-grades --log log.json` holding `[]` refuses with `json: cannot unmarshal array into Go value of type struct { Lines []decide.LogLine "json:\"lines\"" }`. | Say what shape is wanted, `{"lines": [...]}`, as import.go:168 already does. | S |
| 12 | cmd/nova-decide/main.go:56 | The banner's how-it-works is a pseudo-run: a schema with `q`/`ok`, state `R?`, and `ASK OK id=f decision=q backend=fixed recorded=new`, a line the tool does not print. Carried from 1.1.0. | Quote the fixture run's own OK and ANSWER lines. | S |
| 13 | `nova-decide help` | Says "jev or fixed answers" and never what jev is or that it needs JEV_API_KEY; only the spec does (docs/SPEC-NOVA-DECIDE.md:52). | One line in the banner: jev is TypeSafe's model, called over the network with JEV_API_KEY from nova-secrets. | S |
| 14 | pkg/tool/tool.go:512 | The banner says "A verb that lists takes --max"; `findings --max 1` is an unknown flag though findings lists FINDING and SHADOW lines. | Give findings --max and a MORE line. | S |
| 15 | cmd/nova-decide/main.go:137 | grade's usage line omits `--examples`, `--held-out` and `--seed`, which `grade -h` documents. | Add them to the usage line. | S |
| 16 | `nova-decide import -h`, `nova-decide score-grades -h` | No example line; every other deciding verb has one. | Add a fixture-backed example to each. | S |
| 17 | cmd/nova-decide/main.go:257 | import's help: "one nothing answers is counted as unanswered, not recorded". | "a judgment the log does not answer is counted as unanswered, not recorded". | S |
| 18 | `nova-decide calibrate -h`, `nova-decide findings -h` | calibrate reports bars 0.5, 0.7, 0.9 with no --bars and findings uses bar 0.5 with no --bar; neither -h states a default. Carried from 1.1.0. | State the defaults in the flags' usage. | S |
| 19 | `nova-decide ask ... --dry-run` | Prints only the OK line; the ANSWER lines a real run prints are absent. Carried from 1.1.0. | Print the ANSWER lines a fixed backend would give in a dry run. | S |
| 20 | `nova-decide help --json` | Refuses `unknown verb "--json"`, exit 2; the banner says every verb takes --json. Carried from 1.1.0. | Let help take --json. | S |
| 21 | `nova-decide brief -h` | Names the minutes question and none of its options (`under-10`, `10-20`, `20-45` are in the fixture only). Carried from 1.1.0. | List the options in -h. | S |
| 22 | docs/SPEC-NOVA-DECIDE.md:181 | Says a refusal is one line and a backend failure is `<VERB> FAIL`; the binary prints one REFUSED line per problem (`nova-decide ask` prints four) and `ASK FAILED`. Its --dry-run list omits score and import. | Make the grammar the printed lines. | S |
| 23 | docs/SPEC-NOVA-DECIDE.md:107 | The record's model is owed: `tla/DecideRecord.tla` does not exist, so one decision per id and at most one outcome per decision are unchecked. Carried from 1.1.0. | Land the model with TLC records, without waiting on the export verb. | M |
| 24 | pkg/decide/gate.go:392 | gate still calls Make once per failure, each loading and rewriting the record, beside batch.MakeAll. Carried from 1.1.0. | Ask the failures in one MakeAll. | M |
| 25 | docs/USAGE.md | The adoption guide still names nova-decide nowhere (0 occurrences). Carried from 1.1.0. | Add a row in Choosing a tool. | S |
| 26 | docs/CLI.md:2559 | The first run writes `./decisions.jsonl` into the checkout root, against the README's `./trial-` rule. Carried from 1.1.0. | Use `--record ./trial-decisions.jsonl` in the banner and the reference. | S |

## Good, keep

The jev key refusal (`reason=key_absent: JEV_API_KEY is absent from this environment; run under nova-secrets exec --only JEV_API_KEY -- nova-decide ...`) is the model for every refusal. `--op` idempotence is exact: the same op returns the recorded answers, a reused op over another decision refuses. outcome is attach-once with exit 1 on a conflict. A record with a bad line refuses naming file and line, and the record is created 0600. Every verb's `-h` carries an `effect:` line that names what leaves the machine. Required-input refusals now use the one `<VERB> REFUSED: ...; run:` grammar and name every missing flag in one run. The fixed backend makes the whole tool usable with no network.

## Compared with earlier ratings
| earlier | now | evidence |
|---|---|---|
| the banner's how lines are a pseudo-run (READ 1) | STILL THERE | cmd/nova-decide/main.go:56 |
| the command reference is one prose wall (READ 2) | CHANGED | docs/CLI.md section is wrapped prose of about 75 characters a line, still one block per several verbs |
| gate asks one Make per failure (READ 3) | STILL THERE | pkg/decide/gate.go:392 |
| noul undefined in the reference (READ 4) | STILL THERE | docs/CLI.md:2573 uses `noul` with no gloss in the section |
| USAGE.md never introduces the tool (READ 5) | STILL THERE | `grep -c nova-decide docs/USAGE.md` is 0 |
| first run writes ./decisions.jsonl (READ 6) | STILL THERE | docs/CLI.md:2559 |
| gate re-rolls readFiles (READ 7) | STILL THERE | cmd/nova-decide/main.go:568 loops output, card, diff itself |
| headline answer guessed from a name list (READ 8) | STILL THERE | cmd/nova-decide/main.go:799 |
| brief -h names no minutes options (USE 1) | STILL THERE | `brief -h` says "minutes" only |
| dry run prints no ANSWER lines (USE 2) | STILL THERE | `ask ... --dry-run` prints `ASK OK ... recorded=no dry_run=true` alone |
| calibrate's noul direction unstated (USE 3) | CHANGED | calibrate -h now says it scores "a noul's yes"; that a higher p counts as flagged is still implicit |
| default bars unstated (USE 4) | STILL THERE | calibrate -h and findings -h give no default |
| help --json refused (USE 5) | STILL THERE | `nova-decide help --json` exit 2, `unknown verb "--json"` |

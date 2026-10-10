# nova-decide READ and USE rating, nova-tools 1.2.0

Rater: Zhi, a sprint worker on a friend's re-rate card (deepseek/deepseek-v4.1-flash under dsh, the DeepSeek Harness headless runner; the rating is this worker's, not the friend's)
Build: a9fe2a7fd927
READ: 7.5/10
USE: 7/10

This rates nova-decide at a9fe2a7fd927, the head of the release branch sprint/mechanical-2026-10-02. `nova-decide version` prints `nova-decide v1.0.1-0.20261007223734-a9fe2a7fd927 linux/amd64 go1.26.6`. Built and run on a Linux bench machine in a throwaway directory holding a copy of cmd/nova-decide/testdata; no live store, no server, no model called. All thirteen verbs were opened (`help` and every verb's `-h`), docs/SPEC-NOVA-DECIDE.md and the nova-decide section of docs/CLI.md were read cold, every deciding verb ran on the fixtures with the fixed backend, the train and listing verbs ran on the fixture record, and about twenty refusals were provoked. jev ran only as far as its key refusal and its `--dry-run`.

## Reasons

READ. The banner says what the tool is in one line, and the first-run block's ten command lines run as printed from a checkout root. Every verb answers `-h` with its usage, an example where it has one, a paragraph on what the decision asks, the flags with required markers, the exit table and an `effect:` line naming what leaves the machine and what it appends. Unknown verbs and flags list the full set, and the unknown-flag refusal names the nearest. Missing flags are named all at once with what each wants. The jev key refusal is the best line in the tool: it names the variable, the one command that sets it, and that the key is never a flag or a file. The fixed backend makes the whole tool usable with no network.

What keeps READ at 7.5. The banner's how paragraph is still the 1.1.0 pseudo-run (cmd/nova-decide/main.go:56): a state written `R?` and an `ASK OK id=f decision=q backend=fixed recorded=new` line the tool does not print, because the real line adds `tokens_in=0 tokens_out=0`. The banner never glosses jev (cmd/nova-decide/main.go:57); a cold reader learns it is a hosted model called with JEV_API_KEY only from the key refusal. grade's usage line (cmd/nova-decide/main.go:137) and both top-level usage blocks omit `--examples`, `--held-out` and `--seed` that `grade -h` documents. calibrate's `--bars` default 0.5,0.7,0.9 (cmd/nova-decide/main.go:240) and findings' `--bar` default 0.5 (cmd/nova-decide/main.go:317) are printed by no help text. brief's help names the minutes question and none of its six options (cmd/nova-decide/main.go:191). `help --json` is refused as an unknown verb and `help ask --json` silently ignores the flag, while the banner says every verb takes `--json`. The banner promises `--max` to every listing verb, but findings refuses it. The spec's title and the file's opening line still say trained from its own record while section 1 says the export and training verbs are not built, and the record's model is still owed. The spec's refusal grammar (one line, `<VERB> FAIL`) is not the printed one (one `REFUSED` line per problem, `ASK FAILED`).

USE. The first run is clean: ask, read, score, attempt, grade, gate, brief, outcome, calibrate, findings, import and score-grades all run on the fixtures and print typed lines, and `--json` gives the same value as one object on every verb tried, refusals included. A missing `--op` is not the only idempotence: with an explicit `--op`, a second ask, read or gate returns `recorded=existing` and adds no line, and gate's dry run names each failure's held class. `--dry-run` writes nothing (the record file stayed absent). The record is created 0600 and a broken line is refused naming file and line. Refusals exit 2 and an outcome conflict exits 1, and every refusal carries a remedy.

What keeps USE at 7. Defects that a real run meets:
- the default id folds the clock in (cmd/nova-decide/main.go:751), so the same ask run twice without `--op` a second apart records two decisions; and two different fixed answer sets over one schema and state inside one second share the id, so the second ask prints `recorded=existing` with the *first* answer set and the second backend answer is silently discarded (one line in the record). gate's default op (cmd/nova-decide/main.go:597) already hashes content only.
- a choice's probabilities are not checked as a distribution and its value need not be its highest-p option: `p=question:0.9,report:0.9,request:0.9` is recorded, and a choice of `report` with `p=question:0.5,report:0.1,request:0.4` is recorded although the printed value disagrees with its own probabilities.
- `read --diff` over an empty file is `READ OK verdict=LAND`, recorded, while ask refuses an empty state.
- a named `--record` that does not exist is read as empty: findings is `FINDINGS OK scored=0` exit 0, and calibrate says "the record holds no decision named read" rather than that the file is absent.
- `import` with a source glob that matches nothing is `IMPORT OK` with every count 0, exit 0.
- an answer row for a question that was not asked is dropped by the fixed backend before the schema check, so a `ghost` row is accepted, against the spec's "nothing that was not asked".
- score-grades on a wrongly shaped log prints a Go type, not the shape it wants.
- the fixed backend's failure remedy is `make --answers answer every question of the schema`, which is not a command.
- every verb's remedy is `run: nova-decide help`, the whole banner, never `nova-decide <verb> -h`; only the unknown-flag refusal names the verb's help.
- `ask --dry-run` prints only its plan line, not the ANSWER lines a fixed run would record.
- the gate asks one `Make` per failure, each loading and rewriting the record, beside the batch built for that.

A 10 needs: a default id that is a function of the schema and the state only; every remedy a runnable command that changes the failing input; option names in help and in the "not one of its options" refusals; flag defaults printed by `-h`; the how paragraph quoting a real run and jev glossed; `--json` on help and `--max` on findings; a choice checked as a distribution and against its own top option; an empty diff, a missing record and an empty import source refused; the spec's grammar and title made the printed and built ones; and the record's model landed.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | cmd/nova-decide/main.go:751 | with no `--op` the id hashes the RFC 3339 stamp, so the same ask a second later records a second decision (`reply-70571f4c3c43` and `reply-79879ea58e45`, two lines); worse, two different fixed answer sets over one schema and state inside one second share the id, so the second ask prints `recorded=existing` with the first answer set and the second is discarded (one line) | hash the schema and the state only, as gate's default op already does (main.go:597), and say in help that a repeat returns the recorded decision | M |
| 2 | pkg/decide/decide.go:162 | a choice's probabilities are checked one by one in [0, 1] and never as a distribution: `p=question:0.9,report:0.9,request:0.9` is recorded | refuse a choice whose probabilities do not sum to one within a stated tolerance | S |
| 3 | pkg/decide/decide.go:145 | a choice's value is not held to its own highest probability: `value=report p=question:0.5,report:0.1,request:0.4` is recorded, so the printed value disagrees with the probabilities calibrate reads | refuse a value that is not the top option, or name the rule in the spec | S |
| 4 | cmd/nova-decide/main.go:847 | `outcome --dry-run` checks only that the id exists: a second label prints `OUTCOME OK ... recorded=no` exit 0 where the real run prints `OUTCOME FAILED ...; an outcome is attached once` exit 1 | run the recorded-label conflict check in the dry run and return the same exit | S |
| 5 | pkg/decide/decide.go:183 | an empty `--diff` in read is `READ OK verdict=LAND`, recorded, while ask refuses an empty state | refuse an empty diff in read, score and gate as ask refuses an empty state | S |
| 6 | pkg/decide/record.go:80 | a named `--record` that does not exist is loaded as empty: `findings --record missing.jsonl` is `FINDINGS OK scored=0` exit 0, and calibrate says the record holds no such decision rather than the file is absent | refuse a named record that does not exist in the reading verbs; keep create-if-absent for the writers | S |
| 7 | pkg/decide/import.go:136 | a source glob that matches no file is `IMPORT OK` with every count 0, exit 0, so a mistyped `--verdicts` imports nothing silently | refuse a source glob that matches no file, or print matched=0 per source | S |
| 8 | cmd/nova-decide/main.go:778 | the fixed backend's failure remedy is `run: make --answers answer every question of the schema`, which is not a command and does not name the answers shape | remedy per cause: `nova-decide ask -h`, with the answers shape | S |
| 9 | every verb's refusal | each ends `run: nova-decide help`, the whole banner, never `nova-decide help <verb>`; only the unknown-flag refusal points at `<verb> -h` | make the default remedy `nova-decide <verb> -h` | S |
| 10 | cmd/nova-decide/main.go:56 | the how paragraph is a pseudo-run (`state R?`, `ASK OK id=f decision=q backend=fixed recorded=new`) whose printed line the tool does not print and which never names the record file | quote the fixture run's own OK and ANSWER lines, or name schema, state, backend and record and where each lives | S |
| 11 | cmd/nova-decide/main.go:57 | the banner says "jev or fixed answers" and never glosses jev or that it needs JEV_API_KEY; only the key refusal says so | one clause: jev is the hosted model, called with JEV_API_KEY under nova-secrets exec; fixed is a local answers file | S |
| 12 | cmd/nova-decide/main.go:137 | grade's usage line omits `--examples`, `--held-out` and `--seed`, which `grade -h` documents | add them to grade's usage line | S |
| 13 | cmd/nova-decide/main.go:240 | calibrate's `--bars` default 0.5,0.7,0.9 and findings' `--bar` default 0.5 (main.go:317) are printed by no help text | print each flag's default in the `-h` flag list | S |
| 14 | cmd/nova-decide/main.go:191 | brief's help names the minutes question and none of its six options (`under-10`, `10-20`, `20-45`, `45-90`, `90-180`, `over-180`) | list the options in help brief and in every "not one of its options" refusal | S |
| 15 | `nova-decide help --json` | refused as `unknown verb "--json"` exit 2, and `help ask --json` silently ignores the flag, while the banner says every verb takes `--json` | accept `--json` on help, or refuse an unknown flag after the verb | S |
| 16 | pkg/tool/tool.go:522 | the banner promises every listing verb takes `--max`, but `findings --max 1` is an unknown flag though findings lists FINDING lines | give findings `--max` and a MORE line | S |
| 17 | pkg/decide/decide.go:111 | `Problems` is a switch, so one problem per question is reported: a question with a bogus type and no instructions is refused only for the instructions, against the spec's "names every problem" | test each rule and append every problem | S |
| 18 | pkg/decide/backend.go:29 | the fixed backend answers only the schema's questions, so an answer row for a question that was not asked is dropped before the schema check and a `ghost` row is accepted, against the spec's "nothing that was not asked" | check the fixed table's keys too, or write in the spec that extra rows are ignored | S |
| 19 | pkg/decide/gradescore.go:55 | a `--log` that is JSON but not a nova-sprint export refuses with the Go type `struct { Lines []decide.LogLine "json:\"lines\"" }` | say the shape wanted, `{"lines": [...]}` | S |
| 20 | cmd/nova-decide/main.go:257 | import's help clause "one nothing answers is counted as unanswered, not recorded" is ungrammatical | "a judgment no line answers is counted as unanswered, not recorded" | S |
| 21 | pkg/decide/gate.go:392 | the gate asks its failures one `Make` at a time, each loading and rewriting the whole record, beside the batch built for exactly that | ask the failures in one `MakeAll`: one read, one write | M |
| 22 | cmd/nova-decide/main.go:1 | the file opens calling this a system trained from its own record while section 1 says the export and training verbs are not built | say calibrated from its own record until a train verb exists | S |
| 23 | docs/SPEC-NOVA-DECIDE.md:107 | the record's model is owed: `tla/DecideRecord.tla` does not exist, so one decision per id and at most one outcome per decision are unchecked | land the module with TLC records | M |
| 24 | docs/SPEC-NOVA-DECIDE.md:189 | the spec says a refusal is one line naming every problem and a backend failure is `<VERB> FAIL`; the binary prints one `REFUSED` line per problem (`ask` prints four) and `ASK FAILED` | make the spec the printed grammar | S |
| 25 | docs/SPEC-NOVA-DECIDE.md:194 | the `--dry-run` list (ask, read, attempt, grade, gate, brief, outcome) omits score and import, which both take `--dry-run` | add score and import to the list | S |
| 26 | cmd/nova-decide/main.go:758 | `ask --dry-run` prints its plan line alone; the ANSWER lines a fixed run would record are absent, so the answers cannot be previewed without recording a decision | print the answers a fixed run would record, and say a jev run needs the real call | S |
| 27 | `nova-decide import -h`, `nova-decide score-grades -h` | neither carries an example line, where every deciding verb has one | add a fixture-backed example to each | S |

## Good, keep

The jev key refusal (`reason=key_absent: JEV_API_KEY is absent from this environment; run under nova-secrets exec --only JEV_API_KEY -- nova-decide ...`) is the model for every refusal. Missing flags are named all at once with what each wants, an unknown flag names the verb's own flags and the nearest, and an unknown verb lists them all. `--op` replay is exact: the same op returns the recorded result with no new line, and gate's dry run names each failure's held class. A record with a bad line is refused naming file and line, the record is created 0600, and an outcome conflict is exit 1. Every verb's `-h` carries an `effect:` line, and `--json` gives one object from the same value as the lines. The fixed backend makes the whole lifecycle runnable with no network and no key.

## Compared with earlier ratings
| earlier | now | evidence |
|---|---|---|
| with no `--op` the id folded the clock in, so the same ask run twice records twice (1.1.0 READ and USE) | STILL THERE | cmd/nova-decide/main.go:751; two asks a second apart, two lines |
| the banner's how paragraph is a pseudo-run the tool does not print (1.1.0 READ) | STILL THERE | cmd/nova-decide/main.go:56; the real line adds tokens_in=0 tokens_out=0 |
| the file and the spec open calling this a system trained from its record while the train verbs are not built (1.1.0 READ) | STILL THERE | cmd/nova-decide/main.go:1; docs/SPEC-NOVA-DECIDE.md:1 and :19 |
| the record's model is owed (1.1.0 READ) | STILL THERE | docs/SPEC-NOVA-DECIDE.md:107; no decide module under tla/ |
| the gate asks one Make per failure beside the batcher (1.1.0 READ) | STILL THERE | pkg/decide/gate.go:392 |
| the headline answer is guessed from a fixed name list (1.1.0 READ) | STILL THERE | cmd/nova-decide/main.go:799 |
| the adoption guide never introduces the tool (1.1.0 READ) | STILL THERE | `grep -c nova-decide docs/USAGE.md` is 0 |
| the first-run examples write `./decisions.jsonl` into the checkout root (1.1.0 READ) | STILL THERE | cmd/nova-decide/main.go:66; README.md:37 and :95 use `./trial-` |
| `--dry-run` prints no ANSWER lines (1.1.0 USE) | STILL THERE | cmd/nova-decide/main.go:758; ask --dry-run prints `questions=2 state_bytes=101 recorded=no dry_run=true` alone |
| calibrate's and findings' defaults are unstated (1.1.0 USE) | STILL THERE | cmd/nova-decide/main.go:240 and :317 |
| `help --json` is refused or ignored (1.1.0 USE) | STILL THERE | `nova-decide help --json` exit 2, `unknown verb "--json"` |
| outcome's help shows only the one-word label (1.1.0 READ) | STILL THERE | cmd/nova-decide/main.go:221; the joined class form is docs/SPEC-NOVA-DECIDE.md:139 |

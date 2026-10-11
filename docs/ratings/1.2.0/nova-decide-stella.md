# nova-decide READ and USE rating, nova-tools 1.2.0

Rater: claude-opus-5-5 (Claude Code)
Build: 3a5792afa395
READ: 7.5/10
USE: 8/10

## Reasons
READ. `nova-decide help` and every verb's `-h` (all thirteen verbs and `help`) were read cold, then docs/SPEC-NOVA-DECIDE.md sections 1 to 7 and the nova-decide section of docs/CLI.md. No v1.2.0 tag exists yet, so the rated tree is the release branch head named by Build; `nova-decide version` prints `v1.0.1-0.20261006144833-3a5792afa395`. The help is the family's skeleton done well: one usage line per verb, one runnable example per deciding verb from the checkout root, an exit table, and an `effect:` line on every verb. The verb texts say what the state is and what each question asks. The first confusion is cmd/nova-decide/main.go:59: the how paragraph is still a compressed pseudo-run (`state R?`, `id=f`) and not a line the tool prints. The second is cmd/nova-decide/main.go:57, "jev or fixed answers": jev is never said to be a hosted model that needs `JEV_API_KEY` until the key refusal says so. The first doubt is docs/SPEC-NOVA-DECIDE.md:1, still titled "trained from its own record" while section 1 says the export and training verbs are not built, and the record's TLA+ model is still owed (docs/SPEC-NOVA-DECIDE.md:101; tla/ holds no decide module). The first boredom is the spec's sections 8 to 14, about 600 lines of sprint bindings that a caller of `ask` never needs, and they sit between the reader and nothing. Defaults the code sets are not printed by `-h`: calibrate's `--bars` is 0.5,0.7,0.9 (main.go:240) and findings' `--bar` is 0.5 (main.go:317), but both help texts are silent. Brief's `minutes` question names no options in the help, and grade's top usage line drops `--examples`, `--held-out` and `--seed`.

USE. Used for real on a Linux bench in a throwaway directory inside the job directory, with a scratch HOME, the fixed backend, no key, no network, no live store and no server. One schema of my own was written (a noul and a two-option choice), two states were asked, both were labelled, and the record was calibrated. Calibrate refused while the record was one-sided, then gave `auc=1`, three BAR lines and a CATCH-ALL line, and with `--question size=large --json` gave the same as one object. Every built-in decision ran on the testdata: read, score (with and without `--dry-run`), attempt, grade, gate (with no bars and with `--bars 0.8,0.8`), brief on one card and on a directory of three with `--max 2` (a MORE line), outcome, calibrate (`defect` and `verdict=BOUNCE`), findings (default bar and `--bar 0.05`), import (verdicts and reports, dry run, then twice to show idempotency), and score-grades. Thirty concurrent asks into one record gave thirty clean lines, and the record still loaded. About 20 refusals were provoked: no flags (all four missing flags in one turn), an unknown flag (`did you mean --op?`), an unknown verb, `--backend wat`, fixed with no `--answers`, `--answers` with jev, jev with no key (names the exact `nova-secrets exec` line), `--timeout 0`, a schema with no name and a one-option choice, an answers file with p=1.5 and an unknown option (all problems in one line), an op id reused over another state, a second label (exit 1, both labels named), a label for an unknown id, a misspelt option in calibrate, a broken record line (named by file and line, for reads and writes alike), and `--since yesterday`. Every one exited 2, wrote nothing, and said what it wanted.

The score is held at 8 by remedies and replay. With no `--op`, the same ask run twice a second apart recorded two decisions (`shipit-4728ccea5336`, `shipit-889971efec2c`) because the stamp is hashed into the id (main.go:751); gate's default op (main.go:597) already hashes content only. A misfit answers file is answered with `run: make --answers answer every question of the schema`, which is not a command. A brief whose answers misfit says `run the same line again` while nothing was recorded, so the retry repeats the failure. The minutes refusal does not list the six options. Most refusals point at `nova-decide help` instead of `nova-decide <verb> -h`. An import glob that matches nothing is `IMPORT OK` with every count 0, exit 0.

A 10 needs:
- the default id a function of the schema and the state only;
- every remedy a runnable command that changes the failing input;
- option names in the help and the refusal wherever a choice is answered by a fixed file;
- flag defaults printed by `-h`;
- the banner's how paragraph quoting a real run, and jev glossed;
- the record's model landed, and the spec titled for what is built.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | cmd/nova-decide/main.go:751 | with no --op the id hashes the RFC 3339 stamp, so the same ask a second later records a second decision (shipit-4728ccea5336 then shipit-889971efec2c), and on jev would ask twice; gate's default op (main.go:597) already hashes content only | hash the schema and the state only, as gate does, and say in help that a repeat returns the recorded decision | S |
| 2 | cmd/nova-decide/main.go:778 | a fixed answers file that misfits the schema prints `run: make --answers answer every question of the schema`, which is not a command; main.go:613 has the same shape for the class question | make the remedy a command (`nova-decide <verb> -h`) and name the answers file to fix | S |
| 3 | cmd/nova-decide/main.go:739 | brief with a misfit answers file prints `run: run the same line again: a recorded card is answered from the record` while the card line says recorded=no; the retry repeats the failure | when the failure is a schema misfit, name the answers file and the question's options as the remedy | S |
| 4 | cmd/nova-decide/main.go:191 | help brief names minutes but not its options (under-10, 10-20, 20-45, 45-90, 90-180, over-180, pkg/decide/brief.go:81), and the refusal `minutes chose "soon", not one of its options` does not list them either | list the options in help brief and in every "not one of its options" refusal | S |
| 5 | cmd/nova-decide/main.go:59 | the how paragraph is a shorthand pseudo-run (`state R?`, `id=f`) and not a line the tool prints; the first example's real output says `id=first ... tokens_in=0 tokens_out=0 recorded=new` | quote the first example's real two lines, or name the nouns (schema, state, backend, record) and where each lives | S |
| 6 | cmd/nova-decide/main.go:57 | "jev or fixed answers": jev is never glossed in the top help; a cold reader learns it is a hosted model that needs JEV_API_KEY only from the key_absent refusal | one clause: jev is the hosted model, asked with JEV_API_KEY under nova-secrets exec; fixed is a local answers file | S |
| 7 | cmd/nova-decide/main.go:240 | calibrate --bars defaults to 0.5,0.7,0.9 and findings --bar (main.go:317) to 0.5, brief --width, --max and every --timeout have defaults, but `-h` prints none of them | print each flag's default in the -h flag list | S |
| 8 | cmd/nova-decide/main.go:174 | `nova-decide help --json` is refused as unknown verb "--json" (exit 2), and `nova-decide help ask --json` silently ignores the flag, while the top help says every verb takes --json | accept --json on help, or refuse an unknown flag after the verb | S |
| 9 | nova-decide ask | missing flags, a bad --backend, --timeout 0 and missing files all end `run: nova-decide help`; only the unknown-flag refusal points at `nova-decide ask -h`, where the verb's flags are | make every verb refusal's remedy `nova-decide <verb> -h` | S |
| 10 | pkg/decide/decide.go:111 | Problems is a switch, so one problem per question is reported: a question with no instructions and type "bogus" is refused only for the instructions, and the type needs a second turn; spec section 2 says every problem in one error | test each rule separately and append every problem | S |
| 11 | nova-decide ask --schema nofile.json --state nofile.txt --answers nofile2.json | the refusal names the missing schema and state files but not the missing --answers file, so a third turn is needed | read every named input before refusing, as the flag check does | S |
| 12 | pkg/decide/import.go:136 | an import glob that matches no file is `IMPORT OK` with every count 0 and exit 0, so a mistyped --verdicts or --reports imports nothing silently | refuse a source glob that matches no file, or print matched=0 per source | S |
| 13 | nova-decide ask --backend fixed --dry-run | the dry run prints only the OK line, no ANSWER lines, though the fixed answers are known; `read --dry-run` on a recorded op does print them | print the answers a fixed dry run would record | S |
| 14 | nova-decide gate | with no --bars every failure prints class=flaky with route=caused, and GATE OK does not say the bars were unset, so the two words look like a contradiction | print bars=unset (or the two values) on GATE OK | S |
| 15 | cmd/nova-decide/main.go:67 | every deciding verb's effect is classed delivery even with --backend fixed, which only appends to the record; the text now says jev sends, but an AI screening by class still sees a send | class the effect local write, naming the send as jev's only | S |
| 16 | nova-decide outcome | a conflicting label is `OUTCOME FAILED ...: an outcome is attached once` at exit 1 with no `run:` remedy, the only result line without one | add a remedy: label another decision, or keep the recorded label | S |
| 17 | docs/SPEC-NOVA-DECIDE.md:192 | the spec says a backend failure prints `<VERB> FAIL id=...`; the tool prints `ASK FAILED id=...` | say FAILED in the spec | S |
| 18 | docs/SPEC-NOVA-DECIDE.md:195 | the dry-run list (ask, read, attempt, grade, gate, brief, outcome) omits score and import, which both take --dry-run | add score and import to the list | S |
| 19 | docs/SPEC-NOVA-DECIDE.md:1 | the title says "trained from its own record" while section 1 says the export and training verbs are not built | title it calibrated from its own record until a train verb exists | S |
| 20 | docs/SPEC-NOVA-DECIDE.md:101 | the record's model (tla/DecideRecord.tla) is still owed; tla/ holds no decide module, so the one-writer guarantee the record rests on is unchecked | land the module with Ask, Replay, Attach and Act and check it with TLC | M |
| 21 | docs/CLI.md:2542 | the first run writes ./decisions.jsonl into the checkout root while the README's rule for created files is a ./trial- name | use --record ./trial-decisions.jsonl in the first run | S |
| 22 | cmd/nova-decide/main.go:137 | grade's usage line omits --examples, --held-out and --seed, which grade -h documents | add them to the usage line | S |

## Good, keep
Refusals that name every missing flag in one turn with what each wants, `did you mean` for a near flag, and a key refusal that prints the exact `nova-secrets exec --only JEV_API_KEY` line.
A misfit answers file reported whole: p outside [0, 1], an unknown option and a missing probability in one line.
`--op` replay (`recorded=existing`), a repeated label (`changed=false`), a conflicting label at exit 1 naming both, and brief's per-card replay.
The record under its file lock: thirty concurrent asks gave thirty whole lines; a broken line is named by file and line before any read or write.
calibrate refusing a one-sided record and a misspelt option rather than printing a meaningless AUC.
`effect:` on every verb, and `--json` carrying a refusal's why and remedy.

## Compared with earlier ratings
| earlier | now | evidence |
|---|---|---|
| with no --op the id folds the clock in, so the same ask run twice records twice (1.1.0 READ and USE) | STILL THERE | cmd/nova-decide/main.go:751; two runs a second apart gave two ids |
| the banner's how paragraph is a pseudo-run whose printed line the tool does not print (1.1.0 READ) | STILL THERE | cmd/nova-decide/main.go:59 |
| the spec's title claims training while the train verbs are not built (1.1.0 READ) | STILL THERE | docs/SPEC-NOVA-DECIDE.md:1 |
| the record's model is owed (1.1.0 READ) | STILL THERE | docs/SPEC-NOVA-DECIDE.md:101; no decide module in tla/ |
| minutes' options named neither in help brief nor in the refusal (1.1.0 USE) | STILL THERE | cmd/nova-decide/main.go:191; the `soon` refusal lists none |
| a misfit answers remedy is `make --answers ...`, not a command; brief says run the same line again with nothing recorded (1.1.0 USE) | STILL THERE | cmd/nova-decide/main.go:778 and :739 |
| help trips on --json (1.1.0 USE) | STILL THERE | `nova-decide help --json` is unknown verb "--json", exit 2 |
| calibrate's default bars and findings' default bar undocumented (1.1.0 USE) | STILL THERE | main.go:240 and :317 set them; -h prints neither |
| --dry-run omits the answers a fixed run prints (1.1.0 USE) | STILL THERE | `ask --dry-run` prints only the OK line |
| GATE OK does not say the bars were unset (1.1.0 USE) | STILL THERE | `GATE OK ... route=caused` beside class=flaky |
| the effect line said delivery for a fixed run (1.1.0 USE) | CHANGED | main.go:67 now says jev sends; the class is still delivery |
| calibrate's scoring direction undocumented (1.1.0 USE) | FIXED | calibrate -h: --positive is "the outcome labels the answer should flag", scored by the p its answer gave |
| the first run wrote ./decisions.jsonl into the checkout root (1.1.0 READ) | STILL THERE | docs/CLI.md:2542 |
| the command reference was one prose wall (1.1.0 READ) | CHANGED | docs/CLI.md:2528 now opens with a First run block; the verb paragraphs below are still dense |

# nova-swarm USE rating, nova-tools 1.1.0

Rater: deepseek-v4.1-flash
Build: 333b481fee59
Score: 6.5/10

## Reasons
nova-swarm is a large orchestration tool, and its top-level help is a complete contract: it states every verb, its flags, the effects, the exit table and the sandbox rules, and its `example:` block (template read-pr, template worker, lint --rules) runs as printed. Three store-free jobs succeed end to end on the first try: the slots lease lifecycle, lint of a card template, and worker check. Refusals name every independent problem in one run: `slots take` with no flags names all four missing inputs. The score is held down by the machine interface, which is absent. No verb accepts `--json`, so an AI must scrape prose and cannot act on a result without guessing at its shape; the standard asks every verb to render the one result value as JSON. `--dry-run` is offered only on step and disk-guard, not on slots init/take/release or native, which write a store or spend. The refusal grammar has two shapes, and the required-flag and bad-value shape omits the next command that the unknown-flag and unknown-verb shapes print. The native help's own example exits 2 as written. A 10 needs `--json` on every verb, `--dry-run` on every verb that writes, one refusal grammar whose every line ends in a runnable next command, and an example that runs wherever it is printed.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | `nova-swarm slots list --store ./slots --json` | no verb accepts `--json`: the refusal is `unknown flag --json; the flags of slots list are --store`. An AI caller gets only prose lines (`SLOT ... owner=... pid=... until=...`) and must parse them field by field, and cannot rely on one schema across verbs. The standard asks every verb to render its one result value as JSON. | accept `--json` on every verb and emit the same facts as the lines, a refusal included. | L |
| 2 | `nova-swarm slots take --store ./slots --owner rater --n 1 --for 30m` | the writing verbs have no dry run: `slots init`, `slots take` and `slots release` change the store on the call, and `native` and `member` spend, while `--dry-run` appears only on step and disk-guard. A reader cannot see the plan before the write. | add `--dry-run` to every verb that writes or spends, printing the same plan the real run takes and writing nothing. | M |
| 3 | `nova-swarm template` | the required-flag refusal is `nova-swarm template: --name is required; it wants one of ...; refusing to guess` -- no `REFUSED` word and no `run:` line, while `nova-swarm frobnicate` prints `nova-swarm REFUSED: unknown verb ...; run: nova-swarm help`. Two grammars, and the one a first-run reader meets most often omits the next command. | give the required-flag and bad-value paths the same `VERB REFUSED: ...; run: ...` shape as the unknown-flag and unknown-verb paths. | M |
| 4 | `nova-swarm native --harness ./harness --model provider/model --card card.md --slot slots/1 --root jobs --deadline 30m --tokens unmetered` | the `example:` block in `nova-swarm native -h` fails as written: it prints `NATIVE REFUSED: the harness binary ./harness is missing; run: nova-swarm native -h` and exits 2. A cold reader who pastes the example meets a refusal, not a run. | make the example run from the binary alone (a fake harness the help names), or drop the example and say what a first run needs. | M |
| 5 | `nova-swarm verify --result result.md --contract 'RESULT: demo sha=000000000000' --label demo` | after `nova-swarm template --name result`, the shipped template's line 1 is `# <task>` and its own verify refuses it: `RESULT REFUSED "demo" line 1 is "# <task>"`, exit 1. The template the tool tells a reader to fill fails the tool's own check. | make `template --name result` print the contract line first, the shape verify accepts. | S |
| 6 | `nova-swarm step --dry-run --card card.md --dir .` | the refusal reads `nova-swarmstep REFUSED: ...` with the tool and the verb joined, so the status word is glued to the binary name and a reader matching on `nova-swarm step REFUSED` misses it. | print the tool and verb separated by one blank, as every other refusal does. | S |
| 7 | `nova-swarm lint --card card.md --json` | the unknown-flag refusal truncates the flag list: `... --trust and 1 more`, hiding `--typed`, so an AI cannot enumerate the flags from the refusal and must open the help. | list every flag, or say where the full list is with a count that names the omitted one. | S |

## Good, keep
- The top-level `example:` block runs as printed: `template --name read-pr`, `template --name worker` and `lint --rules` all exit 0.
- One run names every independent problem: `nova-swarm slots take` with no flags prints all four missing inputs, each with what it wants.
- `step --dry-run` and `disk-guard --dry-run` print the real plan (`STEP PLAN step=2 lang=regex paths=marker.txt posts=1 wall=...`) and write nothing; the slots lifecycle prints a runnable next state each time.

## Compared with earlier ratings
| earlier | now | evidence |
|---|---|---|
| the native -h example fails as written | STILL THERE | `nova-swarm native --harness ./harness --model provider/model --card card.md --slot slots/1 --root jobs --deadline 30m --tokens unmetered` prints `NATIVE REFUSED: the harness binary ./harness is missing; run: nova-swarm native -h`, exit 2 |
| the result template fails verify | STILL THERE | `nova-swarm template --name result` then `nova-swarm verify --result result.md --contract 'RESULT: demo sha=000000000000' --label demo` prints `RESULT REFUSED "demo" line 1 is "# <task>"`, exit 1 |
| two refusal formats | STILL THERE | `nova-swarm template` prints `nova-swarm template: --name is required; ...; refusing to guess` while `nova-swarm frobnicate` prints `nova-swarm REFUSED: unknown verb "frobnicate"; ...; run: nova-swarm help` |
| no --json or --dry-run on the verbs that spend | STILL THERE | `nova-swarm verify --result good.md --contract 'RESULT: demo sha=000000000000' --label demo --json` prints `unknown flag --json`, and `nova-swarm slots take --store ./slots --owner rater --n 1 --for 30m` writes a lease with no dry run |
| required-input refusals omit a next command | STILL THERE | `nova-swarm template` and `nova-swarm verify` end at `refusing to guess` with no `run:` line, while the unknown-flag and unknown-verb paths both carry one |
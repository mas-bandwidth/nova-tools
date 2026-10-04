# nova-swarm USE rating, nova-tools 1.1.0

Rater: Grok
Build: 75c8e4680221
Score: 6.5/10

## Reasons

The first run the banner names works with no setup. `nova-swarm template --name read-pr`, `nova-swarm template --name worker` and `nova-swarm lint --rules` each exit 0. A bare command names the verbs and the one command that only looks, then `run: nova-swarm help`.

Two jobs finish without a worker. Saving `template --name card` and running `nova-swarm lint --card card.md --child-rules` exits 0: `LINT OK` plus one NOTE per unfilled line, and each NOTE names the line and says to fill it. Filling those lines and linting again exits 0 with `LINT OK` and no NOTE. Separately, `slots init`, `slots take`, `slots list` and `slots release` run against a directory in scratch. A take past the share exits 2, names the holder, and prints the next command `nova-swarm slots list --store ./slotstore`. `verify` passes when line 1 equals `--contract` and exits 1 when it does not. An empty worker file lists every missing field, and what each wants, in one run.

Four refusals, on the verbs that get that far. A missing `--name` names the flag and the legal set, and does not guess. An unknown flag names the flag, the flags that exist, and often a near miss, then `run: nova-swarm help template`. An unknown verb names the verb and the list, then `run: nova-swarm help`. A bad `--name` and a `--capacity 0` each say what the flag wants and what they got. `slots init` with no flags names all four gaps in one run. `member` with no flags names all four of its required flags in one run, and does not dial.

The help offers no `--json` and no `--dry-run` on any verb. `nova-swarm template --name read-pr --json` and the same command with `--dry-run` are unknown flags. The verbs that spend are `native` and `member`. `member` is judged from `member -h` only: it wants a sprint server, and there is no dry run to show a pass. `native` does not get as far as its flags.

Guesses. `profile -h` does not give the timeline columns. A made-up file is accepted. `slots list -h` does not define `stranded`. `worker check -h` prints `--env` and `--max` with no text, and passing `--env` changes nothing visible. The result template's first line is not the `RESULT:` line `verify -h` describes; that much is in the help, and the template still fails verify.

Not tried, because each needs a real service and the help offers no dry run: a `native` child, a model call, and a `member` pass against a server. No credential is copied. The fakes the help names: it names none.

A 10 keeps this card loop and this slots loop, lets the `native` example fail on the harness it names rather than on which binary is first, refuses a timeline it cannot read, and puts `--json` and `--dry-run` on the verbs that spend.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | `nova-swarm native --harness ./harness --model provider/model --card card.md --slot slots/1 --root jobs --deadline 30m --tokens unmetered` | The example on `native -h` exits 2 before it looks for `./harness`. The lines are DOCTOR DRIFT and DOCTOR REFUSED: the binary just built is not the binary in the local bin, and the remedy is to copy the local one over the built one. `nova-swarm native` and `nova-swarm native --dry-run` end the same way, so the missing flags never appear. | Do not run that check before flag parsing, and let a build under test compare equal to itself. Say so on `native -h`. | L |
| 2 | `nova-swarm native --dry-run` | Help offers no `--json` and no `--dry-run`. On `template` and `lint` both flags are unknown. On `native` the doctor refusal hides even that. `member -h` has neither flag, and `member` is the other verb that spends. | Accept `--dry-run` on `native` and `member` from the same path as a real run, and print one JSON value for every verb. | L |
| 3 | `nova-swarm profile --jobs './garb/*'` | A directory whose timeline is not a timeline exits 0 with `PROFILE SUMMARY jobs=0` and no refusal. A one-row file exits 0 with `turns=1` and `read=0.0`. The zeros look like a measurement. | Refuse a timeline whose columns are not the ones the verb reads, and name those columns on `profile -h`. | M |
| 4 | `nova-swarm template` | A missing flag prints `nova-swarm template: --name is required...` and stops, with no `run:` line. An unknown verb prints `REFUSED` and `run: nova-swarm help`. The same split is on `slots init` and on `member`. | Use one line for every refusal: the problem, what the input wants, and the next command. | M |
| 5 | `nova-swarm worker check -h` | `--env` and `--max` are printed with no description. The worker template from the banner's example fails `worker check` on the harness only; the other angle-bracket fields are accepted as values. | Say what `--env` and `--max` want, and refuse a field that is still a placeholder. | S |
| 6 | `nova-swarm slots list --store ./slotstore` | A lease taken a moment earlier is `state=live` and `stranded=1`. `slots list -h` does not define either word, so the next act is a guess. The release by label does free it. | Define the list fields on `slots list -h`, and do not mark a lease stranded when its own take has just returned. | S |

## Good, keep

The three banner examples exit 0 with nothing to invent, and a bare command points at `nova-swarm help`.

A card from `template --name card` lints clean after its own NOTE lines are filled, and each NOTE names the line.

A take past the share exits 2, names the holder, and prints `nova-swarm slots list --store ./slotstore` as the next command. An empty worker file names every missing field in one run.

## Compared with earlier ratings
| earlier | now | evidence |
|---|---|---|
| the native -h example fails as written | STILL THERE | `nova-swarm native --harness ./harness --model provider/model --card card.md --slot slots/1 --root jobs --deadline 30m --tokens unmetered` exits 2 with DOCTOR REFUSED and does not look for ./harness |
| the result template fails verify | STILL THERE | `nova-swarm verify --result result-template.md --contract 'RESULT: trial sha=0123456789ab' --label trial` exits 1 and reports line 1 as `# <task>` |
| two refusal formats | STILL THERE | `nova-swarm template` has no run: line; `nova-swarm fly` prints REFUSED and `run: nova-swarm help` |
| no --json or --dry-run on the verbs that spend | STILL THERE | `nova-swarm template --name read-pr --dry-run` is an unknown flag; `nova-swarm native --dry-run` is refused by the doctor check first; help text contains neither flag |
| required-input refusals omit a next command | STILL THERE | `nova-swarm template` names what --name wants and prints no next command; `nova-swarm template --name read-pr --nope` does print `run: nova-swarm help template` |

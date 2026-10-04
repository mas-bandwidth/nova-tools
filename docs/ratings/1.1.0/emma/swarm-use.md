# nova-swarm USE rating, nova-tools 1.1.0

Rater: gemini-2.5-pro
Build: 264f9c0135c7
Score: 7/10

## Reasons
The tool provides responsive local workflows for card templating, linting, and slot leasing without demanding network stores or daemon setup. Generating a task card and validating it against child rules runs instantly and catches structure and placeholder omissions. Slot reservation, listing, and release work cleanly against throwaway local directories.

A score of 10 would require fixing the native -h example to run cleanly as printed, ensuring the result template passes verify out of the box, standardizing missing flag refusals to include REFUSED and a remedy command, adding --dry-run to native and member, and implementing --json across lint, verify, and slots list.

The native and member verbs were evaluated from their help text and flag specifications; live model invocation and sprint server daemons were not started to preserve environment safety and prevent unmetered spending.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | `nova-swarm native --harness ./harness --model provider/model --card card.md --slot slots/1 --root jobs --deadline 30m --tokens unmetered` | runnable example in native -h fails exit 2 because ./harness is an unresolved placeholder path | bundle an internal runnable fake harness or provide an example invocation that executes cleanly | M |
| 2 | `nova-swarm template --name result` | generated result template opens with heading # <task> on line 1 which verify immediately rejects | change line 1 of template to RESULT: <label> sha=<sha12> so it satisfies contract verification | S |
| 3 | `nova-swarm native` | missing flag refusal prints ad-hoc format omitting the REFUSED token and next remedy command | format missing argument errors using standard REFUSED grammar with run: remedy hints | M |
| 4 | `nova-swarm native -h` | verbs that spend tokens or launch child processes lack a dry-run flag to inspect plans safely | add --dry-run to native and member to output staged execution commands without starting processes | L |
| 5 | `nova-swarm lint --card card.md --json` | inspection and verification verbs lack structured machine-readable JSON output modes | support --json across lint verify profile and slots list | M |
| 6 | `nova-swarm worker check worker.json` | worker description template fails check with exit 1 because placeholder harness is not installed | allow worker check to validate structural syntax independently of host binary presence | S |

## Good, keep
Card template generation and child-rules linting provide fast, self-contained task verification with no external services.
Slots leasing operates entirely on local filesystem directories with clear isolation and clean release commands.
Flag validation evaluates all required arguments in a single pass reporting every missing flag simultaneously.

## Compared with earlier ratings
| earlier | now | evidence |
|---|---|---|
| the native -h example fails as written | STILL THERE | `nova-swarm native --harness ./harness ...` exits 2: NATIVE REFUSED: the harness binary ./harness is missing |
| the result template fails verify | STILL THERE | `nova-swarm template --name result` line 1 causes verify to fail: RESULT REFUSED "test" line 1 is "# <task>" |
| two refusal formats | STILL THERE | `nova-swarm unknownverb` uses REFUSED while `nova-swarm native` uses ad-hoc prefix |
| no --json or --dry-run on the verbs that spend | STILL THERE | `nova-swarm native -h` and `nova-swarm member -h` provide neither flag |
| required-input refusals omit a next command | STILL THERE | `nova-swarm native` lists required flags with no run: remedy command |

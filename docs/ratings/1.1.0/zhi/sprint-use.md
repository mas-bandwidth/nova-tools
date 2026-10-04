# nova-sprint USE rating, nova-tools 1.1.0

Rater: a large language model, cold to this tool
Build: 99e4a903966c
Score: 8/10

## Reasons

The first successful run was the documented no-Redis twin: `NOVA_SPRINT_REDIS=mem:sprint.twin NOVA_SPRINT_ACTOR=boss nova-sprint init --members m1:2 --coordinator boss --owner boss` exits 0, creates the four tables, and says the twin beats every member at each verb. The real job the tool exists for, a card flow, ran end to end: `add --stream s --count 2` moved two cards to ready, `start` changed the state, two `tick` calls dealt both cards to m1, and `queue --as m1` listed the packet with the exact take and finish commands. A second, different job: `where` printed the sprint table with the two ready cards and the fleet row, and `card s-1` printed the card's timeline and next step.

The four provoked refusals all exit 2 and name the next command: `add --count 1` says `wants --stream and either ids, --count <n> or --sentinel <id>; run: nova-sprint add -h`; an unknown flag lists the flags of add; an unknown verb lists all 52 verbs; a bad --count says it wants a whole number. `queue --json` returns one JSON object with the cards array, so a program can parse the queue without guessing.

What costs the score: `nova-sprint log --max 1` prints all 19 lines and closes `LOG OK lines=19 of=19`, so --max does nothing on log. Usage refusals ignore --json: `nova-sprint add --count 1 --json` prints the text refusal instead of the JSON object the flag promises elsewhere. `nova-sprint version --bogus` silently prints the version and exits 0, accepting an unknown flag. `play` on the twin moves cards (TAKE OK moved=2, FINISH OK moved=2) and is no longer a no-op.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | `nova-sprint log --max 1` | prints all 19 lines and closes `LOG OK lines=19 of=19`; --max does nothing | honour --max on log or refuse it | S |
| 2 | `nova-sprint add --count 1 --json` | the usage refusal ignores --json and prints text, unlike the JSON the flag promises on other verbs | render usage refusals as JSON when --json is given | S |
| 3 | `nova-sprint version --bogus` | version silently accepts an unknown flag and exits 0 | refuse unknown flags on version | S |

## Good, keep

The twin walkthrough that lets a cold user run a whole card flow without Redis. The packet output with the exact take and finish commands to copy. The refusal grammar with the full verb list.

## Compared with earlier ratings
| earlier | now | evidence |
|---|---|---|
| log --max does nothing | STILL THERE | `nova-sprint log --max 1` prints `LOG OK lines=19 of=19` |
| play on a twin reports OK and moves nothing | FIXED | `nova-sprint play` on the twin printed `TAKE OK moved=2 refused=0` and `FINISH OK moved=2 refused=0` |
| usage refusals ignore --json | STILL THERE | `nova-sprint add --count 1 --json` prints the text refusal |
| version silently accepts unknown options | STILL THERE | `nova-sprint version --bogus` exits 0 and prints the version |

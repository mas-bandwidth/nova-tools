# nova-sprint USE rating, nova-tools 1.1.0

Rater: Grok
Build: 75c8e4680221
Score: 7.5/10

## Reasons

`nova-sprint help` exits 0. The first line says what the tool is. The runnable card flow is not the `example:` block at the bottom. It is the no-git pair under "trying it without a Redis": finish with no `--head`, then `merge --stream s1 --batch 1`. Run on `mem:` under a scratch file, that pair lands one card. `init`, `add --stream s1 --count 1`, `start`, two `tick`s, `take --as m1 --epoch 0`, the finish, two reads begun and then ok, one more `tick`, `merge`, and a last `tick` each exit 0. The last tick prints that the sprint is done, 1 landed, and `where` shows that one landed card. `card s1-1` tells the same story in time order. That is the first successful run, and it is the job the tool is for.

A second job, on a fresh twin, is the failed-work loop. `finish --failed --report 'tests red'` exits 0. `inbox` prints the judgment and the line `nova-sprint rework s1-1`. That line, run as printed, exits 0. The next `queue --as m1` packet names the failure report as this attempt's fix. `play --seed 7 --ticks 1 --every 1s` on that running twin exits 0, prints PLAY OK, and its take moves 1. It does not tick. `where` stays on review until a hand `tick`, which then prints the drain and `card` says working, attempt 3.

Four refusals each name the problem and a next command, on stderr, exit 2. `nova-sprint add` wants `--stream` and ids, `--count`, or a brief file. `nova-sprint add --strem s1 --count 1` names the unknown flag, lists the flags, and asks if `--stream` was meant. `nova-sprint dance` lists every verb, and the list includes version. `nova-sprint where --every nope` says `--every` wants a duration such as 30s or 5m. They do not name every problem at once: `nova-sprint add --count zz --score zz` names `--count` and is silent about `--score`.

`--json` and `--dry-run` match the help where the verb got far enough to run. `nova-sprint land --stream s1 --dry-run` on a stream with nothing queued exits 1, prints LAND REFUSED with `nova-sprint queue --stream s1`, and LAND DONE with `dry_run=yes`. The same command with `--json` is one object whose item carries that reason, and stdout is empty of prose. `nova-sprint where --json` is one object and, as `where -h` says, it carries the readers and merge tables the text frame hides. `where --all` draws those tables. Usage does not join that shape: `nova-sprint add --json`, `nova-sprint finish --json`, and `nova-sprint where --every nope --json` print the refusal as text on stderr and leave stdout empty.

The guesses are where the first run lives, and which screen is current. The `example:` block's second line, `nova-sprint add --stream s1 --count 3 --brief-file brief.txt`, exits 2 because brief.txt is not there, and `inbox --wait` on a twin exits 2. After a rework, `card s1-1` and `where` still say review until `tick`. The verb line already said MOVED. Nothing on `card` says a tick is owed. `log --max` does not cap: `--max 1` prints the same 29 lines as the log without it. `nova-sprint version --bogus` exits 0 and prints the version. `nova-sprint version -h` does the same. `nova-sprint help version` exits 2 and calls version an unknown verb.

Not tried, because each needs a service or a remote the help names, and this run stays on the twin. `run --listen` and `dashboard --listen` serve a fleet. `run` on a twin exits 2 and names `tick`. `fleet sync` and `friend sync` read a config database. `friend beat` is not run. `quack` wants a clone URL. `land` with git is not run; `land --dry-run` is. `friend clean --dry-run --file` on a file that is not there exits 3, names a migrate command, and says nothing was removed. `where --watch` and `inbox --wait` exit 2 on the twin and name `tick`.

A 10 puts the twin flow in the `example:` block, makes `card` say when a tick is still owed, honors `--max` on `log`, refuses an unknown option on `version`, and prints a usage refusal as JSON when `--json` is set, every bad flag in that one line. The landed card, the pasted inbox line, and the refusal grammar are already at that bar.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | `nova-sprint add --stream s1 --count 3 --brief-file brief.txt` | This is the second line of the help `example:` block. It exits 2: brief.txt is not there. The block's `inbox --wait` then exits 2 on a twin. The flow that lands sits higher in the same help, as two stand-in lines after a git recipe. | Make the `example:` block the no-git twin flow, the lines that exit 0 as printed. | S |
| 2 | `nova-sprint card s1-1` | Right after `rework s1-1` exits 0 and says the card moved to working, `card` and `where` still say review, and `card` says no judgment is open. `queue --as m1` already shows the next attempt. One `tick` drains the move, and then `card` says working. | Say on `card` and `where`, before the tick, that a move is waiting for `nova-sprint tick`. | S |
| 3 | `nova-sprint log --card s1-1 --max 1` | The flag text says it caps listed items, 0 meaning all. The command exits 0 and prints the same 29 lines as `nova-sprint log --card s1-1` with no `--max`. | Cut the log at `--max` and print a MORE line with the total, or drop the flag from this verb. | S |
| 4 | `nova-sprint version --bogus` | An unknown option exits 0 and prints the version line. `nova-sprint version -h` prints that line too, not usage. `nova-sprint help version` exits 2 and says version is an unknown verb, while `nova-sprint dance` lists version among the verbs. | Refuse an unknown option, and teach `version` from `help version` the way every other verb is taught. | S |
| 5 | `nova-sprint add --json` | A usage refusal with `--json` is text on stderr and an empty stdout. The same is true of `nova-sprint finish --json` and `nova-sprint where --every nope --json`. `nova-sprint add --count zz --score zz` names `--count` and not `--score`. A state refusal is different: `land --dry-run --json` is one object. | One JSON refusal for usage too, and name every bad flag in that one answer. | M |

## Good, keep

The no-git pair in help, followed by `tick`, lands a card, and each line is MOVED or OK with a command to run next. The inbox line `nova-sprint rework s1-1` runs as printed, and the packet's fix is the failure report. An unknown flag names the flags and the nearest one, and a twin refuses `run`, `where --watch`, and `inbox --wait` by naming `tick`.

## Compared with earlier ratings
| earlier | now | evidence |
|---|---|---|
| log --max does nothing | STILL THERE | `nova-sprint log --card s1-1 --max 1` exits 0 and prints the same 29 lines as the log without `--max` |
| play on a twin reports OK and moves nothing | CHANGED | `nova-sprint play --seed 7 --ticks 1 --every 1s` on a running twin exits 0, prints PLAY OK, and take moves 1. It does not tick. On a stopped twin it exits 2 and names run, which the twin refuses |
| usage refusals ignore --json | STILL THERE | `nova-sprint add --json` exits 2 with the refusal on stderr and an empty stdout |
| version silently accepts unknown options | STILL THERE | `nova-sprint version --bogus` exits 0 and prints the version line |
| three cold uses scored 7, 7.5 and 5 | CHANGED | this use is 7.5/10: the twin flow lands a card, and the example block does not run as printed |

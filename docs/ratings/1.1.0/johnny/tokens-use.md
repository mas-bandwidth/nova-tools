# nova-tokens USE rating, nova-tools 1.1.0

Rater: Grok
Build: 75c8e4680221
Score: 7.5/10

## Reasons

`nova-tokens help` and `nova-tokens -h` print the same banner and exit 0. The first run is the setup line above `example:`, then the six commands under it. Pasted as printed, in a fresh directory, `nova-tokens fold --out ./out --day 2026-09-11 --repos ./repos.tsv --claude bench=./transcripts` exits 0 with `TOKENS DAY ... input` matching the fixture: input 812, output 40, cache write 1200, cache read 90000, reasoning a dash. `nova-tokens check --out ./out` prints `CHECK OK`. `nova-tokens sum --out ./out --month 2026-09` prints the same four numbers and `reasoning=-`. `nova-tokens sources` agrees. That is a real job, and the numbers are the numbers in the file.

A second job, a different day and a path the rules file does not name, is `nova-tokens fold --out ./two-out --all --repos ./repos.tsv --claude bench=./two`. It exits 0 with `other=100.0%` and dashes for the three types the line did not carry. `nova-tokens session --claude-session ./session.jsonl` prints one `SESSION` line whose input, cache and output match the same fixture, and with `--out` writes a day file. `nova-tokens session -h` is the only verb help whose text contains the letters json, and that is the transcript suffix, not a `--json` flag.

Four refusals, all exit 2. `nova-tokens fold` names `--out`, `--day` or `--all`, `--repos` and a source, each with what the flag wants, in one run. `nova-tokens fold --day 2026-13-40 --max -3` adds the bad day and the negative ceiling to that same list. `nova-tokens fold --day yesterday --out ./out --repos ./repos.tsv --claude bench=./transcripts` names `yesterday` and the shape it wants. `nova-tokens invent` names the unknown verb. `nova-tokens fold --not-a-flag` names `-not-a-flag`. The missing-flag lines are `TOKENS REFUSED:`; the unknown flag and the unknown verb are a second grammar, `nova-tokens fold:` and `nova-tokens:`, and neither offers the nearest real name. The next command on every one of them is `nova-tokens help`, which is long but is the door the banner names.

Help offers no `--json` and no `--dry-run`. `nova-tokens sum --out ./out --month 2026-09 --json` and `nova-tokens fold ... --dry-run` are unknown flags. There is no second rendering to compare with the lines, and no preview that writes nothing.

The costly miss is the status word. `nova-tokens report --who ada --day 2026-09-11 --repos ./repos.tsv --claude bench=./mixedsrc`, with one unreadable file beside a readable transcript, exits 1, prints `TOKENS UNREADABLE`, and still prints `REPORT OK` on stderr and the note body on stdout. An AI that sends on OK sends a note the exit code says is incomplete. The same shape on a clean report exits 0, so OK is not always a lie, which makes the lie harder to see.

`nova-tokens fold --out ./overlap-out --day 2026-09-11 --repos ./repos.tsv --claude bench=./transcripts --claude extra=./copy` exits 0, prints `TOKENS OK`, and writes input 1624 for one message of 812. The `TOKENS NOTE` on that same stdout says the messages are counted twice. The note is clear. The exit is still success.

`REPORT OK` carries `subject=tokens 2026-09-11 at=<stamp> build=<id>` with blanks inside the value, on a line that already has `at=` and `build=` fields. A who value with a blank is written into the body as the literal characters backslash, x, 2, 0. Help does not say that.

`nova-tokens help` says no environment variable is consulted. The same banner lists `--password-env` on `ledger` and on `report --redis`. `nova-tokens ledger` with no flags refuses before any store is contacted, and the wants clause names `--all`, which `nova-tokens ledger --all` then rejects as an unknown flag. `report --redis` with no month refuses the same way. Those two verbs were not run against a store; help has no `--dry-run` for them. `profiles` was refused for a missing `--swarm-root`; help does not give a file shape, so no profile run was attempted. OpenCode and a bus directory were not tried: help requires sqlite3 and a database copy for one, and a note grammar the banner does not print a sample of for the other.

Where a guess was required: what `usd=0` means on `TOKENS AVG` (help never mentions that line, and the transcript had no price), and where the subject value ends. The day file and the `SUM PAIR` line did not require a guess. A ten keeps this first run and these refusals, and makes OK mean the claim held, a doubled id exit 1, and every field one token.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | `nova-tokens report --who ada --day 2026-09-11 --repos ./repos.tsv --claude bench=./mixedsrc` | Exit 1, stderr has TOKENS UNREADABLE and still REPORT OK, and stdout is the note body. | Print REPORT FAIL when a declared source is unreadable, and write no body. | S |
| 2 | `nova-tokens fold --out ./overlap-out --day 2026-09-11 --repos ./repos.tsv --claude bench=./transcripts --claude extra=./copy` | Exit 0 and TOKENS OK, with input 1624 for one message of 812. The NOTE says counted twice and the exit stays 0. | Exit 1 when two declared sources share a message id, and do not write the doubled day. | M |
| 3 | `nova-tokens report --who ada --day 2026-09-11 --repos ./repos.tsv --claude bench=./transcripts` | subject= holds blanks and repeats at= and build=, so the value is not one field. A who with a blank is stored as backslash-x20, which help does not mention. stderr also prints TOKENS AVG usd=0 when the transcript carried no price. | Escape subject= like the other fields, and print a dash, not 0, when no source reported a price. Name both in help. | S |
| 4 | `nova-tokens sum --out ./out --month 2026-09 --json` | Help offers no --json and no --dry-run. The flag is an unknown option, so the sum has no machine rendering and fold has no preview. | Accept --json for the same result, and --dry-run on fold that prints the day lines and writes nothing. | M |
| 5 | `nova-tokens ledger` | The refusal names --all in the wants clause. nova-tokens ledger --all is then an unknown flag. The banner says no environment variable is consulted while ledger -h lists --password-env. | Name only flags this verb has, and state the password variable in the same sentence that says which variables are ignored. | S |

## Good, keep

The example block runs as printed. Fold, check and sum agree on 812, 40, 1200, 90000 and a dash, and a path that matches no rule prints other=100.0% instead of a guessed repo.

A bare fold reports every missing flag in one run, and each line says what the flag wants and that the tool is refusing to guess.

A type the transcript omits stays a dash on the day line and on SUM PAIR, and sum writes nothing.

## Compared with earlier ratings
| earlier | now | evidence |
|---|---|---|
| REPORT OK with exit 1 | STILL THERE | nova-tokens report --who ada --day 2026-09-11 --repos ./repos.tsv --claude bench=./mixedsrc exits 1 and stderr contains REPORT OK who=ada |
| subject= unquoted with blanks | STILL THERE | the same report, on a clean directory, prints subject=tokens 2026-09-11 at=<stamp> build=<id> with blanks inside the value |
| --json numbers as strings | CHANGED | nova-tokens sum --out ./out --month 2026-09 --json exits 2 with flag provided but not defined: -json, and no JSON is printed |
| the unknown-option refusal omits the offending flag | FIXED | nova-tokens fold --not-a-flag exits 2 with flag provided but not defined: -not-a-flag |

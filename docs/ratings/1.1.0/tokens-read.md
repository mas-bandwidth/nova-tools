# nova-tokens READ rating, nova-tools 1.1.0

Rater: deepseek-v4.1-flash
Build: c28448a54d56
Score: 7.5/10
README: 7/10

## Reasons

The README's tokens row is README.md:32: "token spend per day, model and repository, read from AI session logs", and the banner's first line at cmd/nova-tokens/main.go:49 is that sentence word for word. The tool is honest where it counts: every path is a flag (cmd/nova-tokens/main.go:100), a dash is not a zero (internal/tokens/tokens.go:50), exit 1 still writes (cmd/nova-tokens/main.go:96), and a fold merges by source rather than recomputing the whole file (internal/tokens/dayfile.go:225). Those are the parts a stranger can trust, and they are why the score is above the middle.

Confused first at README.md:32. The one command shown points at ./cmd/nova-tokens/testdata/example-bench, which exists only in a source checkout (README.md:58), and the setup sentence names OpenCode and sqlite3 that the shown `sources` command does not exercise.

Bored first at README.md:48. After one dense cell the page turns into a general install and trial essay whose sample binary is a different command, and the tool's own row is not named again until README.md:100.

Doubted first at README.md:50. "These are the Nova Tools 1.0.0 commands" sits in a tree whose release notes are 1.1.0 (docs/RELEASE-NOTES-1.1.0.md:1), and README.md:51 installs `@v1.0.0`. A cold reader is told the current commands are the previous release's.

The tool says what it is for in three lines and the code does that: fold reads the declared logs into one file per day keyed by (day, model, repo), check is the gate, sum adds day files and writes nothing, report prints a friend's note body. The package comment at internal/tokens/tokens.go:1 is one sentence and true. Refusals name what a missing flag wants and refuse to guess (cmd/nova-tokens/main.go:319). The output has one value with two renderings (cmd/nova-tokens/out.go:1). The tests are the contract: cmd/nova-tokens/demanded_test.go alone is 2,130 lines and pins the exit table and the line shapes.

It is not a ten as writing. The help an AI gets is the usage constant from cmd/nova-tokens/main.go:49 to cmd/nova-tokens/main.go:191, 143 lines, and the verbs live in one 1,563-line file: cmdFold alone runs from cmd/nova-tokens/main.go:590 to cmd/nova-tokens/main.go:842 and shares main.go with report, sum, check, sources and the whole banner. A reader finds the verbs in a minute and then does not find one verb's behavior in a minute. docs/CLI.md:1494 is a single 2,642-character paragraph that crams every flag, source kind and Redis rule into one line, and docs/USAGE.md:263 tells the reader there are no defaults and no environment variables, which the tool's own --timeout default (cmd/nova-tokens/main.go:353) and its Redis logins (cmd/nova-tokens/main.go:104) contradict. The banner then says --timeout is the one flag with a default (cmd/nova-tokens/main.go:108) while --weights defaults too (cmd/nova-tokens/session.go:58). And a fold that sees two declared sources feed one message id names the doubling but leaves it out of its failure set (cmd/nova-tokens/main.go:822), so a doubled day can still print TOKENS OK.

A ten needs: one verb per file, a banner that stops after the three questions and the example, a command-reference paragraph a reader can finish, one accurate sentence about defaults and environment, and a cross-source overlap that cannot exit 0.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | cmd/nova-tokens/main.go:822 | The failure set omits the overlap list, so two declared sources that share message ids write the doubled day and can still print TOKENS OK; the note at cmd/nova-tokens/main.go:960 names it and changes no exit. | Add the overlap count to bad, or refuse the second declaration before any day file is written. | M |
| 2 | cmd/nova-tokens/main.go:590 | cmdFold runs from line 590 to line 842 inside a 1,563-line main.go that also holds report, sum, check, sources and the 143-line banner. | Move each verb into its own file, as session and ledger already sit beside main. | L |
| 3 | docs/CLI.md:1494 | One 2,642-character paragraph carries every flag, source kind and Redis rule for the tool. | Break it into a short paragraph per verb, or move the flag detail under each verb. | M |
| 4 | docs/USAGE.md:263 | The adoption page says there are no defaults and no environment variables are consulted; --timeout defaults to 120 (cmd/nova-tokens/main.go:353) and three environment reads exist (cmd/nova-tokens/main.go:104). | State the named exceptions in the same sentence. | S |
| 5 | cmd/nova-tokens/main.go:108 | The banner says --timeout is the one flag with a default, but --weights also defaults (cmd/nova-tokens/session.go:58) and the spec names both (docs/SPEC-TOKENS.md:423). | Name the two flags that have defaults, or say the one flag with a default on fold. | S |
| 6 | README.md:50 | These are the Nova Tools 1.0.0 commands, and the install at README.md:51 pins v1.0.0, in a 1.1.0 tree (docs/RELEASE-NOTES-1.1.0.md:1). | Name the 1.1.0 release and its install command. | S |

## Good, keep

The banner's first line is the README row word for word (README.md:32, cmd/nova-tokens/main.go:49).

A dash is not a zero, exit 1 still writes, and written= is a separate fact from the exit (cmd/nova-tokens/main.go:96, internal/tokens/tokens.go:50).

A fold merges by source and keeps a row no declared source wrote, and a row it can neither keep nor recompute is refused before anything is written (internal/tokens/dayfile.go:225).

## Compared with earlier ratings
| earlier | now | evidence |
|---|---|---|
| a 1454-line main.go | STILL THERE | cmd/nova-tokens/main.go is 1563 lines now, and cmdFold, report, sum, check and sources still share it. |
| a 220-line fold | STILL THERE | cmdFold runs from cmd/nova-tokens/main.go:590 to cmd/nova-tokens/main.go:842. |
| five line tokens and three status words in one tool | STILL THERE | docs/SPEC-TOKENS.md:515 starts the one grammar block with OK, FAIL and the informational tokens. |
| overlapping sources can still yield doubled successful totals | STILL THERE | cmd/nova-tokens/main.go:822 leaves overlaps out of bad, and cmd/nova-tokens/main.go:960 only warns. |
| the README score sat between 6.5 and 8.4 | CHANGED | README.md:32 matches the banner, but README.md:50 still calls the commands 1.0.0 in a 1.1.0 tree; this read scores the README 7. |

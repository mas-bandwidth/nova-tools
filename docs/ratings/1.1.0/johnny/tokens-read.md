# nova-tokens READ rating, nova-tools 1.1.0

Rater: Grok
Build: 75c8e4680221
Score: 7.5/10
README: 7.5/10

## Reasons

README.md:31 is the tokens row: "token spend per day, model and repository, read from AI session logs", and the first command is `sources` on the checkout fixture. That sentence is the banner's first line at cmd/nova-tokens/main.go:46.

Confused first at README.md:31. The want is "See where your tokens went", the command is `sources`, and the setup sentence names an OpenCode database and sqlite3 that the command does not pass.

Bored first at README.md:48. After one dense cell, the page leaves the tool for a 1.0.0 install essay whose sample binary is a different command.

Doubted first at README.md:48. "These are the Nova Tools 1.0.0 commands" sits under a table whose tokens cell points at this checkout's fixture, and the page's first link, README.md:16, sends a reader to an install section that pins that same release.

The tool says what it is for in three lines and the code does that. Fold reads declared logs into one file per day, keyed by day, model and repo, and keeps the five counts apart. A dash stays a dash. Check is the gate. Sum adds day files and writes nothing. The header at cmd/nova-tokens/main.go:1 says why, in the present tense, and the refusals at cmd/nova-tokens/main.go:270 name what a missing flag wants. That honesty is the part worth keeping.

It is not close to a ten as an essay an AI can finish. The help an AI actually gets is the usage constant from cmd/nova-tokens/main.go:46 through cmd/nova-tokens/main.go:163. The normative grammar at docs/SPEC-TOKENS.md:499 is a second vocabulary: the second word is OK, FAIL, or a noun, and the block runs through many line shapes. cmdFold at cmd/nova-tokens/main.go:554 still owns report, sum, check and sources in the same 1491-line file. A stranger finds the verbs, then does not find one verb's behavior in a minute.

The costly claim the code bears out against itself is the overlap. internal/tokens/tokens.go:177 says two declarations of one tree still double the day, and cmd/nova-tokens/main.go:792 does not put that overlap in the failure set, so the run can print TOKENS OK. The note at cmd/nova-tokens/main.go:921 names the doubling and leaves the numbers. docs/CLI.md:1415 says the same thing in prose: check and sum stay green. An AI that trusts exit 0 trusts a total that counted one message twice.

docs/USAGE.md:263 says no environment variable is consulted. docs/SPEC-TOKENS.md:36 excepts the two Redis verbs, and docs/CLI.md:1436 names the password variable. The adoption page, which the README points at first, drops that exception.

A ten needs one first door, a banner that stops after the three questions and the example, each verb in its own file, one output value an AI can take as lines or as JSON, and a cross-source overlap that cannot exit 0. The contract underneath is already the right one: no guessed path, no silent skip, no invented split, dash is not zero.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | cmd/nova-tokens/main.go:792 | Two sources that share message ids still write the doubled day and can print TOKENS OK. The note names the pair and does not change the exit. | Count a cross-source id overlap as a failure, or refuse the second declaration before any day file is written. | M |
| 2 | cmd/nova-tokens/main.go:554 | cmdFold runs through the return at line 808 inside a file that also holds report, sum, check, sources and the whole banner. | Move each verb into its own file, the way session and ledger already sit beside main. | L |
| 3 | docs/SPEC-TOKENS.md:501 | The second word of a line is OK, FAIL, or one of many nouns, and cmd/nova-tokens/version.go:65 refuses a second shape, so there is no JSON of the same value and no dry run. | Render one result as the line and as JSON, and let fold --dry-run print the day lines and write nothing. | L |
| 4 | docs/USAGE.md:263 | The adoption page says no environment variable is consulted. The spec's first rule excepts the Redis verbs, and the command reference names the password variable. | State that exception in the same sentence on the adoption page. | S |

## Good, keep

The banner's first line matches the README row, and a missing flag says what it wants and that the tool is refusing to guess (cmd/nova-tokens/main.go:270).

A dash is not a zero, exit 1 still writes, and written= is a separate fact from the exit (cmd/nova-tokens/main.go:83).

A fold recomputes only the sources it declared and keeps every other row, and a row it cannot take apart is left unwritten (docs/SPEC-TOKENS.md:160).

## Compared with earlier ratings
| earlier | now | evidence |
|---|---|---|
| a 1454-line main.go | STILL THERE | cmd/nova-tokens/main.go:1491 is the last line, and the file is longer than 1454 |
| a 220-line fold | STILL THERE | cmd/nova-tokens/main.go:554 defines cmdFold and the function returns at line 808 |
| five line tokens and three status words in one tool | STILL THERE | docs/SPEC-TOKENS.md:501 says the second word is OK, FAIL, or an informational token, and the block from line 512 lists the shapes |
| overlapping sources can still yield doubled successful totals | STILL THERE | internal/tokens/tokens.go:177 says the numbers still do not change, and cmd/nova-tokens/main.go:792 leaves overlap out of the failure set |
| the README score sat between 6.5 and 8.4 | CHANGED | README.md:31 is the same sentence as cmd/nova-tokens/main.go:46, and README.md:48 still calls the set the 1.0.0 commands; this read scores the README 7.5 |

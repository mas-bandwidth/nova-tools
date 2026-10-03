# nova-version READ rating, nova-tools 1.1.0

Rater: Grok
Build: 3a1cbd4f7245
Score: 7.5/10
README: 7/10

## Reasons

The README row is README.md:39. Its third column is the banner sentence: which version of each tool is installed, recorded and compared. The first confusion is that same row's setup clause, which says the trial runs go version on the included local manifest. A cold reader cannot tell whether the job is to inventory installed tools or to print the Go toolchain line. The first stretch that bores is README.md:48, where the page restates a 1.0.0 install and a bus trial after the table already gave every tool a first command. The first claim that does not hold up is README.md:48 calling these the 1.0.0 commands, while this tree is rated as 1.1.0 and the version row's contract is more than one report.

The tool is readable once the entry point is found. cmd/nova-version/main.go is one call. internal/update/versiontool.go declares example, moved, snapshot, diff, report and send, each with an effect, and the How paragraph names those nouns, the hand-written manifest, and that this binary shares the manifest with the update tool. docs/SPEC-VERSION.md states moved, the directory snapshot and diff as numbered rules with the tests that pin them. The exit table says what 0, 1 and 2 mean.

It is not one essay. docs/CLI.md:1619 points only at the update spec, while the entry comment points only at the version spec, and the verbs live in both. snapshot is two operations under one name: a six-column manifest count, and a four-column directory inventory. The section explains that split with a fixed adopted count and a bench timing story, and it never names moved, the verb the version spec opens on. report and send print their own lines and therefore take no --json, so the one-result rule stops at the verbs an AI is most likely to parse. The implementation sits in the update package, whose command file still dispatches the other binary. A 10 is one package, one spec, one first run in the README, the command reference and the banner, both snapshot shapes named without a fleet count, moved in that section, and --json on report and send.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | internal/update/versiontool.go:14 | The binary is a function in the update package. internal/update/cli.go:291 is the branch that reaches it, and that file still dispatches the other binary, so a reader does not find one directory that is this tool. | Move these verbs into a package that only this binary links, and leave the shared manifest reader as a library. | L |
| 2 | docs/CLI.md:1619 | The section names one spec, and cmd/nova-version/main.go:3 names the other. moved, directory snapshot and diff live in the version spec; report, send and snapshot --file live in the update spec. | Open the section with both specs and the verbs each owns, in one sentence. | S |
| 3 | docs/CLI.md:1644 | Two snapshot shapes are explained as an adopted count of 16 against 32 executables, and docs/CLI.md:1667 measures a cold start on one bench. cmd holds 18 tool directories. The section never names moved. | Say manifest-count versus directory-inventory in one paragraph, name moved, and drop the counts and the timing story. | S |
| 4 | internal/update/versiontool.go:137 | report and send call Prints, so the banner's every-verb --json line excludes them. An AI that wants one object has to scrape REPORT lines instead. | Render report and send through the shared result value, the same way snapshot already does. | M |
| 5 | README.md:39 | The table's first command is report on a fixture whose installed column is go version. The command reference's first run is example then report at docs/CLI.md:1624. The banner's example block at cmd/nova-version/main.go:7 adds snapshot --file and version. Three doors. | Put one first-run sequence in the table, the command reference and the banner. | S |

## Good, keep

The banner sentence is the README sentence, and the How paragraph names report, snapshot, diff, moved and the hand-written manifest, then says the first run is the binary alone.
docs/SPEC-VERSION.md numbers the rules and the tests that pin them, and a mixed stamp is refused by naming both binaries at internal/update/snapverb.go:213.
example writes a one-tool manifest and names the next command, and a plain report needs no bus and no remote.

## Compared with earlier ratings
| earlier | now | evidence |
|---|---|---|
| lives inside package update | STILL THERE | cmd/nova-version/main.go:22 calls VersionTool in internal/update |
| a 345-line verb in cli.go | CHANGED | internal/update/cli.go:715 starts movedVerb and internal/update/cli.go:895 returns it |
| a doc section explained by fleet numbers | STILL THERE | docs/CLI.md:1644 states an adopted count and docs/CLI.md:1667 times a cold start on one bench |
| stale fixed-count prose | STILL THERE | docs/CLI.md:1685 says 16 and 32 while cmd holds 18 tool directories |
| split verb homes | STILL THERE | docs/CLI.md:1619 names the update spec and cmd/nova-version/main.go:3 names the version spec |
| README scored 6.5 to 8.4 | STILL THERE | README.md:39 still leads with a fixture report and README.md:48 still stamps 1.0.0 |

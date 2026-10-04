# nova-version READ rating, nova-tools 1.1.0

Rater: gemini-2.5-pro
Build: 264f9c0135c7
Score: 8/10
README: 8.5/10

## Reasons
The README introduces nova-version accurately in its overview table, highlighting its role in reporting, recording, and comparing installed tool versions with a working example manifest. The tool design demonstrates strong CLI discipline: standard banner formatting, explicit and unguessable paths, positive duration validation, and multi-problem refusal reporting that guides callers directly to valid commands.

However, a cold reading reveals several points of structural friction. The first place of confusion is docs/CLI.md:1619, where the command reference points readers to SPEC-UPDATE.md as the tool contract, even though docs/SPEC-VERSION.md governs its primary verbs. The first place of boredom is docs/CLI.md:1667, where ten lines of text recite host benchmark timings to explain why the default timeout is thirty seconds rather than five. The first place of doubting a claim is internal/update/versiontool.go:137, where reportFlags invokes f.Prints(), suppressing structured output and leaving report and send without structured JSON support despite standard guidelines expecting JSON across all verbs. Furthermore, snapshot combines two completely distinct operations into one verb: counting adopted tools in a manifest and capturing executable inventories from a directory.

A score of 10 would require moving nova-version into its own internal package, extracting movedVerb from cli.go into a dedicated file, implementing structured JSON output for report and send, updating the contract link in CLI.md, and pruning host benchmark figures and hardcoded inventory counts from documentation.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | internal/update/cli.go:715 | movedVerb and its helpers span 345 lines inside cli.go mixed with legacy update routines | extract movedVerb and its parsing logic into a dedicated movedverb.go file | M |
| 2 | internal/update/versiontool.go:1 | nova-version shares package update with nova-update instead of living in its own package | migrate nova-version verbs into a dedicated internal package | L |
| 3 | internal/update/versiontool.go:137 | report and send invoke f.Prints() which bypasses structured output and suppresses JSON support | implement structured Out results for report and send so JSON functions uniformly | M |
| 4 | docs/CLI.md:1619 | Command reference links to SPEC-UPDATE.md instead of SPEC-VERSION.md for tool contract | update contract link to point to docs/SPEC-VERSION.md | S |
| 5 | docs/CLI.md:1667 | Timeout rationale recites hardware benchmark timings rather than stating operational constraints | replace host benchmark figures with concise explanation of cold execution costs | S |
| 6 | docs/CLI.md:1644 | Documentation hardcodes specific inventory counts such as the adopted 16 and 32 executables | rephrase documentation using generic terms describing manifests versus directory contents | S |
| 7 | internal/update/versiontool.go:79 | snapshot bundles two completely distinct operational modes behind complex flag validation | separate manifest checking and directory inventory into distinct verbs | M |

## Good, keep
Binary version inspection using internal/buildinfo guarantees exact provenance from compiled executables rather than file names or guessed paths.
Deterministic diffing between snapshot inventories highlights additions, deletions, and modifications without executing target binaries.
Strict multi-problem refusal grammar that validates required paths, flags, and positive durations simultaneously with actionable next-step hints.

## Compared with earlier ratings
| earlier | now | evidence |
|---|---|---|
| lives inside package update | STILL THERE | internal/update/versiontool.go:1 |
| a 345-line verb in cli.go | STILL THERE | internal/update/cli.go:715 |
| a doc section explained by fleet numbers | STILL THERE | docs/CLI.md:1667 |
| stale fixed-count prose | STILL THERE | docs/CLI.md:1644 |
| split verb homes | STILL THERE | internal/update/versiontool.go:54 |
| the README then: 6.5 to 7 from one rater, 8.4 from another | CHANGED | README.md:39 |

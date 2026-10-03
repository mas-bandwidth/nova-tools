# nova-check READ rating, nova-tools 1.1.0

Rater: OpenAI gpt-5.6-sol
Build: 2c02b2aa2042
Score: 6.5/10
README: 8/10

## Reasons

The README gives a crisp problem statement, a runnable fixture, and an honest first command. The command reference then reveals a much broader product: local prose checks, branch acceptance, a usage-evidence ledger, remote convergence, and an editor share one binary. An AI can find each verb, but it cannot form one compact model of what the tool is for. A 10 would separate those concerns or explain the unifying record-layer boundary in newcomer language before presenting the long verb inventory.

My first confusion is README.md:48: the trial coherently pins its commands and install link to 1.0.0, but gives no equally direct route to the current 1.1.0 commands being rated. My first boredom is README.md:21, where a dense seventeen-row HTML table must be scanned before the shorter adoption path. My first doubted claim is README.md:34: “checks over markdown records and repositories” is accurate at a high level but hides remote forge reads, durable state, receipt writes, and spelling edits that the banner later discloses at cmd/nova-check/main.go:28.

The writing is strongest when it states concrete contracts and limits. docs/CLI.md:27 gives a real first run, docs/CLI.md:42 explains the output and cap, and docs/SPEC.md:344 specifies hostile-path handling precisely. Tests teach those promises by executing the banner examples and the documented transcript. The entry point is easy to find, but its 112-line banner and eleven operational families make the tool feel like a toolbox with a shared executable name. Terms such as self repo, corpus, floor set, dogfood, edge, and convergence carry local meanings that require sustained reading before a stranger can safely choose a verb.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | README.md:48 | The trial is clearly pinned to 1.0.0, but a reader seeking the current 1.1.0 contract gets no parallel install or command path. | Add a current-release trial beside the retained 1.0.0 compatibility path. | S |
| 2 | cmd/nova-check/main.go:26 | The banner needs more than one hundred lines to explain checks, branch review, evidence receipts, remote trends, and an editor; the shared name no longer predicts one job. | Split remote workflow and evidence-ledger duties into focused tools, leaving record checks under nova-check. | L |
| 3 | cmd/nova-check/main.go:112 | The repository standard promises --json on every verb, but help expressly omits it from quickstart and dogfood, so automation must learn verb-specific output surfaces. | Render quickstart and dogfood through the shared result structure and expose --json on both. | M |
| 4 | docs/SPEC.md:327 | The section opens with “Ten record-layer checks,” but the verb list immediately presents eleven work verbs plus help and version, forcing the reader to infer which item is not counted. | Name the ten counted checks explicitly and label quickstart as an orchestration command. | S |
| 5 | docs/CLI.md:46 | The explanation depends on corpus, self repo, ledger, floor, and silent loss as established vocabulary before a general Markdown user has a reason to learn it. | Lead with an ordinary missing-text example, then introduce each contract term beside the command that needs it. | M |

## Good, keep

Keep the runnable quickstart with its small included fixture and explicit next checks. Keep the exact exit-code, output-cap, and deliberate-limit prose. Keep tests that execute the help examples and compare the documented first-run transcript with real output.

## Compared with earlier ratings
| earlier | now | evidence |
|---|---|---|
| 5.5 then 6: unrelated tools behind one name | STILL THERE | cmd/nova-check/main.go:26 spans local checks, branch acceptance, receipt writes, remote trend reads, durable state, and editing. |
| 5.5 then 6: private vocabulary | STILL THERE | docs/CLI.md:46 introduces corpus, self repo, ledger, and silent loss as a single conceptual bundle. |
| 5.5 then 6: two cap flags | CHANGED | cmd/nova-check/main.go:116 documents --fail-max for record findings while hygiene retains --max at cmd/nova-check/main.go:70; the split now follows different result families but still costs recall. |
| 5.5 then 6: documentation says a shipped verb does not exist | FIXED | docs/CLI.md:24 lists spelling and cmd/nova-check/main.go:256 dispatches it. |
| 8.3: newcomer path assumes one self-repository workflow | CHANGED | README.md:34 now offers an included fixture, but docs/CLI.md:29 still teaches the tool through a self repo and later verbs assume local process terms. |
| README 6.5 to 7 and 8.4 | CHANGED | README.md:19 gives a clear problem-first table and README.md:34 a runnable check fixture, while README.md:48 gives only the coherent older-release trial rather than a current 1.1.0 path. |

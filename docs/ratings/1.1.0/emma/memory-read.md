# nova-memory READ rating, nova-tools 1.1.0

Rater: gemini-2.5-pro
Build: 2c02b2aa2042
Score: 8/10
README: 8/10

## Reasons
The tool provides a disciplined, in-memory lexical retrieval system that treats personal records as read-only data. Rebuilding the BM25 and trigram index on each run eliminates background server state, cache invalidation bugs, and index drift. The refusal philosophy is exemplary: missing flags report all omissions at once with guidance toward working invocations, exit codes preserve semantic boundaries, and the evaluation harness measures MRR and Recall@k against gold fixtures rather than assuming retrieval quality.

A score of 10 would require decomposing main.go into focused packages, eliminating stderr scraping for JSON refusal formatting, moving shell quoting routines to shared helpers, relocating the quickstart function words map, and clarifying boot output behavior.

The first place of confusion was docs/CLI.md:347, where boot is presented alongside retrieval commands without making clear that it merely validates pins and measures file sizes rather than loading content into context.
The first place of boredom was cmd/nova-memory/main.go:477, where a 22-line table of common English function words is hardcoded directly into command line flag dispatch logic.
The first place of doubting a claim was cmd/nova-memory/main.go:50, where the banner asserts that an unrelated probe provides a universal noise threshold, even though the fixed corporate marketing sentence shares vocabulary with business notes and fails to calibrate short queries.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | cmd/nova-memory/main.go:1286 | main.go is a 1286-line monolithic file bundling flag parsing, shell quoting, pin checking, gold parsing, and verb execution | decompose into separate source files for quickstart, boot, eval, and verification | L |
| 2 | cmd/nova-memory/main.go:864 | cmdSearch and cmdCheck redirect stderr to an in-memory buffer to scrape text for JSON refusals | handle structured validation errors directly without capturing stderr streams | M |
| 3 | cmd/nova-memory/main.go:516 | shell argument escaping functions span 50 lines duplicating general-purpose CLI helpers | move commandLineFor and shellArg helpers to an internal shared utility package | M |
| 4 | cmd/nova-memory/main.go:477 | quickstartFunctionWords map occupies 22 lines inside command dispatch | relocate function word filtering to internal memindex alongside tokenizer routines | S |
| 5 | cmd/nova-memory/main.go:1240 | readGold parsing logic is embedded directly in the CLI entry point file | move gold fixture loading into a dedicated evaluation helper | S |
| 6 | cmd/nova-memory/main.go:50 | banner claims fixed probe serves as universal noise baseline across every corpus | qualify calibration documentation to note query length and domain vocabulary effects | S |
| 7 | docs/CLI.md:347 | boot summary implies session memory loading without clarifying that only sizes and counts are verified | document that boot performs validation without echoing file contents | S |

## Good, keep
Rebuilding the index in memory on every run guarantees byte-identical deterministic results without persistent database drift.
Refusal messages report all missing flags simultaneously in sorted order alongside actionable next-step hints.
The eval verb pairs BM25 retrieval with an objective known-answer benchmark computing MRR and Recall@k.

## Compared with earlier ratings
| earlier | now | evidence |
|---|---|---|
| a 1,394-line main.go that hand-prints every verb twice and scrapes its own stderr for --json | CHANGED | cmd/nova-memory/retrieval.go:35 and cmd/nova-memory/main.go:864 |
| the universal unrelated/noise calibration claim is not supported | STILL THERE | cmd/nova-memory/main.go:50 |
| the README then: 6.5 to 7 from one rater, 8.4 from another | CHANGED | README.md:32 |

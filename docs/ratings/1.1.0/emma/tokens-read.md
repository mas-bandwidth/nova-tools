# nova-tokens READ rating, nova-tools 1.1.0

Rater: gemini-2.5-pro
Build: 264f9c0135c7
Score: 7.5/10
README: 8/10

## Reasons
The tool enforces an exacting, durable accounting discipline for token expenditures across diverse AI session transcripts and provider logs. Its design principles are strong: every path is an explicit flag, no ambient environment variables are consulted outside Redis credentials, the five token dimensions remain strictly segregated without lossy conversion, and updates to day files use atomic writes under a file lock. Exit code semantics clearly separate successful execution from unreadable inputs or partial claims.

A score of 10 would require decomposing main.go into discrete verb files, streamlining cmdFold into smaller composable units, deduplicating message IDs across overlapping sources, and standardizing line output and status tokens to align with the shared tool skeleton.

The first place of confusion was docs/CLI.md:1402, where check is described as the verification gate but bare check permits unrecorded intermediate date gaps without failure unless --strict or --no-spend is explicitly supplied.
The first place of boredom was cmd/nova-tokens/main.go:46, where 120 lines of dense narrative documentation and flag specifications are embedded in a raw string constant inside the CLI entry point.
The first place of doubting a claim was cmd/nova-tokens/main.go:21, where the package documentation asserts that a message is counted once and never estimated, yet cross-source transcript overlaps fold duplicate IDs into doubled spend totals and exit 0.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | cmd/nova-tokens/main.go:1492 | main.go is a 1492-line monolithic file bundling CLI parsing, fold, sum, check, report, and sources | decompose into individual verb files per command | L |
| 2 | cmd/nova-tokens/main.go:554 | cmdFold spans 257 lines coordinating lock acquisition, transcript reading, source merging, and reporting | factor fold execution and validation passes into focused internal helpers | L |
| 3 | internal/tokens/tokens.go:215 | Folder.Add folds identical message IDs across multiple sources into rows rather than deduplicating or failing | deduplicate shared IDs across sources or refuse overlapping inputs | M |
| 4 | cmd/nova-tokens/main.go:608 | command output uses disparate line tokens like TOKENS FOLD and status words like CHECK MISSING | align line tokens and status words with the unified tool skeleton | M |
| 5 | cmd/nova-tokens/main.go:323 | sourceFlags.check executes over 100 lines of manual flag mutual-exclusivity and dependency checks | adopt structured flag group validation | M |
| 6 | docs/CLI.md:1402 | documentation describes check as the gate without prominently noting that date gaps pass without --strict | document default gap allowance versus strict calendar verification | S |
| 7 | cmd/nova-tokens/main.go:46 | usage constant spans 120 lines repeating specifications from external documentation | trim usage banner to concise command syntax and core nouns | S |

## Good, keep
Strict read-only ingestion with atomic writes and directory locking prevents concurrent ledger corruption.
Separation of the five token dimensions without synthetic blending or estimated fill.
Refusal messages report every missing flag and mutual exclusivity conflict in one turn with runnable remedies.

## Compared with earlier ratings
| earlier | now | evidence |
|---|---|---|
| a 1,454-line main.go | STILL THERE | cmd/nova-tokens/main.go:1492 |
| a 220-line fold | STILL THERE | cmd/nova-tokens/main.go:554 |
| five line-tokens and three status words in one tool | STILL THERE | cmd/nova-tokens/main.go:608 |
| overlapping sources can still yield doubled, successful totals | STILL THERE | internal/tokens/tokens.go:215 |
| the README then: 6.5 to 7 from one rater, 8.4 from another | CHANGED | README.md:31 |

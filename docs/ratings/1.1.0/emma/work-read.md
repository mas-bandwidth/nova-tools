# nova-work READ rating, nova-tools 1.1.0

Rater: gemini-2.5-pro
Build: 264f9c0135c7
Score: 8.5/10
README: 7/10

## Reasons
The tool demonstrates an exceptionally rigorous architecture. It models issue tracking through a single canonical s-expression tree file governed by a formal TLA+ specification (tla/WorkImport.tla). Both import and verify operate through a strictly read-only GitHub GraphQL seam that counts calls and validates plan budgets before issue retrieval begins. The round-trip encode-decode check inside import ensures no corrupted data is written, and verify checks source equality field for field.

However, adoption starts on the harder path. The first place of confusion is README.md:24, where nova-work is completely missing from the overview table and absent from docs/CLI.md, giving a newcomer no introduction or entry point in the main documentation. The first place of boredom is docs/SPEC-WORK-V1.md:82, where fifteen rows of mechanical field-to-keyword mappings catalog routine GitHub payload attributes. The first place of doubting a claim is docs/SPEC-WORK-V1.md:64, which asserts that the tree file is strictly canonical and identified by its SHA-256 hash, yet cmd/nova-work/import.go:107 embeds the dynamic wall-clock fetch timestamp in :fetched on every run, causing identical repository states to generate differing hashes across imports.

A score of 10 would require implementing structured JSON output across all verbs instead of disabling it with Prints(), adding nova-work to the README directory table and docs/CLI.md, providing an offline quickstart verb using the bundled reliable fixture, removing the dynamic fetch timestamp from the canonical tree hash, and replacing the string substitution in decode.go with a clean error abstraction.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | cmd/nova-work/import.go:27 | import and verify use f.Prints() to disable structured output and refuse --json as unknown, violating the standard requirement that every verb supports --json | remove f.Prints() and render both line text and structured JSON from common tool.Out values | L |
| 2 | README.md:24 | nova-work is absent from the tool directory table and omitted from docs/CLI.md, leaving newcomers with no front-door documentation | add nova-work to the README table with its banner summary and document its verbs in docs/CLI.md | M |
| 3 | cmd/nova-work/main.go:42 | the first run requires a live authenticated GitHub CLI login, lacking an offline quickstart mode or local fixture demonstration | add an offline quickstart verb that exercises import and verify against the bundled replay fixture | M |
| 4 | docs/SPEC-WORK-V1.md:64 | the spec claims the tree is canonical and identified by its SHA-256, but import.go:107 records a live wall-clock timestamp in :fetched | normalize or exclude fetch timestamps from the canonical tree hash computation | S |
| 5 | internal/workfile/decode.go:29 | Decode performs string replacement on error messages to rewrite plan phrasing into tree terminology | decouple internal/worklang refusal text so callers do not perform text substitutions | S |
| 6 | cmd/nova-work/import.go:87 | runImport writes ad-hoc failure banners to stderr and exits instead of returning structured refusal objects | route execution failures through tool.Refuse or standard error structures | S |

## Good, keep
Strict preflight call budget estimation that checks anticipated requests against limits before reading issues.
Automatic encode-decode round-trip verification before writing the tree file to disk.
Field-for-field diff classification distinguishing missing, extra, and drifted attributes with exact paths.

## Compared with earlier ratings
| earlier | now | evidence |
|---|---|---|
| adoption still starts on the harder path | STILL THERE | cmd/nova-work/main.go:42 |
| the one tool built the way the standard says; small leftovers only | STILL THERE | cmd/nova-work/import.go:27 |
| the README then: 6.5 to 7 from one rater, 8.4 from another | STILL THERE | README.md:24 |

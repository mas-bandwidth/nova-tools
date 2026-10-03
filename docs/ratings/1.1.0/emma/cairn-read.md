# nova-cairn READ rating, nova-tools 1.1.0

Rater: gemini-2.5-pro
Build: 2c02b2aa2042
Score: 8/10
README: 7/10

## Reasons
The documentation and code present a focused tool for checkpointing session state across boundaries without imposing lifecycles or consolidation. The four verbs (open, append, index, receipt) provide an intuitive interface that treats persistence and publication separately.

First confused: docs/CLI.md:2088 lacks the standard ### First run section mandated by ONBOARDING point 3, leaving nova-cairn as an outlier among commands in the reference document. In addition, README.md:16 points to docs/USAGE.md#installing where the installing anchor does not exist.

First bored: docs/SPEC-CAIRN.md:15 recites a lengthy catalog of negative constraints (no seal, no consume, no delete, no grading, no consolidation) that is reiterated nearly verbatim in cmd/nova-cairn/main.go:11 and internal/cairn/cairn.go:6.

First doubted a claim: internal/cairn/cairn.go:536 invokes atomicfile.WriteFile without atomicfile.NoReplace, allowing concurrent appends with identical entry IDs to overwrite each other rather than failing as conflicts. Furthermore, cmd/nova-cairn/main.go:50 asserts that append stores entries under entries/<session>/<entry>.json, which does not apply to flat bench stores.

A 10 would need:
1. Add the missing ### First run section to docs/CLI.md:2088.
2. Fix or redirect the broken installing anchor in README.md:16.
3. Prevent concurrent same-ID appends from overwriting by using atomicfile.NoReplace.
4. Extract flat bench file mutation logic from internal/cairn/cairn.go into a dedicated file.
5. Provide a runnable command example without brackets in docs/CLI.md:2098.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | internal/cairn/cairn.go:536 | atomicfile.WriteFile is called without NoReplace allowing concurrent appends with the same ID to overwrite | Pass atomicfile.NoReplace so colliding concurrent appends yield a conflict error | S |
| 2 | docs/CLI.md:2088 | nova-cairn section omits the standard ### First run heading required by ONBOARDING point 3 | Add ### First run with runnable example invocations and output transcript | S |
| 3 | cmd/nova-cairn/main.go:50 | How banner states append keeps entries in entries directory which does not apply to flat stores | Note in banner that flat bench stores append sections to the session markdown file | S |
| 4 | docs/CLI.md:2098 | Example command includes bracketed optional flag syntax [--text] which cannot run as printed | Provide concrete command without shell bracket syntax | S |
| 5 | README.md:16 | Anchor docs/USAGE.md#installing does not exist in target document | Add installing heading to USAGE.md or link directly to document root | S |
| 6 | internal/cairn/cairn.go:268 | appendBench trims trailing newlines and appends a single newline altering text bytes | Store entry text byte-for-byte as specified in SPEC-CAIRN.md | S |
| 7 | internal/cairn/cairn.go:245 | Bench store append implementation remains mixed with nested store logic in cairn.go | Move appendBench and bench file helpers into a dedicated flat store file | M |

## Good, keep
Four clean verbs (open, append, index, receipt) that record session notes durably without imposing lifecycle mechanisms.
Explicit separation between local fsync persistence and remote publication across all verbs.
Whole-verb remedy generation in noRecord errors quoting exact shell-executable commands.

## Compared with earlier ratings
| earlier | now | evidence |
|---|---|---|
| the store keeps two shapes tangled in one file | CHANGED | internal/cairn/read_existing.go:28 |
| same-id concurrent appends can overwrite what the banner promises never is | STILL THERE | internal/cairn/cairn.go:536 |

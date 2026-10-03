# nova-update USE rating, nova-tools 1.1.0

Rater: gemini-2.5-pro
Build: 50c0c29cb5dc
Score: 7.5/10

## Reasons
The tool was exercised cold inside a clean scratch directory using compiled binaries from cmd/nova-update. The onboarding workflow runs cleanly: nova-update example writes a working one-tool manifest (Go) and outputs the next command to execute. Subsequent invocations of report and status read the installed Go toolchain and verify it against local checks in milliseconds. A complete fake tool upgrade was constructed and executed locally: status correctly identified a stale version (1.0.0 vs 1.1.0), apply --dry-run printed the planned command without modifying state, apply ran the update command to completion, and status subsequently confirmed an equal state exiting 0. Provoked refusals for missing required flags, unknown flags, unknown verbs, and malformed durations reliably surfaced all missing inputs simultaneously and directed callers to specific help targets.

However, several usability frictions prevent a higher score. First, declaring a tool dependency with kind pin using local:go version causes nova-update status to parse the output tokens incorrectly, reporting installed=version and latest=version instead of the semver string. Second, release verbs such as build enforce a strict v-prefix requirement, refusing version inputs like 1.2.3 that the core apply verb expects. Third, when nova-update watch encounters a failing check, its entire output—including the final ADOPT DONE summary line—is routed to stderr while stdout remains empty. Fourth, neither watch nor release supports structured --json output. Finally, verbs requiring external services (report --store requiring a running store, report --send requiring an active message bus, and release cut/adopt requiring remote forge and machine connectivity) were evaluated only through help and dry-run modes and could not be run live in scratch isolation.

A 10/10 would require fixing pin token extraction to read dotted semver strings, standardizing version prefix expectations between apply and release, keeping regular status summaries on stdout for watch, and implementing structured --json across all verbs.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | `nova-update status --file pin.tsv` | Declaring kind pin with local:go version extracts literal version instead of semver numbers | Use dotted version extraction instead of naive token indexing for pin kinds | S |
| 2 | `nova-update release build --version 1.2.3 --out ./rel --source .` | release build rejects version 1.2.3 demanding v-prefix while apply expects numbers without v | Unify version prefix handling across release and apply verbs | S |
| 3 | `nova-update watch --adopt checks.tsv` | When a check fails the entire output including ADOPT DONE moves to stderr while stdout is empty | Keep standard summary lines on stdout and reserve stderr for tool error messages | S |
| 4 | `nova-update watch --adopt checks.tsv --json` | watch refuses --json flag even though repository standards expect JSON support across all verbs | Implement structured JSON output for watch verb | M |
| 5 | `nova-update release build --json` | release verbs reject --json flag even though repository standards expect JSON support across all verbs | Implement structured JSON output across release verbs | M |
| 6 | `nova-update apply --file versions.tsv go --version v1.2.3` | apply rejects v-prefixed version strings when applying updates with version arguments | Accept either v-prefixed or bare semver strings transparently | S |

## Good, keep
Deterministic multi-flag refusal messages that identify all missing arguments simultaneously with actionable remedies.
Safe and verifiable update execution via --dry-run planning prior to executing any external commands.
Local manifest generation with runnable onboarding examples that require zero network infrastructure.

## Compared with earlier ratings
| earlier | now | evidence |
|---|---|---|
| a pin through the help's own local:go version reports latest=version | STILL THERE | `nova-update status --file pin.tsv` printed STATUS EQUAL name=go kind=pin installed=version latest=version |
| release verbs refuse the help's example version | STILL THERE | `nova-update release build --version 1.2.3 --out ./rel --source .` printed BUILD REFUSED: the version "1.2.3" is not v-prefixed |
| watch's summary moves to stderr | STILL THERE | `nova-update watch --adopt checks.tsv` with a failing check routed ADOPT DONE to stderr |
| a local fake upgrade completes and rechecks | FIXED | `nova-update apply --file fake.tsv faketool` upgraded 1.0.0 to 1.1.0 and recheck exited 0 |

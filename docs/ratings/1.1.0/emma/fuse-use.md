# nova-fuse USE rating, nova-tools 1.1.0

Rater: gemini-2.5-pro
Build: 2c02b2aa2042
Score: 9/10

## Reasons
The tool functions reliably and strictly as documented. It operates cleanly on local files with zero external service dependencies. File operations are atomic and safe against torn writes, and error handling consistently fails closed when files are missing or unreadable. Multi-error reporting allows callers to correct all parameter mistakes in a single turn, and help doors are clearly named in every refusal.

A score of 10 would require preserving the original incident timestamp when re-blowing a surface, providing structured JSON output on status and inspection verbs, implementing dry-run support for write verbs, and permitting standard -h help flags on informational subcommands.

All verbs (init, status, check, lockdown, quarantine, lift quarantine, lift lockdown, path, version) were exercised locally against test files in scratch. No verbs required external services or network access.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | `nova-fuse quarantine --box ./fuse-box.json site-a "updated reason"` | re-blowing an already quarantined surface silently overwrites its timestamp and reason without notice | preserve the original incident timestamp or notify the caller that an existing quarantine was replaced | S |
| 2 | `nova-fuse status --box ./fuse-box.json --json` | status rejects the standard json flag preventing automated tools from consuming structured state | support --json output on status to emit structured lockdown and quarantine arrays | M |
| 3 | `nova-fuse quarantine --box ./fuse-box.json site-a "reason" --dry-run` | write verbs reject dry-run preventing operators from previewing state changes before committing them | implement --dry-run across write verbs to print planned state mutations without touching disk | M |
| 4 | `nova-fuse status -h` | informational subcommands reject standard -h flags despite not serving as gatekeeper endpoints | allow -h on non-gate verbs like status path and init to print usage without error | S |

## Good, keep
World-readable JSON box format allows auditing without specialized tooling.
Fail-closed semantics treat absent or corrupted box files as blown, preventing accidental ingestion leaks.
Clear multi-error reporting names both missing configuration flags and missing positional arguments in a single turn.

## Compared with earlier ratings
| earlier | now | evidence |
|---|---|---|
| a re-blow silently rewrites the fuse's time and reason | STILL THERE | `nova-fuse quarantine --box ./fuse-box.json site-a "second reason"` updates timestamp and reason without warning |
| the unknown-option refusal omits the offending flag | FIXED | `nova-fuse check --box ./fuse-box.json --bogus` outputs unknown flag --bogus |

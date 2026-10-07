# nova-redis review, 2026-10-05

Rater: inception/mercury-2.5
Build: 3baf154bb6ef
Verdict: GOOD WITH FIXES for an AI to use
Score: 7.5/10

## Reasons
The help clearly explains what the tool does, its verbs, and how to use it with a runnable example block. The exit codes are documented (0=ok, 1=store error, 2=usage/connection error) and every refusal names what it wants with a remedy. Running the tool against a scratch store shows it works correctly for spill/recall and fn load/check.

First confusion: docs/SPEC-REDIS.md:67 mentions "no TTL policy" in the context of serve persistence rules, but it is not clear this refers to the fleet store keys not expiring, not scratch keys which do have TTL. The spec does clarify scratch keys have TTL in rule 2 (line 91-93), but this cross-reference is not obvious.

First doubted claim: The example block in the banner (docs/CLI.md section for nova-redis) shows `nova-redis spill --addr 127.0.0.1:6379 ...` but there is no `quickstart` or example store running in the tool itself. An AI cannot run these examples without first starting a redis-server.

To reach a 10, the tool should have a quickstart verb or self-contained example that runs with `--dry-run` or against an embedded test store.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | `nova-redis fn load --help` (missing) | The help output for subcommands (fn load, fn check, spill, recall) does not show individual help text. Only the main help shows usage. | Add `--help` support for `fn load` and `fn check` subcommands similar to other nova tools | S |
| 2 | `nova-redis spill --addr 127.0.0.1:6379 --owner rowan --name test3 2>&1` | Missing --ttl and --value flags report separate refusal lines, but a single run should report all missing required flags at once. | Aggregate missing flag errors and report them together in one refusal line | S |
| 3 | docs/SPEC-REDIS.md:41-46 | The spec says fn check outputs OK/STALE/MISSING but does not explicitly state that exit code 1 is used for STALE and MISSING, only for "failure". The code implements this but the spec is slightly ambiguous. | Add explicit "exit 1" after STALE or MISSING in the spec | S |

## Good, keep
- Every refusal names what it wants and provides a remedy that can be pasted
- The tool refuses to guess (missing --addr is exit 2 with explicit refusal)
- JSON output could be added via --json flag for machine-readable results

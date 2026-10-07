# nova-config review, 2026-10-05

Rater: inception/mercury-2.5
Build: 52046bd9
Verdict: GOOD WITH FIXES for an AI to use
Score: 7/10

## Reasons

This is a well-structured configuration management tool for a fleet. The spec (docs/SPEC-CONFIG.md) is comprehensive, and the CLI help (docs/CLI.md) clearly documents flags, environment variables, and expected output. The kind-based design (machine, friend, fleet, sprint) is consistent across verbs.

First confusion: docs/CLI.md line 1534 says the password is read from "the variable NOVA_PG_PASSWORD_ENV names (NOVA_PG_PASSWORD when unset)" which is correct but the naming is confusing—it reads like a meta-variable. The actual env var is NOVA_PG_PASSWORD by default. This requires reading docs/nova-config/README.md or the code to understand.

First doubted claim: The tool says "Postgres is the permanent store" in usageTop but requires Redis for many common operations like `machine list --redis` (for live facts) and `apply`. The separation is documented but a new user might not realize Redis is practically required for most workflows.

## Findings

| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | docs/CLI.md:1534 | Env var naming is confusing: NOVA_PG_PASSWORD_ENV sounds like a meta-variable that names another variable, when it's just a password holder. Requires reading docs/nova-config/README.md to clarify. | Clarify with a comment in docs: "NOVA_PG_PASSWORD_ENV: name of the variable holding the password (defaults to NOVA_PG_PASSWORD)" | S |
| 2 | `nova-config migrate` docs | When schema is already current, docs/nova-config/README.md:55-57 says it prints `applied=0` but the example block at README.md:50 only shows `applied=5`. The output for the current-schema case isn't shown as an example line. | Add an example showing the `applied=0` output when running migrate again | S |
| 3 | docs/SPEC-CONFIG.md:315 | Exit code description says "Exit 1 is the store saying no" but docs/CLI.md:1538 correctly says "Exit 1 is the store or Redis saying no". The spec is slightly imprecise. | Update SPEC-CONFIG.md to match CLI.md: "Exit 1 is the store or Redis saying no" | S |

## Good, keep

- Clear separation between Postgres (permanent) and Redis (rebuildable copy)
- All required fields named with helpful descriptions
- Consistent grammar across all kinds (add/set/remove/list/show/history)
- History tracking built-in with actor information
- Every refusal prints a next step command

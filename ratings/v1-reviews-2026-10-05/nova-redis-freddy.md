# nova-redis review, 2026-10-05

Rater: inception/mercury-2.5 (opencode harness)
Build: 3baf154
Verdict: GOOD WITH FIXES for an AI to use
Score: 8.5/10

## Reasons
This tool follows the nova-tool standard closely: clear banner, explicit refusals with remedies, and consistent exit codes. The help tells you what to type, and every refusal names its fix. Exit code 2 is used before any dial for missing/invalid flags; exit code 1 for store refusals (MISSING, EXPIRED); exit code 0 for success. This is correct.

First confusion: the usage text says the password comes from NOVA_REDIS_PASSWORD_ENV (defaulting to NOVA_REDIS_PASSWORD) but the constant definitions show PasswordEnvEnv defaults to NOVA_REDIS_PASSWORD_ENV, then PasswordEnv (NOVA_REDIS_PASSWORD). The example doesn't show how to set these variables in practice.

First doubted claim: "fn load is for the one place that deploys" - but there's no mechanism to prevent multiple deployers from overwriting each other's library. The TLA+ model exists (MCRedisFnTwoDeployers) but the tool doesn't prevent this race.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | docs/SPEC-REDIS.md and main.go:45-52 | The password variable documentation says `--password-env` defaults to `NOVA_REDIS_PASSWORD_ENV`, else `NOVA_REDIS_PASSWORD`, but the code comment says the same thing while `PasswordEnvEnv = "NOVA_REDIS_PASSWORD_ENV"` and `PasswordEnv = "NOVA_REDIS_PASSWORD"` - a reader must trace through two levels of indirection | Add one clear sentence: "defaults to the variable named in NOVA_REDIS_PASSWORD_ENV, or NOVA_REDIS_PASSWORD if that is unset" | S |
| 2 | cmd/nova-redis/main.go:45-52 (usage text) | The example shows spill/recall but doesn't show how a bench actually runs these commands (via nova-secrets exec) | Add one example showing the full command: `nova-secrets exec --only NOVA_REDIS_PASSWORD -- nova-redis spill --addr ...` | S |
| 3 | cmd/nova-redis/main.go (usage text) | The usage text lists the verbs but doesn't state what exit codes mean at a glance | Add exit code table after usage (like other nova tools have) | S |

## Good, keep
- Clear refusal messages that name what is missing and why guessing is bad ("refusing to guess localhost")
- Consistent exit codes: 2 before dial for bad flags, 1 for store refusals, 0 for success
- One round trip per verb (spill and recall each make exactly one pipeline call)

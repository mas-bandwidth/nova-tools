# nova-config review, 2026-10-05

Rater: inception/mercury-2.5
Build: 52046bd9
Verdict: GOOD WITH FIXES for an AI to use
Score: 7.5/10

## Reasons
This tool does one thing well: managing fleet configuration in Postgres and applying it to Redis. The spec is thorough (SPEC-CONFIG.md), and the CLI is generated from descriptors so all kinds have consistent verbs. However, for cold AI use there are three issues:

1. **docs/CLI.md lacks a dedicated nova-config section for first-run examples** (line 1490+ exists but is brief). A cold reader cannot paste working commands without reading the separate docs/nova-config/README.md.
2. **The help system is inconsistent:** `nova-config <kind> <verb> -h` works, but `nova-config help` returns the full banner. A stranger doesn't know which to use first.
3. **Password handling is opaque:** The NOVA_PG_PASSWORD_ENV mechanism is not explained in the help; it's only in the docs. A first run without env setup will fail.

A 10 would have inline help that shows a minimal working example (like `nova-config migrate --print`), and the help output would reference the README for deeper context.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | docs/CLI.md:1490-1539 | nova-config section exists but lacks first-run commands that don't need a store | Add `kinds` and `migrate --print` example transcript at start of section | S |
| 2 | cmd/nova-config/main.go:202-203 | `help` prints full usage but doesn't say to run `<verb> -h` for verb-specific help | Add one line to help output: "For a single verb's flags, run: nova-config <verb> -h" | S |
| 3 | docs/nova-config/README.md:21-22 | Password env mechanism is documented but not referenced in CLI help | Add a brief note in the help output pointing to the README for password setup | S |

## Good, keep
- Generated kinds with consistent verbs: the descriptor approach means adding new kinds requires zero CLI code changes.
- Clear separation between permanent config (Postgres) and runtime copy (Redis), with `apply` as the bridge.
- Every write records history with actor and timestamp; `history` verb makes this inspectable.

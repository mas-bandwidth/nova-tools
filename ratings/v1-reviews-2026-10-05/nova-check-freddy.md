# nova-check review, 2026-10-05

Rater: Inception/Mercury-2.5 (harness: opencode)
Build: 3baf154bb6ef96ed62037ecde8a8a8b93ea39149
Verdict: GOOD WITH FIXES for an AI to use
Score: 8.5/10

## Reasons

This is a well-engineered CLI that follows strong design principles: no-guessing (missing flags refuse), capped output with remedies, clear exit codes, and a thoughtful first-run path via quickstart. Help is comprehensive.

First confusion: `hygiene` requires `--identity` but the flag's description says "a string of one or more identity pairs" - doesn't explicitly say format is `Name <email>`. Found in `docs/CLI.md` line 19.

First doubted claim: `corpus` reads a ledger "you wrote in advance" - an AI can't pre-write this. Found in `docs/CLI.md` line 45.

## Findings

| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | `nova-check help` / `docs/CLI.md` | `--identity` description doesn't explicitly say the format is `Name <email>` - AI may guess wrong format | Add "(format: Name <email>)" to the flag description | S |
| 2 | `corpus` verb / `docs/CLI.md` line 45 | Says "you wrote in advance" but AI can't pre-write ledgers - AI user may not know what to do | Clarify AI can't use corpus without a pre-prepared ledger; suggest manual setup or alternative | S |
| 3 | `convergence` verb | Only --json flag exists on convergence; other verbs like quickstart, links, nocode don't support --json for machine parsing | Add --json flag to all listing verbs with consistent output shape | M |

## Good, keep

- No-guessing law: every missing flag refuses with clear hint about what it wants
- Capped output with `--fail-max` and `MORE` line showing how to see rest
- `quickstart` verb for first run with zero setup requirements

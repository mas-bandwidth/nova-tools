# nova-sandbox review, 2026-10-05

Rater: inception/mercury-2.5
Build: 52046bd9 (v1.0.0)
Verdict: GOOD WITH FIXES for an AI to use
Score: 7.5/10

## Reasons
The tool is well-engineered with comprehensive docs (SPEC-SANDBOX.md) and strong security design. However, an AI using it cold would face two problems:

1. Confusion: HOME is required but not documented in the banner usage. The example shows HOME being set, but the main usage line doesn't mention it (docs/CLI.md line 747-770; cmd/nova-sandbox/main.go usage block lines 43-63).

2. Doubt: The bare form usage says "run: nova-sandbox help" for unknown verbs, but help <unknown> prints the full help without noting it's not a verb (main.go lines 193-200). An AI would try `nova-sandbox help probe` then `nova-sandbox probe help` wondering which is right.

A 10 would need: HOME mentioned in usage banner; help output explicitly distinguishing verbs from flags.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | `nova-sandbox --write /tmp/x -- /bin/sh -c pwd` (no HOME) | SANDBOX REFUSED reason=home_outside: HOME is unset but required (cmd/nova-sandbox/main.go:672-680) | Add HOME=<dir> to usage banner; clarify in example that HOME must point inside a --write | S |
| 2 | `nova-sandbox help probe` vs `nova-sandbox probe help` | Both work but only the first is documented; an AI would not know which to use (main.go:193-200; usage line 63) | In help output for each verb, show that `help <verb>` and `<verb> help` are both accepted | S |
| 3 | `nova-sandbox --read /tmp/a --write /tmp/a` | SANDBOX REFUSED reason=bad_read: same path in both lists (policy.go:607-613) | Already correctly refused and named; could be slightly clearer that --write already includes read | S |
| 4 | `nova-sandbox --read /tmp/x --write /tmp/y --net-deny --net-listen` | SANDBOX REFUSED reason=bad_net (policy.go:544-546) | Already correctly refused and named | S |

## Good, keep
- The probe verb proves the sandbox works before running actual commands (SPEC-SANDBOX rule 10)
- Refusals always name the flag and the desired form (SPEC-SANDBOX rule 16)
- The JSON/one-line output grammar is consistent across all verbs (SPEC.md conventions)
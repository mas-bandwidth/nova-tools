# nova-sandbox review, 2026-10-05

Rater: gpt-5.6-terra
Build: 3baf154bf084
Verdict: GOOD WITH FIXES for an AI to use
Score: 7.5/10

## Reasons
The tool has a clear security purpose, a precise containment contract, and help for every advertised verb. My first confusion was that the first `probe` example asks for a personal credential-file path (`docs/CLI.md:768-771`) while the surrounding explanation says it is optional and its contents are never read (`docs/CLI.md:780-787`). My first doubted claim was that a refusal always tells an AI what to type next (`docs/CLI.md:797-815`): `nova-sandbox help pobe` quietly exits 0 with the general banner instead of identifying the misspelled verb. The deliberate invalid flag was otherwise well handled by `nova-sandbox check --bogus`. A 10 would retain the security contract while making the smallest safe invocation, a misspelled help target, and a nested-sandbox failure equally clear.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | `nova-sandbox check --json`; `HOME=/Volumes/nova/ai/stella/working/jobs/review-sandbox-stella.w2~15/nova-tools/scratch/home nova-sandbox probe --write /Volumes/nova/ai/stella/working/jobs/review-sandbox-stella.w2~15/nova-tools/scratch --json`; `cmd/nova-sandbox/main.go:484-495` | `check` reports an available backend, but inside this enclosing Studio sandbox the next safe probe fails both `write_inside` and `read_root`; the result says to run probe help rather than naming the enclosing sandbox as a likely remedy. | Say in `check` and the First run text that it checks backend availability only; direct a probe whose own write and root checks both deny to run outside an enclosing sandbox. | S |
| 2 | `nova-sandbox help pobe`; `cmd/nova-sandbox/main.go:192-200` | A typo in `help <verb>` returns the full banner with exit 0 and no notice that `pobe` is not a verb. An AI can mistake this for successful verb-specific help. | Prefix the general banner with one line naming the unknown help target and the available verbs. | S |
| 3 | `docs/CLI.md:768-787`; `cmd/nova-sandbox/main.go:100-104` | The first probe transcript supplies `--secret /Users/me/.config/anthropic/env` even though the binary and following prose say `--secret` is optional. An AI without a credential file is led to invent a path or stop. | Make the first transcript the no-secret probe; show the secret form separately as an optional extension. | S |

## Good, keep
- The first-run sequence makes `check` precede work and makes the containment purpose plain (`docs/CLI.md:705-719`).
- The specification says clearly that a missing backend is a refusal rather than degraded containment (`docs/SPEC-SANDBOX.md:43-53`).
- `nova-sandbox check --bogus` names the malformed flag and a usable help route.

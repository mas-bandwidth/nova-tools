# nova-ci AI-usability review, 2026-10-15

Reviewer: x-ai/grok-build-0.1
Build: f6c35d67aaa1
Score: 9.5 (52/55)

## Rubric
| # | criterion | score | evidence |
|---|---|---|---|
| 1 | cold | 5 | `nova-ci slowtests --example` -> `CI-SLOW package=... seconds=65.1s budget=60s slowest=...` |
| 2 | refusals | 5 | `nova-ci slowtests --bogusflag` -> `nova-ci slowtests REFUSED: unknown flag --bogusflag; the flags of slowtests are ...; run: nova-ci slowtests -h` |
| 3 | output | 4 | `nova-ci local --dry-run` -> `nova-ci local: base=origin/dev ... packages=145 ...` (local uses "nova-ci <verb>:" while others use "CI-*"; --json where offered) |
| 4 | dry-run | 5 | `nova-ci local --dry-run` -> `nova-ci local: would run nice -n 15 make test ...` (no tests run); new-rule --dry-run "would write"; github --dry-run "ev=-" |
| 5 | idempotent | 5 | `n/a: writes take no --op (local temp; new-* refuse on existing file; github receipt append)` |
| 6 | no prompts | 5 | `n/a: no verb waits for interactive answer; --example and flags suffice` |
| 7 | exit codes | 5 | `nova-ci slowtests --example --enforce` -> exit 1 (CI-SLOW) matches help "1 a CI-SLOW line under --enforce" |
| 8 | one word | 4 | `nova-ci help` -> "bench run", "github receipt" (two words in list) |
| 9 | push | 5 | `n/a: no poll/wait on state; inputs read once to completion` |
| 10 | no leaks | 5 | `nova-ci github receipt --dry-run ...` -> `CI RECEIPT ... ev=-` (no secret) |
| 11 | true help | 4 | `nova-ci slowtests --example --budget 10` -> `CI-SLOW OK packages=1 ...` (help claims only over-budget CI-SLOW lines) |

## 10/10 blockers
| # | where | what an AI meets | fix | size |
|---|---|---|---|---|
| 1 | `nova-ci slowtests --example` | `CI-SLOW OK packages=1 ...` (within budget); help only describes "one CI-SLOW line per package ... over" | document CI-SLOW OK (and WARN) forms and fields | S |
| 2 | `nova-ci help` | lists "bench run", "github receipt" (two words) | use single-word verbs (or document as one) | M |
| 3 | `nova-ci slowtests --example --budget 10` | `CI-SLOW OK ...` and `CI-SLOW WARN ...` lines not described in help | update help to cover exact success/warn lines | S |
| 4 | `nova-ci local --dry-run` (outside git) | long refusal includes `git rev-parse ...: exit 128: fatal: not a git repository...` | shorten; omit raw subproc failure text | S |
| 5 | `nova-ci slowtests` (when load probe fails) | `CI-LOAD load=unknown ... (the load could not be read: sysctl -n vm.loadavg: exec: "sysctl": ... not found)` | clean load-fail output or document | S |
| 6 | `nova-ci github receipt -h` | no --json (refuses `unknown flag --json`); same for local; old 2026-10-02 USE still present | accept --json on every verb (incl. dry) | S |

## Nice-to-haves
| # | where | what | fix | size |
|---|---|---|---|---|
| 1 | `nova-ci local --dry-run` | 145 pkgs in one long line, repeated in make cmd | shorten dry-run output or add --max | M |
| 2 | `nova-ci functional ./cmd/nova-version` | OK reason="no-functional-tag-in-1-dirs" (internal) | user-facing reason text | S |
| 3 | old ratings 2026-10-02 | some fixed since (functional now keeps ./ prefix and is pasteable; new-rule --dry-run now consistently refuses outside checkout) | keep the fixes | - |

## Top 3 fixes
1. document and stabilize the CI-SLOW OK / WARN output grammar in help and code for slowtests (raises 3 and 11)
2. make all verbs single words (bench-run etc) or update one-word criterion handling
3. clean refusal messages to omit internal command failures (e.g. git fatal); add --json to local and github receipt

## Good, keep
- --dry-run works for all writes (services not needed; bench/github judged from --dry-run alone)
- every refusal names cause + next command (one turn recovery)
- --json (where offered) roundtrips the same verdict+remedy for ok and refused
- first run zero-setup via --example (matches banner)
- checked after Score: 2026-10-02 defects "functional drops ./" and "dry-run lies on checkout guard" are no longer present in this build; "no --json on github receipt" and local output dialect still here

## What could not be tried
- nova-ci local (without --dry-run): runs unit tests, long, temp writes
- nova-ci new-rule/new-verb (without --dry-run): would write scaffold files
- nova-ci bench run: needs --host (external bench service)
- nova-ci github receipt (without --dry-run): writes receipt to Redis
- slowtests with >20 findings (MORE line); large streams; sleeps allowlist cases
- full help <verb> for bench/github (covered via -h)

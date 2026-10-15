# nova-hook AI-usability review, 2026-10-15

Reviewer: Mercury (mercury-3-preview-1002)
Build: cc2f2f8c8148
Score: 8.5/10 (46/55)

## Rubric
| # | criterion | score | evidence |
|---|---|---|---|
| 1 | cold | 4 | `nova-hook version` -> `nova-hook v1.2.26-0.20261015145010-cc2f2f8c8148 linux/amd64 go1.26.6`; help is self-contained, but the first read verb refuses until a bus Redis is provisioned |
| 2 | refusals | 5 | `nova-hook bogus` -> `HOOK REFUSED: unknown verb "bogus"; the verbs are status, hook, prove, unhook, friends, serve, version; run: nova-hook help` |
| 3 | output | 5 | `nova-hook version --json` -> `{"result":{"verb":"version","status":"ok","exit":0},"facts":{},"payload":"nova-hook v1.2.26-0.20261015145010-cc2f2f8c8148 linux/amd64 go1.26.6"}` |
| 4 | dry-run | 3 | `nova-hook hook --as mercury --role member --dry-run` -> `HOOK NOTE --dry-run was given and the verb never read it (Call.DryRun)`; hook/prove/unhook have --dry-run, serve (a write) does not |
| 5 | idempotent | 2 | `nova-hook prove -h` shows no --op flag; prove spends a nonce once, so the same op twice changes state; no write offers a repeat-no-change guarantee |
| 6 | no prompts | 5 | `nova-hook version` and every refusal returned immediately; no interactive prompt appears anywhere in help |
| 7 | exit codes | 4 | `nova-hook status --as mercury` -> exit 2 with `STATUS REFUSED: ... refusing to guess; run: nova-hook help`; 0 and 2 match help, 1 unverifiable without a store |
| 8 | one word | 5 | `nova-hook help` lists `status, hook, prove, unhook, friends, serve, version`: distinct single-word verbs, no duplicates |
| 9 | push | 4 | help: challenges and notifications are pushed as `HOOK CHALLENGE`/bus messages to the name; serve polls its own record in passes but pushes outward |
| 10 | no leaks | 5 | `nova-hook status --as mercury --redis 127.0.0.1:9` -> `STATUS REFUSED: ... as the default user, no password ... connection refused`; no credential leaks |
| 11 | true help | 4 | `nova-hook status -h` exit 0 matches documented `exit codes: 0`; claims about store-dependent behaviour unverifiable here |

## 10/10 blockers
| # | where | what an AI meets | fix | size |
|---|---|---|---|---|
| 1 | `nova-hook serve -h` | serve is a delivery write (issues challenges, unhooks silent names, expires asks) yet its flags list `--as, --down-after, --every, --json, --judge-within, --once, --redis` and no `--dry-run`, unlike hook/prove/unhook; an AI cannot preview a serve pass | add a `--dry-run` that prints the one pass it would make (challenges, judged names, expires) and writes nothing | M |
| 2 | `nova-hook status` (fresh checkout) | `STATUS REFUSED: --redis is required: NOVA_BUS_REDIS is unset, and with no NOVA_SPRINT_REDIS the fleet's bus row ... cannot be read either; refusing to guess` -- every read verb refuses until a bus Redis is provisioned, and the store invocation does not show the one line that sets one up | print the store-setup one-liner (`nova-config fleet set --bus <host:port>`, then apply) in the bare help, or accept a `--redis memory://` scratch store for read-only probes | S |

## Nice-to-haves
| # | where | what | fix | size |
|---|---|---|---|---|
| 1 | hook/prove/unhook -h | no write takes `--op`; idempotency is not documented or guaranteed, and prove spends a nonce once | add a `--op`/idempotency contract or document the spend-once nonce as intended | M |
| 2 | exit code 1 | the `1 the verb ran and said no` branch needs a live store to observe; no offline path demonstrates it | add a fixture or an integration test path so the 1 branch is reachable and pinned | M |
| 3 | hook -h | the hook help is very long with many harness flags; a new AI must read it all before a first hook | add a short "quickstart" example near the top for the default case | S |

## Top 3 fixes
1. Add `--dry-run` to serve (fixes the one write without a dry-run; moves criterion 4).
2. Make the first read succeed without a provisioned store (add a scratch/`--redis` helper or print the exact provisioning one-liner; moves cold).
3. Add a `--op`/idempotency contract on writes (moves criterion 5).

## Good, keep
- Every refusal names both the cause and the next command to run, and even refusals honour `--json` as one structured object.
- A consistent `VERB`-prefixed key=value line grammar across status/hook/prove/friends, with `--json` on every verb.
- Help is deeply self-documenting: per-verb `-h` lists flags, effect, and exit codes, and the bare invocation lists all verbs; the `--dry-run`-given-but-not-read warning is a thoughtful safety touch.

## What could not be tried
- `status` success (a proven record), `friends` listing, `hook`, `prove`, `unhook`, `serve` with a live result: all need a live bus Redis store and a hooked identity; running them would write live state (prohibited), so they were judged from `help`/`-h` and the `--dry-run` paths. The `--dry-run` paths reached before the store refused and warned they never read the flag, so their success output was not observed. Exit code 1 ("verb ran and said no") could not be demonstrated.

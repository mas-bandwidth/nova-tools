# nova-redis AI-usability review, 2026-10-15

Reviewer: mercury-3-preview-1002
Build: 4540d7fd37ca
Score: 9.5/10 (52/55)

## Rubric
| # | criterion | score | evidence |
|---|---|---|---|
| 1 | cold | 5 | `nova-redis` -> `REDIS REFUSED: no verb given; the verbs are serve, spill, recall, fn load, fn check, acl render, acl check, acl apply, install store, install bus, uninstall store, uninstall bus, version; run: nova-redis help` |
| 2 | refusals | 5 | `nova-redis spill --bogus x` -> `SPILL REFUSED: unknown flag --bogus; the flags of spill are --addr, --dry-run, --json, --name, --owner, --password-env, --redis, --ttl, --user, --value; run: nova-redis spill -h` |
| 3 | output | 5 | `nova-redis spill --dry-run --json --redis 127.0.0.1:6379 --owner ada --name n --ttl 10m --value hi` -> `{"result":{"verb":"spill","status":"ok","exit":0},"facts":{"key":"ada:n","ttl":"10m0s","expires":"2026-10-15T16:19:54Z","bytes":2,"store":"127.0.0.1:6379","written":0,"dry_run":true}}` |
| 4 | dry-run | 4 | `nova-redis fn load --dry-run` -> `FN-LOAD REFUSED: unknown flag --dry-run` (a write verb with no --dry-run) |
| 5 | idempotent | 4 | no --op pattern; write verbs are distinct, and re-run idempotency is untestable without a live store (forbidden here), so judged from help and dry-run only |
| 6 | no prompts | 5 | none of ~20 runs waited on input; all returned immediately |
| 7 | exit codes | 5 | `nova-redis version` -> exit 0; `nova-redis spill --bogus` -> exit 2; help states 0/1/2 and observed 0 and 2 match |
| 8 | one word | 4 | verbs include two-word forms fn load, fn check, acl render, acl check, acl apply, install store, install bus, uninstall store, uninstall bus |
| 9 | push | 5 | no polling loop in the tool; it dials the store directly and reports a refusal |
| 10 | no leaks | 5 | password passed only as an env-var NAME (--password-env); refusals mention `no password` and variable names, never values |
| 11 | true help | 5 | `nova-redis help` claim "Every verb but ... takes --json" held for version/spill/recall; all 12 verbs answered `-h` with exit 0 |

## 10/10 blockers
| # | where | what an AI meets | fix | size |
|---|---|---|---|---|
| 1 | `nova-redis spill --bogus` | `SPILL REFUSED: unknown flag --bogus; the flags of spill are ...` stops after the unknown flag and does not also name the missing required flags (--owner, --name, --ttl, --value), so one run reports only one of several problems | after an unknown flag, still list each required flag that is absent | S |
| 2 | `nova-redis fn load --dry-run` | `FN-LOAD REFUSED: unknown flag --dry-run`; fn load is a write verb (loads library functions) yet has a dry-run, unlike serve/spill/acl apply/install/uninstall | add --dry-run to fn load that prints what it would load and writes nothing | S |
| 3 | `nova-redis spill --dry-run --redis 127.0.0.1:6379 --owner ada --name n --ttl 10m --value "hello there"` | the plan prints `bytes=11` but not the words, so a dry-run hides the text it would store | print the value on the dry-run line and in the JSON, in the quoting the line already uses | S |

## Nice-to-haves
| # | where | what | fix | size |
|---|---|---|---|---|
| 1 | `nova-redis spill --addr 127.0.0.1:6379 ...` | emits an extra line `SPILL NOTE --addr is --redis` above the result line, so the documented example prints two lines | move the NOTE to stderr, or update the help example to --redis | S |
| 2 | verbs list in `nova-redis help` | eight verbs are two-word (fn load, acl render, install store, ...), harder for an AI to tab-complete or tokenize as one thing | give each multiword verb a one-word alias, keeping the current spelling | M |

## Top 3 fixes
1. Make refusals report every problem at once: after an unknown flag, name the missing required flags too.
2. Add `--dry-run` to `fn load` so verb write verb has one (criterion 4).
3. Show the value in the spill dry-run plan instead of only the byte count.

## Good, keep
- Refusals are excellent: every one names the cause, the flags that exist, and the next command, with exit 2.
- `--json` uses one stable envelope `{"result":{verb,status,exit,remedy,why},"facts":{}}` across version, spill, and recall.
- Password hygiene is right: only the env-var name is passed and echoed, never the value; the socket-path form of `--redis` now works (an earlier rating's `--addr /tmp/redis.sock` refusal is gone).

## What could not be tried
- serve, spill (real), recall (real), fn load, fn check, acl check (live), acl apply (live): each needs a running Redis store or serve, and starting a server on this machine is forbidden; judged from help and --dry-run only.
- exit code 1 (ran and said NO, e.g. recall of a missing key): not reachable without a live store.

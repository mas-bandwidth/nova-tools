# nova-redis AI-usability review, 2026-10-15

Reviewer: mercury-3-preview-1002
Build: 4540d7fd37ca
Score: 8/10 (45/55)

## Rubric
| # | criterion | score | evidence |
|---|---|---|---|
| 1 | cold | 5 | `nova-redis` -> `REDIS REFUSED: no verb given; ... run: nova-redis help`; `nova-redis acl render` -> `ACL RENDER OK users=4 functions=41 library=3ff536374b59b81e` |
| 2 | refusals | 4 | `nova-redis spill` -> `SPILL REFUSED: --addr is required: ... --value is required ... --owner is required ... --name is required ... --ttl is required` (every problem at once, each with `run: nova-redis help`); but the missing-flag refusal names `--addr` while help's canonical flag is `--redis` |
| 3 | output | 5 | `nova-redis spill --json --dry-run ...` -> `{"result":{"verb":"spill","status":"ok","exit":0},"facts":{...}}`; typed lines are `SPILL OK key=ada:note ttl=10m0s ... dry_run=true` |
| 4 | dry-run | 4 | `nova-redis spill --dry-run --redis 127.0.0.1:6379 ...` -> `SPILL OK ... written=0 dry_run=true`; serve, spill, acl apply and install/uninstall store/bus have `--dry-run`, but the write verb `fn load` has none, and install's dry-run needs the full secrets stack to render |
| 5 | idempotent | 0 | `nova-redis help` shows no `--op` flag on any write verb; a retried `spill` overwrites the value instead of returning a recorded result |
| 6 | no prompts | 5 | every command, including refusals, finished and exited without waiting on input; no interactive prompt appeared |
| 7 | exit codes | 5 | `nova-redis spill` (missing flags) -> `EXIT=2`; `nova-redis spill --redis 127.0.0.1:1 ...` -> `EXIT=2`; `nova-redis spill --dry-run ...` -> `EXIT=0`; 0 done, 2 could-not-run, as help states |
| 8 | one word | 4 | one verb per thing (serve, spill, recall, fn load, fn check, acl render/check/apply, install store/bus, uninstall store/bus, version), but `--addr` and `--redis` are two names for the same flag |
| 9 | push | 5 | n/a: nova-redis is a direct read/write to a Redis store; it waits on no state by polling |
| 10 | no leaks | 5 | `nova-redis spill --redis 127.0.0.1:1 ...` -> `SPILL REFUSED ... as the default user, no password: unreachable ...` (no password printed); serve writes only the password's SHA-256 |
| 11 | true help | 3 | `nova-redis spill -h` -> `effect: local write: writes files on this machine` although spill writes the store at `--redis`; `nova-redis install store -h`'s example uses `--as <seat> --key <file> --sops <path> --secret <NAME>` angle-bracket placeholders that do not run; help also promises `--max` for a listing verb and none lists |

## 10/10 blockers
| # | where | what an AI meets | fix | size |
|---|---|---|---|---|
| 1 | `nova-redis help`, `nova-redis spill -h` | no `--op` id on any write verb, so a retry after a timeout re-writes and changes state instead of returning the recorded result (criterion 5 scores 0) | give write verbs (spill, acl apply, install/uninstall store/bus) `--op <id>`; same id again returns the recorded result and writes nothing | M |
| 2 | `nova-redis spill -h` | `effect: local write: writes files on this machine` is false: spill writes the store at `--redis`, which may be another machine, and writes no local file; an AI judging blast radius from the effect line misjudges it (fn load and acl apply share the line) | add a store-write effect class and use it on spill, fn load and acl apply | S |
| 3 | `nova-redis install store --dry-run --units <dir>` | refuses for missing secrets yet prints `INSTALL-STORE NOTE --dry-run was given and the verb never read it (Call.DryRun); it may have written` on a run that wrote nothing; the "may have written" line contradicts the dry-run promise, and the dry-run cannot render without a full secrets stack | on the refusal path, do not emit the "may have written" NOTE; let the dry-run print the unit with a placeholder login | M |
| 4 | `nova-redis spill` (no `--redis`) | the missing-flag refusal says `SPILL REFUSED: --addr is required`, but help's canonical flag is `--redis`; the deprecated spelling leaks into the primary refusal | name `--redis` in the required-flag refusal and keep `--addr` as an accepted alias | S |
| 5 | `nova-redis install store -h` | the example is `nova-redis install store --dry-run --secrets ... --as <seat> --key <file> --sops <path> --secret <NAME>`: angle-bracket placeholders, not a command a cold reader can run | give a runnable dry-run example with concrete values | S |

## Nice-to-haves
| # | where | what | fix | size |
|---|---|---|---|---|
| 1 | `nova-redis help` | help says `A verb that lists takes --max <n>` but no nova-redis verb lists; a reader may go looking for `--max` | print the `--max` sentence only for a tool with a listing verb | S |
| 2 | `nova-redis acl render -h` | `acl render` takes no `--json`, so the ACL table cannot be read as one JSON object | accept `--json` on `acl render` for the same value | S |
| 3 | `nova-redis version --json` | the version, OS, arch and Go version sit unparsed in one `payload` string with empty `facts` | render them as individual facts (`version`, `os`, `arch`, `go`) | S |
| 4 | `nova-redis fn load -h` | the write verb `fn load` has no `--dry-run`, so the library deploy cannot be planned store-free | add `--dry-run` printing the library digest and store it would use, dialling nothing | S |

## Top 3 fixes
1. Add `--op <id>` idempotency to the write verbs; same id repeats the recorded result and writes nothing — this lifts criterion 5 from 0 toward 5.
2. Correct the effect lines on spill, fn load and acl apply to state the store-write blast radius.
3. Fix the install dry-run path so a dry-run that wrote nothing never prints "it may have written" and can be previewed without the secrets stack.

## Good, keep
The store-free surface is real: `spill --dry-run`, `acl apply --dry-run`, `serve --dry-run` and `uninstall store --dry-run` each print the plan (with `written=0`) and change nothing. Refusals list every missing input at once, each with a next command, and exit codes tell the truth (0 done, 2 could-not-run). Passwords never appear in arguments or output — the login flags take variable names and serve writes only the password's SHA-256; bind validation accepts loopback and tailnet addresses only.

## What could not be tried
serve, spill (live), recall (live), fn load, fn check (live), acl check and acl apply (live) need a live Redis store, which this card may not start or touch; they were judged from their help and `--dry-run`. install store, install bus and their uninstall counterparts require a secrets store, an age key, a sops binary and the system unit loader, so their live runs were not exercised.

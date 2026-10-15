# nova-redis AI-usability review, 2026-10-15

Reviewer: Gemini 4 Argon (High)
Build: b930c415a495
Score: 8.5/10 (47/55)

## Rubric
| # | criterion | score | evidence |
|---|---|---|---|
| 1 | cold | 5 | `nova-redis help` -> `nova-redis acl render` (`ACL RENDER OK users=4 functions=41 library=3ff536374b59b81e`, exit 0) and `nova-redis spill --dry-run --redis 127.0.0.1:1 --owner ada --name note --ttl 10m --value hi` (`SPILL OK key=ada:note ttl=10m0s ... written=0 dry_run=true`, exit 0) |
| 2 | refusals | 4 | `nova-redis serve --bind 8.8.8.8 --port 99999 --dir rel/path --dry-run` -> reports all three bad values in one run at exit 2; `nova-redis fn check` -> `FN-CHECK REFUSED: --addr is required...` (names deprecated `--addr` instead of `--redis`) |
| 3 | output | 3 | `nova-redis spill --dry-run --redis 127.0.0.1:1 --owner ada --name note --ttl 10m --value hi --json` -> `{"result":{"verb":"spill","status":"ok","exit":0},"facts":{...}}` (exit 0), but 10 of 13 verbs (`serve`, `fn load`, `fn check`, `acl render`, `acl check`, `acl apply`, `install`, `uninstall`) do not support `--json` |
| 4 | dry-run | 4 | `nova-redis acl apply --dry-run --redis 127.0.0.1:1` -> `ACL APPLY OK dry-run=true users=4 set=0 would=4 library=3ff536374b59b81e store=127.0.0.1:1 rotated=0` (exit 0), while `nova-redis fn load --dry-run` is refused at exit 2 (`unknown flag --dry-run`) |
| 5 | idempotent | 4 | `nova-redis acl apply`, `fn load`, `install`, `uninstall`, and `spill` converge target state on repeat, though `spill` takes no `--op` and resets the key's TTL |
| 6 | no prompts | 5 | `nova-redis recall --redis 127.0.0.1:1 --owner ada --name note` -> exits 2 immediately without prompting on stdin |
| 7 | exit codes | 5 | `nova-redis version` -> exit 0; `nova-redis bogus` -> exit 2; `nova-redis recall --redis 127.0.0.1:1 --owner ada --name note` -> exit 2 |
| 8 | one word | 4 | Accepts both `--redis` and deprecated `--addr` (and prints `--addr` in `help` examples and missing-flag refusals); dry-run fact key is `dry_run=true` on `spill` and `serve` but `dry-run=true` on `acl apply` |
| 9 | push | 5 | `n/a: nova-redis has no wait or poll verb in the binary` |
| 10 | no leaks | 5 | `nova-redis recall --redis 127.0.0.1:1 --owner ada --name note` -> `RECALL REFUSED key=ada:note class=unreachable: redis at 127.0.0.1:1 as the default user, no password: unreachable...` (never prints a password or secret value) |
| 11 | true help | 3 | `nova-redis help` states `A verb that lists takes --max <n> (default 20, 0 lists all) and says MORE for the rest`, but no verb in `nova-redis` accepts `--max`; `spill -h`, `fn load -h`, and `acl apply -h` state `effect: local write: writes files on this machine` when they write to the Redis store over `--redis` |

## 10/10 blockers
| # | where | what an AI meets | fix | size |
|---|---|---|---|---|
| 1 | `nova-redis fn load --dry-run` | `FN-LOAD REFUSED: unknown flag --dry-run; the flags of fn load are --addr, --password-env, --redis, --user; run: nova-redis fn load -h` (exit 2); `fn load` is the only write verb in `nova-redis` without `--dry-run` (carried from the 2026-10-02 ratings) | Add `--dry-run` to `fn load` so it prints the embedded library name, SHA, function count, and target store with `loaded=0 dry_run=true` without dialling Redis | S |
| 2 | `nova-redis spill` and `nova-redis help` | `nova-redis spill` (missing `--redis`) prints `SPILL REFUSED: --addr is required...`, and `nova-redis help`, `spill -h`, and `recall -h` examples pass `--addr 127.0.0.1:6379`, which emits `SPILL NOTE --addr is --redis` when run | Replace `--addr` with `--redis` in all help examples and missing-flag refusal messages | S |
| 3 | `nova-redis acl check --redis 127.0.0.1:1` | Prints `ACL CHECK FAILED store=127.0.0.1:1 err=redis at 127.0.0.1:1 as the default user, no password: unreachable: dial tcp 127.0.0.1:1: connect: connection refused remedy="log in as a user that may run ACL GETUSER, ACL CAT and ACL SETUSER..."` (exit 2), prescribing an ACL login remedy when the TCP port is unreachable | Classify unreachable dial errors in `acl check` the same way `fn check` and `recall` do (`start the store or correct --redis`) before falling back to the ACL permission remedy | S |
| 4 | `nova-redis spill -h`, `nova-redis fn load -h`, `nova-redis acl apply -h` | All three per-verb help screens print `effect: local write: writes files on this machine` (carried from the 2026-10-02 ratings), even though they write to the Redis server over `--redis` | Label store-mutating verbs with a store-write effect description and omit the banner's `A verb that lists takes --max <n>` sentence when no verb takes `--max` | S |
| 5 | `nova-redis fn check --redis 127.0.0.1:1` and `nova-redis acl apply --dry-run --redis 127.0.0.1:1` | `fn check` prints `FAILED nova_sprint ...` with no verb prefix while other verbs print `ACL CHECK FAILED`, `RECALL REFUSED`, `FN-CHECK REFUSED`, or `INSTALL STORE DRY-RUN`; `acl apply --dry-run` emits `dry-run=true` (hyphen) while `spill --dry-run` and `serve --dry-run` emit `dry_run=true` (underscore) | Prefix `fn check` lines with `FN CHECK` and standardize on `dry_run=true` across all verbs | S |

## Nice-to-haves
| # | where | what | fix | size |
|---|---|---|---|---|
| 1 | `nova-redis fn check --json`, `nova-redis acl render --json`, `nova-redis acl check --json` | Only `spill`, `recall`, and `version` accept `--json`; the remaining 10 verbs refuse `--json` at exit 2 | Add `--json` output to `fn check`, `fn load`, `acl render`, `acl check`, `acl apply`, `serve --dry-run`, `install --dry-run`, and `uninstall --dry-run` | M |
| 2 | `nova-redis version -h` and `nova-redis acl render -h` | Every verb's `-h` repeats the full tool-wide `exit codes:` paragraph mentioning `spill`, `recall`, `fn load`, `fn check`, and `serve`, and omits `acl`, `install`, and `uninstall` | Give each verb its own concise exit-code line in `<verb> -h` | S |

## Top 3 fixes
1. Add `--dry-run` to `nova-redis fn load` so every write verb in `nova-redis` has a store-free dry run.
2. Update `nova-redis help`, `spill -h`, `recall -h`, and the missing-address refusal from `--addr` to `--redis` so copying the help example no longer triggers `SPILL NOTE --addr is --redis`.
3. Fix `nova-redis acl check`'s unreachable-store remedy (so connection-refused errors do not blame `ACL GETUSER` permissions) and fix the `effect:` line on `spill -h`, `fn load -h`, and `acl apply -h`.

## Good, keep
- Store-free dry-runs on `spill --dry-run`, `serve --dry-run`, `acl apply --dry-run`, `install store --dry-run`, and `uninstall store --dry-run` (keeping the 1.2.0 fix where `acl apply --dry-run` no longer dials the store).
- Multi-error validation in one pass: `nova-redis spill` names all 5 missing required flags at once, and `nova-redis serve --bind 8.8.8.8 --port 99999 --dir rel/path --dry-run` names all 3 invalid flag values in a single exit-2 refusal.
- `nova-redis acl render` prints the complete 13-family, 4-user, 41-function ACL policy offline in deterministic `key=value` and `ACL SETUSER` lines.

## What could not be tried
- `nova-redis serve` (without `--dry-run`), `nova-redis spill` (without `--dry-run`), `nova-redis recall` against a live store, `nova-redis fn load`, `nova-redis acl apply` (without `--dry-run`), `nova-redis install store/bus` (without `--dry-run`), and `nova-redis uninstall store/bus` (without `--dry-run`): not run against a live Redis instance or host service manager because starting a server or mutating live state is forbidden by the review rules.

# nova-sprint AI-usability review, 2026-10-15

Reviewer: mercury-3-preview-1002
Build: 63e3e239faee
Score: 9.0/10 (50/55)

## Rubric
| # | criterion | score | evidence |
|---|---|---|---|
| 1 | cold | 5 | `nova-sprint help` -> `usage: nova-sprint init ...`; the "trying it without a Redis" twin flow ran end to end to a landed card on the first attempt from help alone |
| 2 | refusals | 5 | `nova-sprint frobnicate` -> `nova-sprint REFUSED: unknown verb frobnicate; ... run: nova-sprint help`; `nova-sprint where --bogus` -> `REFUSED: unknown flag --bogus; ... run: nova-sprint help where`; `nova-sprint take s1-1.w1@1` -> `REFUSED: wants --as <member>...; run: nova-sprint take -h`; `nova-sprint where --at-epoch abc` -> `REFUSED: invalid value for --at-epoch...; run: nova-sprint help where` |
| 3 | output | 5 | `nova-sprint where --json | python3 -c 'import sys,json;d=json.load(sys.stdin);print(d["landed"])'` -> `1`; line grammar stable (MOVED/OK/REFUSED) |
| 4 | dry-run | 3 | `nova-sprint land --stream s1 --repo-dir work --base sprint/s1 --dry-run` -> `LAND DONE batches=1 cards=1 refused=0 dry_run=yes` then a real `land` pushed, so dry-run wrote nothing; but `nova-sprint stream remove s1 --dry-run` -> `REFUSED: unknown flag --dry-run` (also absent on merge, start, stop) |
| 5 | idempotent | 5 | `nova-sprint add --stream s2 --count 1 --one --op test-1` twice -> second run `op=test-1-1 replay=yes` and changed nothing |
| 6 | no prompts | 5 | every command above returned without waiting; twin mode refuses interactive verbs (`run`, `inbox --wait`, `where --watch`) |
| 7 | exit codes | 5 | `nova-sprint` -> `2`; `nova-sprint help` -> `0`; `nova-sprint stream remove s1` -> `1`; match help's "0 done, 1 failed or incomplete, 2 usage" |
| 8 | one word | 4 | verbs are distinct, but transitional aliases overlap: help documents `fleet down` = `hold --return`, `reader away` = `hold --return`, `friend down` = `hold` (`kept for one release`) |
| 9 | push | 3 | not observable against a live store (forbidden); the twin refuses `inbox --wait`/`where --watch`, so push-vs-poll could not be exercised |
| 10 | no leaks | 5 | `nova-sprint where --json` output and every line contained no secret values; the `JEV_API_KEY` note names an environment variable, not a value |
| 11 | true help | 5 | help's `mem:<file>` twin claim worked; exit-code and flag claims matched every refusal and run |

## 10/10 blockers
| # | where | what an AI meets | fix | size |
|---|---|---|---|---|
| 1 | `nova-sprint stream remove s1 --dry-run` | `nova-sprint stream remove REFUSED: unknown flag --dry-run` (and `merge`, `start`, `stop` also take no `--dry-run`); a write with no dry-run cannot be previewed | add `--dry-run` to every write verb, printing what would change and writing nothing | S |
| 2 | `nova-sprint help` | two words for one action in the same table: `fleet down`/`reader away`/`friend down` are `hold --return`/`hold`, `fleet up`/`reader up`/`friend up` are `unhold`; a new AI must learn the alias set | drop the aliases and keep one verb per action (or mark them as hard-deprecated and refuse with a pointer to the canonical verb) | S |

## Nice-to-haves
| # | where | what | fix | size |
|---|---|---|---|---|
| 1 | `nova-sprint help` | the banner is ~740 lines; the first-run path is buried in prose | print a short "first run" recipe at the top of help, then the full reference | S |
| 2 | `nova-sprint` (bare) | bare invocation prints the whole verb list on one wrapped line | wrap the available list or print the short banner | S |
| 3 | `nova-sprint where --json` | huge JSON blob with no compact summary field for a program | add a `summary` field already present in the human line to the JSON | S |

## Top 3 fixes
1. Add `--dry-run` to every write verb (stream remove, merge, start, stop) so every state change can be previewed (criterion 4).
2. Remove the transitional verb aliases so each action has exactly one word (criterion 8).
3. Give `help` a short first-run recipe at the top so a cold AI lands its first job faster (criterion 1 polish).

## Good, keep
- The refusals are exemplary: every one names the cause, the offending value, and the exact next command to run.
- The `mem:<file>` twin store is a superb way for an AI to learn all write verbs safely without touching a live store, and it is documented in help.
- Exit codes are consistent and match the documented contract, and `--json` is real, parseable JSON on the verbs that offer it.

## What could not be tried
- Live-store write verbs (take, finish, release, land, hold, etc. against a real Redis/forge): not permitted by this card; judged from help and `--dry-run`.
- `run`, `inbox --wait`, `where --watch`, `dashboard`, `server`, `install`/`uninstall`, `adopt`, `play`: require a live store, a server, a fleet, or a harness and were refused or not run in twin mode.
- Criterion 9 (push vs poll): needs a live store and a running tick; not observable in the twin.

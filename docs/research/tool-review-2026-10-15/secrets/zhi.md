# nova-secrets AI-usability review, 2026-10-15

Reviewer: Mercury (mercury-3-preview-1002)
Build: cc2f2f8c8148
Score: 9/10 (49/55)

## Rubric
| # | criterion | score | evidence |
|---|---|---|---|
| 1 | cold | 5 | `nova-secrets help` -> `nova-secrets: encrypted secrets in a git repository, handed to one command at a time`; from it alone I made a store and ran gate and names |
| 2 | refusals | 5 | `nova-secrets names --store ./x` -> `SECRETS NAMES REFUSED: missing --as <name>; store ./x is not a directory; run: git clone <store url> ./x` |
| 3 | output | 5 | `nova-secrets names --store ... --as bench-a --json` -> `{"result":{"verb":"names","status":"ok","exit":0},"facts":{...},"items":[...]}`; line grammar `SECRETS NAMES OK as=bench-a keys=3 shown=3 sealed=2 clear=1` |
| 4 | dry-run | 4 | `nova-secrets seal -h` lists `--dry-run` but `nova-secrets keygen -h` and `nova-secrets seat add -h` do not; two write verbs have no dry-run |
| 5 | idempotent | 3 | no `--op` flag in `nova-secrets help` or any verb's `-h`; writes carry no caller-chosen op id |
| 6 | no prompts | 4 | `nova-secrets seal -h` -> `reads one value at a hidden prompt (or --stdin)`; the default waits on interactive input, only `--stdin` avoids it |
| 7 | exit codes | 5 | `nova-secrets gate --base does-not-exist --head HEAD` -> `SECRETS GATE REFUSED: ...` exit 2; `GATE APPROVE` exit 0; `GATE FAILED` exit 1; exec refusal exit 125 |
| 8 | one word | 5 | `nova-secrets help` lists distinct single verbs `version, exec, names, check, gate, keygen, place, placed, seal, seat add, seat inject, help` |
| 9 | push | 3 | `nova-secrets seal -h` -> `waits up to two minutes for the gate's approval, reporting progress while it waits`; approval is polled, not pushed |
| 10 | no leaks | 5 | names prints only key names and counts, never values; place's receipt keys the value by file and a ciphertext blob id, never the value |
| 11 | true help | 5 | `nova-secrets names -h` exits 0 and prints usage; the documented refusals, OK lines, JSON and exit codes all reproduced as printed |

## 10/10 blockers
| # | where | what an AI meets | fix | size |
|---|---|---|---|---|
| 1 | `nova-secrets help` (setup, first value, example) | the runnable first-run and example lines hard-code `/opt/homebrew/bin/sops` and `/opt/homebrew/bin/age-keygen`, so the printed first sitting fails on Linux and the remedy reads `brew install` | use `$(command -v sops)` and `$(command -v age-keygen)` in every runnable line | S |
| 2 | `nova-secrets names --max nope` | `SECRETS NAMES REFUSED: invalid value for --max: it wants n names to list before a MORE line; 0 lists all (default 20)` has no `run:` line, unlike every other refusal | append the verb's `run: nova-secrets names -h` to bad-value refusals | S |
| 3 | `nova-secrets placed --machine bench-a --receipts ./nodir` | answers `SECRETS PLACED OK machine=bench-a count=0` for an absent receipts directory instead of refusing or saying so | refuse an explicit but absent `--receipts`, or print `receipts=absent` on the OK line | S |
| 4 | `nova-secrets seat -h` | prints the whole root banner instead of the two subverb helps | print the `seat add` and `seat inject` usage and `run: nova-secrets seat add -h` | S |
| 5 | `nova-secrets keygen -h`, `nova-secrets seat add -h` | the two local-write verbs have no `--dry-run`, so an AI cannot preview a write before making it | add `--dry-run` to keygen and seat add | S |

## Nice-to-haves
| # | where | what | fix | size |
|---|---|---|---|---|
| 1 | `--json` | only `names` takes it; `nova-secrets check --json` is refused | give every verb a `--json` form, or say in each `-h` why not | M |
| 2 | writes | no `--op` idempotency; a retry after a timeout relies on the branch stamp | add `--op`, or document the retry guarantee | M |
| 3 | `nova-secrets seal` | default blocks on a hidden terminal prompt; only `--stdin` is non-interactive | refuse when stdin is not a TTY unless `--stdin` | S |
| 4 | `nova-secrets seal`, `seat inject` | the PR approval is polled for up to two minutes | keep the poll but print the poll command and progress on a stable line | S |

## Top 3 fixes
1. Make the help's example and first-value lines portable (`$(command -v sops)` / `age-keygen`) so the documented first run works on any OS.
2. Add `--dry-run` to `keygen` and `seat add`, the two write verbs that lack one.
3. Give every bad-value refusal a `run:` line and make `placed` refuse or flag an absent receipts directory.

## Good, keep
Refusals name every problem at once with a runnable next command, and exit codes (0/1/2/125) are truthful and documented; `names` and its `--json` are one clean value with a stable line grammar that never prints a secret.

The `gate` verb is fully runnable from help alone with a git store, and its APPROVE / FAILED / REFUSED lines and exit codes are unambiguous.

`exec` and `check` carry a full store prerequisite with why it holds and a remedy per refusal, so a new caller can recover in one turn.

## What could not be tried
- `keygen`: needs the `age-keygen` binary, which the environment blocks.
- `seal`, `seat add`, `seat inject`, `place`, `check`, and a successful `exec`: need real age keys and `sops`, which the environment blocks; `exec`'s refusal paths (exit 125) were run instead.
- `placed` with real receipts: no receipts exist without a `place` run; its absent-directory path was run.
- The full `seal` push/merge/poll path: would need `gh`, a live store and a network tail.

# nova-secrets AI-usability review, 2026-10-15

Reviewer: Mercury 3 Preview (inception/mercury-3-preview-1002)
Build: cc2f2f8c8148
Score: 7.5/10 (41/55)

## Rubric
| # | criterion | score | evidence |
|---|---|---|---|
| 1 | cold | 4 | `nova-secrets help` -> `first run: setup: makes a store from an empty directory (it needs age-keygen and sops on PATH)`; solid banner and pasteable setup, but every example pins `/opt/homebrew/bin/sops` and the advertised `first value:` cannot run after `setup:` on a fresh store |
| 2 | refusals | 4 | `nova-secrets names --store x --as y --max abc` -> `SECRETS NAMES REFUSED: invalid value for --max: it wants n names to list before a MORE line; 0 lists all (default 20)` (cause named, but no run: line); every other refusal tested carries a runnable next command |
| 3 | output | 4 | `nova-secrets names --store store --as fake --json` -> `{"result":{"verb":"names","status":"ok","exit":0},"facts":{...},"items":[...]}`; typed-line and JSON agree, but --json exists on names alone |
| 4 | dry-run | 3 | `nova-secrets keygen -h` -> no --dry-run flag; `nova-secrets seat add -h` -> no --dry-run flag; only place, seal and seat inject offer it, so not every write does |
| 5 | idempotent | 0 | `nova-secrets help` -> no `--op` flag on any write verb; a retry after a timeout is not safe |
| 6 | no prompts | 3 | `nova-secrets seal -h` -> `effect: store write. Reads one value at a hidden prompt (or --stdin)`; seal without --stdin waits on an interactive answer |
| 7 | exit codes | 5 | `nova-secrets gate --store store --base BASE --head HEAD` -> `GATE FAILED ...` exit 1; `nova-secrets version` exit 0; `nova-secrets exec` refusal exit 125; all as help states |
| 8 | one word | 5 | `nova-secrets help` -> one verb per noun (exec, names, check, gate, keygen, place, placed, seal, seat add, seat inject), no duplicate verbs or tools |
| 9 | push | 5 | `nova-secrets check -h` reads git refs; seal/place deliver via git/gh/ssh; nothing is polled; n/a for pure inspection verbs |
| 10 | no leaks | 4 | `nova-secrets names --store store --as fake` -> `SECRETS NAMES OK as=fake keys=2` prints no value; names/JSON and receipts carry no secret; exec/place could not be run end-to-end without sops |
| 11 | true help | 4 | verified exit codes, one refusal grammar, `SECRETS NAMES MORE` line, `GATE FAILED` verdict, and store-shape remedies; example binary paths are not portable and could not be exercised on Linux |

## 10/10 blockers
| # | where | what an AI meets | fix | size |
|---|---|---|---|---|
| 1 | `nova-secrets help` (write verbs) | no `--op` on seal, place, seat add or seat inject, so a retried write is not idempotent; `--op` appears nowhere in help | add a caller-chosen `--op` to each write verb and replay the recorded result on a repeat | M |
| 2 | `nova-secrets keygen -h`, `nova-secrets seat add -h` | keygen and seat add write state but offer no `--dry-run`, so a reader cannot preview a write | add `--dry-run` to keygen and seat add, printing the plan and writing nothing | M |
| 3 | `nova-secrets names --store x --as y --max abc` | `SECRETS NAMES REFUSED: invalid value for --max: it wants n names to list before a MORE line; 0 lists all (default 20)` ends with no `run:` line | append `run: nova-secrets names -h` to bad-value refusals | S |
| 4 | `nova-secrets seal -h` | seal without `--stdin` blocks on a hidden interactive prompt, which an unattended AI cannot answer; the flag is easy to miss | refuse when stdin is not a TTY, or default to `--stdin` for non-interactive callers | S |

## Nice-to-haves
| # | where | what | fix | size |
|---|---|---|---|---|
| 1 | `nova-secrets gate -h`, `nova-secrets exec -h` | `--json` is offered on names only; gate and exec refuse `--json`, while the house rule wants one value two renderings | give every verb's result a `--json` rendering, or say in each -h why none | M |
| 2 | `nova-secrets help` example lines | examples hard-code `/opt/homebrew/bin/sops` and `/opt/homebrew/bin/age-keygen`, which fail on Linux where the flag lines say `command -v` | use `"$(command -v sops)"` and `"$(command -v age-keygen)"` in every example | S |
| 3 | `nova-secrets seat -h` | prints the whole root banner instead of the two subver usage lines | print the two subverb usage lines and `run: nova-secrets seat add -h` | S |
| 4 | `nova-secrets names -h` and others | the root's one exit paragraph (about check failures and exec's 125) is printed under every verb's -h where it does not apply | pass each verb its own exit lines | S |
| 5 | `nova-secrets help` first value | on a brand-new store the advertised `first value:` cannot run after `setup:` because a new seat gets its first values by seat add, never by seal | let a written `seat add` without `--from` create the first value, and print that line in setup | M |

## Top 3 fixes
1. Add `--op` to every write verb (seal, place, seat add, seat inject) so a retry is idempotent: the single largest missing criterion (idempotent), worth five points alone.
2. Add `--dry-run` to keygen and seat add so every write is previewable before it lands.
3. Append the `run: nova-secrets <verb> -h` remedy to bad-value refusals, fixing the one refusal that does not name its next command.

## Good, keep
- `exec` is the right shape for an AI: the value goes only into one child's environment, the OK line names seat and file without a value, the child's exit passes through, and exec's own refusals are 125, apart from any child's status.
- One refusal grammar, `SECRETS <VERB> REFUSED: <why>; run: <next>`, with a runnable remedy for a missing flag, an absent store, a missing sops binary, an unknown verb or flag, and a store that is not a git working copy.
- `--dry-run` on seal, seat inject and place prints PLAN lines that name file, recipients, branch and receipt without reading or showing a value; names and names/JSON print no value; the strict store-shape and mode-0700 checks surface as actionable refusals.

## What could not be tried
- keygen: could not run; age-keygen is unreadable and not executable in this sandbox (Permission denied), so no key file or rule block could be made.
- seal (real run and dry-run success): needs sops plus a really encrypted store; sops is unavailable, so only refusals were observed.
- exec (real run): needs sops to decrypt the seat file; only refusals (missing flags, unknown flags, exit 125) were observed.
- check (full pass/fail): stops at the sops-binary check, so the decrypt-and-verify path was not exercised.
- place and place --dry-run (success path): needs sops and ssh; attempted, refused at the sops-binary check.
- seat add and seat inject (real runs): need sops, gh and a git remote; not run.
- The store used was a fabricated minimal fixture (a git dir, a .sops.yaml, a hand-written sops-shaped seat yaml), so real sops round-trips were not exercised; gate was run over two commits of that fixture.

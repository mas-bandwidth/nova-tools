# nova-config AI-usability review, 2026-10-15

Reviewer: qwen3.8-max-0902
Build: 32e60350ad5a
Score: 8.0/10 (44/55)

## Rubric
| # | criterion | score | evidence |
|---|---|---|---|
| 1 | cold | 5 | `nova-config kinds` -> `CONFIG KINDS count=8` (exit 0, no store, no other doc; the bare refusal names every real verb; all 48 verb `-h` exit 0 with usage, effect and a worked example) |
| 2 | refusals | 4 | `nova-config machine show` -> `REFUSED: the name is required: the first argument after the verb; no store named: --pg not given; NOVA_PG_DSN unset; ...; run: nova-config login, or export NOVA_PG_DSN=postgres://user@host:5432/nova, or --file <path>` (every problem at once, next command named; two remedies fall short: blockers 3 and nice-to-have 1) |
| 3 | output | 4 | `nova-config machine list --file missing.json --json` -> `{"result":{"verb":"machine list","status":"refused","exit":1,"remedy":"nova-config migrate --file missing.json","why":["schema config is at version 0 and this binary carries 45"]},"facts":{}}` (stable `CONFIG <NOUN> k=v` lines with a total; --json on every verb, refusals included, remedy inside; deduction: status prints a data line on a refused run, blocker 1) |
| 4 | dry-run | 4 | `nova-config machine remove -h` -> `--dry-run  print the change the remove would record (CONFIG DRY-RUN, from the same checks) and write nothing; it still reads the store` (every write's -h carries --dry-run: add, set, remove, fleet/sprint/tier set, apply, migrate, login, logout, loop run; migrate also has --print which "connect[s] to nothing"; "writes nothing" unverified: this card forbids running any write) |
| 5 | idempotent | 1 | `nova-config machine set -h` -> `effect: store write: one row and its history row, in PostgreSQL or the --file` (no write takes --op: no `-h` of the 48 offers it, and no help line says a repeated unchanged write changes nothing; retry-safety is undiscoverable from help) |
| 6 | no prompts | 5 | `nova-config login --check` -> `REFUSED: no login is recorded at .../nova-config/login.json; run: nova-config login --store <dir> --as <seat> --key <file> --secret <NAME> --dsn <dsn> --actor <name>` (answered at once; no probe ever waited on input; secrets come from a named variable or the secret store per -h, never a prompt) |
| 7 | exit codes | 3 | `nova-config machine list --file missing.json` -> exit 1, `REFUSED: schema config is at version 0 and this binary carries 45`, while `nova-config friend list --file missing.json` -> exit 2, `is not there yet`: one cause, two codes; the table, printed in all 48 helps, otherwise matched every run (usage 2, fixture store said no 1, done 0) |
| 8 | one word | 4 | `nova-config apply -h` -> `--check  the same as --dry-run` (a second word for one thing, and --check is a different read mode on machine self and login; otherwise one add/set/remove/list/show/history over all 8 kinds, no duplicate verbs; --as/--friend are documented one-release aliases) |
| 9 | push | 5 | n/a: no verb waits on state; reads answer at once and apply is a single push of the rows into Redis, nothing to poll |
| 10 | no leaks | 5 | `nova-config inventory --fixture inv-fixture.yml --host bench-b` -> `"nova_pg_dsn": "postgres://nova_config@localhost:5432/nova"` (DSN printed without a password; help: the password is never on the line, NOVA_PG_PASSWORD_ENV names the variable; login --check: "the password is never shown") |
| 11 | true help | 4 | `nova-config inventory --example` -> a fixture that `nova-config inventory --fixture ex-fixture.yml --list` reads back, exit 0 (every claim run held: self prints the name and opens no store, --list/--host exclusive, unknown host exits 1 with the known names, the bare refusal's verb list is the real list; write-side claims untestable read-only) |

## 10/10 blockers
| # | where | what an AI meets | fix | size |
|---|---|---|---|---|
| 1 | `nova-config machine list --file missing.json` (also `tier list`, `status`; `friend list` differs) | exit 1 `REFUSED: schema config is at version 0 and this binary carries 45` for a file that is not there, while `friend list` on the same file exits 2 `--file missing.json is not there yet`, and `status` prints `CONFIG STATUS file=missing.json schema=0 redis=-` on stdout before refusing at exit 1: one cause, three answers, two exits | name the true cause (the file is not there) with one refusal and one exit in every kind, and print no data line on a refused run | M |
| 2 | `nova-config tier add -h` | exits 0 advertising `effect: store write: one row and its history row`, while the banner's kinds text says the tier rows are `created by migrate` and `nova-config fleet add -h` refuses at the door: `REFUSED: fleet is one row, created by migrate; want set, show or history` — tier add is the one singleton door still advertising a write it cannot make (the 2026-10-02 rating names this dead end; still there) | refuse at the door as fleet and sprint do: tier's rows are made by migrate; want set, show, list or history | S |
| 3 | `nova-config machine set -h` (and every write verb's -h) | effect promises "one row and its history row" per write, but no help line says what repeating a write with unchanged values records, and no write takes --op: a caller that timed out cannot learn from help whether retrying is safe | one line in every write verb's -h stating the repeat behaviour: whether rev rises and a history row lands, or an unchanged write is a no-op | S |
| 4 | `nova-config inventory --fixture inv-fixture.yml --host nope` | `REFUSED: --host "nope" names no machine row; known machines: bench-a, bench-b; run: nova-config machine list` — the remedy cannot run in fixture mode (it refuses: no store named) and points the reader to login and a DSN they do not have | when --fixture is in effect, the remedy carries it: `run: nova-config inventory --fixture <file> --list` | S |
| 5 | `nova-config apply -h` | `--check  the same as --dry-run` — a second word for dry-run, while `machine self -h` and `login -h` give --check a different meaning (a real read with its own exits): one flag, two meanings in one tool | retire apply's --check alias with a one-release note; --dry-run is the one word | S |
| 6 | `nova-config help tier` | prints usage, effect and the exit table but no verb line and no example, while `nova-config help machine` prints six example lines (the 2026-10-02 rating names it; still there) | print the tier verbs and their examples as help machine does | S |

## Nice-to-haves
| # | where | what | fix | size |
|---|---|---|---|---|
| 1 | `nova-config machine list --max abc --file missing.json` | `REFUSED: invalid value "abc" for flag -max: parse error; run: nova-config machine list -h` — single-dash spelling of a --flag, and no word of what --max wants | spell the flag as help spells it and say what it wants (an integer, 0 prints all) | S |
| 2 | every write verb | no --op idempotency key, the repo-wide standard for a safe retry | add --op to add, set, remove, apply, migrate: the same op again returns the recorded result and changes nothing | M |
| 3 | `nova-config help`, the `example:` block | every example line writes (migrate makes try.json, then add and set); a reader not allowed to write has no runnable example | add a read-only example: kinds; inventory --example to a file; inventory --fixture on it | S |
| 4 | `nova-config bogus` | the want list omits help and version: `want kinds, migrate, status, apply, inventory, login, logout, or a kind` (the 2026-10-02 rating names it; still there) | name help and version in the want list | S |
| 5 | `nova-config help`, the first-run line | the sentence runs on around the export line: "first run: the example: lines need no database and write only ./try.json; the fleet's store is ... then migrate." (the 2026-10-02 rating names it; still there) | two sentences: what the example needs, then what the fleet's store needs | S |

## Top 3 fixes
1. One name and one exit for a --file that is not there, in every kind, with no stdout line on a refused run (blocker 1; lifts criteria 7 and 3).
2. State in each write verb's help what repeating an unchanged write does (blocker 2; the only help-side answer for criterion 5 until --op exists).
3. Make inventory's fixture-mode refusal remedy a command that runs with --fixture (blocker 3; lifts criterion 2).

## Good, keep
- The cold path with no database and no write: kinds (exit 0), machine self (prints the name, opens no store), and inventory --example into --fixture, which reads the printed fixture back — an AI can try real verbs before adopting the tool.
- Refusals name every problem at once (machine show reports the missing name and the missing store in one line) and the --json envelope carries the remedy even when refusing.
- Every verb's -h: usage copied from the parent help, an effect line classifying the verb (inspection, store write, external delivery), a worked example and the exit table — 48 of 48 exit 0.
- Two of the 2026-10-02 rating's defects are gone and stayed gone: the friend kind now reads a stated pronoun field with they/them as the unstated default, and a --json refusal is the one result object on stdout, remedy inside, stderr quiet.

## What could not be tried
- `migrate` (every form, --print and --dry-run included): a write verb, forbidden by this card; it is the only door to the --file store, so no kind's list, show, history or width could be run against rows — judged from -h alone.
- `add`, `set`, `remove` of all eight kinds, --dry-run included: write verbs, forbidden; judged from -h (each carries --dry-run, "write nothing; it still reads the store").
- `apply` and `apply --dry-run`: writes Redis and its dry run "still reads the store and Redis" per -h; no service this card may touch; judged from -h.
- `login`, `logout`: record and remove the login file, writes; only the read half ran: `login --check`, exit 2, remedy the full command.
- `loop run`: runs the loop's command under its lock on a machine, an external effect; judged from -h.
- `status`, list and show against PostgreSQL or Redis: stores this card may not touch; the --file alternative needs migrate first.
- `machine self --check`, `machine width <name>`, `<kind> history <name>`: read rows out of a store, and no store was openable; `machine self` without --check ran (exit 0, opens no store).

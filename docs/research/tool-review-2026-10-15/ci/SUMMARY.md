# nova-ci AI-usability review summary, 2026-10-15

Reviews: 5 (R1..R5, in byte order of file name)
Build: cc2f2f8c8148, b4f484c84916, f6c35d67aaa1
Score: 9.0/10 (range 8.5-9.5)

## Rubric
| # | criterion | median | range | the evidence that decided it |
|---|---|---|---|---|
| 1 | cold | 5 | 5-5 | `nova-ci slowtests --example` -> `CI-SLOW package=... seconds=65.1s budget=60s`, zero setup |
| 2 | refusals | 5 | 4-5 | `nova-ci slowtests --bogus` -> `REFUSED: unknown flag --bogus; ... run: nova-ci slowtests -h`; cause and next command always named |
| 3 | output | 4 | 3-5 | `nova-ci version --json` -> `REFUSED: takes no flags and no arguments, got 1`; only slowtests/functional take --json, and functional's `-run` pattern is unbounded |
| 4 | dry-run | 4 | 4-5 | every write verb has --dry-run except `nova-ci bench run`, which refuses `unknown flag --dry-run` |
| 5 | idempotent | 4 | 0-5 | no write verb takes `--op` (`nova-ci github receipt -h` lists no --op); a retry after a timeout is not provably safe |
| 6 | no prompts | 5 | 5-5 | every verb ran to completion non-interactively |
| 7 | exit codes | 5 | 5-5 | empty stream -> exit 1 `CI-SLOW FAILED`; unknown verb -> exit 2, as the help states |
| 8 | one word | 5 | 4-5 | verbs are single words (slowtests, functional, local, new-rule, new-verb, version, help); bench/github are two-word groups with one verb each |
| 9 | push | 5 | 5-5 | n/a: nothing polls; github receipt pushes one row, nothing is waited on |
| 10 | no leaks | 5 | 5-5 | `nova-ci github receipt --dry-run` -> `CI RECEIPT ... ev=-`; the password is read from an env-named variable, never on the line |
| 11 | true help | 4 | 4-5 | `nova-ci --help` says "slowtests reads the events of the packages you name", but slowtests takes no package args and reads a `go test -json` stream on stdin |

## Consensus 10/10 blockers
| # | where | what an AI meets | fix | size | named by |
|---|---|---|---|---|---|
| 1 | `nova-ci version --json`, `local --json`, `new-rule --json`, `new-verb --json`, `github receipt --json` | only slowtests and functional accept --json; every other verb refuses it with exit 2 | accept `--json` on every verb from the shared output envelope | M | R2, R3 |
| 2 | `nova-ci bench run` | the one write verb with no `--dry-run`; a caller cannot preview the copy, command and run directory before it happens | add `--dry-run` that prints the plan and writes nothing | S | R1, R2 |
| 3 | `nova-ci slowtests --example --json` | line output prints `per-cpu=<n>` but `--json` facts omit `per_cpu` | include `per_cpu` in the `--json` facts | S | R2, R4 |
| 4 | `nova-ci --help` | the banner says "slowtests reads the events of the packages you name" but slowtests takes no package names and reads stdin, so a cold reader calls it with arguments it refuses | reword to name the `go test -json` stream as the source | S | R2, R5 |
| 5 | `nova-ci github receipt -h` | no write verb takes `--op`; a timed-out delivery retried can double-write and is not provably safe | add `--op <id>` idempotency: the same id returns the recorded result and changes nothing | M | R1 |
| 6 | `nova-ci functional ./cmd/nova-ci ./internal/ci` | the second output line is one unbounded `-run` alternation regex (1397 chars for two packages) with no `--max` and no file | bound it under `--max` with a `MORE shown=<n> total=<n>` line or write it to a named file | M | R5 |
| 7 | `nova-ci bench run -h`, `nova-ci bench -h` | both print the whole 130-line top-level banner inside the `exit codes:` block (133 lines for `bench run -h`) | pass only bench run's single exit-code summary to the help printer | S | R2 |
| 8 | `nova-ci bench run` | missing-flag refusals emit multiple `BENCH-RUN REFUSED:` lines pointing to `run: nova-ci help` instead of the single-line `bench run -h` grammar | route through the single-line refusal formatter pointing to `nova-ci bench run -h` | S | R2 |
| 9 | `nova-ci slowtests --example` | the successful `CI-SLOW OK` and `CI-SLOW WARN` forms are undocumented in help, which describes only over-budget lines | document the OK/WARN line forms and fields | S | R3 |
| 10 | `nova-ci help` | the verbs list contains two-word names `bench run` and `github receipt` | use single-word verbs or document the two-word groups | M | R3 |
| 11 | `nova-ci local --dry-run` (outside git) | the refusal carries raw subprocess text: `git rev-parse ...: exit 128: fatal: not a git repository...` | omit raw subproc failure text; name only the missing checkout | S | R3 |
| 12 | `nova-ci slowtests` (when the load probe fails) | `CI-LOAD load=unknown ... sysctl -n vm.loadavg: exec: "sysctl": ... not found` is noisy | clean the load-fail reason or fall back silently to `load=-` | S | R3 |
| 13 | `nova-ci bogus-verb` | the unknown-verb refusal lists the verbs but names no nearest, while an unknown flag does (`did you mean --budget?`) | name the nearest verb as the flag path already does | S | R5 |

## Top 3 fixes
1. Accept `--json` on every verb (local, new-rule, new-verb, github receipt, version) from the shared envelope; the single largest score gain, named by two reviews.
2. Add `--dry-run` (and `--op` idempotency) to `bench run` and the other write verbs so every write is previewable and retry-safe.
3. Fix the banner's false "slowtests reads the packages you name" sentence and document the `CI-SLOW OK/WARN` grammar so cold trust and true help hold.

## Disagreements
Where a criterion was scored two or more points apart, and which evidence holds when the command is rerun:
- **rubric 5 (idempotent)**: scores 0, 1, 4, 5, 5 (range 0-5). R1 reads the missing `--op` as a 1/5 blocker, R4 as 0/5; R3/R5 score 5 as n/a (scaffolds refuse on file-exists; local writes a temp dir); R2 scores 4. Rerun: `nova-ci github receipt -h` still lists no `--op`, so the write verbs remain without an op id — R1's and R4's evidence holds; the n/a readings do not change that.
- **rubric 3 (output)**: scores 3, 4, 4, 4, 5 (range 3-5). R1's 5 ran only slowtests JSON; R2's 3 rests on `nova-ci version --json` refusing. Rerun: `nova-ci version --json` -> `REFUSED: takes no flags and no arguments, got 1` (exit 2), so R2's evidence holds.
- All thirteen blockers above were rerun from the binary built at the checkout tip and reproduced; none was dropped. The load-probe blocker (12) reproduces because this host lacks `sysctl`, so the raw `exec: "sysctl": not found` text appears in the `CI-LOAD` line and in `slowtests --json` facts as `load_why`.

## Nice-to-haves
- `nova-ci functional --json` prints `{"packages":[],"pattern":""}` with no result/facts/items envelope; emit the shared envelope. (R1)
- The leftover second `package main` in `cmd/nova-ci/timing.go` is reached by no verb and named in no help; move it under `tools/` or wire it in as a verb. (R1)
- `nova-ci functional`'s empty-selection reason `reason=no-functional-tag-in-1-dirs` is a machine word; print the directories inspected, e.g. `dirs=./a,./b`. (R3, R5)
- `docs/CLI.md`'s nova-ci section has no `### First run` heading; add one with the two `slowtests --example` commands. (R5)
- `nova-ci local --dry-run` prints ~145 packages in one long line repeated in the make command; shorten or cap with `--max`. (R3)
- Keep the fixes already landed from the earlier round: `functional` keeps its `./` prefixes and pastes, and `new-rule --dry-run` consistently refuses outside a checkout. (R3)

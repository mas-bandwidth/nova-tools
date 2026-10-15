# nova-ci AI-usability review, 2026-10-15

Reviewer: mercury-3-preview-1002
Build: cc2f2f8c814
Score: 9.5/10 (52/55)

## Rubric
| # | criterion | score | evidence |
|---|---|---|---|
| 1 | cold | 5 | `nova-ci slowtests --example` -> `CI-SLOW package=github.com/mas-bandwidth/nova-tools/internal/example seconds=65.1s budget=60s` |
| 2 | refusals | 5 | `nova-ci slowtests --budget notanumber --example` -> `nova-ci slowtests REFUSED: invalid value for --budget ... run: nova-ci slowtests -h` |
| 3 | output | 4 | `nova-ci functional --json ./cmd/nova-ci` -> `{"packages":["./cmd/nova-ci/"],"pattern":"^(...)$"}`; lines and JSON agree, but the `-run` pattern line is unbounded (1398 chars for two packages, no --max, no file) |
| 4 | dry-run | 4 | `nova-ci new-rule --root <repo> --dry-run trtestrule` -> `nova-ci new-rule NOTE --dry-run wrote nothing`; every write verb has --dry-run except `bench run` |
| 5 | idempotent | 5 | n/a: no write verb takes `--op`; scaffolds refuse a rerun on file-exists (help exit 2) so a repeat never diverges, and local writes only a temp dir |
| 6 | no prompts | 5 | every verb ran to completion non-interactively; nothing waited on stdin |
| 7 | exit codes | 5 | `printf '' \| nova-ci slowtests` -> exit 1 `CI-SLOW FAILED packages=0 slowest=none ... run: nova-ci slowtests --allow-empty`; help's 0/1/2 per-verb table matched every run |
| 8 | one word | 5 | verbs are distinct names (slowtests, functional, local, new-rule, new-verb, version, help); bench/github are two-word groups with one verb each |
| 9 | push | 5 | `nova-ci github receipt --dry-run` -> `CI RECEIPT ... ev=-`; a receipt is pushed as one row, nothing is polled |
| 10 | no leaks | 5 | `nova-ci github receipt --dry-run` -> `ev=-`; help states the password is read from an env-named variable, never on the line |
| 11 | true help | 4 | `nova-ci help` -> `In your own module, slowtests reads the events of the packages you name`, but slowtests takes no package names and reads stdin; every other claim probed held (per-verb -h, empty stream, dry-runs, exit codes) |

## 10/10 blockers
| # | where | what an AI meets | fix | size |
|---|---|---|---|---|
| 1 | `nova-ci functional ./cmd/nova-ci ./internal/ci` | the second output line is one `-run` pattern of 1398 characters for just two packages, with no bound and no file; `functional -h` offers no `--max`, so a whole-tree `./...` run is a single impractically large argument | bound the pattern under `--max` with a `MORE shown=<n> total=<n>` line or write it to a named file and print that path with the count | M |
| 2 | `nova-ci help` | the banner says `slowtests reads the events of the packages you name`, but slowtests takes no package names and reads a `go test -json` stream on stdin, so a cold reader calls it with arguments it refuses | reword to: the packages are named to `go test -json`, whose stream slowtests reads from stdin | S |
| 3 | `nova-ci bogus-verb` | `unknown verb "bogus-verb"; the verbs are ... run: nova-ci help` lists the verbs but names no nearest, while an unknown flag does (`did you mean --budget?`) | name the nearest verb the way the flag path already does | S |

## Nice-to-haves
| # | where | what | fix | size |
|---|---|---|---|---|
| 1 | `nova-ci bench -h` (117 lines) and `nova-ci bench run -h` (133 lines) | bench help prints the whole banner under `exit codes:`, where every other verb prints only its own paragraph | hand the exit-code summary, not the banner, to the help printer | S |
| 2 | `nova-ci bench run` | the one write verb with no `--dry-run`; an AI must guess the remote effect | add `--dry-run` that stages the copy locally and prints the command line, writing nothing | S |
| 3 | docs/CLI.md nova-ci section | no `### First run` heading, while sibling tool sections have one | add `### First run` with the two `slowtests --example` commands | S |
| 4 | `nova-ci functional` | the empty-selection reason `reason=no-functional-tag-in-1-dirs` is a machine word that does not name the directories inspected | print the count and directories, e.g. `dirs=./a,./b` | S |

## Top 3 fixes
1. Bound functional's `-run` pattern (blocker 1) — it is the one truly bad output and the tallest barrier to a real adoption run.
2. Fix the slowtests help sentence (blocker 2) — a false claim breaks cold trust.
3. Name the nearest verb in refusals (blocker 3) — a paste-able recovery is the whole point of the refusal grammar.

## Good, keep
The first run works from the binary alone: `slowtests --example` reads a built-in stream and prints a CI-SLOW finding and a CI-LOAD line with nothing installed. Refusals name every problem at once with a command that runs, and every write verb except bench offers `--dry-run` whose output is the plan. Lines are one stable grammar (CI-SLOW, CI-LOAD, CI-SLEEPS, REFUSED) and `--json` renders the same value. Old defect still there: the unbounded `-run` pattern was in the 2026-10-02 rating (finding 5) and is unchanged; the unknown-verb-no-nearest (finding 9) is unchanged; bench help printing the whole banner (finding 1) is unchanged.

## What could not be tried
- `bench run` without `--dry-run`: needs a live bench host reachable by ssh; judged from help and the missing-`--host` refusal.
- `github receipt` for real: dials a Redis store; only `--dry-run` was run, which dials nothing.
- `local` for real: runs the whole Makefile test target over 145 packages; only `--dry-run` was run.
- `new-rule` / `new-verb` real writes: would modify the checkout; only `--dry-run` was run.

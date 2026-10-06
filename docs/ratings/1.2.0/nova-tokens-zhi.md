# nova-tokens READ and USE rating, nova-tools 1.2.0

Rater: Claude Opus 5.5 in Claude Code, a sprint worker on a friend's re-rate card; the rating is this worker's, not the friend's
Build: 58bfcee39c17
READ: 7/10
USE: 7/10

No v1.2.0 tag exists yet. The build rated is the head of sprint/mechanical-2026-10-02; `nova-tokens version` prints `nova-tokens v1.0.1-0.20261006150510-58bfcee39c17 linux/amd64 go1.26.6`. Built and run on a Linux bench machine in a scratch directory made for the trial; no live store, no server. Ran the banner's setup and all seven example lines as printed, then every verb against hand-made transcripts: fold (overlap, shrink, partial read, no-id, `--dry-run`, `--json`), check (`--strict`, `--no-spend`, `--through`, a stray), sum, sources, report (local mode with `--note`, `--dry-run`, a partly and a wholly unreadable source), session (`--role`, `--out`, `--dry-run`, an empty and a garbage transcript), profiles, ledger `--dry-run`, and ledger and report `--redis` against a closed port. The store mode with a real store was not run.

## Reasons

READ. The banner says what the tool is in its first line, how it works in the next five, and points at a first run on line 8. Every rule a caller needs to trust a number is stated in plain words: a dash is never a zero, exit 1 still writes, a day that would shrink is refused, two sources of one provider sharing message ids are refused, a fold merges by source. Every verb's `-h` quotes its usage lines from the banner, gives an example and an `effect:` line that says exactly what it writes, then its flags.

What costs the score. The banner is 160 lines and its first-run pointer at cmd/nova-tokens/main.go:50 sends the reader to `setup:` at main.go:170, past a hundred lines of rules. Some sentences are not true: "--timeout is the one flag with a default" (main.go:105) while `--weights` and session's `--day` have defaults and report's `--by` defaults to `model`; "sources is the one that only looks" (main.go:249) while sum, check and profiles also only look. The report mode sentence (main.go:64) says one rule three times. Every `-h` ends with the same seven-line exit paragraph, `version -h` included, which can fail none of those ways. `report -h` lists `--bus`, `--swarm` and `--by` without saying which mode each belongs to. `session -h` does not say the role also becomes the repo cell, nor print the `--weights` numbers it defaults to. `-h` example lines carry no `example:` label, and `--timeout <int>` shows no default. The docs drift from the binary: docs/CLI.md:1746 says "one setup command" (there are ten lines) and "two exceptions" to the environment rule (the banner names three); docs/USAGE.md:186 still says "no defaults and no environment variables"; README.md:54 still installs 1.0.0.

A 10 is a banner whose first run sits under its usage block, every sentence true of the binary, each verb's own exit causes in its `-h`, and docs that say what the binary says.

USE. The setup and the six fold-check-sum-sources-report lines ran as printed, each exit 0 with one typed status line. The safety rules hold when attacked: two `--claude` sources over copies of one transcript were refused before anything was written; an unreadable transcript left the day file byte-identical (same md5) with four `TOKENS SHRANK` lines and exit 1, and `--dry-run` printed the same plan with `dry_run=true`; a transcript with a garbage line folded the rest and said `FAILED` with `badline=1`; a transcript with no message id was `FOLD FAILED dropped=1 of 1`, not a quiet zero; `check` on an empty `--out` is `CHECK FAILED`, a stray file is named, and `--strict`, `--no-spend` and `--through` each did what the help says. Refusals list every missing flag in one run, an unknown flag gets the flag list and a suggestion, and report with a wholly unreadable source is now `REPORT FAILED`.

What costs the score. The banner's own seventh example line doubles the day: after the fold, `session --claude-session ./session.jsonl --out ./out` adds message `example-1` again as a `claude-session` row on repo `unattributed`, `check` says `CHECK OK`, and `sum` says `input=1624` for a day that spent 812, against main.go:131's "check and sum never see a doubled day". The overlap refusal guards two `--claude` flags but not fold against session. With one of two transcripts unreadable, report prints `REPORT OK` and exits 1 while its `--json` says `"status":"failed"`. session makes a missing `--out` (and leaves an empty directory holding `fold.lock` when it then fails), where fold refuses a missing `--out`. session over an empty transcript prints zeros, not dashes, and exits 0 without `--out` but 1 with it. `sum` says `missing=1` for a calendar gap where `check` says `missing=0 gap=1` for the same files: one word, two meanings. `sum` over an empty `--out` and `profiles` over an empty root are OK exit 0, where check calls the same emptiness a failure. The machine-interface findings from 1.1.0 are all still there: JSON counts are strings beside numbers; missing-flag and bad-value refusals point at `nova-tokens help`, not the verb's `-h`; `report --dry-run` puts `dry_run=true` on stderr; `version --json` says `got 1`; `REPORT OK` repeats `at=` and `build=` inside an unquoted `subject=`. Report's local mode accepts `--by bogus` and ignores it. One fold prints both `FOLD FAILED` and `TOKENS FAILED`, and session's `TOKENS DAY` says `day=` where fold's says `date=`.

A 10 needs the overlap rule across fold and session, one status word per exit, one meaning per field name, one JSON type per fact, every refusal pointing at the verb's `-h`, and session refusing a missing `--out` as fold does.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | `nova-tokens session --claude-session ./session.jsonl --out ./out` after the banner's fold example | The banner's seventh example books message `example-1` a second time (`claude-session` on `unattributed`); `check` is `CHECK OK` and `sum` is `input=1624` for a day that spent 812, against cmd/nova-tokens/main.go:131. | Apply the shared-message-id refusal between session and the Claude sources already in the day file, or make the example fold a different transcript. | M |
| 2 | cmd/nova-tokens/report.go:343 | With one of two transcripts unreadable, report prints `REPORT OK ... rows=4` and exits 1; `--json` says `"status":"failed"`. | Print `REPORT FAILED` (or `REPORT PARTIAL`) whenever the exit is 1. | S |
| 3 | cmd/nova-tokens/session.go:110 | session runs `os.MkdirAll` on `--out`; fold refuses `--out does not exist`. A session over an empty transcript with a new `--out` leaves that directory holding an empty `fold.lock` and exits 1. | Refuse a missing `--out` as fold does, before the lock. | S |
| 4 | `nova-tokens session --claude-session ./empty.jsonl` | An empty transcript prints `SESSION turns=0 input=0 ... weighted=0` exit 0 without `--out`, and exit 1 with it; zeros where the tool's rule is a dash for a count no source gave. | Refuse or fail a session with no turn the same way with or without `--out`, and print dashes. | S |
| 5 | cmd/nova-tokens/sum.go:16 | `sum` prints `missing=1` for a calendar gap; `check` over the same files prints `missing=0 gap=1`. One field name, two meanings. | Name sum's field `gap=` as check does. | S |
| 6 | `nova-tokens sum --out ./empty --month 2026-09`; `nova-tokens profiles --swarm-root ./empty` | Both print OK and exit 0 over nothing, while `check` calls an `--out` with no day file `CHECK FAILED`. | Say in sum's OK line that it found no day file (`days=0` plus a NOTE), and fail profiles over an empty root. | S |
| 7 | `nova-tokens sum --out ./out --month 2026-09 --json`; `fold --json` | Counts are JSON strings (`"input":"812"`, `"turns":"1"`, fold's `"files":"1"`) beside numbers (`"days":1`); session's are numbers. | Emit every count as a JSON number and `-` as null. | S |
| 8 | `nova-tokens fold --out ./out --day 2026-09-11 --json` | Missing-flag and bad-value refusals carry `"remedy":"nova-tokens help"`; only the unknown-flag and positional refusals name `fold -h`. | Point every refusal after a verb at `nova-tokens <verb> -h`. | S |
| 9 | `nova-tokens report ... --note ./note.txt --dry-run` | The body goes to stdout and `dry_run=true note=./note.txt` to stderr; a caller capturing stdout never sees it was a dry run. | Say in `report -h` that status lines go to stderr, or carry the marker in the body. | S |
| 10 | cmd/nova-tokens/report.go:343 | `REPORT OK ... at=... build=... subject=tokens 2026-09-11 at=... build=...`: the pair repeats inside an unquoted value with spaces. | Quote `subject=` or put it on its own line. | S |
| 11 | cmd/nova-tokens/version.go:70 | `version --json` refuses `takes no flags and no arguments, got 1`, not naming `--json`. | Name what it got. | S |
| 12 | `nova-tokens report --who ada --day 2026-09-11 ... --by bogus` | Local mode accepts and ignores `--by bogus`, exit 0; `report -h` lists `--by`, `--bus` and `--swarm` without their mode. | Refuse store-mode flags in local mode, and label each flag's mode in `-h`. | S |
| 13 | cmd/nova-tokens/fold.go:276 | A fold with no message id prints `FOLD FAILED` and then `TOKENS FAILED`: two prefixes for one verb. | Print it as a `TOKENS NOTE` before the one `TOKENS FAILED` line. | S |
| 14 | `nova-tokens session --out ./o3` | session's `TOKENS DAY` line says `day=`, fold's says `date=`. | Use `date=` in both. | S |
| 15 | `nova-tokens session -h` | `--role` does not say the role also replaces the repo cell (`--role Worker` wrote repo `Worker`), and it accepts capitals where a label is `[a-z0-9-]+`. | Say so in `-h` and hold the role to the label grammar. | S |
| 16 | `nova-tokens session -h`; `fold -h` | `--weights` says "the defaults are the ratios of one vendor's published list prices" without the numbers; `--timeout <int>` shows no 120. | Print `(default 1,1.25,0.1,5)` and `(default 120)`. | S |
| 17 | cmd/nova-tokens/main.go:105 | "--timeout is the one flag with a default", while `--weights`, session's `--day` and report's `--by` have defaults. | Name every default, or narrow the sentence. | S |
| 18 | cmd/nova-tokens/main.go:249 | The bare-verb refusal says "sources is the one that only looks"; sum, check and profiles write nothing too (docs/CLI.md:1759 repeats it). | List the read-only verbs or drop the clause. | S |
| 19 | cmd/nova-tokens/main.go:64 | "giving both --who and --redis selects the store mode (--redis wins); a mix of --who and --redis prints the store summary" says one rule three times. | "--redis selects the store mode, whatever else is given." | S |
| 20 | cmd/nova-tokens/main.go:50 | The first-run pointer is at line 50; the `setup:` and `example:` it names are at line 170. | Move setup and example directly under the usage block. | S |
| 21 | cmd/nova-tokens/main.go:85 | Every `-h`, `version -h` included, ends with the full seven-line exit paragraph about fold and report. | Print each verb's own exit causes. | S |
| 22 | docs/CLI.md:1746 | Says the help has "one setup command" (it has ten lines) and "two exceptions" to the environment rule (the banner names three). | Match the banner. | S |
| 23 | docs/USAGE.md:186 | "there are no defaults and no environment variables", against `--timeout`, `--weights` and the Redis variables. | Name the exceptions there. | S |
| 24 | README.md:54 | "These are the Nova Tools 1.0.0 commands" and installs `@v1.0.0`. | Name the current release. | S |

## Good, keep

The safety rules are real and testable from the banner alone: the overlap refusal before any write, naming both labels and the count; the shrink refusal leaving the file byte-identical and naming each type with what the file said and what the sources say now; `TOKENS PARTIAL` and the no-id `FAILED` instead of a quiet zero; `check` refusing to go green over nothing.

`--dry-run` is the real run's plan, refusals included, ending `dry_run=true`, and session's dry run makes no directory.

The dash rule: a count no source gave is `-` in the day file, in sum and in report's `usd=`, and sum counts the dashes beside the totals.

Refusals that name every missing flag at once, and an unknown flag answered with the verb's flag list and a suggestion.

The `effect:` line on every `-h`, which says exactly what the verb writes, where, and what it leaves.

## Compared with earlier ratings
| earlier | now | evidence |
|---|---|---|
| overlapping sources can double a day | CHANGED | two `--claude` sources over one transcript are `TOKENS REFUSED` exit 2; fold plus the banner's `session --out` still doubles the day under `CHECK OK` (finding 1) |
| REPORT OK with exit 1 | CHANGED | a wholly unreadable source is `REPORT FAILED` exit 1; one unreadable file of two is still `REPORT OK` exit 1 (finding 2) |
| --json numbers as strings | STILL THERE | sum `--json`: `"input":"812"`, `"turns":"1"` beside `"days":1` |
| missing-flag remedy points at the global help | STILL THERE | `fold --out ./o2 --day 2026-09-11 --json` gives `"remedy":"nova-tokens help"` |
| report --dry-run marker on stderr | STILL THERE | `report ... --dry-run 2>/dev/null` shows the body and no `dry_run=true` |
| version refusal does not name the flag | STILL THERE | `version --json`: `VERSION REFUSED: takes no flags and no arguments, got 1` |
| subject= unquoted with spaces | STILL THERE | `REPORT OK ... subject=tokens 2026-09-11 at=... build=...` |
| --timeout called the one flag with a default | STILL THERE | cmd/nova-tokens/main.go:105 |
| docs/USAGE.md says no defaults and no environment | STILL THERE | docs/USAGE.md:186 |
| README names 1.0.0 | STILL THERE | README.md:54 |

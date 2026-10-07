# nova-tokens READ and USE rating, nova-tools 1.2.0

Rater: Mercury 2.5 in a generic sprint worker on a re-rate card
Build: a65b5cba44ab

READ: 7/10
USE: 7/10

No v1.2.0 tag exists on the forge yet, so this rates the release candidate at head of sprint/mechanical-2026-10-02. Built and run on darwin/amd64 in a scratch directory under the sandbox; no live store, no server. Every verb ran: fold, check, sum, sources, report, session, profiles, ledger, and version.

## Reasons

READ. The banner answers what the tool is in its first line, then how it works, a usage block for all verbs, the exit table, and example lines. Every verb's `-h` prints its usage and flags.

What keeps READ at 7: Several defects in the banner and help. `--timeout` is called "the one flag with a default" while `--weights` defaults to `1,1.25,0.1,5` and session's `--day` defaults to every stamped day. The report mode line repeats the same rule three ways. Every `-h` ends with the full seven-line exit paragraph, even for `version -h` which can fail none of those ways. `report -h` lists `--bus` and `--swarm` which the local-mode usage omits. `session -h` doesn't say `--role` also replaces the repo cell. `fold -h` doesn't show `--timeout`'s default. The setup/example lines sit at line 170 but first-run instructions sit at line 50.

USE. The setup and example lines ran. The safety rules hold: two `--claude` sources with same message ids are refused before writing. Shrink is refused and the file is left unchanged. `--dry-run` works. `check` on empty is CHECK FAILED. Refusals name missing flags.

What keeps USE at 7: The banner's own example double-counts: `fold` then `session --out` with the same transcript adds message `example-1` twice. `check` on empty is CHECK FAILED but `sum` and `profiles` on empty are OK. JSON types are inconsistent: some facts are strings when others are numbers. Missing-flag remedies point at global help. `report --dry-run` shows `dry_run=true` on stdout (not stderr). `version --json` doesn't name the bad flag. The overlap remedy is not pastable.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | `nova-tokens session --claude-session ./session.jsonl --out ./out` after the banner's fold example | The banner's own example counts message `example-1` twice: fold writes a row, session adds a second row with same id; `check` is CHECK OK and `sum` doubles input. | Apply shared-message-id refusal between session and fold. | M |
| 2 | `nova-tokens check --out ./empty` | CHECK FAILED on empty. | M |
| 3 | `nova-tokens sum --out ./empty --month 2026-09` | SUM OK on empty; inconsistent with check. Exit 1 with FAILED. | S |
| 4 | `nova-tokens profiles --swarm-root ./empty` | PROFILES OK on empty; inconsistent with check. Exit 1 with FAILED. | S |
| 5 | `nova-tokens sum --json` | input, output, cache_write, cache_read, turns are strings while days is a number. Emit all counts as numbers. | S |
| 6 | `nova-tokens fold -h` | Missing-flag refusal says `run: nova-tokens help`. Point at `fold -h`. | S |
| 7 | `nova-tokens report --dry-run` | dry_run=true printed on stdout with body. Put on stderr or in JSON. | S |
| 8 | `nova-tokens version --json` | Says "got 1" without naming --json. Name the bad flag. | S |
| 9 | cmd/nova-tokens/main.go:105 | "--timeout is the one flag with a default" is false. | S |
| 10 | cmd/nova-tokens/main.go:64 | Report mode rule repeated three times. One clause. | S |
| 11 | cmd/nova-tokens/session.go:196 | --role doesn't say it replaces repo cell. Add it. | S |
| 12 | cmd/nova-tokens/main.go:50 | First-run setup/example at line 170, instructions at 50. Move them up. | S |
| 13 | cmd/nova-tokens/main.go:85 | Every -h has full exit paragraph. Print verb-specific exits. | S |
| 14 | cmd/nova-tokens/report.go:59 | report -h lists --bus, --swarm not in usage. Add or remove. | S |
| 15 | cmd/nova-tokens/fold.go:384 | Overlap remedy `run: nova-tokens fold ... without --claude b` not pastable. Print full command. | S |
| 16 | cmd/nova-tokens/main.go:105 | fold -h --timeout doesn't show default 120. Add it. | S |
| 17 | cmd/nova-tokens/session.go | SESSION DAY uses day=, FOLD DAY uses date=. Use same field name. | S |

## Good, keep

Overlap rule refuses two declared sources sharing message ids before writing. Shrink rule leaves file unchanged. --dry-run prints the plan. check on empty is FAIL. Dash never becomes zero.

## Compared with earlier ratings

| earlier | now | evidence |
|---|---|---|
| overlapping sources can still yield doubled successful totals | CHANGED | two --claude sources with same id are TOKENS REFUSED; fold + session --out still doubles |
| --timeout called the one flag with a default | STILL THERE | main.go:105 |
| --json numbers as strings | STILL THERE | sum --json prints input as string |
| missing-flag remedy points at global help | STILL THERE | fold refusal says `nova-tokens help` |
| report --dry-run marker on stderr | CHANGED | dry_run=true now on stdout |
| version refusal does not name the flag | STILL THERE | version --json says "got 1" |
| subject= unquoted with spaces | STILL THERE | REPORT OK has subject="tokens 2026-09-11 at=..." |

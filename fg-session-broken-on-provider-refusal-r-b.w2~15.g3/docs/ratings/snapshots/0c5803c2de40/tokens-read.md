# nova-tokens READ rating, current baseline 0c5803c2de40

Rater: deepseek-v4.1-flash
Build: 0c5803c2de40
Score: 8/10
README: 8.5/10

## Reasons
The tool earns its score on trust. The dash-not-zero rule is stated once and kept
everywhere (`internal/tokens/tokens.go:48`); the fold merges by source so one run cannot
erase another source's rows (`internal/tokens/dayfile.go:244`); a day that would shrink is
refused and left byte-identical (`internal/tokens/dayfile.go:177`); and the tests are named
after the numbered spec rules, so the contract is legible before the code
(`cmd/nova-tokens/demanded_test.go:719`). The README sentence and the banner's first line
agree (`README.md:31`, `cmd/nova-tokens/main.go:46`), and every path is a flag with a
refusal that says what the flag wants. A cold reader can fold a fixture and read it back
within a minute.

A 10 needs the weight pulled down and one grammar per event class. The entry point is 1,492
lines holding five verbs plus a 119-line banner (`cmd/nova-tokens/main.go:555`); the banner
restates the spec rule for rule (`cmd/nova-tokens/main.go:46`); the command reference keeps
a 2,500-character paragraph (`docs/CLI.md:1402`); and `session` prints a `TOKENS DAY` line
whose fields the fold's grammar does not have (`cmd/nova-tokens/session.go:140`). None of
these stops a reader using the tool, and together they are what keeps it off the top.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | `cmd/nova-tokens/main.go:555` | One 1,492-line file holds `fold`, `sources`, `report`, `sum` and `check`; the earlier rating's 1,454-line entry point has grown, so a reader still cannot take one verb at a time. | Move each `cmd*` into its own file beside `ledger.go`, `session.go` and `profiles.go`; put the banner in `usage.go`. | L |
| 2 | `cmd/nova-tokens/main.go:46` | The banner is 119 lines and restates the spec (merge-by-source, supersedes, gap counting, unattributed) after the example; the onboarding standard asks for how-it-works in at most five lines. | Keep line 1, a five-line how-it-works, the example and the exit table; move the rest to `help <verb>` and `docs/CLI.md`. | M |
| 3 | `docs/CLI.md:1402` | A single 2,500-character paragraph bundles `check`, `sources`, `profiles`, `version`, the provider parsers, the xAI export and `session`; a reader cannot scan it. | Split it into one short paragraph or bullet per verb. | M |
| 4 | `cmd/nova-tokens/session.go:140` | `session` prints a `TOKENS DAY` line whose fields (`day=`, `retained=`, `model=`, `weighted=`) are not the fold's `TOKENS DAY` grammar (`docs/SPEC-TOKENS.md:526`, `cmd/nova-tokens/main.go:877`); a scanner keyed on that event class reads the wrong fields. | Give `session` its own event token, or add its fields to the spec's grammar. | M |
| 5 | `cmd/nova-tokens/ledger.go:144` | A Redis store that does not answer prints `LEDGER FAILED` or `REPORT FAILED` and exits 1, where the family's table makes could-not-run exit 2 (`docs/SPEC.md:86`). | Exit 2 on a dial or transport failure; keep 1 for a verb that ran and said no. | S |
| 6 | `cmd/nova-tokens/main.go:1239` | `report` writes the body to stdout before `--note` is written, so a refused `--note` (a symlinked directory) leaves an artifact on stdout plus a `REPORT REFUSED`, though the spec promises `--note` is the artifact. | Write `--note` first, or refuse before emitting stdout. | S |
| 7 | `cmd/nova-tokens/main.go:1231` | A committed comment argues a grammar change is proposed in the pull-request body, so a cold reader cannot tell whether the `REPORT FAIL` line is settled. | State the rule in `docs/SPEC-TOKENS.md` and cite it, or drop the pull-request reference. | S |
| 8 | `cmd/nova-tokens/profiles.go:29` | `profiles` measures swarm budget overshoot by matching the literal `YOUR TOKEN BUDGET IS ` in another tool's prompt file; the banner's one line never mentions it, so the verb's place in an accounting tool is unclear. | Say in the banner and the command reference why this measurement lives here, or move it to the swarm tool. | S |

## Good, keep
- The dash-versus-zero rule, stated once (`internal/tokens/tokens.go:48`) and held by a test that forbids writing a literal zero for an unread type (`cmd/nova-tokens/contract_test.go:257`).
- The merge-by-source fold, so a one-source run cannot erase another source's row (`internal/tokens/dayfile.go:244`), with the shrink check on the merged totals (`cmd/nova-tokens/main.go:738`).
- Tests named after the numbered spec rules, so the contract reads as a list before the code does (`cmd/nova-tokens/demanded_test.go:719`).

## Compared with earlier ratings
| earlier | now | evidence |
|---|---|---|
| a 1,454-line `main.go` | STILL THERE | `cmd/nova-tokens/main.go:555`; the file is 1,492 lines at this baseline |
| a 220-line fold in the entry point | CHANGED | `cmd/nova-tokens/main.go:555` delegates to `internal/tokens/tokens.go:170` |
| five line-tokens and three status words in one tool | CHANGED | `docs/SPEC-TOKENS.md:518` gives one event class per line |
| overlapping sources yield doubled, successful totals | CHANGED | `internal/tokens/tokens.go:176` counts the shared ids and `cmd/nova-tokens/main.go:922` names them; the numbers still double by design |
| README then 6.5 to 8.4 | CHANGED | `README.md:19` table; this read scores it 8.5 |

# nova-cairn READ rating, nova-tools 1.1.0

Rater: OpenAI gpt-5.6-sol
Build: 2c02b2aa2042
Score: 6.5/10
README: 8.5/10

## Reasons

The README gives a crisp reason to choose nova-cairn, a safe local first command, and the crucial boundary that persistence does not mean publication. The command reference and specification explain the two storage shapes, retry behavior, provenance, limits, and publication semantics in enough detail to support serious adoption. Before code, I was confused by `[--text]` inside a shell block at docs/CLI.md:2098 and bored when the runnable sequence at docs/CLI.md:2094 led into detailed prose through docs/CLI.md:2136 without observed output or a marked first-run path; I noted no specific unsupported claim that I doubted at that stage. Later source review found that the unconditional conflict promise is implemented with read-then-write sequences and no same-entry exclusion. The implementation also makes a re-open report the caller's new stamp and policy even though the operation is a no-op, and an open whose record write succeeds but log append fails cannot heal that log on retry. The README explicitly pins its general trial to 1.0.0; that is coherent, but it adds friction for a reader rating 1.1.0 because the current install path is not adjacent. A 10 would make the same-entry write atomic, make retries heal incomplete opens, report stored metadata on re-open, and give cairn a short copyable first-run transcript for the current release.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | internal/cairn/cairn.go:500 | Nested append checks absence before atomic replacement at line 536, while flat append checks a snapshot at line 247 before an independent append at line 270; two concurrent different writes for one ID can both pass the check, defeating the documented conflict guarantee. | Serialize by session and entry or use exclusive creation plus conflict reconciliation so only one first write can win. | M |
| 2 | internal/cairn/cairn.go:411 | Re-opening returns as a no-op, but cmd/nova-cairn/main.go:169 reports the new caller stamp and publication policy as if they were stored; the result is not a receipt of actual state. | Return stored open metadata and render it, including an explicit duplicate or already-open fact. | M |
| 3 | internal/cairn/cairn.go:424 | Open writes the session record before appending its provenance log; if the log append at line 427 fails, retry sees the record at line 411 and returns success without healing the missing open event or source. | Detect and repair the missing matching open log record on an idempotent retry, or commit record and provenance as one recoverable operation. | M |
| 4 | docs/CLI.md:2088 | The cairn reference begins without a First run section or observed output, so a cold reader must infer success shapes and navigation from descriptive prose. | Add a First run heading with a small end-to-end transcript and explain the result fields immediately below it. | M |
| 5 | docs/CLI.md:2098 | The shell block includes `[--text]` as if it were executable syntax; copying the line passes a literal argument instead of showing either valid command. | Move the optional flag into prose or show separate receipt commands with and without `--text`. | S |
| 6 | README.md:48 | The repository trial is explicitly pinned to 1.0.0, which is internally coherent but leaves a 1.1.0 reader without an adjacent current installation command. | Add a clearly labeled current-release install path while preserving the pinned historical trial where needed. | S |

## Good, keep

Keep the README's concise plain-file purpose and its explicit local-persistence boundary. Keep the specification's careful distinction between nested and flat stores, especially nested precedence and the promise that flat records gain no sidecars. Keep the exact retry, conflict, source, and publication semantics in the command reference.

## Compared with earlier ratings
| earlier | now | evidence |
|---|---|---|
| 2026-10-02 READ 8: two store shapes tangled in one file | CHANGED | Flat read parsing now lives in internal/cairn/read_existing.go:59, while flat append and shared dispatch remain in internal/cairn/cairn.go:245 and internal/cairn/cairn.go:478; responsibilities are partly separated rather than fully untangled. |
| 2026-10-02 READ 7.2: concurrent same-id appends can violate conflict semantics | STILL THERE | internal/cairn/cairn.go:500 checks before the replacing write at line 536, and the flat path reads at line 247 before appending at line 270, with no same-ID exclusion between each pair. |
| 2026-10-02 README 6.5 to 8.4 | CHANGED | README.md:25 gives a concrete local open command and explicitly says local persistence does not imply publication; README.md:48 deliberately pins the broader trial to 1.0.0, leaving current-release setup elsewhere. |

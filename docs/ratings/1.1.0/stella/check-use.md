# nova-check USE rating, nova-tools 1.1.0

Rater: OpenAI gpt-5.6-sol
Build: b9282f35e392
Score: 8/10

## Reasons

The tool completed two different small jobs from its own help: a record-tree quickstart and a branch hygiene check. The first run was easy to assemble, both success summaries were compact, and JSON preserved the facts and findings in a shape an AI could act on without parsing prose. Broken-link and spelling findings named the file, line, target or replacement precisely.

All four provoked refusals exited 2 and named the immediate problem. A call missing two independent kernel inputs reported both in one run, which saves a repair turn. The recovery command is always the full `nova-check help`, however, even when `nova-check help links` is the much smaller relevant door. The root help is long enough that returning to it for a flag typo costs attention.

I had to guess how `--file` combines with `--dir`: using the ordinary repository-relative spelling `./records/problems.md` made the tool look under `records/records/` and report the requested file as unreadable. The shorter `problems.md` worked. A 10 would remove that path trap, refuse an empty first run instead of printing an overall OK, send refusals to verb help, and give receipt writes and spelling edits an exact mutation preview.

No service-backed or remote verb was run. `convergence` was not tried because it reads a remote forge; dogfood receipt writes were judged from help and were not performed. Spelling without `--write` safely reports findings and replacements, but help offers no `--dry-run` that proves the exact edit plan of the mutation path; dogfood record likewise offers no documented dry run to execute.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | `nova-check quickstart --dir ./empty` | An empty directory prints LINKS OK, NOCODE OK, and QUICKSTART OK at exit 0; the warning says it may be the wrong directory but does not change the green verdict. | Make zero checked files a refusal or a failed first run unless an explicit allow-empty flag is set. | S |
| 2 | `nova-check links --dir ./records --file ./records/problems.md --json` | The natural repository-relative file spelling is joined to --dir again and becomes an unreadable records/records path; only --file problems.md works. | Accept a path already beneath --dir or make the help say that relative --file values are relative to --dir. | M |
| 3 | `nova-check help spelling` | A read-only scan safely reports findings and replacements, but the verb can edit every selected file with --write and offers no exact preview of the mutation path. | Add --dry-run that emits the exact planned edits from the write path and writes nothing. | M |
| 4 | `nova-check links --dir ./records --bogus` | The refusal identifies the unknown flag but sends the caller to the full root help rather than the concise links help. | Print `run: nova-check help links` for verb-level invocation errors. | S |
| 5 | `nova-check help` | The first door is more than one hundred lines and mixes local checks, branch review, receipt storage, remote readings, and editing before the examples. | Put the first run and grouped verb index first, and move detailed flag narratives into verb help. | M |

## Good, keep

Keep the compact typed success lines and the JSON facts and item arrays. Keep reporting every independent missing input in one run. Keep findings that name the exact file, line, target, and suggested replacement.

## Compared with earlier ratings
| earlier | now | evidence |
|---|---|---|
| 7, 7.5, and 8 | CHANGED | `nova-check quickstart --dir ./records` and `nova-check hygiene --repo ./repo --base HEAD^ --head HEAD --identity 'Agent <agent@example.invalid>' --paths note.md` both completed clearly, while path and empty-input traps keep the score at 8. |
| 8: green over zero files in spelling and links | STILL THERE | `nova-check quickstart --dir ./empty` reports LINKS OK files=0 and ends QUICKSTART OK at exit 0. |
| 8: two refusals name one problem | FIXED | `nova-check kernel` reports both missing --file and missing budget in the same exit-2 run. |
| 8: every remedy is the whole help | STILL THERE | `nova-check links --dir ./records --bogus` ends with `run: nova-check help` although verb help exists. |
| 10: link findings, spelling fixes, JSON and dry-run as documented | CHANGED | `nova-check links --dir ./records --file problems.md --json` and `nova-check spelling --file ./records/problems.md --json` return actionable read-only items, but `nova-check help spelling` exposes --write without an exact edit preview. |

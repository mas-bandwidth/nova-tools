# nova-cairn AI-usability review, 2026-10-15

Reviewer: gpt-6-luna
Build: 4540d7fd37ca
Score: 8.5/10 (47/55)

## Rubric
| # | criterion | score | evidence |
|---|---|---|---|
| 1 | cold | 4 | `nova-cairn --help` -> the workflow is runnable, but the usage includes `nova-cairn NOTE: --publish is a recorded word...`, which looks like another command. |
| 2 | refusals | 3 | `nova-cairn open` -> `OPEN REFUSED: --store is required; it wants the checkpoint store directory; refusing to guess; run: nova-cairn help`; combined bad value and unknown flag report only the unknown flag. |
| 3 | output | 4 | `nova-cairn open --store scratch/refusal-store --session x --publish manual --bogus 1 --json` -> `OPEN REFUSED: unknown flag --bogus; the flags of open are --dry-run, --json, --now, --publish, --session, --source, --store; run: nova-cairn open -h`, exit 2; a later `--json` does not select JSON refusal output. |
| 4 | dry-run | 5 | `nova-cairn append --store scratch/help-store --session first --entry e2 --text "would remain absent" --dry-run --json` -> `{"result":{"verb":"append","status":"ok","exit":0},"facts":{"session":"first","entry":"e2","source":"-","persisted":false,"published":false,"publish":"manual","duplicate":false,"stamp":"2026-10-15T16:15:41.655658052Z","dry_run":true}}` |
| 5 | idempotent | 4 | `nova-cairn append --store scratch/help-store --session first --entry stable --text "stable synthetic bytes"` -> `APPEND OK session=first entry=stable source=- persisted=true published=false publish=manual duplicate=true stamp=2026-10-15T16:15:41.844498463Z` |
| 6 | no prompts | 5 | `nova-cairn append --store scratch/help-store --session first --entry stdin --file - --now 2026-01-02T03:04:05Z` -> `APPEND OK session=first entry=stdin source=- persisted=true published=false publish=manual duplicate=false stamp=2026-01-02T03:04:05Z`; stdin contained `stdin synthetic words\n`. |
| 7 | exit codes | 5 | `nova-cairn append --store "$store" --session second --entry x2 --text 'policy conflict synthetic' --publish manual` -> `exit=1` |
| 8 | one word | 4 | `nova-cairn` -> `CAIRN REFUSED: no verb given; the verbs are open, append, index, receipt, version; run: nova-cairn help`; the help's pseudo-`NOTE` line still resembles a command. |
| 9 | push | 5 | `n/a: local file writes and reads do not wait on external state` |
| 10 | no leaks | 5 | `nova-cairn receipt --store "$store" --session first --entry stdin` -> `RECEIPT OK session=first entry=stdin stamp=2026-01-02T03:04:05Z bytes=22 source=- persisted=true published=false publish=manual` |
| 11 | true help | 3 | `nova-cairn receipt --store scratch/alternate-store --session second --entry json-default --json` -> `{"result":{"verb":"receipt","status":"ok","exit":0},"facts":{"session":"second","entry":"json-default","stamp":"2026-10-15T16:15:42.024935754Z","bytes":28,"source":"synthetic-source","persisted":true,"published":false,"publish":"never","text":"SYNTHETIC_ONLY_SECRET_MARKER"}}`; help describes `--text` as the inclusion option. |

## 10/10 blockers
| # | where | what an AI meets | fix | size |
|---|---|---|---|---|
| 1 | `nova-cairn open --store "$store" --session z --publish someday --unknown x` | `OPEN REFUSED: unknown flag --unknown; the flags of open are --dry-run, --json, --now, --publish, --session, --source, --store; run: nova-cairn open -h` omits the invalid policy. | Validate all supplied flags and values together and report every cause. | M |
| 2 | `nova-cairn receipt --store "$store" --session second --entry json-default --json` | JSON includes stored `text` without `--text`, while receipt help describes `--text` as the inclusion option. | State in receipt help that JSON includes text by default and how to omit it. | S |
| 3 | `nova-cairn --help` | Usage includes `nova-cairn NOTE: --publish is a recorded word...`, which appears to name a pseudo-verb. This remains from the earlier rating and lowers both cold usability and one-word clarity. | Move the note outside the command-shaped usage or render it as ordinary prose. | S |
| 4 | `nova-cairn open --store "$store" --session x --publish manual --bogus 1 --json` | The exact-source binary prints a plain refusal and exits 2 instead of rendering JSON, because the parser stops at the unknown flag before the later `--json`. This remains from the earlier rating. | Preserve the requested JSON mode while reporting parser refusals. | S |
| 5 | `nova-cairn receipt -h` | Its help repeats the exit-code rows for `open`, `append` and `index` as well as `receipt`. This remains from the earlier rating. | Show shared exit-code guidance once and only the selected verb's table. | S |

## Nice-to-haves
| # | where | what | fix | size |
|---|---|---|---|---|
| 1 | `open`, `append` | Stable session/entry IDs make identical retries no-ops, but writes have no separate `--op` token. | Consider a distinct operation token if callers need deduplication independent of record IDs. | M |

## Top 3 fixes
1. Make parser refusals honor a later `--json`, and report the unknown flag in the JSON result.
2. Report the invalid publish value alongside an unknown flag in one refusal.
3. Explain the JSON receipt text default in help; remove the pseudo-verb note and limit `-h` exit-code rows to the requested verb.

## Good, keep
Stable IDs make exact append retries return `duplicate=true` without changing store bytes.
Both write verbs offer `--dry-run`; the open reply says `dry_run=true`, the append reply says `persisted=false` and `dry_run=true`, and follow-up listings show no `dry` session or `e2` file.
The four-step help example takes a new reader from session open to a text receipt.

## What could not be tried
All five verbs ran locally against synthetic stores. The installed-tool cold probes were preliminary; exact-source probes and the `internal/ci` and `internal/docs` gates passed on the Linux bench. There is no service-dependent verb.

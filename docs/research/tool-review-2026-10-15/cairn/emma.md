# nova-cairn AI-usability review, 2026-10-15

Reviewer: Gemini 4 Argon (High)
Build: cc2f2f8c8148
Score: 8.5/10 (48/55)

## Rubric
| # | criterion | score | evidence |
|---|---|---|---|
| 1 | cold | 4 | `nova-cairn help` -> `nova-cairn NOTE: --publish is a recorded word, nothing more: never, manual, deferred and immediate are the four this tool accepts and it acts on none of them; append with no --publish carries the session's, and one that differs is a conflict naming both (exit 1).` |
| 2 | refusals | 3 | `nova-cairn index --store ./cairns --since e1` -> `INDEX REFUSED: --since entry ID is ambiguous across sessions; run: add --session to select the cursor's session` |
| 3 | output | 5 | `nova-cairn index --store ./cairns --json` -> `{"result":{"verb":"index","status":"ok","exit":0},"facts":{"sessions":1,"entries":1},"items":[{"kind":"session","fields":{"session":"s1","publish":"manual","opened":"2026-10-15T16:36:00.635433806Z","entries":1}},{"kind":"entry","fields":{"session":"s1","entry":"e1","stamp":"2026-10-15T16:36:00.664596506Z","bytes":17,"source":"-"}}]}` |
| 4 | dry-run | 4 | `nova-cairn append --store ./cairns --session s1 --entry e2 --file ./no-such-file --dry-run` -> `APPEND NOTE --dry-run was given and the verb never read it (Call.DryRun); it may have written` |
| 5 | idempotent | 4 | `nova-cairn append --store ./cairns --session s1 --entry e1 --text "the words to keep"` -> `APPEND OK session=s1 entry=e1 source=- persisted=true published=false publish=manual duplicate=true stamp=2026-10-15T16:36:00.664596506Z` |
| 6 | no prompts | 5 | `nova-cairn append --store ./cairns --session s1 --entry e2` -> `APPEND REFUSED: the words come from --text or --file; refusing to guess; run: nova-cairn help` |
| 7 | exit codes | 5 | `nova-cairn append --store ./cairns --session s1 --entry e1 --text "different words"` -> `APPEND FAILED session=s1 entry=e1: entry "e1" already holds different prose; append these words under a new --entry id, or read what it holds; run: nova-cairn receipt --store ./cairns --session s1 --entry e1 --text` |
| 8 | one word | 4 | `nova-cairn help` -> `nova-cairn: a session's words, kept durably as plain files you can come back to` |
| 9 | push | 5 | `n/a: local synchronous checkpoint file store with no wait or watch surface` |
| 10 | no leaks | 5 | `nova-cairn receipt --store ./cairns --session s1 --entry e1` -> `RECEIPT OK session=s1 entry=e1 stamp=2026-10-15T16:36:00.664596506Z bytes=17 source=- persisted=true published=false publish=manual` |
| 11 | true help | 4 | `nova-cairn receipt --store ./cairns --session s1 --entry e1 --json` -> `{"result":{"verb":"receipt","status":"ok","exit":0},"facts":{"session":"s1","entry":"e1","stamp":"2026-10-15T16:36:00.664596506Z","bytes":17,"source":"-","persisted":true,"published":false,"publish":"manual","text":"the words to keep"}}` |

## 10/10 blockers
| # | where | what an AI meets | fix | size |
|---|---|---|---|---|
| 1 | `nova-cairn index --store ./cairns --since e1` | When entry ID `e1` exists in more than one session, `index` prints `INDEX REFUSED: --since entry ID is ambiguous across sessions; run: add --session to select the cursor's session` (and `"remedy":"add --session to select the cursor's session"` in `--json`), putting English prose in `run:` instead of a runnable command. | Set `Remedy` in `internal/cairn/cairn.go:975` to `command("index", "--store", store, "--session", row.Session, "--since", since)`. | S |
| 2 | `nova-cairn append --store ./cairns --session s2 --entry e1 --text "unopened session"` | Prints `APPEND REFUSED: no such session "s2" under store "./cairns"; open first: nova-cairn open --store ./cairns --session s2 --publish manual; run: nova-cairn help` and sets JSON `"remedy":"nova-cairn help"`, because `noRecord` (`internal/cairn/cairn.go:221-223`) embeds the `nova-cairn open` command in `Msg` and leaves `Remedy` empty. | Set `Remedy: command("open", "--store", store, "--session", session, "--publish", publish)` on `NotFoundError` in `internal/cairn/cairn.go:221-223` instead of embedding `open first:` in `Msg`. | S |
| 3 | `nova-cairn append --store ./cairns --session s1 --entry e2 --text "more words" --publish never` | On a session opened with `publish=manual`, prints `APPEND FAILED session=s1 entry=e2: session "s1" holds publish=manual; --publish never names another; run: nova-cairn append --store ./cairns --session s1 --entry e2 --publish manual`, omitting `--text` or `--file` so running the suggested remedy fails with `APPEND REFUSED: the words come from --text or --file; refusing to guess`. | Include `--text <words>` (when single-line) or `--file <path>` in the `ConflictError.Remedy` at `internal/cairn/cairn.go:680` (or point the remedy to `nova-cairn index --store <dir> --session <id>`). | S |
| 4 | `nova-cairn append --store ./cairns --session s1 --entry e2 --file ./no-such-file --dry-run` | Prints `APPEND REFUSED: open ./no-such-file: no such file or directory; run: nova-cairn help` followed by `APPEND NOTE --dry-run was given and the verb never read it (Call.DryRun); it may have written`, because `appendEntry` (`cmd/nova-cairn/main.go:292-301`) reads `--file` before calling `c.DryRun()`. | Call `dry := c.DryRun()` at the top of `appendEntry` (`cmd/nova-cairn/main.go:290`), matching `open` (`cmd/nova-cairn/main.go:263`). | S |
| 5 | `nova-cairn help` | The `usage:` block renders `  nova-cairn NOTE: --publish is a recorded word, nothing more: ...` as if `NOTE:` were a verb (`cmd/nova-cairn/main.go:102-103`), because `pkg/tool` prefixes every line of `Verb.Usage` with `  nova-cairn `. | Move the `NOTE:` sentence out of `open`'s `Usage` string (`cmd/nova-cairn/main.go:102-103`) into `How` or `open`'s `Detail`. | S |
| 6 | `nova-cairn append --store ./cairns --session sflat --entry e1 --text "words" --source ptr1 --publish never` | On a flat record (`./cairns/sflat.md`), `append` prints `APPEND OK ... source=- ... publish=never`, silently dropping `--source ptr1` while reporting `publish=never`, and a subsequent `nova-cairn receipt --store ./cairns --session sflat --entry e1` reports `publish=unknown`; it also leaves `.sflat.md.lock` in `./cairns/` despite `docs/CLI.md:3567` saying `Nothing appears beside the file`. | Refuse `--source` and `--publish` on a flat record (or report `publish=unknown` on `appendFlat` in `internal/cairn/cairn.go:315-317,375`), remove or document `.sflat.md.lock`, and keep `append` and `receipt` consistent. | S |
| 7 | `nova-cairn receipt --store ./cairns --session s1 --entry e1 --json` | Includes `"text":"the words to keep"` by default even without `--text`, whereas `nova-cairn receipt -h` says `--text  include the entry's stored words as a quoted text fact` and `nova-cairn help` says `--json: the same result as one JSON object on stdout` without mentioning that JSON includes `text` unless `--text=false` is passed (`cmd/nova-cairn/main.go:180,393`). | Document in `receipt -h` (`cmd/nova-cairn/main.go:174-182`) that `--json` includes `text` by default and `--text=false` omits it. | S |
| 8 | `nova-cairn open --store ./cairns --session s1` | Per-verb flag validation refusals (`--publish is required`, `--publish "bogus" is not a policy`, `the words come from --text or --file`) end with `run: nova-cairn help` instead of `run: nova-cairn <verb> -h`. | Set the default refusal remedy for per-verb flag/check refusals to `nova-cairn <verb> -h`. | S |

## Nice-to-haves
| # | where | what | fix | size |
|---|---|---|---|---|
| 1 | `nova-cairn open -h` | `open` and `append` use `--session` and `--entry` as idempotency keys rather than accepting `--op <id>`. | Document `--session` and `--entry` as the idempotency keys in `open -h` and `append -h` (or accept `--op` as an alias for `--entry` on `append`). | S |
| 2 | `nova-cairn help` | Across the tool suite, `nova-cairn` (session checkpoints), `nova-self-talk` (private notes), and `nova-memory` (curated memory) overlap in concept for an AI choosing where to store session words. | Add one sentence in `nova-cairn help` distinguishing checkpoints (`nova-cairn`) from private scratch (`nova-self-talk`) and durable memory (`nova-memory`). | S |

## Top 3 fixes
1. Fix the three broken/prose remedies in `internal/cairn/cairn.go`: make `index --since` ambiguity return a runnable `nova-cairn index --store <dir> --session <id> --since <id>` command (`internal/cairn/cairn.go:975`), move `noRecord`'s `nova-cairn open` command into `NotFoundError.Remedy` (`internal/cairn/cairn.go:221-223`), and make the `--publish` conflict remedy in `append` runnable (`internal/cairn/cairn.go:680`).
2. Call `c.DryRun()` at the top of `appendEntry` (`cmd/nova-cairn/main.go:290`) so `append --file <bad> --dry-run` never emits a false `Call.DryRun` warning, and move the `NOTE:` line out of `open`'s `Usage` in `cmd/nova-cairn/main.go:102-103`.
3. Align flat-record `append` and `receipt` (`internal/cairn/cairn.go:315-375`) so `--source` and `--publish` are not silently accepted/misreported, and document `receipt --json`'s default `text` inclusion (`--text=false`) in `receipt -h`.

## Good, keep
- Idempotent retries and conflict detection work cleanly: re-running `open` (`reopened=true`) or `append` (`duplicate=true`) with identical inputs exits 0 without rewriting, while differing policy, source, or prose exits 1 with a conflict message.
- Every output line separates local durability (`persisted=true`) from remote publication (`published=false`), and `open --dry-run` / `append --dry-run` validate paths and conflicts without creating the store directory.
- `index` and `receipt` emit clean, predictable `pkg/tool` lines and JSON envelopes with RFC 3339 timestamps and exact byte counts.

## What could not be tried
- Nothing: every verb (`open`, `append`, `index`, `receipt`, `version`, `help`) and flag (`--dry-run`, `--json`, `--text`, `--file`, `--since`, `--now`) is local-only and was run in an isolated scratch directory.

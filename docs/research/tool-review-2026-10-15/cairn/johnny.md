# nova-cairn AI-usability review, 2026-10-15

Reviewer: qwen3.8-max
Build: cc2f2f8c8148
Score: 9.5/10 (52/55)

## Rubric
| # | criterion | score | evidence |
|---|---|---|---|
| 1 | cold | 5 | `nova-cairn open --store ./cairns --session s1 --publish manual` -> `OPEN OK session=s1 store=./cairns source=- publish=manual stamp=2026-10-15T15:16:36.128361303Z`; all four `example:` lines ran verbatim from `nova-cairn help` alone, with no other doc |
| 2 | refusals | 4 | `nova-cairn index --store ./missing` -> `INDEX REFUSED: cannot read store "./missing": stat ./missing: no such file or directory; run: nova-cairn help`; every refusal names its cause and a run: line, and missing flags report every problem at once, but a missing store carries Go's raw stat error and generic help where the open verb creates it |
| 3 | output | 5 | `nova-cairn index --store ./cairns --max 2 --json` -> `"more":[{"kind":"entry","shown":2,"total":10,"remedy":"--max <n> raises the ceiling, --max 0 lists all"}]`; lines and JSON are one value in the same order, and every verb, refusals included, takes --json |
| 4 | dry-run | 5 | `nova-cairn open --store ./dry1 --session d1 --publish manual --dry-run` -> `OPEN OK session=d1 store=./dry1 source=- publish=manual stamp=2026-10-15T15:16:57.377936643Z dry_run=true`; ./dry1 was not created, and a dry append under ./dry2 left no entries directory |
| 5 | idempotent | 5 | `nova-cairn append --store ./cairns --session s1 --entry e1 --text "the words to keep"` run twice -> `APPEND OK session=s1 entry=e1 source=- persisted=true published=false publish=manual duplicate=true stamp=2026-10-15T15:16:36.140230348Z`; the entry id serves as the op id, a retry keeps the recorded stamp, and a matching re-open prints reopened=true |
| 6 | no prompts | 5 | `nova-cairn append --store ./cairns --session s1 --entry e12 --file -` with stdin piped -> `APPEND OK session=s1 entry=e12 source=- persisted=true published=false publish=manual duplicate=false stamp=2026-10-15T15:17:32.790753721Z`; no verb of the whole probe waited on an interactive answer |
| 7 | exit codes | 5 | `nova-cairn append --store ./cairns --session s1 --entry e1 --text "OTHER words"` -> `APPEND FAILED session=s1 entry=e1: entry "e1" already holds different prose; append these words under a new --entry id, or read what it holds; run: nova-cairn receipt --store ./cairns --session s1 --entry e1 --text` exit 1; every code observed (0, 1, 2, per verb) matches the table the help states |
| 8 | one word | 5 | `nova-cairn frobnicate --store ./cairns` -> `CAIRN REFUSED: unknown verb "frobnicate"; the verbs are open, append, index, receipt, version; run: nova-cairn help`; one word per thing, no duplicate verbs or tools |
| 9 | push | 5 | n/a: a local plain-file store; every verb completes in one run, no state is waited on, so there is nothing to poll |
| 10 | no leaks | 5 | n/a: no credential ever passes through the tool; --store is a local path, and no line of the whole probe carried a secret-shaped string |
| 11 | true help | 3 | `nova-cairn append --store ./cairns --session s1 --entry e9 --text hi --now 2026-01-01T00:00:00+02:00` -> `APPEND OK session=s1 entry=e9 source=- persisted=true published=false publish=manual duplicate=false stamp=2025-12-31T22:00:00Z` while `append -h` says `--now <string>  RFC 3339 UTC stamp`; the usage block also carries a `nova-cairn NOTE:` line that is refused as an unknown verb, and every verb's -h prints the exit table of all four verbs |

## 10/10 blockers
| # | where | what an AI meets | fix | size |
|---|---|---|---|---|
| 1 | `nova-cairn open --store ./c --session s --publish manual --now 2026-01-01T00:00:00+02:00` | accepted and stored as `stamp=2025-12-31T22:00:00Z` while the flag help and the refusal both say "RFC 3339 UTC"; the help claim is not true when run | refuse a non-Z offset, or change the flag text and the refusal to "any RFC 3339 offset, stored as UTC" | S |
| 2 | `nova-cairn help` | the usage block carries `nova-cairn NOTE: --publish is a recorded word, nothing more: ...`, which reads as a verb; `nova-cairn NOTE` -> `CAIRN REFUSED: unknown verb "NOTE"` | move the note under the usage block without the `nova-cairn ` prefix, or onto `open -h` and `append -h` | S |
| 3 | `nova-cairn index --store ./missing` | `INDEX REFUSED: cannot read store "./missing": stat ./missing: no such file or directory; run: nova-cairn help`; Go's raw stat error, and the remedy misses the verb that creates the store | say `no store at ./missing` and name `nova-cairn open --store ./missing --session <id> --publish <policy>` as the remedy | S |
| 4 | `nova-cairn append --store ./cairns --session ghost --entry g1 --text hi` | the remedy guesses a policy the caller never chose (`open first: nova-cairn open ... --publish manual`), ends `run: nova-cairn help` anyway, and under --json the `remedy` field is `nova-cairn help` with the runnable open buried in `why` | name the four policies (never, manual, deferred, immediate) instead of manual, and set the open command as the remedy, in lines and in JSON | S |
| 5 | `nova-cairn receipt -h` | every verb's -h prints the exit table of all four verbs; a reader of `receipt -h` reads append's conflict rule, and `version -h` lists four verbs' codes and none of its own | quote the verb's own row plus the shared "0 done, 2 usage" line | S |
| 6 | `nova-cairn append --store ./flat --session day2 --entry n1 --text x` on a hand-written flat record | the heading grammar the tool reads (`## <rfc3339> — <entry>`) is nowhere in help; a hand-written `## e1` section is silently not an entry: index shows `entries=0` with no unread count, and `receipt --entry e1` says no such entry | name the heading form on `append -h`, and have index count headings it skipped as `unread=<n>` | S |
| 7 | `nova-cairn help` | "every line says published=false", but `INDEX SESSION` and `INDEX ENTRY` lines carry no published= field | say the append and receipt lines say published=false, or add published= to the index lines | S |
| 8 | `nova-cairn open -h` | the flag list prints Go types: `--store <string>`, `--session <string>`, `--now <string>` where the usage line says `<dir>`, `<id>`, `<rfc3339-utc>` | use the usage line's placeholders in each flag's text | S |
| 9 | `nova-cairn append --store ./cairns --session s1 --entry e9` | `APPEND REFUSED: the words come from --text or --file; refusing to guess; run: nova-cairn help`, while an unknown flag gets the better `run: nova-cairn open -h`; the same generic-help remedy ends the bad-value and missing-file refusals | end every per-verb refusal with `run: nova-cairn <verb> -h` | S |
| 10 | `nova-cairn open --store ./words.txt --session s1 --publish manual` where words.txt is a plain file | `OPEN REFUSED: cannot read the session's open record from words.txt/log.jsonl: not a directory; run: nova-cairn help`; the store's own shape is not the named cause | check the store first: `--store "./words.txt" is not a directory` | S |
| 11 | `nova-cairn append --store ./flat --session day1 --entry n1 --text x` | a hidden `.day1.md.lock` is left beside the flat record, while the banner says "plain files only" and no help names the sidecar | lock the record file itself, or name the lock file in help | S |
| 12 | `nova-cairn open --store ./cairns --session s1 --publish never` on an open session | `OPEN FAILED: session "s1" is already open with publish=manual source=-; ...` carries no typed fields, while `APPEND FAILED session=s1 entry=e1: ...` does; two FAILED grammars in one tool | one shape: `<VERB> FAILED session=<s> ...: <why>; run: <remedy>` for both | S |

## Nice-to-haves
| # | where | what | fix | size |
|---|---|---|---|---|
| 1 | `nova-cairn index --store ./cairns --max 2` | the MORE line's prose tail starts with flag-shaped tokens (`INDEX MORE kind=entry shown=2 total=7 --max <n> raises the ceiling, ...`), which a naive k=v splitter trips on | keep the fields, then put the remedy after a semicolon or on a NOTE line | S |
| 2 | `nova-cairn version --json` | the version line rides in a `payload` field where every other verb uses facts and items only | carry it as a facts field for one stable JSON shape | S |
| 3 | `nova-cairn append ... --entry e1 --text "the words to keep" --now 2026-01-01T00:00:00Z` on a duplicate | the given --now is silently dropped and the recorded stamp returned (correct, but unstated) | add an `asked=` fact so the retry sees its stamp was ignored | S |

## Top 3 fixes
1. Make `--now` match its help: refuse a non-Z offset, or reword the flag and refusal to "any RFC 3339 offset, stored as UTC" — the largest single true-help item.
2. Move the `nova-cairn NOTE:` line out of the usage block — one edit removes a pseudo-verb from the first thing a cold reader reads.
3. Make store refusals actionable: a missing store names the `nova-cairn open` that creates it (no raw Go stat error), and the no-such-session remedy names the four policies instead of guessing manual and is the JSON `remedy` field, not prose in `why`.

## Good, keep
- The `example:` block runs verbatim cold; every claim about the verbs, the exit codes, duplicate and conflict, --max with its MORE line, and --json held when run, and --dry-run on both write verbs writes nothing.
- Idempotency is real and tested: a duplicate append or a matching re-open returns the recorded stamp (duplicate=true, reopened=true), so a retry after a timeout is safe.
- Fixed since the 2026-10-02 ratings and must stay: a re-open prints reopened=true with the recorded stamp (was a fresh stamp, no marker), an empty session lists as `INDEX SESSION session=empty1 ... entries=0`, and an unknown-flag refusal honours --json. The old defects still there are blocker rows above: the NOTE line, the all-verb exit table in every -h, --now non-UTC, the raw Go stat refusal, the guessed --publish manual remedy, the .lock sidecar, and the <string> placeholders.

## What could not be tried
None. Every verb (open, append, index, receipt, version, help) ran end to end against stores under the probe's scratch directory; no verb needs a service, a network or a live store beyond a local directory.

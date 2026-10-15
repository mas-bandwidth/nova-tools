# nova-cairn AI-usability review, 2026-10-15

Reviewer: Mercury
Build: cc2f2f8c8148
Score: 9.5/10 (52/55)

## Rubric
| # | criterion | score | evidence |
|---|---|---|---|
| 1 | cold | 5 | `nova-cairn help` -> `example:` block's four lines ran verbatim; open/append/index/receipt all OK |
| 2 | refusals | 4 | `nova-cairn open --store ./afile --session s --publish never` -> `OPEN REFUSED: cannot read the session's open record from afile/log.jsonl: not a directory; run: nova-cairn help` |
| 3 | output | 4 | `nova-cairn open --store ./cairns --session s1 --publish manual --bogus 1 --json` -> `OPEN REFUSED: unknown flag --bogus; ...` (prose, not the promised JSON) |
| 4 | dry-run | 5 | `nova-cairn append --store ./d2 --session s1 --entry e1 --text hello --dry-run` -> `APPEND OK ... dry_run=true`; `receipt` still refuses and no `entries/` appeared |
| 5 | idempotent | 5 | `nova-cairn append --store ./cairns --session s1 --entry e1 --text "the words to keep"` (2nd) -> `APPEND OK ... duplicate=true`, nothing written |
| 6 | no prompts | 5 | every verb ran to completion with no interactive prompt |
| 7 | exit codes | 5 | `nova-cairn append ...` conflict -> exit 1; `nova-cairn open` (missing flags) -> exit 2; matches the help table |
| 8 | one word | 5 | verbs open, append, index, receipt, version, help: one word each, no duplicates |
| 9 | push | 5 | n/a: no remote state is waited on; all state is local files |
| 10 | no leaks | 5 | no error or output line carries a secret; only entry words and stamps print |
| 11 | true help | 4 | `nova-cairn open --store ./n1 --session s1 --publish manual --now 2026-01-01T00:00:00+02:00` -> `OPEN OK ... stamp=2025-12-31T22:00:00Z` despite help saying "RFC 3339 UTC stamp" |

## 10/10 blockers
| # | where | what an AI meets | fix | size |
|---|---|---|---|---|
| 1 | `nova-cairn open --store ./cairns --session s1 --publish manual --bogus 1 --json` | the unknown flag stops parsing so `--json` is never seen: `OPEN REFUSED: unknown flag --bogus; ...` prints as prose on stderr, though help claims "Every verb takes --json: the same result as one JSON object on stdout" | scan the whole argument list for `--json` before parsing and render every refusal in the mode asked for | S |
| 2 | `nova-cairn help` | the usage block carries `nova-cairn NOTE: --publish is a recorded word, nothing more: ...`, which reads as a verb; `nova-cairn NOTE` returns `CAIRN REFUSED: unknown verb "NOTE"; ...` | move the note under the usage lines without the `nova-cairn ` prefix, or into `open -h` | S |
| 3 | `nova-cairn bogus` | the unknown-verb refusal lists open, append, index, receipt, version and omits `help`, which the usage block lists as a verb | include `help` in the verb list | S |
| 4 | `printf x > afile; nova-cairn open --store ./afile --session s --publish never` | the refusal names an internal path, not the wrong input: `OPEN REFUSED: cannot read the session's open record from afile/log.jsonl: not a directory; run: nova-cairn help` | check the store first and refuse `--store "./afile" is not a directory` | S |
| 5 | `nova-cairn open --store ./n1 --session s1 --publish manual --now 2026-01-01T00:00:00+02:00` | a non-UTC offset is accepted and silently converted to `stamp=2025-12-31T22:00:00Z`, though `--now` help says "RFC 3339 UTC stamp" | refuse a non-`Z` offset, or say "any RFC 3339 offset, converted to UTC" in the flag text | S |
| 6 | `nova-cairn open -h` | every verb's `-h` prints the exit table of all four verbs; `version -h` lists append's conflict code and none of version's own | print the shared `0 done, 2 usage` line plus the verb's own row only | S |
| 7 | `nova-cairn open -h` | flag placeholders print `--store <string>`, `--session <string>`, `--now <string>` where the usage line directly above says `<dir>`, `<id>`, `<rfc3339-utc>` | use the usage line's placeholders in the flag list | S |

## Nice-to-haves
| # | where | what | fix | size |
|---|---|---|---|---|
| 1 | `nova-cairn index -h` | `--max <int>` prints no default, while the banner says default 20 | print `(default 20)` on `--max` | S |
| 2 | `nova-cairn open` / `nova-cairn append` (missing flags) | required-input refusals end `run: nova-cairn help`, while the unknown-flag refusal ends `run: nova-cairn open -h`; the verb's `-h` is the tighter remedy | end every per-verb refusal with `run: nova-cairn <verb> -h` | S |

## Top 3 fixes
1. Honor `--json` for every refusal (scan args before parsing) so the promised one-JSON-object output is always true; this moves criteria 3 and 11.
2. Remove the `nova-cairn NOTE:` fake-verb line from the usage block to clear the cold first-run confusion.
3. Fix `--now` so help and behaviour agree (refuse a non-UTC offset, or say it is converted).

## Good, keep
The four-command example block in `nova-cairn help` runs verbatim and is a genuine cold first run; the open/append/index/receipt sequence worked first try.
Refusals consistently name the cause and a runnable next command, and the unknown-flag refusal points to `-h`; the store/session/entry/policy refusals are one line with a paste-able remedy.
Retry safety by design: the same id and words return `duplicate=true` and write nothing, `--dry-run` exists on both writes and writes nothing, receipts read back everything a write reported, and the typed `key=value` line grammar has one matching JSON object per verb.

## What could not be tried
Nothing: every verb (open, append, index, receipt, version, help) ran end to end against a job-local plain-file store; no verb needs a service, a network or a key.

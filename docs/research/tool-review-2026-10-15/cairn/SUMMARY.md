# nova-cairn AI-usability review summary, 2026-10-15

Reviews: 5 (R1..R5, in byte order of file name)
Build: cc2f2f8c8148, 4540d7fd37ca
Score: 9.5/10 (range 8.5-9.5)

## Rubric
| # | criterion | median | range | the evidence that decided it |
|---|---|---|---|---|
| 1 | cold | 5 | 4-5 | the four-line `example:` block in `nova-cairn help` runs verbatim cold; the `nova-cairn NOTE:` line in usage reads as a command (R2, R4) |
| 2 | refusals | 4 | 3-5 | every refusal names a cause and a next command, but several remedies are prose or guess a policy (`index --since` ambiguous, no-such-session guesses `--publish manual`) instead of a runnable command |
| 3 | output | 5 | 4-5 | lines and JSON are one value and every verb takes `--json`; a parser refusal (unknown flag) still prints prose, not JSON |
| 4 | dry-run | 5 | 4-5 | `--dry-run` on open/append writes nothing (`dry_run=true` / `persisted=false`); append reads `--file` before checking dry-run and emits a false warning |
| 5 | idempotent | 4 | 2-5 | a duplicate append returns `duplicate=true` and a matching re-open prints `reopened=true`; there is no `--op` id flag (R5 scores 2) |
| 6 | no prompts | 5 | 5-5 | every verb completes on argv alone with stdin piped; nothing waits on a TTY |
| 7 | exit codes | 5 | 5-5 | observed 0/1/2 per verb match the stated table |
| 8 | one word | 5 | 4-5 | verbs are one word each; the `nova-cairn NOTE:` line in usage reads as a pseudo-verb |
| 9 | push | 5 | 5-5 | n/a: local plain-file store, no state is waited on |
| 10 | no leaks | 5 | 5-5 | no output or error line carries a secret; only entry words and stamps print |
| 11 | true help | 4 | 3-5 | the example block runs as printed, but `--now` accepts a non-UTC offset despite "RFC 3339 UTC" help, and every `-h` prints the all-verb exit table |

## Consensus 10/10 blockers
| # | where | what an AI meets | fix | size | named by |
|---|---|---|---|---|---|
| 1 | `nova-cairn help` | the usage block carries `nova-cairn NOTE: --publish is a recorded word ...`, which reads as a verb; `nova-cairn NOTE` refuses as an unknown verb | move the note out of the usage block (or onto `open -h`/`append -h`) without the `nova-cairn ` prefix | S | R1, R2, R3, R4, R5 |
| 2 | `nova-cairn open --store ./cairns --session s1 --publish manual --bogus 1 --json` | the unknown flag stops parsing so the later `--json` is never seen, and the refusal prints as prose though help claims every verb takes `--json` | scan argv for `--json` before parsing and render every refusal in the requested mode | S | R1, R4, R5 |
| 3 | `nova-cairn <verb> -h` | every verb's `-h` prints the exit table of all four verbs (e.g. `version -h` lists append's conflict code and none of its own) | quote only the shared "0 done, 2 usage" line plus the verb's own row | S | R1, R3, R4 |
| 4 | `nova-cairn open --store ./n1 --session s1 --publish manual --now 2026-01-01T00:00:00+02:00` | a non-UTC offset is accepted and silently stored as `stamp=2025-12-31T22:00:00Z`, though `--now` help says "RFC 3339 UTC stamp" | refuse a non-Z offset, or reword the help and refusal to "any RFC 3339 offset, stored as UTC" | S | R1, R3 |
| 5 | `nova-cairn open` / `nova-cairn append` (missing/bad flags) | per-verb validation refusals end `run: nova-cairn help`, while the unknown-flag refusal already gives the tighter `run: nova-cairn <verb> -h` | end every per-verb refusal with `run: nova-cairn <verb> -h` | S | R2, R3 |
| 6 | `nova-cairn append --store ./cairns --session ghost --entry g1 --text hi` | the refusal guesses a policy the caller never chose (`open first: nova-cairn open ... --publish manual`), and under `--json` the remedy is the generic `nova-cairn help` with the runnable open buried in `why` | set the open command as the remedy in both lines and JSON, naming the four policies | S | R2, R3 |
| 7 | `printf x > afile; nova-cairn open --store ./afile --session s --publish never` | the refusal names an internal path, not the wrong input: `cannot read the session's open record from afile/log.jsonl: not a directory` | check the store first and refuse `--store "./afile" is not a directory` | S | R1, R3 |
| 8 | `nova-cairn open -h` | flag placeholders print Go types (`--store <string>`, `--now <string>`) where the usage line above says `<dir>`, `<rfc3339-utc>` | use the usage line's placeholders in the flag list | S | R1, R3 |
| 9 | `nova-cairn append --store ./flat --session day1 --entry n1 --text x --source ptr1 --publish never` on a flat record | `--source`/`--publish` are silently dropped (publish reported `never` then `unknown` on receipt) and a `.day1.md.lock` sidecar is left despite "plain files only" | refuse or report these on flat records, keep append/receipt consistent, and remove or document the lock sidecar | S | R2, R3 |
| 10 | `nova-cairn receipt --store ./cairns --session s1 --entry e1 --json` | JSON includes the stored `text` by default without `--text`, while `receipt -h` describes `--text` as the inclusion option | state in receipt help that JSON includes text by default and how to omit it (`--text=false`) | S | R2, R4 |
| 11 | `nova-cairn open --store ./cairns --session z --publish someday --unknown x` | the unknown-flag refusal omits the also-invalid policy; one run reports only the unknown flag | validate all supplied flags and values together and report every cause | M | R4 |
| 12 | `nova-cairn append --store ./cairns --session s1 --entry e2 --text "more words" --publish never` on a `publish=manual` session | the conflict remedy omits `--text`/`--file`, so running it fails with "the words come from --text or --file; refusing to guess" | include `--text`/`--file` in the remedy, or point it to `index --session` | S | R2 |
| 13 | `nova-cairn index --store ./cairns --since e1` with `e1` in two sessions | the refusal puts English prose in `run:` (`add --session to select the cursor's session`) instead of a runnable command | set the remedy to `nova-cairn index --store <dir> --session <id> --since <id>` | S | R2 |
| 14 | `nova-cairn append --store ./cairns --session s1 --entry e2 --file ./no-such-file --dry-run` | append reads `--file` before checking dry-run and prints a false `APPEND NOTE --dry-run was given and the verb never read it` | call `c.DryRun()` at the top of appendEntry | S | R2 |
| 15 | `nova-cairn index --store ./missing` | a missing store leaks Go's raw stat error and the remedy misses the verb that creates it | say `no store at ./missing` and name `nova-cairn open --store ./missing --session <id> --publish <policy>` | S | R3 |
| 16 | `nova-cairn bogus` | the unknown-verb refusal lists open, append, index, receipt, version and omits `help`, which usage lists as a verb | include `help` in the verb list | S | R1 |
| 17 | `nova-cairn append --store ./flat2 --session day2 --entry n1 --text x` on a hand-written flat record | the heading grammar the tool reads (`## <rfc3339> — <entry>`) is nowhere in help; a hand-written `## e1` section is silently not an entry (index shows entries=0 with no unread count) | name the heading form on `append -h` and have index count skipped headings as `unread=<n>` | S | R3 |
| 18 | `nova-cairn help` | help claims every line says published=false, but `INDEX SESSION`/`INDEX ENTRY` lines carry no published= field | say the append and receipt lines say published=false, or add published= to the index lines | S | R3 |
| 19 | `nova-cairn open --store ./cairns --session s1 --publish never` on an open session | `OPEN FAILED: ...` carries no typed fields while `APPEND FAILED session=s1 ...` does; two FAILED grammars in one tool | one shape `<VERB> FAILED session=<s> ...: <why>; run: <remedy>` for both | S | R3 |

## Top 3 fixes
1. Honor `--json` in every refusal by scanning argv before parsing, so the promised one-JSON-object output is always delivered (fixed the output and true-help criteria; R1, R4, R5).
2. Remove the `nova-cairn NOTE:` pseudo-verb line from the usage block, clearing the coldest first impression (R1-R5).
3. Make refusals runnable: set the `nova-cairn open` command as the remedy for a missing or unopened session and for `index --since` ambiguity, and end every per-verb flag refusal with `run: nova-cairn <verb> -h`.

## Disagreements
- Idempotent (criterion 5): R5 scored 2, R1/R3 scored 5. R5's evidence is `append --op myop` being refused; rerunning confirms no `--op` flag exists, yet the duplicate-append and matching re-open idempotency (`duplicate=true`, `reopened=true`) works via the `--session`/`--entry` keys. Both observations hold; the gap is a missing `--op` id flag, a new feature moved to Nice-to-haves.
- True help (criterion 11): R5 scored 5, R3/R4 scored 3. Rerunning shows `--now` accepts and converts a non-UTC offset against "RFC 3339 UTC" help, the `nova-cairn NOTE:` pseudo-verb remains, and every `-h` prints the all-verb exit table; R3/R4's lower score is the accurate read.
- Refusals (criterion 2): R5 scored 5, R2/R4 scored 3. Rerunning shows the `index --since` ambiguity and no-such-session refusals give prose/guessed-policy remedies, not runnable commands; R2/R4's read holds.
- One word (criterion 8): R1/R3/R5 scored 5, R2/R4 scored 4, citing the `nova-cairn NOTE:` pseudo-verb line; it reproduces.

## Nice-to-haves
- `version --json` carries the version in a `payload` field; carry it as a facts field for one stable JSON shape (R3).
- `index`'s `MORE` remedy names only `--max`, not the full runnable re-run; print the quoted re-run with store and session (R3, R5).
- `index --max 2`'s MORE-line prose tail begins with flag-shaped tokens that trip a naive k=v splitter (R3).
- A duplicate append silently drops a given `--now`; add an `asked=` fact so the retry sees its stamp was ignored (R3).
- Document `--session`/`--entry` as the idempotency keys in `open -h`/`append -h` (R2).
- Add a sentence in help distinguishing checkpoints (cairn) from private notes and durable memory (R2).
- Add a `--op <id>` idempotency flag to open/append per the family standard (new feature; R4, R5).

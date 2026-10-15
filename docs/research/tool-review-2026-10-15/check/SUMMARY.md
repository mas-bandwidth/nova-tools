# nova-check AI-usability review summary, 2026-10-15

Reviews: 5 (R1..R5, in byte order of file name)
Build: cc2f2f8c8148, b4f484c84916, f6c35d67aaa1, 453ce3a00131
Score: 9.0/10 (range 7.5-9.5)

## Rubric
| # | criterion | median | range | the evidence that decided it |
|---|---|---|---|---|
| 1 | cold | 5 | 4-5 | every review ran the help `example:` block cold and it ran as printed; the two 4s cite dense or misleading help (the `--json` paragraph, spliced refusal prose) |
| 2 | refusals | 4 | 4-5 | refusals name the cause, what the input wants, and the next command, and one run reports every problem at once; the 4s are because missing-input remedies point to global `nova-check help`, not the verb's own `-h` |
| 3 | output | 4 | 3-5 | one stable `VERB OK/FAILED/REFUSED k=v` grammar with a matching `--json`, but spelling findings use a second `file:line:col: word -> word` grammar, and the dogfood verbs refuse `--json` |
| 4 | dry-run | 5 | 2-5 | every `--dry-run` writes nothing and prints the plan; spelling `--write --dry-run` still prints `SPELLING OK` and exits 0 when it found misspellings it did not write (the 2) |
| 5 | idempotent | 1 | 0-3 | no write verb takes `--op`; a retried dogfood receipt appends a second file (or overwrites the same-second file), so the criterion cannot grade above this |
| 6 | no prompts | 5 | 5-5 | every probe answered in one non-interactive run; nothing waits on stdin |
| 7 | exit codes | 5 | 5-5 | 0/1/2 match the help's table on pass/fail/refusal (verified: ok=0, failed=1, refusal=2) |
| 8 | one word | 5 | 4-5 | help lists each verb once with no duplicates; the 4s flag spelling naming its target three ways (`--dir`/`--file`/`--path`) and the two-word dogfood group |
| 9 | push | 5 | 5-5 | n/a: every verb reads local files or the git index in place; nothing is waited on or polled |
| 10 | no leaks | 5 | 5-5 | outputs carry only paths, counts, hashes and status; no secret appears in any line |
| 11 | true help | 4 | 3-5 | help's example block runs exactly as printed, but the `--json` paragraph's first clause is false and `dogfood ledger` ignores its stated `--max` bound |

## Consensus 10/10 blockers
| # | where | what an AI meets | fix | size | named by |
|---|---|---|---|---|
| 1 | `nova-check dogfood record --cli <cli> --tool nova-check --verb version --by tester --ok --notes probe --receipts ./receipts` | no write verb takes `--op`; the same record run twice appends a second receipt (a run across seconds writes a new file, a run inside the same second silently clobbers the same file), so a retry after a timeout doubles or loses the record | add `--op <id>` to the write verbs (dogfood record, spelling --write, convergence --state) that records the op id and replays the recorded result on a repeat, writing nothing and never clobbering | S/M | R1, R2, R3, R5 |
| 2 | `nova-check kernel` and `nova-check links` | missing and invalid required-flag refusals end `run: nova-check help` (the global help) instead of the verb's own `-h`, and some splice the `it wants` description mid-sentence (`... it wants state the unit: --max-bytes ...`) | point required-flag refusals at `run: nova-check <verb> -h` and fix the `it wants` splice | S | R2, R4, R5 |
| 3 | `nova-check links --dir ./notes > out.txt 2> err.txt` | a failed run puts every line, the count line too, on stderr: out.txt is 0 bytes, so a caller reading stdout sees nothing at all; help does not state the stream split | print result lines, findings and totals on stdout (refusals may stay on stderr), or state the stream split in help | S | R3, R5 |
| 4 | `nova-check links --dir ./empty` | green over zero files: `LINKS OK dir=./empty files=0 ...` at exit 0, so an AI reads a pass where nothing was classified (nocode already prints a NOTE for the same tree) | print a NOTE, or exit 1, when a check classifies zero files | S | R1, R5 |
| 5 | `nova-check links --dir ./broken --exclude ./broken/sub` | `--exclude` (and `links --file`) are resolved relative to `--dir`, so the path a caller passes as given excludes nothing: `excluded=0` and the file is reported broken; help does not state the path base | accept a path that starts with `--dir` as that subtree/file, or refuse a prefix that matches nothing, and state the base in help | S | R1, R4 |
| 6 | `nova-check spelling --file ./notes.md --write --dry-run` | a dry run that finds misspellings still prints `SPELLING OK ... misspellings=1 written=0 dry_run=true` (status ok) at exit 0, hiding that nothing was fixed | return the finding status and exit of the write path on a dry run that finds misspellings, while keeping `written=0` | S | R1, R4 |
| 7 | `nova-check convergence --repo example/project --ledger ./notes.md --receipts ./receipts --retired ./notes.md --since 24h --dry-run` | `--dry-run` refuses at the gh service call before printing any plan (`CONVERGENCE REFUSED: gh pr list --state open ...`), so an offline reader cannot plan | with `--dry-run`, read local sources only and print the plan with the LANDING and PRS forge streams marked absent | M | R1, R3 |
| 8 | `nova-check dogfood ledger --cli <cli> --receipts ./receipts --max 2` | help states `dogfood ledger` caps listings at `--max` with a MORE line, but `--max 2` is ignored and all rows are printed unbounded (173 `DOGFOOD tool=...` rows) without a MORE total | apply `--max` truncation and emit `DOGFOOD MORE shown=<n> total=<total>` | S | R2, R4 |
| 9 | `nova-check help convergence` | `convergence -h` quotes a truncated mid-sentence banner line styled as a command (`... or nova-check dogfood record the`), and `version -h` and `dogfood -h` append a `setup:` fixture block neither uses | keep `setup:` in the top-level banner only and drop or NOTE continuation prose lines from the help excerpt | S | R2, R3 |
| 10 | `nova-check help` | the `--json` paragraph's first clause is false: `Every verb but dogfood ledger, dogfood record, convergence, spelling takes --json` while spelling and convergence take it; a reader of the first clause alone skips `--json` on verbs that have it | one true sentence: every verb takes `--json` except dogfood ledger and dogfood record | S | R3 |
| 11 | `nova-check dogfood ledger --json` and `nova-check dogfood record --json` | both refuse `--json` (`DOGFOOD-LEDGER REFUSED: unknown flag --json`) while the help omits `[--json]` from several verb usage lines | support `--json` on dogfood ledger and record and list `[--json]` consistently on every verb's usage line | M | R2 |
| 12 | `nova-check spelling --file ./notes.md` | the finding line is a second grammar: `SPELLING FIX notes/notes.md:3:0: Teh -> The`, while links prints `LINKS BROKEN file=... line=...`; one parser cannot read both without a special case | print spelling findings as k=v fields (file= line= column= original= replacement=) like every other verb's items | S | R3 |
| 13 | `nova-check dogfood record` refusal vs success | the subverb prefix changes spelling with the status: `DOGFOOD-RECORD REFUSED` is hyphenated while success lines print `DOGFOOD RECORD OK` unhyphenated; a grammar keyed on the prefix splits on one of the two shapes | print the subverb prefix one way in both printers | S | R3 |
| 14 | `nova-check spelling -h` | the target is named three ways — `--dir`, `--file`, `--path` — where links settles for two, and `--path notes.md` repeats `--file notes.md`; a caller must choose between overlapping words for one thing | keep `--dir` and `--file`; fold `--path`'s globs into `--file` | S | R3 |
| 15 | `nova-check dogfood ledger --cli <cli> --receipts ./receipts` | scraping fenced code blocks treats negative examples and table names as real verbs: `DOGFOOD tool=nova-sandbox verb=bogus`, `... verb=-`, `... verb=create demo` | restrict `--cli` verb extraction to the usage block or `### <verb>` headings rather than arbitrary example invocations | S | R2 |

## Top 3 fixes
1. Add `--op` idempotency to the write verbs (dogfood record, spelling --write, convergence --state) so a retry replays the recorded result and writes nothing; this closes the largest scoring gap (criterion 5, median 1/5).
2. Kill the false-green traps: make `spelling --write --dry-run` return the finding status when it finds misspellings it did not write, and route result and finding lines to stdout so a failing run is never silent to a stdout reader.
3. Make help and refusals tell the truth: honor `--max` in `dogfood ledger` with a MORE total, point required-flag refusals at the verb's own `-h`, fix the `--json` paragraph and the truncated `convergence` excerpt, and state the `--exclude` path base.

## Disagreements
- Criterion 4 (dry-run): R4 scored 2, others 4-5. The divergence is real: rerunning `nova-check spelling --file notes/notes.md --write --dry-run` prints `SPELLING OK` and exits 0 while reporting `misspellings=1` and `written=0`; R4 counts that as a failed dry-run, the others grade it green. The finding is kept as blocker 6.
- Criterion 5 (idempotent): R4 scored 0, R1/R3 scored 1, R2/R5 scored 3. All agree no writer takes `--op`; the gap is severity, not reproduction. Rerunning `dogfood record` twice in one second produced the same filename (clobber), across seconds a second file (append); neither is a safe retry.
- Criterion 11 (true help): R1/R5 scored 5, R2/R4 scored 3, R3 scored 4. R1/R5 exercised the `quickstart`/help blocks and saw no false claim; R2/R4 exercised `dogfood ledger --max` (the stated bound is ignored) and R3 read the false `--json` clause. Rerunning both shows the lower scores reflect real untrue claims.

## Nice-to-haves
- (R1) The dogfood subcommand group is spelled as two words (`dogfood ledger`/`record`/`gate`), a deviation from the one-word-per-thing shape; keep the group but state in help it is one namespace of three verbs, or flatten it.
- (R1) Receipt filenames carry a second-resolution timestamp, so same-second runs collide; include an op id or a counter in the filename so a repeat can never overwrite.
- (R3) The unknown-verb refusal lists every verb but suggests no nearest match; add a did-you-mean.
- (R3) `nova-check help <unknown>` refuses at exit 2 rather than printing the help door at exit 0.
- (R3) `spelling --fail-max` is a second word for `--max`, kept "for one release"; remove it once that release passes.
- (R3) The hygiene identity finding quotes the full author email from git history; print it hashed or as initials.
- (R5) `dogfood ledger` prints `tool=<tool> verb=-` rows for tools without a declared verb list; skip empty verb rows or explain them.

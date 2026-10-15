# nova-memory AI-usability review, 2026-10-15

Reviewer: mercury-3-preview-1002
Build: d7e47f037ea8
Score: 9.5/10 (53/55)

## Rubric
| # | criterion | score | evidence |
|---|---|---|---|
| 1 | cold | 5 | `nova-memory quickstart --root <corpus>` -> `QUICKSTART OK done=3` |
| 2 | refusals | 5 | `nova-memory search --root <corpus> glazing` -> `SEARCH REFUSED: --channels is required; refusing to guess; run: nova-memory help` |
| 3 | output | 4 | `nova-memory check --root <corpus> --channels bm25 --k 2 draft.md` -> lines lead `MEMORY OK ...`, `MEMORY HIT ...` not the verb's own name CHECK, deviating from the documented family grammar (verb name first) |
| 4 | dry-run | 5 | n/a: every verb is an inspection (`<verb> -h` says "reads, writes nothing"); there is no write to dry-run |
| 5 | idempotent | 5 | n/a: no write verb exists, so no --op; two identical `stats` runs returned identical receipts |
| 6 | no prompts | 5 | `nova-memory check --root <corpus> --channels bm25 --k 2 - < draft.md` -> `MEMORY OK candidates=1 source=- k=2 ...` (stdin pipe, no prompt) |
| 7 | exit codes | 5 | `nova-memory verify --root <corpus> --links gate --coverage ...` -> `VERIFY FAIL gating=1 ...` exit=1; refusals exit=2; clean runs exit=0 |
| 8 | one word | 5 | `nova-memory help` -> verbs are quickstart, stats, search, check, verify, eval, boot, version, all single distinct words |
| 9 | push | 5 | n/a: the tool waits on no external state; nothing is polled |
| 10 | no leaks | 5 | full search/check/verify transcripts show only corpus paths, no secret-like values: `SEARCH HIT ... root=<corpus>` |
| 11 | true help | 4 | `nova-memory stats --root <corpus> --exclude log/` -> still prints `STATS OK class=log chunks=4`: help says --exclude takes "a path or glob to skip", but a trailing-slash directory path silently skips nothing |

## 10/10 blockers
| # | where | what an AI meets | fix | size |
|---|---|---|---|---|
| 1 | `nova-memory stats --root <corpus> --exclude log/` | the exclusion silently does nothing: `STATS OK class=log chunks=4` still prints, with no line saying the exclusion matched zero files, so a reader believes log is excluded | accept a bare directory path in --exclude, and print a NOTE when an --exclude matches no file | S |
| 2 | `nova-memory check --root <corpus> --channels bm25 --k 2 draft.md` | every line leads with MEMORY (MEMORY OK/HIT/CAND) not the verb CHECK, deviating from the documented family grammar "after the verb's name the first word is OK/REFUSED/FAILED"; an AI keying output on the verb token finds nothing | let CHECK lead check's lines; keep MEMORY only for the tool-level bare refusal | S |

## Nice-to-haves
| # | where | what | fix | size |
|---|---|---|---|---|
| 1 | `nova-memory eval` | a MISS receipt prints the query with \x20 escapes: `EVAL MISS query=how\x20often\x20should\x20the\x20lantern\x20glazing\x20be\x20washed` | print the query, the spaces unescaped | S |
| 2 | `nova-memory boot` | BOOT OK names only counts (`BOOT OK files=1 bytes=1117`) and --json carries no loaded paths, so a reader cannot see which memories were checked without rereading the pin | print one BOOT LOAD line per file and the same items in --json | S |
| 3 | docs/CLI.md | the eval gold-file format lives only in `eval -h`, not in the tool's CLI.md section | mirror the `gold file format` line into CLI.md | S |

## Top 3 fixes
1. Make --exclude honor a trailing-slash directory and say when it matches nothing (S) — a silent no-op is the most misleading behaviour found.
2. Lead check's lines with CHECK instead of MEMORY (S) — restores the one-verb-one-line grammar an AI keys on.
3. Print eval queries with literal spaces instead of \x20 escapes (S) — one readability wart in an otherwise clean line grammar.

## Good, keep
- The bare command names its door in one line (`run: nova-memory help`), and quickstart is a real first run that prints exact editable commands; the whole banner and example ran as printed.
- Every refusal names the cause, all problems at once, and the next command; `--channels is required; refusing to guess; run: nova-memory help` is the model.
- Old defects from the 2026-10-02 ratings are fixed: the CAL rule now states explicitly that a hit at or below CAL is not noise, a `--whole` flag widens the 120-byte snippet, and `eval -h` now documents the gold-file format; exit codes match the help's table and the JSON status matches the line status.

## What could not be tried
- None: every verb (quickstart, stats, search, check, verify, eval, boot, version) ran read-only on the shipped corpus; no verb needs a service or a live store.

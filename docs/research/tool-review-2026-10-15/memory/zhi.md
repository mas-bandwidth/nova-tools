# nova-memory AI-usability review, 2026-10-15

Reviewer: google/gemini-3.8-flash
Build: 453ce3a00131
Score: 10/10 (54/55)

## Rubric
| # | criterion | score | evidence |
|---|---|---|---|
| 1 | cold | 5 | `nova-memory quickstart --root ./corpus` -> `QUICKSTART OK done=3` |
| 2 | refusals | 5 | `nova-memory search` -> `SEARCH REFUSED: --channels is required; refusing to guess; run: nova-memory help` |
| 3 | output | 4 | `nova-memory eval --root ./corpus --channels bm25 --k 3 --floor 0.99 --fail-max 1 --json ./gold_2miss.tsv` -> `{"more":[{"kind":"miss","remedy":"--max <n> raises the ceiling, --max 0 lists all","shown":1,"total":2}]}` |
| 4 | dry-run | 5 | `n/a: read-only tool; every verb is an inspection that writes nothing` |
| 5 | idempotent | 5 | `n/a: read-only tool; every verb is an inspection with no persistent write surface` |
| 6 | no prompts | 5 | `n/a: every verb runs to completion on argv or piped stdin; nothing prompts a TTY` |
| 7 | exit codes | 5 | `nova-memory eval --root ./corpus --channels bm25 --k 3 --floor 0.99 ./gold_fail.tsv` -> `exit: 1` |
| 8 | one word | 5 | `nova-memory help` -> `the verbs are quickstart, stats, search, check, verify, eval, boot, version` |
| 9 | push | 5 | `n/a: in-memory transient index over local files; no background state waited on or polled` |
| 10 | no leaks | 5 | `nova-memory search --root ./corpus --channels bm25 --k 3 lantern` -> `SEARCH OK hits=2 k=3 channels=bm25 files=2 chunks=2: query="lantern"` |
| 11 | true help | 5 | `nova-memory verify --root ./corpus --links info --coverage notes/lantern.md:notes/index-notes.md` -> `VERIFY OK gating=0 info=0 shown=0 coverage=0 frontmatter=0 links=info` |

## 10/10 blockers
| # | where | what an AI meets | fix | size |
|---|---|---|---|---|
| 1 | `nova-memory eval --root ./corpus --channels bm25 --k 3 --floor 0.99 --fail-max 1 --json ./gold_2miss.tsv` | `{"more":[{"kind":"miss","remedy":"--max <n> raises the ceiling, --max 0 lists all","shown":1,"total":2}]}` points to unknown flag `--max` (`EVAL REFUSED: unknown flag --max`) | set JSON remedy to `--fail-max <n> raises the ceiling, --fail-max 0 prints every finding` matching line output | S |
| 2 | `nova-memory check --root ./corpus --channels bm25 --k 3 draft.md` | lines print `MEMORY OK`, `MEMORY CAND`, `MEMORY HIT`, `MEMORY REFUSED:` while verb is `check` and `--json` verb is `"check"` | align line prefix and refusal prefix to `CHECK` (`CHECK OK`, `CHECK REFUSED:`) per grammar standard | S |
| 3 | `nova-memory check --root ./corpus --channels bm25 --k 3 non_existent_file.md` | `MEMORY REFUSED: open non_existent_file.md: no such file or directory; run: nova-memory help` leaks raw Go os.Open error | catch missing file cleanly as `candidate file "non_existent_file.md" does not exist or is not readable` and point to `check -h` | S |
| 4 | `nova-memory boot --root ./corpus --pin ./two_bad_pin.txt` | `BOOT REFUSED: pin entry "missing1.md" does not exist under --root; run: nova-memory help` returns on first bad entry only | validate all pin entries and report all invalid entries in one refusal turn | S |
| 5 | `nova-memory eval --root ./corpus --channels bm25 --k 3 --floor 0.5 ./gold_with_header.tsv` | `EVAL MISS query=query expected=expected` treats standard TSV header row as a test query and lowers recall | recognize and skip standard `query<TAB>expected` header row in gold file | S |

## Nice-to-haves
| # | where | what | fix | size |
|---|---|---|---|---|
| 1 | `nova-memory stats --root ./corpus --exclude 'notes/'` | `--exclude 'notes/'` with trailing slash silently excludes 0 files while `--exclude 'notes'` works | strip trailing slashes in directory exclusion patterns and warn when an exclusion matches 0 files | S |
| 2 | `nova-memory verify -h` | lists `--root <value> corpus root directory, repeatable (required)` but passing two roots is refused | update verify help to state that `--root` names exactly one tree | S |
| 3 | `nova-memory search --root ./corpus --channels bm25 --k 3 --json lantern` | search hit JSON contains `"candidate": 1`, a field belonging to multi-candidate check | omit `candidate` field from search JSON items | S |
| 4 | `nova-memory quickstart --root ./corpus` | prints `$ nova-memory check --root ./corpus --channels bm25 --k 2 -` which refuses if pasted without stdin pipe | show stdin redirection in printed quickstart command line or default to a file candidate | S |
| 5 | `nova-memory eval --root ./corpus --channels bm25 --k 3 --floor 0.99 ./gold_fail.tsv` | text line prints `query=unrelated\x20query\x20words` with hex-escaped spaces | format query in eval MISS lines with double quotes as in search | S |

## Top 3 fixes
1. Fix JSON `more` remedy on `eval` and `verify` to advise `--fail-max <n>` instead of `--max <n>`, so machine consumers are not guided into an unknown-flag refusal.
2. Align `check` CLI output line prefix and refusal prefix from `MEMORY` to `CHECK` (`CHECK OK`, `CHECK REFUSED:`), matching its own JSON verb name and family line grammar.
3. Catch candidate and gold file opening errors cleanly instead of leaking raw Go `open` strings, directing remedies to `<verb> -h`.

## Good, keep
- Setup and example commands in help run cleanly out of the box with zero guessing; quickstart provides an immediate runnable demonstration.
- Transient in-memory index design means zero state to manage, sync, or clean up; every verb is an explicit inspection.
- Fixed since 1.2.0: CAL documentation now explicitly notes CAL is context and not a cutoff; gold file format is documented with examples in `eval -h`; `--whole` flag is available to print full paragraphs; and example commands pass verbatim.

## What could not be tried
- Nothing: every verb (`version`, `quickstart`, `stats`, `search`, `check`, `verify`, `eval`, `boot`) was executed locally against scratch and shipped testdata; nova-memory requires no external services or network.

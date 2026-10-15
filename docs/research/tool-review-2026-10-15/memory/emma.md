# nova-memory AI-usability review, 2026-10-15

Reviewer: Gemini 4 Argon (High)
Build: 75282bf3d953
Score: 8.5/10 (47/55)

## Rubric
| # | criterion | score | evidence |
|---|---|---|---|
| 1 | cold | 5 | `nova-memory help` `setup:` and `example:` run unchanged: `nova-memory quickstart --root ./corpus` -> `QUICKSTART OK done=3` (exit 0) |
| 2 | refusals | 4 | `nova-memory search --root ./corpus` names missing `--channels`, `--k`, and query words in one run with hints (exit 2); `nova-memory boot --root ./corpus --pin pin-bad.txt` stops at the first missing pin entry (`notes/missing1.md`) instead of naming all missing entries at once |
| 3 | output | 4 | `nova-memory stats --root ./corpus --json` -> `{"result":{"verb":"stats","status":"ok","exit":0},"facts":{"schema":"nova-memory/2","files":2,"chunks":2,...}}`; `nova-memory version --json` is refused at exit 2 and `--json` refusals embed `SEARCH REFUSED: ...; run: nova-memory help` prefixes inside `why` |
| 4 | dry-run | 5 | Every verb is a pure read-only in-memory inspection (`effect: inspection: reads, writes nothing`), so no write path or `--dry-run` is needed |
| 5 | idempotent | 5 | Repeated runs of `quickstart`, `stats`, `search`, `check`, `verify`, `eval`, and `boot` leave the corpus untouched and produce identical results (apart from `stats` `build=` duration) |
| 6 | no prompts | 5 | All 8 verbs run non-interactively to completion (`check` reads `<file>` or `-` when explicitly given) |
| 7 | exit codes | 5 | `search`, `stats`, `boot`, `check`, `quickstart`, `version` exit 0; `verify` and `eval` exit 0 on pass and 1 on finding/below-floor; bad invocations exit 2 |
| 8 | one word | 5 | `root`, `channels`, `k`, `class`, `chunks`, and `fused` are used consistently across help text, line receipts, and JSON output |
| 9 | push | 5 | Local in-memory corpus indexer with sub-millisecond build times on small trees and no polling or background waits |
| 10 | no leaks | 5 | `nova-memory boot --root ./corpus --pin pin-bad.txt` -> `BOOT REFUSED: pin entry "notes/missing1.md" does not exist under --root; run: nova-memory help` (clean one-line refusals, `os.OpenRoot` confinement) |
| 11 | true help | 4 | `eval -h` now documents `<gold.tsv>`, `boot -h` now states it checks presence/size, and `--whole` now prints full paragraphs up to 4096B (fixing three 2026-10-02 findings), but `verify -h` still claims `--root` is `repeatable` when `verify` refuses two roots, and `boot -h` / `version -h` still say `(the index lives in memory for the run)` |

## 10/10 blockers
| # | where | what an AI meets | fix | size |
|---|---|---|---|---|
| 1 | `nova-memory version --json` | Prints `{"result":{"verb":"version","status":"refused","exit":2,"remedy":"nova-memory help","why":["VERSION REFUSED: takes no flags and no arguments, got 1; run: nova-memory version -h"]},"facts":{}}` (exit 2), refusing `--json` on `version` even though `docs/CLI.md` requires `--json` on every verb, and wrapping raw `REFUSED:` stderr lines (including indented hint lines as separate array elements) into `why` on every `--json` refusal (`cmd/nova-memory/main.go:294-305`) | Accept `--json` in `cmdVersion` (`cmd/nova-memory/version.go`) and strip the `<TOKEN> REFUSED:` prefix and trailing `; run: ...` suffix when building `why` for `--json` refusals | S |
| 2 | `nova-memory verify -h` | Lists `--root <value>  corpus root directory, repeatable (required)` from the shared `addRootFlags` helper, but running `nova-memory verify --root ./corpus --root ./corpus --links info` refuses at exit 2 with `VERIFY REFUSED: --root names exactly one tree for verification, but 2 were given` (still present from the 2026-10-02 rating) | Register `--root` on `verify` with description `corpus root directory, single tree (required)` instead of `repeatable` | S |
| 3 | `nova-memory boot --root ./corpus --pin pin-bad.txt` | When `pin-bad.txt` contains two missing files (`notes/missing1.md` and `notes/missing2.md`), `boot` exits at the first (`BOOT REFUSED: pin entry "notes/missing1.md" does not exist under --root; run: nova-memory help`), requiring multiple runs despite the banner's guarantee that a refusal reports every problem in one run (still present from the 2026-10-02 rating) | Collect all bad pin entries in `cmd/nova-memory/boot.go` and report every missing, duplicate, or invalid entry before returning exit 2 | S |
| 4 | `nova-memory eval --root ./corpus --channels bm25 --k 2 --floor 0.9 gold-miss.tsv` | Text mode prints `EVAL MISS query=zebra\x20stripes expected=notes/lantern.md` with `\x20` hex escapes for spaces in `query=`, whereas `SEARCH OK` quotes `query="zebra stripes"` and `--json` emits `"query":"zebra stripes"` (still present from the 2026-10-02 rating) | Quote `query=%q` on `EVAL MISS` lines in `cmd/nova-memory/eval.go` to match `SEARCH OK` | S |
| 5 | `nova-memory quickstart --root ./corpus` | When a corpus has a `log/1974-03-11.md` file or `notes/index-notes.md`, `quickstart`'s default demonstration candidate (`corpus-first-paragraph`) selects `log/1974-03-11.md:1` (`"# 1974-03-11"`) or the index listing line `[Lantern care](lantern.md) keeps the glazing clean.`, and its demonstration search ranks the index stub above the note at `score=0.59` below `SEARCH CAL score=1.46` (still present from the 2026-10-02 rating) | Skip heading-only chunks (`# ...`) and `index-*` listings when choosing `quickstart`'s default demonstration paragraph | S |

## Nice-to-haves
| # | where | what | fix | size |
|---|---|---|---|---|
| 1 | `nova-memory boot -h` and `nova-memory version -h` | Both print `effect: inspection: reads, writes nothing (the index lives in memory for the run)` even though neither `boot` nor `version` builds an index, and every `<verb> -h` repeats the full multi-verb `exit codes: by verb (each ran here)...` block (still present from the 2026-10-02 rating) | Omit `(the index lives in memory for the run)` on `boot` and `version`, and print only each verb's own exit codes in `<verb> -h` | S |
| 2 | `nova-memory search --root ./corpus` | Missing-flag and bad-value refusals after a known verb end with `run: nova-memory help` (the 125-line banner) instead of `run: nova-memory <verb> -h` (still present from the 2026-10-02 rating) | Point `refuse()` inside a known verb to `nova-memory <verb> -h` | S |
| 3 | `nova-memory verify --coverage` | Coverage checks whether a file's stem appears anywhere as plain text in the target file rather than requiring a markdown link or wikilink, while `--coverage` in `nova-memory help` says `named in some file` (still present from the 2026-10-02 rating) | State in `--coverage` help that any text occurrence of the file stem satisfies coverage, or require a link | S |

## Top 3 fixes
1. Support `--json` on `nova-memory version` and format `--json` refusals with clean `why` strings instead of raw `REFUSED:` stderr lines (`cmd/nova-memory/main.go:294-305`, `cmd/nova-memory/version.go`).
2. Fix `verify -h` so `--root` is not advertised as `repeatable`, and make `boot` report all invalid/missing `--pin` entries in a single run (`cmd/nova-memory/boot.go`).
3. Quote `query=%q` on `EVAL MISS` lines (`cmd/nova-memory/eval.go`) and skip markdown headings / `index-*` files when picking `quickstart`'s default candidate paragraph (`cmd/nova-memory/quickstart.go`).

## Good, keep
- Major improvements since the 2026-10-02 rating: the banner now explains `SEARCH CAL` honestly (`CAL is context, not a cutoff... a right answer can fall below it`), `eval -h` now documents the `query<TAB>expected[,expected]` format with examples, `boot -h` clearly states it checks pin presence and size, and `--whole` lets `search` and `check` print full paragraphs up to 4096 bytes with explicit `...+<n>B` truncation markers.
- `quickstart` prints each underlying command (`$ nova-memory stats ...`, `$ nova-memory search ...`, `$ nova-memory check ...`) above its output and reminds the caller that `bm25` and `k=3/2` were demonstration choices, not hidden defaults.
- Missing required flags (`--root`, `--channels`, `--k`, `--links`) report all missing flags in a single run along with actionable one-line hints explaining what each flag expects.

## What could not be tried
Every verb (`quickstart`, `stats`, `search`, `check`, `verify`, `eval`, `boot`, `version`, `help`) was run end to end in a scratch directory because `nova-memory` is a pure read-only local indexer with no daemon or live shared state.

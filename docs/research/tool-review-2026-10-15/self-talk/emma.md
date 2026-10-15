# nova-self-talk AI-usability review, 2026-10-15

Reviewer: Gemini 4 Argon (High)
Build: b930c415a495
Score: 9.5/10 (51/55)

## Rubric
| # | criterion | score | evidence |
|---|---|---|---|
| 1 | cold | 5 | `nova-self-talk example ./pages` -> `EXAMPLE OK dir=./pages wrote=RULES.md,journal.md kept=-; run: nova-self-talk pages/journal.md` (exit 0) and `nova-self-talk ./sample.md` -> `SELFTALK FAIL ./sample.md:2: STANDING match="bad at": I am bad at checking the output.` (exit 1) |
| 2 | refusals | 4 | `nova-self-talk bogus` -> `nova-self-talk REFUSED: cannot read "bogus": no such file or directory; it is not a verb either (the verbs are scan, shapes, example, version, help; a file of that name is ./bogus); run: nova-self-talk help` (exit 2), though `nova-self-talk scan --bogus --max -1 ./sample.md` stops at the first flag error (`unknown flag -bogus`) |
| 3 | output | 4 | `nova-self-talk ./sample.md --json` -> `{"result":{"verb":"scan","status":"failed","exit":1},"facts":{"files":1,"skipped":0,"claims":1,"standing":1,"installations":0,"dated":0,"shown":1},"items":[...]}` (exit 1); `scan`, `shapes`, and `example` all take `--json`, while `version --json` is refused at exit 2 |
| 4 | dry-run | 5 | `nova-self-talk example --dry-run ./pages2` -> `EXAMPLE OK dir=./pages2 would-write=RULES.md,journal.md kept=-; run: nova-self-talk example ./pages2` (exit 0, creates nothing) |
| 5 | idempotent | 5 | `nova-self-talk example ./pages` (second run) -> `EXAMPLE OK dir=./pages wrote=- kept=RULES.md,journal.md; run: nova-self-talk pages/journal.md` (exit 0) |
| 6 | no prompts | 5 | `nova-self-talk` -> exits 2 immediately without reading stdin unless `-` is explicitly named |
| 7 | exit codes | 5 | `nova-self-talk ./clean.md` -> exit 0; `nova-self-talk ./sample.md` -> exit 1; `nova-self-talk` (no args) -> exit 2 |
| 8 | one word | 4 | Bare `nova-self-talk <file>...` and `nova-self-talk scan <file>...` are two spellings for the same scan; `example --dry-run` emits `would-write` (hyphen) |
| 9 | push | 5 | `n/a: stateless local text scanner with no watched or polled state` |
| 10 | no leaks | 5 | `n/a: scans caller-named markdown files and handles no secrets or credentials` |
| 11 | true help | 4 | `setup:` and `example:` work verbatim as documented; `nova-self-talk help bogus` prints the full 100-line banner at exit 0 instead of refusing an unknown verb, and `scan -h` / `example -h` omit `<file>...` and `<dir>` from `usage:` |

## 10/10 blockers
| # | where | what an AI meets | fix | size |
|---|---|---|---|---|
| 1 | `nova-self-talk help bogus` | Prints the entire 100-line root banner at exit 0 instead of refusing `"bogus"` as an unknown verb, so an automated caller cannot distinguish a typo from valid verb help (carried from the 2026-10-02 ratings) | Refuse unknown verbs in `nova-self-talk help <verb>` at exit 2 with `nova-self-talk REFUSED: unknown verb "bogus"; the verbs are scan, shapes, example, version, help; run: nova-self-talk help` | S |
| 2 | `nova-self-talk scan --bogus --max abc ./sample.md` and `nova-self-talk example --bogus ./pages` | `scan --bogus --max abc` stops on the first bad flag (`unknown flag -bogus` with one dash) without reporting `--max abc`, `--max abc` emits raw `flag` library text (`invalid value "abc" for flag -max: parse error`), and `example --bogus` emits raw `flag provided but not defined: -bogus` (carried from the 2026-10-02 ratings) | Collect all flag errors across `scan`, `shapes`, and `example`, format unknown flags with `--` and bad `--max` values in the tool's own voice, and report them together at exit 2 | S |
| 3 | `nova-self-talk scan -h`, `nova-self-talk example -h`, `nova-self-talk version -h`, `nova-self-talk shapes -h` | `scan -h` and `example -h` print `usage: nova-self-talk scan [flags]` and `usage: nova-self-talk example [flags]`, dropping the required `<file>...` and `<dir>` arguments, and `version -h`, `shapes -h`, and `example -h` end with the scan's exit-code table (`1 findings ... SELFTALK SKIP files=0`) (carried from the 2026-10-02 ratings) | Include `<file>...` and `<dir>` in `scan -h` and `example -h` usage lines and give `version`, `shapes`, and `example` their own `0 done; 2 could not run` exit-code line | S |
| 4 | `nova-self-talk example --dry-run ./pages` (when `./pages` already holds the example files) | Prints `EXAMPLE OK dir=./pages would-write=- kept=RULES.md,journal.md; run: nova-self-talk example ./pages`, recommending a command that will write nothing instead of the scan command `nova-self-talk pages/journal.md` that `example ./pages` recommends (carried from the 2026-10-02 ratings) | When `would-write=-` in `example --dry-run`, set the `run:` remedy to `nova-self-talk <dir>/journal.md` | S |

## Nice-to-haves
| # | where | what | fix | size |
|---|---|---|---|---|
| 1 | `nova-self-talk version --json` | `scan`, `shapes`, and `example` all support `--json`, leaving `version` as the only verb that refuses `--json` at exit 2 | Add `--json` support to `nova-self-talk version` | S |
| 2 | `nova-self-talk` (refusal prefix vs `SELFTALK` / `SHAPES` / `EXAMPLE`) | Normal lines start with `SELFTALK`, `SHAPES`, or `EXAMPLE`, whereas refusals start with lowercase `nova-self-talk REFUSED:` followed by an indented prose line without a prefix | Use uppercase `SELFTALK REFUSED:` (or `<VERB> REFUSED:`) and prefix continuation notes with `SELFTALK NOTE` | S |

## Top 3 fixes
1. Make `nova-self-talk help <unknown>` refuse at exit 2 naming the valid verbs instead of dumping the full banner at exit 0.
2. Unify flag error handling across `scan`, `shapes`, and `example` so unknown flags (`--bogus`) and non-integer `--max` values use the tool's own refusal grammar and report all flag problems in one run.
3. Fix `scan -h` and `example -h` usage lines to include `<file>...` and `<dir>`, give non-scan verbs their own exit-code line, and point `example --dry-run` to `nova-self-talk <dir>/journal.md` when pages are already present.

## Good, keep
- Zero-dependency cold start: `nova-self-talk example ./pages` materializes self-contained specimen files from the binary itself (`EXAMPLE OK`), and `nova-self-talk ./pages/journal.md` immediately demonstrates `STANDING`, `RANKING`, and `DATED` classification.
- `nova-self-talk shapes` (and `shapes --json`) exposes the complete 22-row detector and license table with a positive `finds=` sentence, a near-miss `passes=` sentence, and the exact regular expression for every row.
- All-or-nothing file reads (`NOTHING was scanned, which is not a green` at exit 2 when any file is unreadable) and the every-run `SELFTALK NOTE` stating that a green clears only known grammatical shapes, never the meaning of a file.

## What could not be tried
- Every verb (`scan`, `shapes`, `example`, `version`, `help`) is local and self-contained; all verbs and flags were tried in `<job>/scratch`.

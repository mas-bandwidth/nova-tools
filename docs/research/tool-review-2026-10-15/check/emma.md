# nova-check AI-usability review, 2026-10-15

Reviewer: Gemini 4 Argon (High)
Build: b4f484c84916
Score: 9.0/10 (49/55)

## Rubric
| # | criterion | score | evidence |
|---|---|---|---|
| 1 | cold | 5 | `nova-check quickstart --dir ./self` -> `QUICKSTART OK done=2 worst-exit=0 next=kernel,attest,floors,corpus (kernel wants a size budget, attest a manifest of what a full boot reads, floors a derived copy and its source, corpus a ledger of protected lines: nova-check help)` |
| 2 | refusals | 4 | `nova-check kernel` -> `KERNEL REFUSED: --file is required; it wants --file <file> is the one kernel file to measure — the file whose size you are holding to a budget, not the directory it lives in; refusing to guess; run: nova-check help` |
| 3 | output | 4 | `nova-check dogfood ledger --cli docs/CLI.md --receipts ./self --max 3 --json` -> `DOGFOOD-LEDGER REFUSED: unknown flag --json; the flags of dogfood ledger are --authors, --cli, --fail-max, --git-timeout, --max, --receipts, --repo, --tools, --tools-timeout; run: nova-check dogfood ledger -h` |
| 4 | dry-run | 5 | `nova-check dogfood record --cli docs/CLI.md --tool nova-check --verb version --by tester --ok --notes "cold test" --receipts ./self --dry-run` -> `DOGFOOD RECORD OK tool=nova-check verb=version by=tester at=2026-10-15T16:51:43Z ok=yes issue=- file=self/20261015T165143Z-nova-check-version-tester-d8ab2dfc.json dry_run=true` |
| 5 | idempotent | 3 | `nova-check dogfood record --cli docs/CLI.md --tool nova-check --verb version --by tester --ok --notes "cold test" --receipts ./self/receipts --op op1` -> `DOGFOOD-RECORD REFUSED: unknown flag --op; the flags of dogfood record are --by, --cli, --closes, --dry-run, --fail-max, --issue, --max, --not-ok, --notes, --ok, --receipts, --tool, --tools, --tools-timeout, --verb; did you mean --ok?; run: nova-check dogfood record -h` |
| 6 | no prompts | 5 | `nova-check kernel --file ./self/docs/SEED-CORE.md --max-bytes -5` -> `KERNEL REFUSED: --max-bytes must be a positive byte budget (got -5); refusing to guess; run: nova-check help` |
| 7 | exit codes | 5 | `nova-check spelling --file docs/CLI.md --max 5` -> `SPELLING FAILED files=1 misspellings=6 shown=5` |
| 8 | one word | 5 | `nova-check version` -> `nova-check v1.2.25-0.20261015164621-b4f484c84916 linux/amd64 go1.26.6` |
| 9 | push | 5 | `n/a: stateless repository and markdown verifier with no watch, wait, or background state transitions` |
| 10 | no leaks | 5 | `nova-check links --dir docs --max 5` -> `LINKS BROKEN file=CLI.md line=3 target=../README.md reason="escapes the tree; cannot survive the repo travelling alone"` |
| 11 | true help | 3 | `nova-check dogfood ledger --cli docs/CLI.md --receipts ./self --max 3` -> `DOGFOOD OK verbs=173 dogfooded=0 by-nonauthor=0 open-edges=0 unfiled=0 unmatched=0` |

## 10/10 blockers
| # | where | what an AI meets | fix | size |
|---|---|---|---|---|
| 1 | `nova-check dogfood ledger --cli docs/CLI.md --receipts ./self --max 3` | Help states that `dogfood ledger` takes `[--max <n>]` and caps listings at `--max` with a `MORE` line, but `--max 3` is ignored and all 173 `DOGFOOD tool=...` rows are printed | Apply `--max <n>` truncation and emit `DOGFOOD MORE shown=<n> total=<total>` in `dogfood ledger` | S |
| 2 | `nova-check dogfood ledger --cli docs/CLI.md --receipts ./self` | Scraping fenced code blocks in `docs/CLI.md` treats negative refusal examples and table names as real verbs (`DOGFOOD tool=nova-sandbox verb=bogus`, `DOGFOOD tool=nova-config verb=-`, `DOGFOOD tool=nova-table verb=create demo`) | Restrict `--cli` verb extraction to the `usage:` block or `### <verb>` headings rather than arbitrary example invocations | S |
| 3 | `nova-check dogfood ledger --json` and `nova-check dogfood record --json` | Both refuse `--json` (`DOGFOOD-LEDGER REFUSED: unknown flag --json`) while top-level `--help` omits `[--json]` from `attest`, `links`, `kernel`, `nocode`, `floors`, `corpus`, `hygiene`, `spelling`, and `version` usage lines | Support `--json` on `dogfood ledger` and `dogfood record` and list `[--json]` consistently on every verb's usage line in `nova-check --help` | M |
| 4 | `nova-check dogfood record --cli docs/CLI.md --tool nova-check --verb version --by tester --ok --notes "cold test" --receipts ./self/receipts` | `dogfood record` refuses `--op` and writes a second timestamped receipt file (`20261015T165202Z-...json` and `20261015T165203Z-...json`) when the identical command is retried | Accept `--op <token>` (or deduplicate by content hash) in `dogfood record` so retrying the same receipt writes nothing twice | S |
| 5 | `nova-check kernel` | Missing-flag and bad-value refusals have spliced prose (`it wants --file <file> is the one kernel file to measure`, `it wants state the unit:`) and end with `run: nova-check help` instead of the verb's own `-h` (`nova-check kernel -h`) | Fix the `it wants` prefix splice and point verb-specific refusals to `run: nova-check <verb> -h` | S |
| 6 | `nova-check convergence -h` and `nova-check version -h` | `convergence -h` quotes a truncated mid-sentence line from the banner (`... or nova-check dogfood record the`), and `version -h` and `dogfood -h` append the `setup:` markdown fixture block that neither uses | Keep `setup:` in the top-level banner only and fix the `-h` banner excerpt boundary so wrapped sentences are not cut mid-line | S |

## Nice-to-haves
| # | where | what | fix | size |
|---|---|---|---|---|
| 1 | `nova-check links --dir ./empty` | Earlier 2026-10-02 finding still present: scanning a directory with zero markdown files exits `0` with `LINKS OK dir=./empty files=0` whereas `nocode` emits a `NOTE` line on empty directories | Emit a `LINKS NOTE` or `SPELLING NOTE` when `files=0` so an empty glob or directory is explicit | S |

## Top 3 fixes
1. Honor `--max <n>` with a `DOGFOOD MORE` summary line in `nova-check dogfood ledger` instead of dumping all 173 rows unbounded.
2. Restrict `--cli` verb parsing in `nova-check dogfood` to `usage:` synopses or `### <verb>` headings so negative examples (`nova-sandbox bogus`, `nova-config -`) are not counted as real verbs.
3. Add `--json` to `dogfood ledger` and `dogfood record`, add `--op` idempotence to `dogfood record`, and point verb refusals to `nova-check <verb> -h`.

## Good, keep
- `nova-check --help` includes a self-contained `setup:` and `example:` block that runs cold in a scratch directory, and `quickstart` names the exact next checks and what each requires.
- Every independent missing or invalid flag is reported in a single run (`nova-check kernel` reports both `--file` and `--max-bytes` / `--max-tokens` at once), and unknown flags suggest the closest flag and `run: nova-check <verb> -h` (keeping the 1.2.0 fix).
- `links` and `spelling` emit clean, grep-friendly `file=... line=...` findings with bounded `--max` output and `MORE` instructions.

## What could not be tried
- `nova-check convergence` without `--dry-run` or live forge credentials: `LANDING` and `PRS` streams query GitHub via `gh` over the network, which is outside isolated local scratch probing; judged from `convergence -h`.

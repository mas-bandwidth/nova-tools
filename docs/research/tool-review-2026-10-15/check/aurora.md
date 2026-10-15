# nova-check AI-usability review, 2026-10-15

Reviewer: mercury-3-preview-1002
Build: cc2f2f8c8148
Score: 9.5/10 (51/55)

## Rubric
| # | criterion | score | evidence |
|---|---|---|---|
| 1 | cold | 5 | `nova-check quickstart --dir ex/self` -> `QUICKSTART OK done=2 worst-exit=0` (the exact commands of the help `example:` block) |
| 2 | refusals | 5 | `nova-check quickstart` -> `QUICKSTART REFUSED: --dir is required; ... refusing to guess; run: nova-check help` |
| 3 | output | 5 | `nova-check quickstart --dir ./self --json` -> `{"result":{"verb":"quickstart","status":"ok","exit":0},...}` |
| 4 | dry-run | 5 | `nova-check dogfood record --cli ./cli.md --tool nova-check --verb version --by reviewer --ok --notes "ran version" --receipts ./receipts --dry-run` -> `DOGFOOD RECORD OK ... dry_run=true` and the receipts dir stayed empty |
| 5 | idempotent | 1 | `nova-check dogfood record ... --notes "same op" --receipts ./r2` run twice -> `r2/20261015T150809Z-...-e04b332e.json` and `r2/20261015T150814Z-...-1e48990e.json`; no write takes `--op` (`nova-check spelling --write --op abc123` -> `SPELLING REFUSED: unknown flag --op`) |
| 6 | no prompts | 5 | every probe ran to completion non-interactively; no prompt waited on |
| 7 | exit codes | 5 | pass `nova-check version` -> exit 0; check failed `nova-check links --dir <repo>` (broken link) -> exit 1; bad invocation `nova-check quickstart` -> exit 2, matching help's table |
| 8 | one word | 5 | `nova-check help` -> `the verbs are quickstart, attest, links, kernel, nocode, floors, corpus, hygiene, dogfood ledger, dogfood record, dogfood gate, convergence, spelling, version` (no duplicates) |
| 9 | push | 5 | n/a: every verb reads local files or the git index in place; nothing is waited on or polled |
| 10 | no leaks | 5 | `nova-check version --json` and `nova-check attest --json` output only paths, counts, hashes and status; no secret seen in any line |
| 11 | true help | 5 | help's first-run block runs exactly as printed (exit 0); every verb behaved as its `-h` states (links, nocode, kernel, attest, floors, corpus, hygiene, spelling, dogfood ledger/record/gate, version) |

## 10/10 blockers
| # | where | what an AI meets | fix | size |
|---|---|---|---|---|
| 1 | `nova-check dogfood record --cli ./cli.md --tool nova-check --verb version --by reviewer --ok --notes "same op" --receipts ./r2` | no writer takes `--op`; re-running the same record appends a second receipt (`e04b332e.json` then `1e48990e.json`), and two identical runs inside one second silently overwrite the same file (`SPELLING REFUSED: unknown flag --op`) | add a `--op <id>` to the three writers (dogfood record, spelling --write, convergence --state) that records the op id and replays the recorded result on a repeat, so a retry changes nothing and never clobbers | S |
| 2 | `nova-check links --dir ./empty` | green over nothing: `LINKS OK dir=./empty files=0 links=0 excluded=0 broken=0` at exit 0, so an AI reads a pass where nothing was checked (old defect, still there; nocode already prints a NOTE for the same tree) | print the NOTE for links and spelling too, or exit 1 when nothing was classified | S |
| 3 | `nova-check links --dir ./self --exclude ./self/docs` | `--exclude` (and `links --file`) are resolved relative to `--dir`, so the path a caller passes as given excludes/reports nothing: `LINKS OK ... excluded=0` (old defect, still there) | accept a path that starts with `--dir` as that subtree/file, or refuse a prefix that matches nothing | S |
| 4 | `nova-check spelling --dir ./s2 --write --dry-run` | a dry run that finds misspellings still closes `SPELLING OK files=1 misspellings=2 written=0 dry_run=true` at exit 0, hiding that nothing was fixed (old defect, still there) | print `SPELLING FAILED` at exit 1 when a dry run found misspellings it did not write | S |
| 5 | `nova-check convergence ... --dry-run` | the dry run still reads the forge through gh and refuses offline (`CONVERGENCE REFUSED: gh pr list --state open ... run: nova-check help`), so an offline reader cannot plan (old defect, still there) | with `--dry-run`, or when gh is absent, mark LANDING and PRS absent instead of refusing | M |

## Nice-to-haves
| # | where | what | fix | size |
|---|---|---|---|---|
| 1 | `nova-check help` verb list | dogfood is a three-verb group spelled as two words (`dogfood ledger`, `dogfood record`, `dogfood gate`), a small deviation from the one-word-per-thing shape | keep the group but state in help that it is one tool namespace of three verbs, or flatten the subcommands | S |
| 2 | `nova-check dogfood record` | receipt filenames carry a second-resolution timestamp, so same-second runs collide | include an op id or a counter in the filename so a repeat can never overwrite | S |

## Top 3 fixes
1. Add `--op` idempotency to dogfood record, spelling --write and convergence --state; the same op replays the recorded result and changes nothing (moves criterion 5 from 1 to 5, about +0.7).
2. Refuse or NOTE a green over zero files in links and spelling, as nocode already does, and resolve a `--exclude`/`--file` path that already carries the `--dir` prefix.
3. Make convergence `--dry-run` mark the forge streams absent instead of reading them through gh, so an offline plan can print.

## Good, keep
- Refusals are first-rate: each names the cause, what the input wants (the missing `--dir` refusal quotes the flag's full description), and the exact next command to run; a bare invocation lists all verbs and points at `nova-check help`. Since 2026-10-02, an invocation with two unknown flags now names both (`--bogus` and `--alsobad`), each pointing at `nova-check links -h`.
- Output is one stable grammar (`VERB OK/FAILED/REFUSED k=v`, `MORE shown=n total=n`) with a matching `--json` rendering of the same value; totals survive `--max` cuts, and the quickstart `example:` block runs exactly as printed.
- The tool refuses to guess (never infers a directory or a ledger from the working directory) and every exit code matches the documented 0/1/2 table; the three writers all expose `--dry-run` that writes nothing.

## What could not be tried
- `convergence`: it reads the forge through `gh`; this sandbox's `gh` refuses (`gh: REFUSED pr list: no GitHub CLI in a card`), so only its help and its refusal were observed — no reading, and its `--dry-run` could not be exercised end to end.
- `hygiene` was run over the staged checkout whose base..head range contains a merge commit; it returned identity/merge findings (environmental, not a tool defect) and the no-merge path was not separately exercised.
- `dogfood gate` with `--require-all` and `--shipped` was not varied; the default path passed.

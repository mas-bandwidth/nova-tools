# nova-self-talk AI-usability review, 2026-10-15

Reviewer: mercury-3-preview-1002
Build: cc2f2f8c8148
Score: 9.5/10 (52/55)

## Rubric
| # | criterion | score | evidence |
|---|---|---|---|
| 1 | cold | 5 | `nova-self-talk example ./pages` -> `EXAMPLE OK dir=./pages wrote=RULES.md,journal.md kept=-` |
| 2 | refusals | 4 | `nova-self-talk --bogus --max abc probe.md` -> `REFUSED: unknown flag -bogus; the flags are --skip, --rule-doc, --max, --json; run: nova-self-talk help` |
| 3 | output | 5 | `nova-self-talk --json ./pages/journal.md` -> `{"result":{"verb":"scan","status":"failed","exit":1},"facts":{"files":1,...}}` |
| 4 | dry-run | 5 | `nova-self-talk example --dry-run ./pages` -> `EXAMPLE OK dir=./pages would-write=RULES.md,journal.md kept=-` then `ls ./pages` -> no such directory |
| 5 | idempotent | 4 | `nova-self-talk example ./pages` 2nd run -> `EXAMPLE OK dir=./pages wrote=- kept=RULES.md,journal.md` (idempotent by rerun; no --op flag) |
| 6 | no prompts | 5 | `nova-self-talk ./pages/journal.md` -> returns immediately, exit 1, no prompt |
| 7 | exit codes | 5 | `nova-self-talk ./pages/journal.md` -> exit 1; `nova-self-talk version` -> exit 0; `nova-self-talk fopen` -> exit 2 |
| 8 | one word | 4 | `nova-self-talk scan probe.md` and bare `nova-self-talk probe.md` are two surfaces for the same scan |
| 9 | push | 5 | n/a: read-only scanner, no state waited on |
| 10 | no leaks | 5 | every output line shows only match words and file paths, no secret material |
| 11 | true help | 5 | claimed an all-skipped run `exits 0 with SELFTALK SKIP files=0, never OK`; `nova-self-talk --skip journal.md --skip RULES.md ./pages/journal.md ./pages/RULES.md` -> `SELFTALK SKIP files=0 skipped=2 reason=all-skipped`, exit 0 |

## 10/10 blockers
| # | where | what an AI meets | fix | size |
|---|---|---|---|---|
| 1 | `nova-self-talk help frobnicate` | prints the full general help text and exits 0, with no signal that the topic is not a verb; an AI cannot tell "verb documented below" from "verb never heard of" | refuse an unknown help topic with the verb list, as the scan path already does | S |
| 2 | `nova-self-talk edge.md` | a line ending `."` swallows the next line into one finding: `She wrote: "I am the best reviewer here."` plus `Keep my weakest instrument in view.` prints one `SELFTALK FAIL edge.md:1: RANKING` whose text spans both lines, flagging a licensed imperative; an AI that edits by the cited line mis-edits | end a sentence at a closing quotation mark and attribute each finding to the physical line its matched words sit on | M |
| 3 | `nova-self-talk --bogus --max abc probe.md` | the refusal names only the first problem (`unknown flag -bogus`) and hides the invalid `--max abc`, which a second run alone reveals; an AI fixes one problem and is re-refused on the next | report every problem in one refusal | S |
| 4 | `nova-self-talk shapes --jsonx` | the verb path keeps raw Go flag wording `flag provided but not defined: -jsonx` while the positional path uses curated `unknown flag -max; the flags are ...`; same defect, two voices | route the verb path through the same curated refusal as the positional path | S |

## Nice-to-haves
| # | where | what | fix | size |
|---|---|---|---|---|
| 1 | `nova-self-talk example ./pages` | the only write has no `--op` flag; idempotency holds only by rerun (`wrote=- kept=RULES.md,journal.md`) | add the `--op` flag to the write | S |
| 2 | `nova-self-talk scan` vs bare `nova-self-talk` | the `scan` verb duplicates the stateless file-list form, and `version`/`help` alias `--version`/`-h`; extra surfaces for one thing | document them as pure aliases or drop the duplicate | S |
| 3 | merged `2>&1` capture | stdout banners/counts and stderr findings interleave in a non-fixed order (stdout block-buffering), so a parser must split streams to get a stable order | print everything to one stream or guarantee cross-stream order | M |

## Top 3 fixes
1. Refuse unknown `help` topics with the verb list (silent success today).
2. End sentences at a closing quote and attribute findings to their physical line.
3. Aggregate every problem into one refusal instead of stopping at the first.

## Good, keep
The refusal style: every bad invocation names the problem and prints the next command, and the second line `NOTHING was scanned, which is not a green` stops the worst misread. The example-to-remedy-to-scan first run with `--dry-run` and `--json` on every offering, and the `shapes` table that publishes each pattern with a finds and a passes sentence so an AI predicts a match without opening code. Non-UTF-8 / binary files now refuse cleanly (`file is not valid UTF-8`, exit 2) instead of a vacuous green, and `version` pins the build identity.

## What could not be tried
None: every verb (scan, shapes, example, version, help) ran for real; the tool touches no service, store or remote, so nothing was unavailable.

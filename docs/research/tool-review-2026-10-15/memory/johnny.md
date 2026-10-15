# nova-memory AI-usability review, 2026-10-15

Reviewer: mercury-3-preview-1002
Build: c0b5300f55cd
Score: 9.5/10 (53/55)

## Rubric
| # | criterion | score | evidence |
|---|---|---|---|
| 1 | cold | 5 | `nova-memory quickstart --root ./corpus` -> `QUICKSTART OK done=3` |
| 2 | refusals | 4 | `nova-memory search --k two lantern` -> `SEARCH REFUSED: invalid value for --k: it wants a whole number (receipts per query, positive (required)); run: nova-memory search -h` |
| 3 | output | 5 | `nova-memory search --root ./corpus --channels bm25 --k 2 --json lantern glazing` -> `{"result":{"verb":"search","status":"ok","exit":0},...}` |
| 4 | dry-run | 5 | `n/a: every verb is an inspection that writes nothing (there is no write to dry-run)` |
| 5 | idempotent | 5 | `n/a: no verb writes, so there is no op to repeat` |
| 6 | no prompts | 5 | `nova-memory check --root ./corpus --channels bm25 --k 1` -> `MEMORY REFUSED: name exactly one candidate file, or - for stdin; refusing to guess` |
| 7 | exit codes | 5 | `nova-memory verify --root ./corpus3 --links gate` -> exit 1 with `VERIFY FAIL gating=2` |
| 8 | one word | 5 | `nova-memory boot --root ./corpus --pin pin.txt` -> `BOOT OK files=1 bytes=60` |
| 9 | push | 5 | `n/a: nothing is waited on; each run builds its index and returns` |
| 10 | no leaks | 5 | `nova-memory verify --root ./corpus3 --links info --frontmatter '**/*.md'` -> `VERIFY FAIL frontmatter notes/a.md: no name: in frontmatter` |
| 11 | true help | 4 | `nova-memory search --root ./corpus --bogus --k two hull` -> `SEARCH REFUSED: unknown flag --bogus; the flags of search are ...` |

## 10/10 blockers
| # | where | what an AI meets | fix | size |
|---|---|---|---|---|
| 1 | `nova-memory search --root ./corpus --bogus --k two hull` | help promises `A refusal reports every flag it can see at once`, but this stops at `unknown flag --bogus` and never names that `two` is not a whole number; same gap in `nova-memory verify --root ./corpus --links maybe --coverage bad` (only `--links` named) | collect every parse and value error before refusing, and print them all in one run | S |
| 2 | `nova-memory check --root ./corpus --channels bm25 --k 1 draft.md` | the check verb prints `MEMORY OK` / `MEMORY CAL` / `MEMORY HIT`, so the line prefix is not the verb's name; every other verb's prefix matches its name (`SEARCH`, `STATS`, `VERIFY`, `EVAL`, `BOOT`) | print `CHECK` for the check verb's lines (help's example line updates with it) | S |
| 3 | `nova-memory search --root ./corpus --channels bm25,bm25 --k 1 lantern` | a repeated channel is accepted and silently doubles fusion: `fused=0.03333` with `channels=bm25,bm25` | refuse a repeated or duplicate channel with the unknown-channel wording | S |
| 4 | `nova-memory stats --root ./corpus --root ./corpus/notes` | overlapping roots index the same files twice with no notice: `files=4 chunks=4` for a two-file tree | detect and refuse overlapping or repeated `--root`, naming both | S |
| 5 | `nova-memory quickstart --root ./corpus` | the demo prints `nova-memory check --root ./corpus --channels bm25 --k 2 -`, which needs stdin; run alone it refuses `- holds no candidate paragraph`, so the printed line is not runnable as printed | print the demo check with a `--draft`-style file or show the pipe feeding stdin | S |

## Nice-to-haves
| # | where | what | fix | size |
|---|---|---|---|---|
| 1 | `nova-memory stats --root ./corpus` | `build=...` timing varies every run, so the line and JSON cannot be diffed | move the timing to a NOTE line or drop it from facts | S |
| 2 | `nova-memory boot -h` | root flag described as `memory root directory` while every other verb says `corpus root directory` | one wording for `--root` | S |
| 3 | `nova-memory check --root ./corpus --channels bm25 --k 1 draft.md` | a hit well below the CAL line is not marked weak, so a reader may misread a low score as relevant | add a below-cal marker on hits at or under CAL | M |

## Top 3 fixes
1. Make refusals report every problem at once (blocker 1), the largest gain on refusals and the help's own claim.
2. Rename the check verb's output prefix from MEMORY to CHECK (blocker 2), so parseability is uniform across verbs.
3. Refuse duplicate/overlapping `--channels` and `--root` (blockers 3, 4), since silent double-counting is the worst of the rough edges.

## Good, keep
Quickstart prints the exact argv each step ran, so a first run teaches the real invocations; the fixture example runs verbatim.
Exit codes are meaningful and match help: search with no hit exits 0, verify/eval findings exit 1, bad invocation exits 2.
Every verb accepts `--json` (a refusal included) with the same `result`/`facts`/`items` shape as the typed lines, so the two renderings cannot drift.

## What could not be tried
All eight verbs (version, quickstart, stats, search, check, verify, eval, boot) ran read-only with no live service required. A very large corpus and a filesystem under permission errors were not tried.

# nova-tokens dogfood — antigravity (zhi), 2026-10-06

Read as a stranger: only `nova-tokens -h`, `nova-tokens help`, `nova-tokens
<verb> -h` and the page under `docs/`. Built from the current tip of
`sprint/mechanical-2026-10-02`, 1c05149f60a74e73707c4eb6325b84194648ffcf, and
used as `nova-tokens v1.0.1-0.20261007140251-1c05149f60a7 linux/amd64 go1.26.6`:
the `setup:` and `example:` lines run as printed in a scratch dir, every
store-free verb once, the refusals and boundaries too. No code changed; a
finding is recorded, never fixed here.

## Findings

1. `nova-tokens fold --out ./out --day 2026-09-11 --repos ./repos.tsv --claude bench=./transcripts`,
   then `nova-tokens session --claude-session ./session.jsonl --out ./out`, then
   `nova-tokens sum --out ./out --month 2026-09` — the printed `setup:` (which
   runs `cp ./transcripts/window.jsonl ./session.jsonl`) and the two printed
   `example:` blocks, run as printed.
   Printed (first 3 lines of the second `sum`):
   ```
   SUM MONTH month=2026-09 at=2026-10-07T14:07:03Z build=v1.0.1-0.20261007140251-1c05149f60a7 days=1 first=2026-09-11 last=2026-09-11 missing=0 rows=2 turns=1
   SUM PAIR model=claude-fable-5-1 repo=schema input=812 output=40 cache_write=1200 cache_read=90000 reasoning=- rough=0 dashes=0,0,0,0,1 nonutc=0 days=1
   SUM PAIR model=claude-fable-5-1 repo=unattributed input=812 output=40 cache_write=1200 cache_read=90000 reasoning=- rough=0 dashes=0,0,0,0,1 nonutc=0 days=1
   ```
   Its later lines read `SUM MODEL model=claude-fable-5-1 input=1624 output=80
   cache_write=2400 cache_read=180000 ...` and `SUM TOTAL input=1624 output=80
   cache_write=2400 cache_read=180000 ...`, and the day file holds two rows for
   the same `example-1` message (`claude:bench` and `claude-session`).
   Expected: the transcript holds one message (`example-1`, input 812), so
   following the tool's own printed example should report 812 once, or refuse
   and name the shared id (as two declared sources of one provider sharing ids
   already are). Instead the `session` step folds the copy `setup:` made and the
   day doubles every type; `check` then prints `CHECK OK ... rows=2` over the
   doubled day.
   Grade: URGENT.

2. The status word is a short form of the verb, not the tool:
   `nova-tokens fold`
   Printed (first 3 of 4 lines):
   ```
   TOKENS REFUSED: --out is required; it wants the directory the day files are written to; refusing to guess; run: nova-tokens help
   TOKENS REFUSED: one of --day <YYYY-MM-DD> or --all is required; it wants one UTC day as YYYY-MM-DD, or --all for every day the sources name; refusing to guess; run: nova-tokens help
   TOKENS REFUSED: --repos is required; it wants a file of <name><TAB><regexp> lines, in priority order, naming your repos; refusing to guess; run: nova-tokens help
   ```
   `nova-tokens` (bare)
   ```
   TOKENS REFUSED: no verb given; the verbs are fold, report, ledger, sum, check, sources, profiles, session, version, and sources is the one that only looks; run: nova-tokens help
   ```
   `nova-tokens check`
   ```
   CHECK REFUSED: --out is required; it wants the directory the day files are written to; refusing to guess; run: nova-tokens help
   ```
   `nova-tokens sum`
   ```
   SUM REFUSED: --out is required; it wants the directory the day files are written to; refusing to guess; run: nova-tokens help
   SUM REFUSED: --month is required; it wants one month as YYYY-MM; refusing to guess; run: nova-tokens help
   ```
   All four exit 2 with stdout empty.
   Expected: the door grammar its own help states, `<tool>[ <verb>] REFUSED:`,
   and docs/STANDARD.md section 2 ("the status word leads every line"): the tool
   identifier should lead — `nova-tokens REFUSED:`, `nova-tokens check
   REFUSED:`, `nova-tokens sum REFUSED:` — so a reader matching a line to the
   tool does not have to translate `TOKENS`/`CHECK`/`SUM` back.
   Grade: NEXT.

3. `nova-tokens -h`
   Printed (first 3 lines):
   ```
   nova-tokens: token spend per day, model and repository, read from AI session logs

   how it works: fold reads the logs you name (Claude Code transcripts, OpenCode
   ```
   `nova-tokens -h | sed -n '128p;140p'`
   Printed:
   ```
   setup:
   example:
   ```
   Line 128 of the 161-line output is `setup:`; the first runnable `example:`
   line is line 140, after ~130 lines of prose, while the `first run:` line at
   line 8 already points at it ("the lines under setup: above example:").
   Expected: the first run near that `first run:` line, so a stranger (or an AI)
   reading top-down meets at least one runnable command on the first screen
   instead of after the whole essay.
   Grade: NEXT.

4. `nova-tokens fold --out ./out --day 2026-09-11 --claude bench=./transcripts`
   (the `fold` with no `--repos` case)
   ```
   TOKENS REFUSED: --repos is required; it wants a file of <name><TAB><regexp> lines, in priority order, naming your repos; refusing to guess; run: nova-tokens help
   ```
   `nova-tokens session --claude-session ./missing.jsonl`
   ```
   TOKENS REFUSED: cannot read ./missing.jsonl: open ./missing.jsonl: no such file or directory; run: nova-tokens session -h
   ```
   Both exit 2 with stdout empty.
   Expected: one remedy target. The verb-specific `run: nova-tokens session -h`
   (or `nova-tokens fold -h`) is the useful one and should end every refusal of
   those verbs, not the generic `run: nova-tokens help` most of them carry.
   Grade: NEXT.

## What the tool got right

- The store-free verbs all ran from the scratch fixture: `fold`, `check`, `sum`,
  `sources` (with and without `--unattributed`), `report`, `session` and
  `profiles`; the day file is a readable TSV with a source on every row and a
  dash (never 0) for a type no source reported.
- `check` and `sum` take `--json` and the JSON carries the same values as the
  lines (the doubled `input=1624` is in both); the writing verbs take
  `--dry-run`, and a dry `fold` prints `written=false would_write=true` and
  `dry_run=true` while changing nothing.
- Missing inputs are named all at once with what each wants and its unit
  (`sum` names `--out` and `--month` in one run), and `--scratch` without
  `--opencode` is refused with the reason.

Not run: `ledger`'s write path needs a live store and the card forbids starting
one, so it was run only against an unreachable address (`LEDGER FAILED
month=2026-08 days=0 rows=0 bad=0`, exit 1); `--opencode` was not used (no
database on the bench).

READ 7/10 — the banner answers what it does, how it works and where its state
lives, and every verb's `-h` is complete, but the runnable example is buried
near the end of the banner.

USE 7/10 — the first run is complete and honest about dashes, but the printed
example's session step doubles the day, which is the one thing a first run must
not do.

urgent=1 next=3

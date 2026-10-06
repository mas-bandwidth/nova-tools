# nova-tokens dogfood — antigravity (zhi), 2026-10-06

Read as a stranger: only `nova-tokens -h`, `nova-tokens help`, `nova-tokens
<verb> -h` and the page under `docs/`. Built from the staged checkout at
927b3a9496f0cf65abea12db73b7971348c48ec1 and used as
`nova-tokens v1.0.1-0.20261006193659-927b3a9496f0 darwin/arm64 go1.27.1`: the
`setup:` lines and the `example:` lines run as printed in a scratch dir, every
store-free verb once, the refusals and boundaries too.

## Findings

1. URGENT — the printed setup plus the two `example:` blocks count one message
   twice. The setup copies the only transcript to the session file
   (`cp ./transcripts/window.jsonl ./session.jsonl`); the first block folds it
   as `claude:bench`, and the second block's
   `nova-tokens session --claude-session ./session.jsonl --out ./out` folds the
   same message again as `claude-session`. The day file then holds two rows for
   the same model, and:
   ```
   nova-tokens sum --out ./out --month 2026-09
   SUM TOTAL input=1624 output=80 cache_write=2400 cache_read=180000 ... turns=1 pairs=2 models=1
   ```
   The transcript's own numbers are input=812, output=40, cache_write=1200,
   cache_read=90000, and the same message id (`example-1`) is in both rows. I
   expected the day to stay at those numbers (the session's spend is the
   transcript's), or a refusal naming the shared id as the two-declared-sources
   case already is; instead following the tool's own printed example inflates
   every total by 100%. A caller who folds their transcripts and then their own
   session meets the same doubling in real use.
   Grade: URGENT.

2. The status word names a short form, not the tool: `TOKENS REFUSED: ...` for
   the bare and unknown-verb refusals, `CHECK REFUSED: ...`, `SUM REFUSED:
   ...`, `SESSION ...`, while the banner opens `nova-tokens:` and other tools
   lead with their own name (`nova-ci REFUSED:`). The one grammar in
   docs/STANDARD.md section 2 wants `nova-tokens <verb> REFUSED: ...`.
   Grade: NEXT.

3. The runnable `example:` block sits at line 128 of `-h`, after about a hundred
   lines of prose, so a stranger (or an AI) reading the banner top-down meets the
   commands only after the whole essay. The `setup:` block is also ten
   `printf`/`>>` lines building one JSON line, awkward to paste. I expected the
   first run near the `first run:` line the banner already names.
   Grade: NEXT.

4. The remedy target is inconsistent between refusals: most end `run:
   nova-tokens help` (e.g. `fold` with no `--repos`), while
   `session --claude-session ./missing.jsonl` ends `run: nova-tokens session -h`;
   the verb-specific one is the more useful and should be everywhere.
   Grade: NEXT.

## What the tool got right

- The store-free first run works: `fold`, `check`, `sum`, `sources`, `report`
  and `session` all ran from the scratch fixture; the day file is a readable TSV
  with a source on every row and a dash (never 0) for a type no source reported.
- Every verb takes `--json` (the same value as the lines) and every writing verb
  `--dry-run`; `check --out ./out --json` is byte-consistent with its lines.
- Missing inputs are named all at once with what each wants and its unit, and
  `--scratch` without `--opencode` is refused with the reason.

READ 7/10 — the banner answers what it does, how it works and where its state
lives, but the prose before the example is long and the example is late.

USE 7/10 — the first run is complete and honest about dashes, but the printed
example's session step doubles the day, which is the one thing a first run must
not do.

urgent=1 next=3

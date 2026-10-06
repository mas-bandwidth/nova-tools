# nova-decide dogfood — antigravity (zhi), 2026-10-06

Read as a stranger: only `nova-decide -h`, `nova-decide help`, `nova-decide <verb> -h` and the page under `docs/`. Built from the staged checkout at
c02c00769c229a6e012b0dff03ef8929016f3b23 and used as
`nova-decide v1.0.1-0.20261006191814-c02c00769c22 darwin/arm64 go1.27.1`: the
`example:` block run as printed (in a scratch dir, with the fixture paths made
absolute), then every other verb once, the refusals and the boundary calls too.

## Findings

1. The status word names the verb in upper case, not the tool: `nova-decide`
   bare prints `DECIDE REFUSED: no verb given; ...` and the verbs print
   `ASK REFUSED:` / `READ REFUSED:` / `GATE OK ...`. The banner opens
   `nova-decide:` and other tools lead with their own name (`nova-ci REFUSED:`),
   so a reader matching lines to the tool has to translate. I expected
   `nova-decide REFUSED:` and `nova-decide ask REFUSED:` (the grammar in
   docs/STANDARD.md section 2, "the status word leads every line").
   Grade: NEXT.

2. Input errors carry the generic remedy `run: nova-decide help` where the want
   is knowable.
   - `nova-decide read --card /nonexistent.md --diff ... --backend fixed --answers ... --record ./d2.jsonl`
     ``` READ REFUSED: open /nonexistent.md: no such file or directory; run: nova-decide help ```
   - `nova-decide score-grades --record ... --log <a gate output> --day 2026-10-06`
     ``` SCORE-GRADES REFUSED: <file>: the log is not a nova-sprint log --json export: invalid character '-' in numeric literal; run: nova-decide help ```
   I expected the refusal to name what the input WANTS (a card file that exists;
   a `nova-sprint log --json` export) as the missing-flag refusals do, so a cold
   reader fixes the call without re-reading the whole banner.
   Grade: NEXT.

## What the tool got right

- The `example:` block is real: all twelve lines ran as printed (only the
  fixture paths made absolute), each against a scratch `--record`.
- `ask` with no flags names every missing flag at once, each with what it wants
  and its unit; `--backend nope` and `--nosuchflag` name the alternatives.
- Every write verb has `--dry-run` (`recorded=no dry_run=true`) and every verb
  takes `--json`; the JSON is the same value as the lines.
- `outcome` on a record that already holds the id exits 1 with the reason
  (`decision card-1 is labelled ok already ...; an outcome is attached once`).
- `gate` routes by the bar over the class answer: with the fixture's
  class=flaky at p=0.86 the default bar leaves it `route=caused`, while the
  `GATE FAILURE` line still prints the class probabilities, so the two fields
  are not in conflict (the route is the bar's judgement, the class is the
  backend's).
- `calibrate` and `findings` read a hand-written record and print the AUC, the
  bars and the finding classes.

READ 9/10 — the banner answers what it does, how it works in five lines and
where its state lives, and the example block is the best first run of the tools
read so far; only the upper-case verb prefixes keep it off a 10.

USE 9/10 — the example block and every verb's `--dry-run`/`--json` let a
stranger use the whole tool with no store and no backend; the generic
`run: nova-decide help` on an input error is the one stumble.

urgent=0 next=2

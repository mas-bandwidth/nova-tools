# nova-decide dogfood — dsh (zhi), 2026-10-06

Read as a stranger: only `nova-decide -h`, `nova-decide help`, `nova-decide help <verb>` and the page under `docs/` (docs/SPEC-NOVA-DECIDE.md, read whole).
Built from the staged checkout at 6ec8bb02edc83283630f2e10e21bd035b3336f65 and
used as `nova-decide devel darwin/arm64 go1.26.6` (built on a bench; the
binary run on the working machine): the `example:` block pasted exactly as
printed from the repo root, then every verb once with its real flags against a
scratch record in a scratch dir — ask (with a bad schema, a bad answers file,
an unknown backend, `--json`, `--dry-run`, state on stdin, op replay and op
reuse on another state), read (with and without `--rule`, op replay), score,
attempt (with and without `--result`), grade (with `--examples`, `--held-out`,
`--seed`), gate (with `--base-red`, `--bars`), brief (a card, a directory,
`--max`), outcome (attach, same label again, a conflicting label at exit 1),
calibrate (on scratch and on the fixture record, the no-negative refusal, a
hand-edited record), import (all three kinds, twice each, a bad `--log`
shape), score-grades, findings (`--bar`, `--since`, a `--shadow`/`--real`
join), version — the refusals and the exit codes too. No `JEV_API_KEY` here,
so jev was exercised only through its key refusal.

## Findings

1. `score-grades` is missing from the tool's own page.
   - `nova-decide help score-grades`
     ``` usage: nova-decide score-grades [flags] from `nova-decide help`: nova-decide score-grades --record <file> --log <file> [--day <date>] ```
     and the description ends "See docs/SPEC-NOVA-DECIDE.md section 11."
   - What I expected: the page under `docs/` documents every verb; section 11
     is the grade decision and its few-shot examples, and no section of
     docs/SPEC-NOVA-DECIDE.md names `score-grades`, so a stranger reading the
     page never learns the verb exists (I found it only in the usage block).
   - Grade: NEXT.

2. Missing flags print one REFUSED line per flag, not one line holding every
   problem.
   - `nova-decide ask --backend fixed --answers answers.json --record decisions.jsonl --op p3`
     ``` ASK REFUSED: --schema is required; it wants the decision's schema, a JSON file; refusing to guess; run: nova-decide help ASK REFUSED: --state is required; it wants the text the decision is made over, a file, or - for stdin; refusing to guess; run: nova-decide help ```
     (exit 2)
   - What I expected: the page's section 7, "A refusal is one line, `<VERB> REFUSED: <every problem, each with what it wants>; run: <remedy>`"; both
     problems were named and the exit was right, but a reader that takes one
     REFUSED line per run stops at the first.
   - Grade: NEXT.

3. `score-grades --log`'s cost-record shape is undiscoverable from nova-decide,
   so every card stays `no_log`.
   - `nova-decide score-grades --record decisions.jsonl --log log.json --day 2026-10-06`
     with `log.json` = `{"lines":[{"card":"c1","verb":"cost","tier":"flash","landed":true}]}`
     and again with `{"lines":[{"card":"c1","verb":"finish","tier":"flash","outcome":"landed","attempt":1}]}`
     ``` SCORE-GRADES OK day=2026-10-06 decisions=2 cards=2 no_log=2 ```
     (exit 0, both shapes)
   - What I expected: the help names the fields a cost record carries (the way
     `import --log`'s refusal prints its shape) or a wrong shape is refused;
     as shipped, two plausible shapes parse and every card is counted `no_log`
     with no pointer to what would count.
   - Grade: NEXT.

4. The banner's how-it-works lines are compressed past readability.
   - `nova-decide -h`
     ``` a decision is a named schema of choice or noul questions over a state; jev or fixed answers: schema {"name":"q","questions":{"ok":{"type":"noul","instructions":"It asks."}}} state R? fixed answers {"ok":{"noul":0.9}} print ASK OK id=f decision=q backend=fixed recorded=new ```
   - What I expected: the how-it-works block to name the tool's nouns in plain
     prose (the standard's point 6); `state R?` reads as a typo and the run-on
     sample line took several reads to see which sample answers which noun. The
     usage lines and the `example:` block recover it, and those run as printed.
   - Grade: NEXT.

5. A bad `--log` for `import --judgments` names its shape only as a Go struct
   literal.
   - `nova-decide import --record decisions.jsonl --judgments judgments --log log.json`
     with `log.json` = `[{"id":"j1","card":"dogfood-x","verb":"rework","answers":["j1"],"at":"2026-10-06T18:00:00Z"}]`
     ``` IMPORT REFUSED: --log log.json is not a nova-sprint log --json export ({"lines": [...]}): json: cannot unmarshal array into Go value of type struct { Lines []struct { Verb string "json:\"verb\""; Answers []string "json:\"answers\"" } "json:\"lines\"" }; run: nova-decide help ```
     (exit 2)
   - What I expected: the shape is named (`{"lines": [...]}`) and the retry
     worked, but the fields read are `verb` and `answers`, which the line could
     say in words; the Go type literal is noise for a stranger who does not read
     Go.
   - Grade: NEXT.

## What held

Every checked behavior matched the page: op replay (`recorded=existing`), op
reuse on another state refused, `outcome` attach once (`changed=false` again),
a conflicting label at exit 1, import idempotent (`existing=1` the second
run), the few-shot refusal naming the short class, calibrate refused with no
negative, the hand-edited record refused by file, line and question
(`hand5.jsonl:27: question defect gives yes the probability 1.5, outside [0, 1]`), the gate routing a no-test package failure `caused` unasked, jev's
key refusal naming `nova-secrets exec`, unknown verb and flag each answered
with the names there are, and `--json` the same value as the lines.

READ 8/10 — the page documents every concept precisely, states its
calibration numbers and their limits (uncalibrated marked on every line), and
every refusal taught me the shape it wanted; minus one for the missing
`score-grades` section and the dense banner lines.

USE 9/10 — every verb ran first try from the help alone against the fixed
backend with no store and no key, and replay and idempotence held on every
write; the one stumble is `score-grades` needing another tool's export shape
to leave `no_log`.

urgent=0 next=5

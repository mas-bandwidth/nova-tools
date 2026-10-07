# nova-decide dogfood, 2026-10-06 (grok)

Read from the tool's own help and its page under `docs/` (`nova-decide -h`,
`nova-decide help`, `nova-decide help <verb>`, `docs/SPEC-NOVA-DECIDE.md`), then
used for real: the `example:` block run as printed (in a scratch dir, the fixture
paths made absolute), then every verb at least once with its real flags against
scratch records and scratch files, the refusals and the boundaries too. Built
from the checkout at `e8f70f600` and used as
`nova-decide v1.0.1-0.20261007032034-e8f70f600ebf linux/amd64 go1.26.6` on a
Linux bench (the fixed backend, no key, no network, no store). No code changed;
a finding is recorded, never fixed here.

## Findings

1. The banner promises `--max` on every listing verb; `findings` refuses it.
   - `nova-decide findings --record cmd/nova-decide/testdata/record.jsonl --since 2026-10-01 --max 1`
     ``` FINDINGS REFUSED: unknown flag --max; the flags of findings are --bar, --heavy, --json, --read-shadow, --real, --record, --shadow, --since; run: nova-decide findings -h ```
     (exit 2)
   - What I expected: `findings` prints item lines (`FINDINGS FINDING class=...`),
     so it is a verb that lists; the banner says "A verb that lists takes `--max
     <n>` (default 20, 0 lists all) and says `MORE` for the rest", and `brief`
     honours exactly that on a directory. Either `findings` takes `--max` and
     prints a `MORE` line, or the banner scopes the promise to the verbs that
     have it. A banner that promises a flag the verb refuses is help that lies.
   - Grade: URGENT.

2. A named record that does not exist is read as empty and reported OK.
   - `nova-decide findings --record ./does-not-exist.jsonl --since 2026-10-01`
     ``` FINDINGS OK scored=0 classes=0 bar=0.5 since=2026-10-01T00:00:00Z ```
     (exit 0)
   - `nova-decide score-grades --record ./does-not-exist.jsonl --log cmd/nova-decide/testdata/record.jsonl`
     ``` SCORE-GRADES OK day=2026-10-06 decisions=0 cards=0 no_log=0 ```
     (exit 0)
   - What I expected: a named input that is absent is refused, by file, the way
     `outcome --id nope` refuses an id the record does not hold (`OUTCOME
     REFUSED: nope: no decision with that id in the record; ...`, exit 2) or
     `ask` refuses an empty state. The standard is "no silent fallback, no
     default that stands in for a missing input", and a reading verb that says
     `OK scored=0` for a file that is not there is a wrong result: a caller
     reads an absent record as an empty one and calibrates nothing.
   - Grade: URGENT.

3. `outcome --dry-run` says OK where the same call really exits 1.
   - `nova-decide outcome --record ./decisions.jsonl --id first --label wrong --dry-run`
     ``` OUTCOME OK id=first label=wrong recorded=no dry_run=true ```
     (exit 0)
   - The same call without `--dry-run`:
     ``` OUTCOME FAILED id=first: decision first is labelled ok already (at 2026-10-07T03:36:39Z), not wrong; an outcome is attached once ```
     (exit 1)
   - What I expected: the dry run prints the plan "the real run would take, from
     the same code path" (the banner and the page both say so), so a conflicting
     label is a plan that refuses and exits 1, not an `OK` with `recorded=no`.
     As shipped the dry run is a false green a script can branch on.
   - Grade: URGENT.

4. A backend answer failure's remedy names a command that does not exist.
   - `nova-decide ask --schema cmd/nova-decide/testdata/schema.json --state cmd/nova-decide/testdata/state.txt --backend fixed --answers ./bad-p.json --record ./r.jsonl --op badp`
     with `bad-p.json` holding `{"asks_something":{"noul":1.5},...}`:
     ``` ASK FAILED id=badp backend=fixed: the backend's answers do not fit the schema: asks_something gives yes the probability 1.5, outside [0, 1]; run: make --answers answer every question of the schema ```
     (exit 2)
   - Running the named remedy:
     ``` make: unrecognized option '--answers' ```
     (make prints its whole usage block; exit 2)
   - What I expected: a remedy a cold reader can act on in one turn. `bad-p.json`
     is the caller's own file, so the fix is to correct the probability there
     (or `run: nova-decide ask -h`); `make --answers ...` is neither a target
     (the Makefile has none) nor a runnable command. The same string ends every
     schema-check failure (`ask` with an unknown option, `ask` with an empty
     state), so the wrong breadcrumb is on the whole failure path.
   - Grade: URGENT.

5. `read` records `LAND` on an empty diff, where `ask` refuses an empty state.
   - `nova-decide read --card cmd/nova-decide/testdata/card.md --diff /dev/null --backend fixed --answers cmd/nova-decide/testdata/read-answers.json --record ./decisions.jsonl --op empty-diff`
     ``` READ OK id=empty-diff decision=read backend=fixed verdict=LAND p=0.92 tokens_in=0 tokens_out=0 recorded=new ```
     ``` READ ANSWER question=defect type=noul value=no p=yes:0.04 ```
     (exit 0, the decision written)
   - What I expected: the same evidence rule `ask` enforces (`ASK FAILED ...
     the state is empty; a decision is made over evidence`, exit 2). A diff of
     zero lines is no diff, and a `LAND` recorded from it trains the record on
     a decision no evidence supports; the card alone is not the read's state.
   - Grade: NEXT.

6. A choice's probabilities are not held to a distribution or to its own value.
   - `nova-decide ask --schema cmd/nova-decide/testdata/schema.json --state cmd/nova-decide/testdata/state.txt --backend fixed --answers ./skew.json --record ./r.jsonl --op skew`
     with `skew.json` holding `{"kind":{"choice":"request","p":{"request":0.1,"report":0.9,"question":0.9}}}`:
     ``` ASK OK id=skew decision=reply backend=fixed tokens_in=0 tokens_out=0 recorded=new ```
     ``` ASK ANSWER question=asks_something type=noul value=yes p=yes:0.9 ```
     ``` ASK ANSWER question=kind type=choice value=request p=question:0.9,report:0.9,request:0.1 ```
   - What I expected: `Schema.Check` to refuse an answer whose probabilities do
     not sum to 1, or that gives the chosen option less than another (the page's
     section 2 says a choice is "answered with the chosen option and a
     probability per option", and section 7 makes the value the routed field).
     As shipped a self-contradictory answer is recorded, and every reader of the
     line has to notice by hand.
   - Grade: NEXT.

7. `score-grades`' cost-record shape is undiscoverable, so a plausible log
   scores nothing.
   - `nova-decide score-grades --record ./grades.jsonl --log ./log1.json --day 2026-10-07`
     with `log1.json` = `{"lines":[{"card":"c1","verb":"cost","tier":"flash","landed":true}]}`:
     ``` SCORE-GRADES OK day=2026-10-07 decisions=1 cards=1 no_log=1 ```
     (exit 0)
   - and with `log2.json` = `{"lines":[{"card":"c1","verb":"finish","tier":"flash","outcome":"landed","attempt":1}]}`:
     ``` SCORE-GRADES OK day=2026-10-07 decisions=1 cards=1 no_log=1 ```
     (exit 0)
   - What I expected: the help (or a bad-shape refusal) to name the fields a
     cost record carries, the way `import --log`'s refusal prints the shape it
     wants. Both plausible shapes parse, every card is counted `no_log`, and
     nothing points at what would count, so the verb can silently do nothing on
     a log a stranger believes is right.
   - Grade: NEXT.

8. `calibrate` on a record that is not there blames the decision, not the file.
   - `nova-decide calibrate --record ./does-not-exist.jsonl --decision read --question defect --positive wrong --negative ok`
     ``` CALIBRATE REFUSED: the record holds no decision named read; run: nova-decide help ```
     (exit 2)
   - What I expected: the cause named (`open ./does-not-exist.jsonl: no such
     file or directory`) with the verb's own help, as finding 2 wants for the
     reading verbs. "The record holds no decision named read" is a different
     fact from "the record is not there", and it sends the reader to look for a
     decision that was never the problem.
   - Grade: NEXT.

## What held

The `example:` block is real: all ten lines ran as printed (only the fixture
paths made absolute), each against a scratch record. Every verb took `--json`
and it carried the same values as the lines (`ask`, `brief`, `findings`,
`calibrate` compared). Op replay returns `recorded=existing` with no new line;
op reuse over another state is refused naming the id; `outcome` attached once
(`changed=true`, then `changed=false`) and a second, different label on the real
run exits 1 naming both. `import` read two `VERDICT.md`, one `REPORT.md` with
`Verdict: HOLD`, and a `--judgments`/`--log` pair in one call
(`verdict_new=2 judgment_new=1 report_new=1 unanswered=1`), was idempotent on the
second run, and `--dry-run` wrote nothing. `brief` on a directory of three with
`--max 2` printed the `MORE kind=card shown=2 total=3` line. `calibrate` printed
the counts, the AUC, the three `BAR` lines and `CATCH-ALL`; `findings` clustered
the fixture record by class with `unnamed`; `gate` read both failures, routed
them, and refused a green output. `help` exits 0 and every verb's `-h` prints
without reading anything; an unknown flag names every flag the verb has, and an
unknown verb names every verb; missing flags are refused one line each with what
each wants.

READ 8/10 — the page under `docs/` is the best part: every noun, the record's
shape, each decision's table and the calibration numbers are stated precisely,
and every refusal I provoked taught me the shape it wanted; it loses two for the
banner `--max` promise it does not keep and the dense four-line how-it-works.

USE 7/10 — a stranger can run the whole tool from the help with the fixed
backend and no store, and replay, idempotence and `--json` hold; it loses three
for the silent empty record, the dry run that promises what the real run
refuses, and the `make --answers` remedy on the backend failure path.

urgent=4 next=4

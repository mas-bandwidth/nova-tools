# nova-decide dogfood, 2026-10-06 (opencode-2)

Read from the tool's own help (`nova-decide -h`, `nova-decide help`,
`nova-decide help <verb>`, `nova-decide <verb> -h`) and its page under `docs/`
(`docs/SPEC-NOVA-DECIDE.md`), then used for real: the `example:` block run as
printed (in a scratch dir, the fixture paths made relative to it), then every
verb at least once with its real flags against scratch records and scratch
files, the refusals and the boundaries too. Built from the checkout at
`abb9bfecc` and used as
`nova-decide v1.0.1-0.20261007153756-abb9bfecc729 linux/amd64 go1.26.6` on a
Linux bench (the fixed backend, no key, no network, no store). No code changed;
a finding is recorded, never fixed here.

## Findings

1. The banner promises `--max` on every listing verb; `findings` refuses it.
   - `nova-decide findings --record ./testdata/record.jsonl --since 2026-10-01 --max 1`
     ``` FINDINGS REFUSED: unknown flag --max; the flags of findings are --bar, --heavy, --json, --read-shadow, --real, --record, --shadow, --since; run: nova-decide findings -h ```
     (exit 2)
   - What I expected: `findings` prints one `FINDING` line per class, so it is a
     verb that lists; the banner says "A verb that lists takes `--max <n>`
     (default 20, 0 lists all) and says `MORE` for the rest", and `brief`
     honours exactly that on a directory of cards. Either `findings` takes
     `--max` and prints a `MORE` line, or the banner scopes the promise to the
     verbs that have it. A banner that promises a flag the verb refuses is help
     that lies.
   - Grade: URGENT.

2. An answer to a question the schema never asked is accepted and silently
   dropped.
   - `nova-decide ask --schema ./testdata/schema.json --state ./testdata/state.txt --backend fixed --answers ./extra-q.json --record ./ev.jsonl --op ev-extra`
     with `extra-q.json` the fixture answers plus `"not_asked":{"noul":0.5}`:
     ``` ASK OK id=ev-extra decision=reply backend=fixed tokens_in=0 tokens_out=0 recorded=new ```
     ``` ASK ANSWER question=asks_something type=noul value=yes p=yes:0.9 ```
     (exit 0)
   - A noul answer with a field it does not have is accepted the same way:
     with `{"noul":0.9,"p":{"x":1}}` for `asks_something` the run is `ASK OK ...
     recorded=new`, exit 0. And `read` accepts an extra `"invented":{"noul":0.5}`
     in the same run.
   - What I expected: the page and the verb's help say a backend's answers are
     held to the schema and "nothing that was not asked" is allowed, and the
     page ends "An answer that does not fit is a failure, never repaired". The
     same check refuses an unasked option of an asked choice (`kind gives a
     probability to "other", not one of its options`, exit 2), so it knows the
     rule for a map's keys and misses it for the answers' keys and a noul's
     fields. A decision recorded from a backend that answered another schema is
     a wrong result, and the extra answer is data the caller never learns was
     dropped.
   - Grade: URGENT.

3. A named input that is not there is read as empty and reported OK.
   - `nova-decide findings --record ./nope.jsonl --since 2026-10-01`
     ``` FINDINGS OK scored=0 classes=0 bar=0.5 since=2026-10-01T00:00:00Z ```
     (exit 0)
   - `nova-decide score-grades --record ./nope.jsonl --log ./imp/log.json --day 2026-10-07`
     ``` SCORE-GRADES OK day=2026-10-07 decisions=0 cards=0 no_log=0 ```
     (exit 0)
   - `nova-decide import --record ./ev.jsonl --judgments ./nodoes --log ./imp/log.json`
     ``` IMPORT OK verdict_new=0 verdict_existing=0 judgment_new=0 judgment_existing=0 report_new=0 report_existing=0 unanswered=0 ```
     (exit 0), and `--verdicts ./does-not-exist.md` is the same `IMPORT OK` with
     every count 0.
   - What I expected: a named input that is absent is refused, by file, the way
     `ask` refuses a missing state (`open ./nope.txt: no such file or
     directory`, exit 2) or `brief` refuses a missing card directory (`stat
     ./nodir: no such file or directory`, exit 2). The standard is "no silent
     fallback": a caller reads an absent record or an absent judgments directory
     as an empty one and calibrates or imports nothing, with no line saying so.
   - Grade: URGENT.

4. Every backend-answer failure carries a remedy that is not a command.
   - `nova-decide ask --schema ./testdata/schema.json --state ./testdata/state.txt --backend fixed --answers ./bad-p.json --record ./t1.jsonl --op bp`
     with `asks_something` at probability 1.5:
     ``` ASK FAILED id=bp backend=fixed: the backend's answers do not fit the schema: asks_something gives yes the probability 1.5, outside [0, 1]; run: make --answers answer every question of the schema ```
     (exit 2)
   - Running the named remedy:
     ``` make: unrecognized option '--answers' ```
     (make prints its whole usage block; exit 2)
   - The same string ends the missing-answer failure (`the answers file has no
     answer to kind; run: make --answers ...`) and the empty-state failure
     (`the state is empty; a decision is made over evidence; run: make
     --answers ...`), so the wrong breadcrumb is on the whole failure path.
   - What I expected: a remedy a cold reader can act on in one turn. `bad-p.json`
     is the caller's own file, so the fix is to correct the number there (or
     `nova-decide ask -h`); for an empty state no answers file is the fix at
     all. `make --answers` is neither a target nor a runnable command.
   - Grade: URGENT.

5. `grade --held-out` and `--seed` are silently ignored without `--examples`.
   - `nova-decide grade --brief ./testdata/card.md --held-out ./nope.txt --backend fixed --answers ./testdata/grade-answers.json --record ./t3.jsonl --op g2`
     ``` GRADE OK id=g2 decision=grade backend=fixed grade=flash p=0.71 tokens_in=0 tokens_out=0 recorded=new ```
     ``` GRADE ANSWER question=grade type=choice value=flash p=flash:0.71,pro:0.08,script:0.21 ```
     (exit 0)
   - What I expected: the help describes both flags as "(with --examples)". A
     flag that depends on another is refused by name, never dropped in silence;
     as shipped a caller who believes the held-out cards were left out grades a
     state that still holds them.
   - Grade: NEXT.

6. `read` records LAND on a card and a diff that are both empty.
   - `nova-decide read --card ./blank.md --diff ./blank.diff --backend fixed --answers ./testdata/read-answers.json --record ./t9.jsonl --op rc`
     (both files zero bytes):
     ``` READ OK id=rc decision=read backend=fixed verdict=LAND p=0.92 tokens_in=0 tokens_out=0 recorded=new ```
     ``` READ ANSWER question=defect type=noul value=no p=yes:0.04 ```
     (exit 0, the decision written)
   - What I expected: the same evidence rule `ask` enforces (`ASK FAILED ...
     the state is empty; a decision is made over evidence`, exit 2). A blank
     card is not less empty than a blank state, and a LAND recorded from it
     trains the record on a decision no evidence supports.
   - Grade: NEXT.

7. `import` drops a verdict file with no label and never says it saw it.
   - With `imp2/v/VERDICT.md` holding an empty first line:
     `nova-decide import --record ./t8.jsonl --verdicts './imp2/v/*.md'`
     ``` IMPORT OK verdict_new=0 verdict_existing=0 judgment_new=0 judgment_existing=0 report_new=0 report_existing=0 unanswered=0 ```
     (exit 0)
   - What I expected: the page makes the first word of the first line the label.
     A source that carries no label is named (a `NOTE`, or a skipped count), not
     dropped, so a stranger can tell a malformed verdict from a glob that
     matched nothing; the printed counts are the same for both.
   - Grade: NEXT.

8. The page's failure line is `FAIL`; the tool prints `FAILED`.
   - `nova-decide ask ... --answers ./bad-p.json ...` prints
     ``` ASK FAILED id=bp backend=fixed: the backend's answers do not fit the schema: ... ```
     (exit 2)
   - `docs/SPEC-NOVA-DECIDE.md` section 7 says a backend that fails "is `<VERB>
     FAIL id=... backend=...: <why>; run: <remedy>` at exit 2". The tool's own
     onboarding standard says the word is `FAILED`.
   - What I expected: the page and the tool agree on the status word a reader
     greps for. (`import -h` likewise prints `unanswered` in its result and
     never names it, while the page's section 4 does.)
   - Grade: NEXT.

9. `outcome` and `calibrate` on a record that is not there blame the decision.
   - `nova-decide outcome --record ./nope.jsonl --id first --label ok`
     ``` OUTCOME REFUSED: first: no decision with that id in the record; run: nova-decide help ```
     (exit 2)
   - `nova-decide calibrate --record ./nope.jsonl --decision read --question defect --positive wrong --negative ok`
     ``` CALIBRATE REFUSED: the record holds no decision named read; run: nova-decide help ```
     (exit 2)
   - What I expected: the cause named (`open ./nope.jsonl: no such file or
     directory`), as `ask` names a missing state file. "The record holds no
     decision named read" is a different fact from "the record is not there",
     and it sends the reader to look for a decision that was never the problem.
   - Grade: NEXT.

10. The no-key refusal carries two `run:` clauses.
    - `env -u JEV_API_KEY nova-decide ask --schema ./testdata/schema.json --state ./testdata/state.txt --backend jev --record ./t1.jsonl --op jev1`
      ``` ASK REFUSED reason=key_absent: JEV_API_KEY is absent from this environment; run under `nova-secrets exec --only JEV_API_KEY -- nova-decide ...` (the key is never a flag or a file); run: nova-decide help ```
      (exit 2)
    - What I expected: the refusal grammar keeps `run:` for the remedy that ends
      the line. Here the reason's own remedy sentence is also written `run
      under ...`, so the line has two "run" clauses and a parser that takes the
      first gets a sentence, not the command the line ends with.
    - Grade: NEXT.

## What held

The `example:` block is real: all ten lines ran as printed (fixture paths made
relative to the scratch dir), each against a scratch record. Every verb took
`--json` and it carried the same values as the lines (`ask`, `read`, `score`,
`attempt`, `grade`, `gate`, `brief`, `outcome`, `calibrate`, `import`,
`score-grades`, `findings`, `version` compared). Op replay returned
`recorded=existing` with no new line, and `--dry-run` wrote nothing for `ask`,
`outcome`, `import` and `brief`. `outcome` attached once (`changed=true`, then
`changed=false`) and a second, different label exited 1 naming both. The record
loader refused a malformed line, a duplicate decision id, an outcome before its
decision, and a probability outside [0, 1], each by file and line. `import` read
two heavy-read `VERDICT.md`, one `HOLD` `REPORT.md` and a `--judgments`/`--log`
pair in one call (`verdict_new=2 ... report_new=1 unanswered=1`), was idempotent
on the second run, and named a `--log` without `--judgments`. `gate` read two
failures and a build failure (the build failure `route=caused recorded=unasked`),
and refused a green output. `brief` on a directory of three printed the `MORE
kind=card shown=2 total=3` line, refused an empty directory and a missing one,
and `grade --examples` refused a class with fewer than ten and named the bad
line. `calibrate` printed the counts, the AUC, the three `BAR` lines and
`CATCH-ALL`; `findings` clustered the fixture record by class with `unnamed`.
`help` exits 0 and every verb's `-h` prints without reading anything (a bad
`--schema` beside `-h` still exits 0); an unknown flag names every flag the verb
has, an unknown verb names every verb and the nearest, and missing flags are
refused one line each with what each wants. `ask` refused a `--timeout` of
nonsense and of zero, `brief` refused `--width 0` and `--max -1`, `findings`
refused a `--bar` outside [0, 1] and a bad `--since`, and `score-grades` refused
a bad log and a bad `--day`.

READ 8/10 — the page under `docs/` is the best part: every noun, the record's
shape, each decision's table and the calibration numbers are stated precisely,
and every refusal I provoked taught me the shape it wanted; it loses two for the
banner's `--max` promise it does not keep and the stale `FAIL` word (with
`unanswered` missing from `import`'s help) against the tool it describes.

USE 7/10 — a stranger can run the whole tool from the help with the fixed
backend and no store, and replay, idempotence, the record loader's refusals and
`--json` all hold; it loses three for the missing input read as empty, the
`make --answers` remedy on the whole failure path, and the extra answer accepted
and dropped in silence.

urgent=4 next=6

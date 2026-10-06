# nova-decide dogfood, 2026-10-06 (opencode)

Rater: opencode (flash tier), a cold run of the binary, its `-h` and `help <verb>` output, and
its page under `docs/` alone; no memory of the tool's code or earlier ratings.
Build: 4b29da81ad74. Store: a scratch directory, the fixed backend, no network and no key.
Every verb ran at least once with its real flags (ask, read, score, attempt, grade, gate, brief,
outcome, calibrate, import, score-grades, findings, version, help), plus the refusals below.

## Findings

1. `nova-decide findings --record /tmp/nova-decide-nope-dir/rec.jsonl`
   printed:
   ```
   FINDINGS OK scored=0 classes=0 bar=0.5 since=2026-09-29T18:54:01Z
   ```
   expected: a record path that does not exist is named and refused (as `calibrate` refuses a
   record it cannot read), not answered with a confident empty report.
   grade: NEXT

2. `nova-decide findings --record ./cmd/nova-decide/testdata/record.jsonl --read-shadow ./nope.jsonl --heavy ./nope3.jsonl`
   printed:
   ```
   FINDINGS OK scored=5 classes=4 bar=0.5 since=2026-09-29T18:51:00Z
   FINDINGS FINDING class=stranded_fragment count=2 cards=s1-1,s1-2
   ...
   FINDINGS SHADOW_READ against=heavy count=0 tp=0 fp=0 fn=0 tn=0 precision_pct=0 recall_pct=0
   ```
   expected: a `--read-shadow` or `--heavy` path that does not exist is refused; a typo is
   otherwise indistinguishable from "no shadows yet", and the zeros read as a measured score.
   The same silent ignore holds for `--shadow ./nope.jsonl --real ./nope2.jsonl` (no `SHADOW`
   line, `shadow_pending=0`).
   grade: URGENT

3. `nova-decide help --json`
   printed:
   ```
   {"result":{"verb":"","status":"refused","exit":2,"remedy":"nova-decide help","why":["unknown verb \"--json\"; ..."]}}
   ```
   expected: `help` takes `--json` like every verb the banner says does, or refuses the trailing
   flag with what `help` takes; `nova-decide help ask --json` silently ignores it too.
   grade: NEXT

4. `nova-decide ask --schema ./cmd/nova-decide/testdata/schema.json --state ./cmd/nova-decide/testdata/nope.txt --backend fixed --answers ./cmd/nova-decide/testdata/answers.json --record ./r.jsonl`
   printed:
   ```
   ASK REFUSED: open /.../cmd/nova-decide/testdata/nope.txt: no such file or directory; run: nova-decide help
   ```
   expected: the flag and what it wants, as every other refusal names them
   (`--state <file|->: the text the decision is made over`), not a raw OS error.
   `score-grades --log ./nope.json` gives the same shape.
   grade: NEXT

5. `nova-decide calibrate --record ./cmd/nova-decide/testdata/record.jsonl --decision read --question defect --positive wrong --negative ok`
   printed:
   ```
   CALIBRATE OK decision=read schema=505bd379c3753631 question=defect option=yes positives=3 negatives=5 skipped=0 auc=0.933
   CALIBRATE BAR at=0.5 caught=2 of=3 bounced=1 of_negatives=5
   CALIBRATE BAR at=0.7 caught=2 of=3 bounced=0 of_negatives=5
   ```
   expected: `calibrate -h` states the default bars it prints (0.5, 0.7, 0.9) and the scoring
   direction (a noul is scored by p of yes, higher flags), so a caller phrases the question
   defect-style and reads the bars without the spec; the help says only "the thresholds to report".
   grade: NEXT

6. `nova-decide ask --schema ./cmd/nova-decide/testdata/schema.json --state ./cmd/nova-decide/testdata/state.txt --backend fixed --answers ./cmd/nova-decide/testdata/answers.json --record ./decisions.jsonl --op brandnew --dry-run`
   printed:
   ```
   ASK OK id=brandnew decision=reply backend=fixed questions=2 state_bytes=101 recorded=no dry_run=true
   ```
   expected: with the fixed backend, the `ASK ANSWER` lines the run would record (or a note that
   answers need a real run), so a caller can preview what a fixed run answers before recording.
   grade: NEXT

7. `nova-decide gate --output ./cmd/nova-decide/testdata/gate-output.txt --card ./cmd/nova-decide/testdata/card.md --diff ./cmd/nova-decide/testdata/card.diff --base-red TestPortInUse --backend fixed --answers ./cmd/nova-decide/testdata/gate-answers.json --record ./decisions.jsonl --op c1@1@gate`
   printed:
   ```
   GATE OK op=c1@1@gate decision=gate backend=fixed failures=2 route=caused
   GATE FAILURE key=example/tools/internal/serve.TestPortInUse id=... class=flaky p=caused:0.06,flaky:0.86,pre-existing:0.08 route=caused recorded=new
   ```
   expected: the route agrees with the class it prints, or a NOTE says both bars are unset so every
   failure routes `caused`; `class=flaky` beside `route=caused` reads as a contradiction.
   grade: NEXT

8. `nova-decide ask --schema ./cmd/nova-decide/testdata/schema.json --state ./cmd/nova-decide/testdata/state.txt --backend fixed --answers ./cmd/nova-decide/testdata/answers.json --record ./decisions.jsonl --op first`
   printed:
   ```
   ASK OK id=first decision=reply backend=fixed tokens_in=0 tokens_out=0 recorded=new
   ASK ANSWER question=asks_something type=noul value=yes p=yes:0.94
   ```
   then `git status --porcelain` in the checkout root: `?? decisions.jsonl`.
   expected: the first run (and the banner's `example:` block) uses a `./trial-decisions.jsonl`
   name as the README row does, or the file is gitignored; as printed it leaves a stray file.
   grade: NEXT

9. `nova-decide -h` (the banner's how-it-works block)
   printed:
   ```
   schema {"name":"q","questions":{"ok":{"type":"noul","instructions":"It asks."}}}
   state R? fixed answers {"ok":{"noul":0.9}} print ASK OK id=f decision=q backend=fixed recorded=new
   ASK ANSWER question=ok type=noul value=yes p=yes:0.9; exit 0 means recorded, never approved.
   ```
   expected: the lines the tool prints (the fixture run prints `id=first decision=reply`,
   `question=asks_something`), or an explicit schematic label; a cold reader tries to run it.
   grade: NEXT

10. `nova-decide ask --schema ./cmd/nova-decide/testdata/schema.json --state ./cmd/nova-decide/testdata/state.txt --backend fixed --answers ./cmd/nova-decide/testdata/answers.json --record ./noop.jsonl` (run twice)
    printed:
    ```
    ASK OK id=reply-91bbd8441ed7 decision=reply backend=fixed tokens_in=0 tokens_out=0 recorded=new
    ASK OK id=reply-f3b446cebd3e decision=reply backend=fixed tokens_in=0 tokens_out=0 recorded=new
    ```
    expected: without `--op`, the same ask records one decision or warns it is not idempotent;
    the id folds the clock, so a re-run of the same ask silently appends a second decision.
    grade: NEXT

11. `nova-decide ask --schema ./cmd/nova-decide/testdata/schema.json --state ./cmd/nova-decide/testdata/state.txt --backend fixed --answers ./badanswers.json --record ./r.jsonl`
    printed:
    ```
    ASK FAILED id=reply-6c42196f0a0f backend=fixed: the answers file has no answer to kind; run: make --answers answer every question of the schema
    ```
    expected: a `run:` line that names a command to run, as every other refusal does; `make
    --answers ...` reads as the build tool `make` and does not run.
    grade: NEXT

READ 8/10 — the banner, `help`, the `docs/CLI.md` first run and the spec agree on the nouns
(noul, choice, backend, record) and every example command ran as printed; what costs the two
points is the banner's schematic how-it-works line that no run produces and a first run that
leaves `./decisions.jsonl` in the tree.

USE 8/10 — every verb ran first or second try on the fixed backend, the refusals name every
missing thing at once and end with the next command, and `--json`, `--dry-run` and `--op` behave;
what costs the two points is `findings` silently scoring against record files that do not exist
and the raw OS errors where other verbs name the flag.

urgent=1 next=10
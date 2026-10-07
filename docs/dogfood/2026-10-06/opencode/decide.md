# nova-decide: cold run 2026-10-06

## 1. `nova-decide version`
Command: `nova-decide version`
Output (first 3 lines): nova-decide devel darwin/arm64 go1.27.1
Expected: Version string
Grade: NEXT (shows "devel" instead of a tagged release like "1.1.0")

## 2. `nova-decide brief`
Command: `nova-decide brief --card ./testdata/greet.md --backend fixed --answers ./testdata/brief-answers.json --record ./tmp-decisions.jsonl`
Output (first 3 lines):
BRIEF OK decision=brief backend=fixed cards=1 asked=1 existing=0 failed=0
BRIEF CARD id=greet op=greet@brief-825042ac p_converges=0.72 minutes=under-10 failed=- uncalibrated=true recorded=new
Expected: BRIEF result
Grade: URGENT (report shows "uncalibrated=true" - a card brief should use calibrated probabilities)

## 3. `nova-decide ask`
Command: `nova-decide ask --schema ./testdata/schema.json --state ./testdata/state.txt --backend fixed --answers ./testdata/answers.json --record ./tmp-decisions.jsonl --op test-ask`
Output (first 3 lines):
ASK OK id=test-ask decision=reply backend=fixed tokens_in=0 tokens_out=0 recorded=new
ASK ANSWER question=asks_something type=noul value=yes p=yes:0.94
Expected: ASK result with probabilities
Grade: NEXT (tokens_in/tokens_out always 0 with fixed backend - useful but could be clearer)

## 4. `nova-decide read`
Command: `nova-decide read --card ./testdata/card.md --diff ./testdata/card.diff --backend fixed --answers ./testdata/read-answers.json --record ./tmp-decisions.jsonl --op test-read`
Output (first 3 lines):
READ OK id=test-read decision=read backend=fixed verdict=LAND p=0.92 tokens_in=0 tokens_out=0 recorded=new
READ ANSWER question=defect type=noul value=no p=yes:0.04
Expected: READ result with verdict
Grade: NEXT (verdict=LAND with p=0.92 is good; tokens always 0 is confusing at first glance)

## 5. `nova-decide score`
Command: `nova-decide score --card ./testdata/card.md --diff ./testdata/card.diff --backend fixed --answers ./testdata/score-answers.json --record ./tmp-decisions.jsonl --op test-score`
Output (first 3 lines):
SCORE OK id=test-score decision=score backend=fixed top=record_made_claim p=0.08 tokens_in=0 tokens_out=0 recorded=new
Expected: SCORE result with top defect
Grade: NEXT (shows many defect questions; top=... makes it scannable)

## 6. `nova-decide attempt`
Command: `nova-decide attempt --brief ./testdata/card.md --result ./testdata/result.md --reason "verdict not-done: tests red" --backend fixed --answers ./testdata/attempt-answers.json --record ./tmp-decisions.jsonl --op test-attempt`
Output (first 3 lines):
ATTEMPT OK id=test-attempt decision=attempt backend=fixed class=needs-pro p=0.78 tokens_in=0 tokens_out=0 recorded=new
Expected: ATTEMPT result with class
Grade: NEXT (class=needs-pro with p=0.78 makes classification explicit)

## 7. `nova-decide grade`
Command: `nova-decide grade --brief ./testdata/card.md --backend fixed --answers ./testdata/grade-answers.json --record ./tmp-decisions.jsonl --op test-grade`
Output (first 3 lines):
GRADE OK id=test-grade decision=grade backend=fixed grade=flash p=0.71 tokens_in=0 tokens_out=0 recorded=new
Expected: GRADE result with grade value
Grade: NEXT (grade=flash with p=0.71 is good; could show what "flash" means)

## 8. `nova-decide gate`
Command: `nova-decide gate --output ./testdata/gate-output.txt --card ./testdata/card.md --diff ./testdata/card.diff --base-red TestPortInUse --backend fixed --answers ./testdata/gate-answers.json --record ./tmp-decisions.jsonl --op test-gate`
Output (first 3 lines):
GATE OK op=test-gate decision=gate backend=fixed failures=2 route=caused
GATE FAILURE key=example/tools/internal/serve.TestPortInUse id=test-gate/example/tools/internal/serve.TestPortInUse class=flaky p=caused:0.06,flaky:0.86,pre-existing:0.08 route=caused recorded=new
Expected: GATE result with failure analysis
Grade: NEXT (gate=flaky classification is useful; output file not created in my run - may need to verify)

## 9. `nova-decide outcome`
Command: `nova-decide outcome --record ./tmp-decisions.jsonl --id test-read --label LAND --note "read verb tested successfully"`
Output (first 3 lines):
OUTCOME OK id=test-read decision=read label=LAND changed=true
Expected: OUTCOME success
Grade: NEXT (changed=true indicates outcome was written; good)

## 10. `nova-decide calibrate`
Command: `nova-decide calibrate --record ./tmp-decisions.jsonl --decision read --question defect --positive wrong --negative LAND`
Output (first 3 lines):
CALIBRATE REFUSED: 0 positive and 1 negative outcomes of read; a calibration wants at least one of each; run: nova-decide help
Expected: CALIBRATE result
Grade: NEXT (helpful error message; user must understand calibration needs balanced outcomes)

## 11. `nova-decide findings`
Command: `nova-decide findings --record ./tmp-decisions.jsonl`
Output (first 3 lines):
FINDINGS OK scored=1 classes=0 bar=0.5 since=2026-09-29T19:53:26Z
Expected: FINDINGS summary
Grade: NEXT (shows scored=1, classes=0; could explain what "classes" means)

## 12. `nova-decide --dry-run`
Command: `nova-decide ask --schema ./testdata/schema.json --state ./testdata/state.txt --backend fixed --answers ./testdata/answers.json --record ./tmp-decisions.jsonl --op test-dryrun --dry-run`
Output (first 3 lines):
ASK OK id=test-dryrun decision=reply backend=fixed questions=2 state_bytes=101 recorded=no dry_run=true
Expected: Dry run result without changes to record
Grade: NEXT (dry_run=true flag visible; no record modification confirmed)

READ 8/10 — Scores are high because the output is clear and structured, with each verb returning well-formatted results.
USE 8/10 — The tool is easy to use with consistent flag patterns and useful output, though the fixed backend always shows 0 tokens which can be confusing at first.

The tool works well overall. Main friction points: tokens always showing 0 with fixed backend (confusing at first glance); calibration requires understanding balanced outcomes; grade/flash semantics not self-explanatory; calibrate error message is helpful but assumes some domain knowledge.

urgent=1 next=11

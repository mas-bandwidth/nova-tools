# The read rubric (Glenn 2026-09-24 12:10 PM ET: "quantify how to do a good review, what to look for")

One rubric for every reader: friends, Rowan, cold graders. A score is a claim about evidence. Anyone should be able to recompute it from the line.

## Order of work
1. **Gates first, mechanically.** Run them as commands, paste what they print. A gate failure caps the score at 7 and the line names the gate. No prose before the gates.
2. **Then the number.** Start at 10, deduct per finding, one evidence line per deduction: `file:line` (PR) or a quoted sentence (spec) and what is wrong in one clause.
3. **Then the verdict line**, in the typed form, nothing else in the comment.

## The three gates
| gate | PR read | spec read | how to prove it |
|---|---|---|---|
| CI / test | every required check success at the exact head; a cancelled or timed-out job is red | the DONE-WHEN command is `go test -v -run '^(A|B|...)$'` (or the suite's verbose form), names every test, and says one `--- PASS` per name, none SKIP, exit 0 | run it; for a spec, run it against dev tip and expect FAIL for tests not yet written |
| base / dependencies | base is dev (nova-tools) or main / fixed-table-form (schema) or main (rowan-tools), not stacked; every DEPENDS-ON entry merged | exactly one DEPENDS-ON line in the one form (#3409) with a WHY; base-sha is a real dev commit | `pulls/<n>` base field; one compare call; `grep -c '^DEPENDS-ON:'` equals 1 |
| scope / paths | the diff stays inside the PATHS the body names and does what the DONE-WHEN says; nothing else changed | every PATHS entry exists at dev tip or is marked new; EST has a measured basis or says "unmeasured, by analogy to <spec>"; an Out of scope section exists | `pulls/<n>/files` vs PATHS; git trees API at tip, one call |

## Deductions (the number)
| finding | deduct | what counts as evidence |
|---|---|---|
| a stub: unconditional OK, TODO, always-zero, a helper nothing calls, a test that calls the helper instead of the verb | HOLD (score at most 4) | the line, and the call site that should exist |
| security or data loss: a secret in argv or a file, a DEL of a key others write, a fallback to localhost when an address is empty, an unchecked lease or fence | HOLD (at most 5) | the line and what it exposes or erases |
| behaviour wrong against the spec's DONE-WHEN sentence | -2 each | the sentence and the line that contradicts it |
| a test that cannot fail (asserts nothing, passes with the code removed, wrong exit-code order) | -2 each | what you removed or changed to make it pass anyway |
| out-of-PATHS file with product code | -2 each, scope gate red | the file name |
| out-of-PATHS test-only edit the change genuinely needs | -1 once, say so | the file name and why needed |
| error swallowed (`\|\| true`, ignored return, `_ =`) on a path that matters | -1 each | the line |
| body missing DONE-WHEN, PATHS or DEPENDS-ON | -1 once | which |
| stale claim in the body (a number, a file that is not there, a dependency already merged) | -1 once | the claim and the fact |
| a real design question the spec did not settle | -1, name it as a question | the two options |
| style, naming, comment wording, a nit with no behaviour | 0 | mention at most one, unscored |

A 10 means: all three gates proven mechanically and no deduction. A 9 means one cosmetic deduction with a one-line fix. An 8 means one small defect with a clear fix. 7 or below always names a gate failure or a behavioural defect. Never a 10 with a gap listed.

## Evidence lines
`- item n: <file:line or "spec §x, quote">: <what is wrong in one clause>; fix: <one clause>`. No praise lines, no restating the PR. A line the author can act on without asking a question.

## Budget
A read is 10 to 20 minutes. If the gates take longer than that (CI still running, a dependency unresolved), post `PENDING who=<you> head=<sha> gate=<which>` and move on; never wait in a read.

## The typed line
`SCORE|HOLD|DISPOSITION who=<you> head=<full sha> score=N/10 gates=ci:<ok|red>,base:<ok|no>,scope:<ok|no>: <items>` for a PR;
`SPEC who=<you> rev=<k> sha=<full body sha256> score=N/10 gates=test:<ok|no>,deps:<ok|no>,paths:<ok|no>: <items>` for a spec.
The lander and the rewrite children parse exactly this. A 10 carries `gates: all three pass mechanically`.

## Calibration
Every reader's lines are compared with the others' on the same head: within-1 agreement and the false-10 rate (a 10 where another reader proved a gate failure) are reported per reader per day (nova-tools#3522 for Emma; #3486 for cost). A reader whose false-10 rate is above 5% reads with the gate script until it is under.

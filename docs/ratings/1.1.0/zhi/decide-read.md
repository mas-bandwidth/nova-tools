# nova-decide READ rating, nova-tools 1.1.0

Rater: deepseek-v4-pro
Build: 4296165394da
Score: 8.5/10
README: 9/10

## Reasons

The essay that is nova-decide opens the same way in four places, and that one
sentence is right: "typed decisions with probabilities, recorded so each one
can be calibrated against its outcome" (README.md:34, docs/CLI.md:2212, the
banner's `What` at cmd/nova-decide/main.go:53, and the spec's opening at
docs/SPEC-NOVA-DECIDE.md:3). A cold reader meets the tool's purpose in three
lines and is never handed a different story later.

The entry point, verbs and data are found in a minute. main.go is a thin
dispatch: it declares ten verbs and their flags, then leans on the shared
internal/tool skeleton for the banner, help, refusal grammar, exit table,
`--json` and `--dry-run`, and on internal/decide for the decision system. The
spec's two-side table (docs/SPEC-NOVA-DECIDE.md:10-13) maps the decide verbs
(ask, read, score, attempt, grade, gate, brief) against the train verbs
(outcome, calibrate, findings), all over one record. Each file under
internal/decide is one thing: decide.go holds the schema and answers, record.go
the append-only record and its lock, backend.go and jev.go the two transports,
and one file per decision's schema (read.go, score.go, attempt.go, grade.go,
gate.go, brief.go).

Names a stranger understands: schema, choice, state, record, backend, outcome,
calibration. "noul" is a coinage, but it is defined on first use as a yes/no
over one statement (docs/SPEC-NOVA-DECIDE.md:32). Comments say why, in the
present tense, and every function that implements a rule cites its spec section
(decide.go:1-18, jev.go:58-60, score.go:11-16). The tool is one family with the
rest: it inherits the skeleton's shape rather than re-writing it, and its tests
pin the exact schema wording to fixtures so a reworded question breaks loudly
(main_test.go, and internal/decide's *_test.go).

Claims the code bears out, checked by reading alone: a missing probability is
never read as 0 (jev.go:92-94), the key is scrubbed from any error body
(jev.go:126-128), the record is appended under an exclusive lock and refuses a
hand-edited duplicate (record.go:143-153), and `--dry-run` runs the same code
path and writes nothing (tool.go:537-540). The spec is honest about what is not
calibrated: `brief` and the judgment bar say so and ship empty rather than
pretending a number means more than it does.

What a 10 would need: the calibration sections (SPEC-NOVA-DECIDE.md sections
9-14) are long and dense, and a cold reader meets walls of AUC tables
(e.g. :253-267, :304-318) before the one-line takeaway; a 10 would open each
with its headline and push the tables down. "noul" would be spelled "yes/no (a
noul)" at first use so the coinage is optional, and the CLI first-run block of
eleven commands would name one canonical first command and the rest as
follow-ups.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | docs/SPEC-NOVA-DECIDE.md:253 | the calibration sections bury each headline in dense AUC tables, so a cold reader must wade through numbers to learn whether a decision can be trusted | open each calibration section with one summary sentence (the AUC and the bar it supports) and move the tables below it | M |
| 2 | docs/CLI.md:2223 | the First run block is eleven commands, more than a "first run"; a stranger cannot tell which one to paste first | name one canonical first command and mark the rest as follow-ups | S |
| 3 | docs/SPEC-NOVA-DECIDE.md:32 | "noul" is a coined word defined inline once; a reader meeting it in output has to remember the coinage | spell it "yes/no (a noul)" at first use and keep the parenthetical beside every answer line | S |

## Good, keep

The one-sentence purpose that is byte-identical across the README row, the
banner, the CLI reference and the spec, so the tool never tells two stories.
The two-sides-over-one-record picture and the honesty that uncalibrated
decisions print `uncalibrated=true` instead of a made-up confidence.

## Compared with earlier ratings
| earlier | now | evidence |
|---|---|---|
| no earlier rating | CHANGED | the first rating of this tool |

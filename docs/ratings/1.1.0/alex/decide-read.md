# nova-decide READ rating, nova-tools 1.1.0

Rater: abliterated-model-large-v2
Build: 2c2e4d40128
Score: 9/10
README: 9/10

## Reasons

The README's row for the tool: "Let a cheap model make a typed call, and learn how far to trust it." (README.md:34), and its one-line description, "typed decisions with probabilities, recorded so each one can be calibrated against its outcome", is the same sentence the banner's first line prints (cmd/nova-decide/main.go:53), as the standard asks.

First confusion: README.md:34, the row's setup cell names "a TypeSafe key"; the README never says what that is, and the tool's own help never uses the word, calling the backend jev instead, so a cold reader cannot connect the two.

First boredom: docs/SPEC-NOVA-DECIDE.md:253, where the dated calibration tables begin; sections 9 to 14 carry provenance inline among the rules a reader came for, and the spec is half history by weight.

First doubted claim, and it held: "The same --op over another card, diff or schema refuses; over the same inputs it returns the recorded decision and asks nothing, so a long run resumes" (docs/CLI.md:2297); the code does exactly that (internal/decide/firstread.go:86, internal/decide/record.go:143).

The doc says what the tool is for in three lines and the code does that: the README row, the CLI reference's opening (docs/CLI.md:2228) and the spec's first paragraph (docs/SPEC-NOVA-DECIDE.md:3) name one thing, a typed decision with probabilities, recorded and calibrated against its outcomes, and the ten verbs divide exactly so: seven ask and record, three read the record back. The entry point, the verbs and the data are found in a minute: the banner's how-it-works names the nouns (a schema, a backend, the record) and where the truth lives (cmd/nova-decide/main.go:55). Each file is one thing: every decision of internal/decide has its own small file (read.go, attempt.go, grade.go, score.go, gate.go, brief.go), the record and its lock are record.go, the transports are backend.go and jev.go. Comments say why, in the present tense (internal/decide/jev.go:59: a missing number is never read as 0, which would be a confident no). It is one family with the other tools: internal/tool's skeleton, a refusal that names every problem at once each with what it wants (cmd/nova-decide/main.go:279), the exit table (cmd/nova-decide/main.go:60), --json and --dry-run on every deciding verb. The tests teach the contract: cmd/nova-decide/main_test.go:75 holds every refusal to the every-problem-at-once grammar, main_test.go:132 pins ask-once, replay-from-record and calibrate-read-back as one loop, and firstrun_test.go:56 executes the documented transcript line by line, nothing normalised. Claims the code bears out: the key never reaches the record (cmd/nova-decide/main_test.go:171), the record is read whole on every call with its size stated and plain (docs/SPEC-NOVA-DECIDE.md:21, internal/decide/record.go:77), the fixed backend opens nothing (internal/decide/backend.go:16), and a backend answer outside the schema is a failure, never repaired (internal/decide/decide.go:126).

What keeps it from 10: the coined word "noul" reaches user-facing help and the command reference with no definition at first use (finding 1); the spec's list of verbs that take --dry-run omits score, which takes it (finding 2); one backend carries two names across the README and everything else (finding 3); and the spec's second half carries its dated calibration narratives inline where a manual reader wants the rule (finding 4). A 10 defines every word a stranger meets at its first use, and the spec's lists match the code's verbs exactly.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | cmd/nova-decide/main.go:85 | the read and score verbs' help, and docs/CLI.md:2254, use the coined word "noul" with no definition at first use; the definition lives in the spec two clicks away, and a stranger cannot look it up anywhere the help names | add one parenthetical at the first use in each of the two verbs' help and in the CLI reference, "(a yes-or-no question answered with a probability)" | S |
| 2 | docs/SPEC-NOVA-DECIDE.md:170 | the spec's list of verbs that take --dry-run names ask, read, attempt, grade, gate, brief and outcome, and omits score, which takes one (cmd/nova-decide/main.go:107); a spec is normative, so a reader of it alone would not try score --dry-run | add score to the list | S |
| 3 | README.md:34 | the row's setup names "a TypeSafe key" while the tool, the CLI reference and the spec call the backend jev; one thing, two names, and the README's name is the one the tool never prints | say the backend's own name in the row, with the vendor named once in the spec only | S |
| 4 | docs/SPEC-NOVA-DECIDE.md:253 | sections 9 to 14 carry the dated calibration tables inline among the rules (score at 253, attempt at 304, grade at 354, gate at 418, judgment at 519, brief at 659); a manual reader meets six narratives before the bars' meaning, and the weight dulls the read | keep each section's rule first and move the dated tables to one calibration section at the spec's end, linked from each rule | M |

## Good, keep

The banner's first line is the README's sentence word for word, and the how-it-works names the tool's nouns and where the truth lives in five lines.

The refusal grammar: every problem of one invocation at once, each saying what it wants, and the remedy a command that runs; the tests hold it (cmd/nova-decide/main_test.go:75).

The op-id replay contract, and the tests that execute the documented transcript instead of trusting it (cmd/nova-decide/firstrun_test.go:56): a long run resumes, and what the docs print is what the tool prints.

## Compared with earlier ratings
| earlier | now | evidence |
|---|---|---|
| no earlier rating | CHANGED | the first rating of this tool |

# nova-decide READ rating, nova-tools 1.1.0

Rater: gemini-2.5-pro
Build: 4296165394da
Score: 8.5/10
README: 8/10

## Reasons
The tool has an unusually crisp and coherent architecture: typed decisions over a shared record file, an offline fixed backend alongside a model transport, and an explicit calibration workflow to quantify confidence rather than relying on uncalibrated assertions.

Reading README.md top to bottom:
- Confused: README.md:34 introduces a reference to a TypeSafe key and calibration against outcomes without prior definition or context in the table or introductory guide.
- Bored: README.md:24 presents a nineteen-row catalog where complex command snippets, environment prerequisites, and multi-sentence explanations are compressed into narrow table cells.
- Doubted a claim: README.md:50 states that commands install from the 1.0.0 release, but nova-decide is a newly introduced tool in 1.1.0 that does not exist in 1.0.0.

To reach a 10/10, the tool would need:
1. Addition of the promised TLA+ model in tla/DecideRecord.tla to back the concurrency and record integrity claims in the specification.
2. An introductory note in the command reference clarifying why the first run walkthrough switches from ./decisions.jsonl to a fixture record for calibration.
3. An environment variable or flag to configure the model endpoint URL in the backend transport to simplify local mock testing.
4. Clarification in README.md:34 describing the role of TypeSafe and Jev in relation to the decision system.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | docs/CLI.md:2216 | Section points to specification claiming TLA+ model tla/DecideRecord.tla is owed but no model file exists | Add the DecideRecord.tla specification and configuration files under tla | M |
| 2 | README.md:50 | Document states all tools install from the 1.0.0 release which omits this tool | Update release version references to reflect 1.1.0 tools | S |
| 3 | docs/CLI.md:2232 | First run transcript switches record files for calibration without explaining why | Document that calibration requires a dataset containing both positive and negative outcomes | S |
| 4 | README.md:34 | Table entry introduces TypeSafe without explaining its relationship to Jev | Clarify in the table that Jev is the remote model backend provider | S |
| 5 | docs/CLI.md:2274 | Command reference notes backend endpoint without option to redirect requests for local mock testing | Support a custom endpoint URL through an environment variable or flag | S |

## Good, keep
Strict separation between typed schemas and generic transport backends allowing completely offline execution.
Atomic appending to a shared JSON lines record file protected by advisory file locking.
Consistent output contract reporting probability distributions alongside discrete choices in typed lines and json.

## Compared with earlier ratings
| earlier | now | evidence |
|---|---|---|
| no earlier rating | CHANGED | the first rating of this tool |

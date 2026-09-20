RESULT tools22-rule-toolwork-1-L912 sha=5298f6be12ea — does the code at this base do what docs/SPEC-TOOLWORK.md rule 1 says?
CONFORMS internal/onboarding/transcript.go:124,627,747
SPEC docs/SPEC-TOOLWORK.md:912 rule 1
PKG internal/onboarding
ASK For every `## <tool>` section in docs/TESTS.md, a test in `cmd/<tool>` must parse every `$` line under `### First run` into commands with their expected output blocks, run each command in-process against a fixture in one temp directory, and compare the whole output — same number of lines, same lines, same order.
Deciding lines — `internal/onboarding/transcript.go`:
- `Steps` (line 124) parses transcript lines into `Step` structs holding command args and expected output (`Want`).
- `Compare` (line 627) checks length, then each line in order, applying declared norms to both sides.
- `Execute` (line 747) walks steps in order via a `Runner`, stopping on the first invokable failure.
- `ExecuteWith` (line 813) extends Execute with platform/requirement conditions and returns skips.
These four functions together implement #1602's shape — in-process `run()`, one temp directory per sitting, whole-output comparison by count/line/order.
GUARDED-BY transcript_test.go:50,101,119,207
- `TestAnAbridgedTranscriptIsRed` (line 104): Compare refuses when the tool prints more lines than the document shows.
- `TestAReorderedTranscriptIsRed` (line 119): Compare refuses when lines are in the wrong order.
- `TestExecuteStopsAtACommandItCannotInvoke` (line 207): Execute stops at the first failed command.
Left owed — 3 of 21 tools still lack the line-for-line approach for their TESTS.md first run:
  - `cmd/nova-bus/firstrun_test.go` uses shape-set (`printed map[string]bool`) only.
  - `cmd/nova-post/firstrun_test.go` has no executable transcript test (#1631).
  - `cmd/nova-sandbox/firstrun_test.go` has no executable transcript test.
  (7 others have both the new line-for-line test and a legacy shape test.)
Grep pattern used: `"onboarding\.Steps\|onboarding\.Execute\|onboarding\.Compare\|printed\["` across `cmd/nova-*/firstrun_test.go` and `internal/onboarding/*.go`.
`git status --short` prints nothing.
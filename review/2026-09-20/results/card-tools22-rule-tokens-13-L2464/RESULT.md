"RESULT tools22-rule-tokens-13-L2464 sha=5298f6be12ea — does the code at this base do what docs/SPEC-TOKENS.md rule 13 says?
CONFORMS cmd/nova-tokens/firstrun_test.go:205
SPEC docs/SPEC-TOKENS.md:2464 rule 13
PKG internal/tokens
ASK The `### First run` described in docs/CLI.md must actually run: fold one fixture transcript and one fixture bus note into a temp directory, `check` it, `sum` it, every path a flag, and the transcript in the docs must be exactly what the tool prints, with the fixture bus lane on `example.com`.
docs/CLI.md:3336 The transcript lives in [TESTS.md](TESTS.md), where a test executes it against `cmd/nova-tokens/testdata/example-bench` on every run. Three lines: fold one fixture transcript and one fixture bus note into an output directory, check it, sum it. Every path is a flag — there is no default output directory, no default transcript directory, no default bus and no default rules file, and no environment variable is consulted.
docs/TESTS.md:702 Fixture: `cmd/nova-tokens/testdata/example-bench` (copied into a temp directory first, because a first run WRITES; the bus lane is `example.com`).
cmd/nova-tokens/firstrun_test.go:229 fixtureIn(t) — the fold WRITES, so it runs against a copy of the fixture in t.TempDir() ... then onboarding.Execute(steps, runDocumented(t)) runs fold/check/sum line-for-line against the transcript.
cmd/nova-tokens/testdata/example-bench/bus/participants.json:3 {"name": "Emma", "lane": "from-emma", "git_email": "emma@example.com"}, — the fixture bus lane is `example.com`.
cmd/nova-tokens/main.go:245 --<flag> is required; it wants <wants>; refusing to guess — every path is a flag; no default directory is invented.
GUARDED-BY cmd/nova-tokens/firstrun_test.go:205 TestFirstRunTranscriptIsWhatTheToolPrintsLineForLine
GREPS grep -n "First run" docs/CLI.md; grep -n "example.com" docs/CLI.md; grep -rn "example.com" --include='*.go' internal/tokens/ cmd/nova-tokens/; grep -rn "example-bench" --include='*.go' cmd/nova-tokens/; grep -rn "func Test" cmd/nova-tokens/*_test.go | grep -i firstrun
Left owed
git status --short
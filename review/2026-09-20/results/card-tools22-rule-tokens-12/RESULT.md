RESULT tools22-rule-tokens-12 sha=5298f6be12ea — does the code at this base do what docs/SPEC-TOKENS.md rule 12 says?
CONFORMS cmd/nova-tokens/contract_test.go:16
SPEC docs/SPEC-TOKENS.md:2454 rule 12
PKG cmd/nova-tokens/*_test.go
ASK The contract tests in `cmd/nova-tokens/*_test.go` must cover all three exit codes and every refusal sentence, include demanded test 1 (environment ignored at cmd/nova-tokens/demanded_test.go:20 TestRule1EveryPathIsAFlagAndNoEnvironmentIsConsulted) and demanded test 11 (largest plausible state bounded at cmd/nova-tokens/bounded_test.go:138 TestFoldIsBoundedAtTheLargestPlausibleState), run the audit over every printed argument (cmd/nova-tokens/audit_test.go:12 TestEveryPrintedArgumentIsLiteralQuotedOrEscaped), and have no test reaching outside t.TempDir() or the fake sqlite3; for `publish`'s tests (amendment), they must keep real git, fake git, bare repo and clone all inside t.TempDir() with no remote, credential, private ledger, or network socket touches.

Deciding lines:
  - Demanded test 1: cmd/nova-tokens/demanded_test.go:20 `func TestRule1EveryPathIsAFlagAndNoEnvironmentIsConsulted` — env vars set, three-line refusal with TOKENS REFUSED prefix, path opened count proves nothing outside flags was consulted
  - Demanded test 11: cmd/nova-tokens/bounded_test.go:138 `func TestFoldIsBoundedAtTheLargestPlausibleState` — 10 SOURCE lines, 20 of each kind, 7 MORE lines, line/byte caps
  - Audit: cmd/nova-tokens/audit_test.go:12 `func TestEveryPrintedArgumentIsLiteralQuotedOrEscaped` + `TestNoOtherWriterOrShadowCanBypassTheEscape` — classify every fmt arg or bypass
  - Every exit code: 0 (most verb successes), 1 (cmd/nova-tokens/demanded_test.go:120 `wantExit(t, r, 1)`), 2 (cmd/nova-tokens/demanded_test.go:35 `wantExit(t, r, 2)`)
  - Refusal sentences: cmd/nova-tokens/firstrun_test.go:87 `TestEveryRefusalSaysWhatTheInputWantsAndOneRunNamesEveryProblem` — "refusing to guess", "it wants", "run: nova-tokens help"
  - No test outside t.TempDir(): all 100+ t.TempDir() calls; zero uses of os.TempDir(), os.UserHomeDir, http, httptest, net.Dial, net.Listen in cmd/nova-tokens/*_test.go
  - Fake sqlite3 on PATH: cmd/nova-tokens/helpers_test.go:163 `func fakeSqlite3` — copies self to t.TempDir()/bin, puts directory on PATH
  - Amendment (publish): no publish tests exist in cmd/nova-tokens/; `records publish` is refused (cmd/nova-tokens/boundary_test.go:571 `TestTheRecordsNamespaceIsNotAVerbUntilItsGateIsDecided`); the amendment is vacuously satisfied

GUARDED-BY cmd/nova-tokens/demanded_test.go:20 TestRule1EveryPathIsAFlagAndNoEnvironmentIsConsulted
GUARDED-BY cmd/nova-tokens/bounded_test.go:138 TestFoldIsBoundedAtTheLargestPlausibleState
GUARDED-BY cmd/nova-tokens/audit_test.go:12 TestEveryPrintedArgumentIsLiteralQuotedOrEscaped
GUARDED-BY cmd/nova-tokens/audit_test.go:16 TestNoOtherWriterOrShadowCanBypassTheEscape
GUARDED-BY cmd/nova-tokens/firstrun_test.go:87 TestEveryRefusalSaysWhatTheInputWantsAndOneRunNamesEveryProblem
GUARDED-BY cmd/nova-tokens/boundary_test.go:571 TestTheRecordsNamespaceIsNotAVerbUntilItsGateIsDecided (amendment: no publish verb to have tests for)
GUARDED-BY cmd/nova-tokens/boundary_test.go:305 TestNoVerbTouchesACheckoutOrItsRemote (all verbs inside t.TempDir() with fake git)

Greps ran:
  - `grep -rn "func Test" --include='*_test.go' cmd/nova-tokens/` — 90+ test functions
  - `grep -rn "t.TempDir\|fakeSqlite3" --include='*_test.go' cmd/nova-tokens/` — 100+ t.TempDir() calls, fakeSqlite3 in helpers_test.go
  - `grep -rn "os\.TempDir\|os\.UserHomeDir\|http\.\|httptest\|net\.Dial\|net\.Listen" --include='*_test.go' cmd/nova-tokens/` — zero matches
  - `ls cmd/nova-tokens/*publish*` — no publish test files exist
  - `glob **/publish*_test.go` — only internal/tokens/publish_frictions_test.go (unit test, no git), cmd/nova-swarm/publish_test.go (different binary)

Left owed: none

git status --short: (clean — no output)
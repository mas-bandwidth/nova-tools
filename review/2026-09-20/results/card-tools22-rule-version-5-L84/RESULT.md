RESULT tools22-rule-version-5-L84 sha=5298f6be12ea — does the code at this base do what docs/SPEC-VERSION.md rule 5 says?
ABSENT
SPEC docs/SPEC-VERSION.md:84 rule 5
PKG cmd/nova-version
ASK The implementation must define a test `TestApplyShaBuildsOneStampOnAFakeBench` that exercises the `apply --sha` build path with a fake `go` recording exactly one `-X main.version=` linker flag and a set of fake binaries that echo that stamp back, verifying the `APPLY SHA` output line carries `stamp=` of the revision and `built=` of the `cmd/*` count.
The test `TestApplyShaBuildsOneStampOnAFakeBench` does not exist.
Grep results:
  grep -rn "TestApplyShaBuildsOneStampOnAFakeBench" --include='*.go' . — no matches
  grep -rn "OneStampOnAFakeBench" --include='*.go' . — no matches
  grep -rn "APPLY SHA" --include='*.go' . — no matches
  grep -rn "apply.*--sha\|ApplySha\|applySha" --include='*.go' . — no matches in non-comment code
  grep -rn "func Test" --include='*_test.go' cmd/nova-version/ — lists 4 test functions, none match (TestVersionStampAndUsage, TestRefusalNamesVersionNotUpdate, TestVersionHelpDoesNotDemandAnApplyVerb, TestVersionReportFileUsageStatesShape)
  grep -rn "func Test.*Apply" --include='*_test.go' . — 8 matches in entire repo, none related to apply --sha
  go test ./cmd/nova-version/ -run TestApplyShaBuildsOneStampOnAFakeBench — [no tests to run]
The wider `apply --sha` verb itself (`nova-update apply --sha <sha> --repo <dir> --bin <dir>`) is absent from the codebase: `updateVerbs` in `internal/update/cli.go` only lists `nova-update apply --file <path> <name>`, and no code path produces `APPLY SHA` output. The feature specified in rules 5-12 and 5-10 of the numbered section above line 84 is not implemented at this base.
Left owed: the entire ApplySha verb and its test suite (rules 5-11 under "Red tests this section demands" at docs/SPEC-VERSION.md:84-90).

git status --short: (nothing)
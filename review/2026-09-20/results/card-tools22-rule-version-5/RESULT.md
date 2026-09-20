RESULT tools22-rule-version-5 sha=5298f6be12ea
CONFORMS internal/update/snapverb.go:175
SPEC docs/SPEC-VERSION.md:169 rule 5
PKG cmd/nova-version
ASK The snapshot verb must exit 2 naming the tool when `version` exits non-zero or prints nothing parseable.
Deciding lines:
  internal/update/snapverb.go:175 — return refusal(errs, "SNAPSHOT", fmt.Errorf("cannot read %s version (%s) (%s)", e.Name(), reason, remedy)) — non-zero exit (caught by p.Reason != "" at line 151)
  internal/update/snapverb.go:179 — return refusal(errs, "SNAPSHOT", fmt.Errorf("cannot read %s version (it printed no version line: ...)", e.Name(), e.Name())) — unparseable output (caught by parseVersionLine ok=false at line 177-178)
GUARDED-BY internal/update/inventory_spec_test.go:163 TestSnapshotRefusesABinaryWithNoVersion
Greps run:
  grep -rn "exit 2\|exit(2)\|os.Exit(2)\|TestSnapshotRefusesABinaryWithNoVersion\|RefusesABinaryWithNoVersion\|no version\|NoVersion\|parseable\|nothing parseable" --include='*.go' cmd/nova-version/ -- no match in cmd/nova-version
  grep -rn "RefusesABinaryWithNoVersion\|NoVersion\|parseable\|nothing parseable\|exit 2\|os.Exit(2)" --include='*.go' internal/update/ -- found test and related code
  grep -rn "func snapshotVerb\|func runVersion\|non-zero\|ReadVersion\|parseVersion\|Parse(" --include='*.go' internal/update/ | grep -v '_test.go' -- found snapverb.go as the implementation
  grep -rn "func Test" --include='*_test.go' cmd/nova-version/ internal/update/ | grep -i "refuse.*no.*version\|BinaryWithNoVersion" -- found test
  GOMAXPROCS=8 go test ./internal/update/ -count=1 -run TestSnapshotRefusesABinaryWithNoVersion — PASS 0.013s
Left owed: none
git status --short: (nothing printed)
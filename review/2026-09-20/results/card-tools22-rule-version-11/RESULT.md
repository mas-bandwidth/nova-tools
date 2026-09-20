"RESULT tools22-rule-version-11 sha=5298f6be12ea — does the code at this base do what docs/SPEC-VERSION.md rule 11 says?
CONFORMS internal/update/snapverb.go:175
SPEC docs/SPEC-VERSION.md:175 rule 11
PKG cmd/nova-version
ASK nova-version snapshot must enforce a per-binary timeout via context-based deadline management, returning exit code 2 with the deadline duration named in the refusal message and never writing a partial file to the --out path.

deciding lines (snapverb.go):
  internal/update/snapverb.go:148   ctx, cancel := context.WithTimeout(run, timeout)
  internal/update/snapverb.go:151   if p.Reason != "" {                    // child died before answering
  internal/update/snapverb.go:161   case "timeout":                        // enters on deadline breach
  internal/update/snapverb.go:169     reason = "timeout after " + timeout.String()  // deadline named in message
  internal/update/snapverb.go:175   return refusal(errs, "SNAPSHOT", ...)  // exit 2, no write reached
  internal/update/snapverb.go:197   if err := os.WriteFile(outPath, ...); err != nil {  // only after loop finishes

GUARDED-BY internal/update/inventory_spec_test.go:275 TestSnapshotIsBoundedByTheClock

greps run:
  sed -n '155,201p' docs/SPEC-VERSION.md
  ls cmd/nova-version/
  grep -rn "<a distinctive word>" --include='*.go' . → grep -rn "deadline\|Deadline\|timeout\|Timeout\|injected\|clock\|Clock\|partial\|exit 2\|ExitCode.*2\|exitCode" --include='*.go' cmd/nova-version/
  grep -rn "snapshot\|Snapshot\|--timeout\|--budget\|deadline\|Deadline" --include='*.go' internal/update/ | head -80
  grep -rn "TestSnapshotIsBoundedByTheClock\|bounded.*clock\|InjectClock\|injected.*clock" --include='*.go' internal/update/
  grep -n "func refusal\|func Run\|func Main\|func process\|RefusePartial" --include='*.go' internal/update/
  grep -n "env\.Now\|env\.Client\|Environment" --include='*.go' internal/update/snapverb.go
  grep -rn "func Test" --include='*_test.go' cmd/nova-version/ → none found under cmd/nova-version
  grep -rn "func Test" --include='*_test.go' internal/update/ | grep -i "bounded\|clock\|timeout.*snapshot\|deadline.*snapshot\|slow\|sleep"
  GOMAXPROCS=8 go test ./internal/update/ -count=1 -run TestSnapshotIsBoundedByTheClock -v (PASS)

Left owed

git status --short
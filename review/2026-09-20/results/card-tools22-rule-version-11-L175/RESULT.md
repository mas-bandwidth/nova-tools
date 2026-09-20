RESULT tools22-rule-version-11-L175 sha=5298f6be12ea
CONFORMS internal/update/snapverb.go:148
SPEC docs/SPEC-VERSION.md:175 rule 11
PKG cmd/nova-version
ASK The snapshot verb must use injected-clock-seam time bounds so a fake child sleeping past its timeout exits with code 2 naming that bound in the error message, and must not write any partial --out before confirming all binaries read successfully.

Deciding lines from internal/update/snapverb.go:

Line 40: var snapshotChildTimeout = 30 * time.Second
-- package variable serving as a test seam for clock injection; tests can assign a shorter deadline without real time passing.

Lines 136-137: run, cancelRun := context.WithTimeout(context.Background(), budget)
defer cancelRun()
-- whole-run budget enforced through Go context cancellation.

Lines 148-149: ctx, cancel := context.WithTimeout(run, timeout)
p := process(ctx, []string{path, "version"}, nil, ChildCap)
-- per-binary deadline inherited from the budget context; if the budget runs out first, run.Err() != nil triggers the budget branch.

Lines 153-171: reason, remedy := p.Reason, ... / case "timeout": reason = "timeout after " + timeout.String()
-- when a child is killed by its deadline, the error names the timeout duration (the deadline).

Lines 197-198: if err := os.WriteFile(outPath, []byte(b.String()), 0o644); err != nil { ... }
-- --out is written only after every binary succeeded through the loop and mixed-stamp check; no failure path reaches this line, guaranteeing no partial output.

GUARDED-BY internal/update/inventory_spec_test.go:275 TestSnapshotIsBoundedByTheClock
Test at inventory_spec_test.go:275 sets snapshotChildTimeout to 150ms, creates a shell-script stub that sleeps 5s then prints version, verifies exit 2 with "nova-slow" and "150ms" in stderr, and asserts no --out file was created.

Grep done:
  grep -rn "snapshotChildTimeout\|--timeout\|WithTimeout\|timeout after" --include="*.go" internal/update/snapverb.go
  grep -rn "func TestSnapshot" --include="*_test.go" cmd/nova-version/ internal/update/
  grep -rn "TestSnapshotIsBoundedByTheClock" --include="*_test.go" .

Left owed: 11

git status --short
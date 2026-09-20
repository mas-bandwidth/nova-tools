RESULT tools22-rule-version-13-L177 sha=5298f6be12ea
CONFORMS internal/update/snapverb.go:148
SPEC docs/SPEC-VERSION.md:177 rule 13
PKG cmd/nova-version
ASK the snapshot verb must enforce `--timeout` per-binary (exit 2 naming tool+duration, no partial `--out`), `--budget` per-run (exit 2 naming budget, not the slow tool), and refuse non-positive bounds (exit 2 naming the flags).
Deciding lines:
- `snapverb.go:148`: `ctx, cancel := context.WithTimeout(run, timeout)` — each child bounded by `--timeout`
- `snapverb.go:157-159`: `if run.Err() != nil { reason = "budget" }` — budget exhaustion named as budget, not tool
- `snapverb.go:169-170`: `reason = "timeout after " + timeout.String()` — timeout names tool and duration
- `snapverb.go:172-173`: `reason = "the run's " + budget.String() + " budget was spent before this binary was read"` — budget names budget
- `snapverb.go:123-125`: `if timeout <= 0 || budget <= 0` → `"invalid bound (use positive --timeout/--budget)"` — non-positive named
GUARDED-BY internal/update/inventory_spec_test.go:330 TestSnapshotTakesItsBoundsFromFlags
- Subtest "timeout bounds one binary": binary sleeping 30s with `--timeout 200ms` → exit 2 naming "nova-slow" and "200ms", no `--out` written
- Subtest "budget bounds the run": four sleep-30 binaries with `--timeout 10s --budget 300ms` → exit 2 naming "budget" and "300ms"
- Subtest "a bound must be positive": `--timeout 0` → exit 2 naming `--timeout`
Grep commands run:
  grep -rn "TestSnapshotTakesItsBoundsFromFlags" --include='*.go' .
  grep -rn "timeout\|budget" --include='*_test.go' internal/update/
  grep -rn "snapshotChildTimeout\|snapshotBudget" --include='*.go' .
Left owed: none
git status --short:
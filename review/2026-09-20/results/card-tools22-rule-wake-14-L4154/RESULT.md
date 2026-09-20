RESULT tools22-rule-wake-14-L4154 sha=5298f6be12ea — does the code at this base do what docs/SPEC-WAKE.md rule 14 says?
CONFORMS internal/wake/run.go:37
SPEC docs/SPEC-WAKE.md:4154 rule 14
PKG internal/wake
ASK Rule 14 requires run.go to implement two gh api calls per head for check-runs and statuses, reuse the entry bucket rule from entry.go without re-spelling it, define FINAL without entry states, cap the 100-run limit as unreadable, and enforce a 30s --entry-interval floor when --run is given.
internal/wake/run.go:37-40:
    // RunIntervalFloor is --entry-interval's floor when any --run is given. Two
    // REST calls per head every 5 seconds is 1,440 calls an hour per head, so four
    // watched heads spent a whole REST pool.
    const RunIntervalFloor = 30 * time.Second
internal/wake/run.go:120-121:
    rawRuns, err := gh(ctx, r.Timeout, &r.calls, "api",
        fmt.Sprintf("repos/%s/%s/commits/%s/check-runs?per_page=100", owner, rname, sha))
internal/wake/run.go:139-140:
    rawStatus, err := gh(ctx, r.Timeout, &r.calls, "api",
        fmt.Sprintf("repos/%s/%s/commits/%s/status", owner, rname, sha))
internal/wake/run.go:136-137:
    if runs.Total > RunCheckLimit {
        return "", fmt.Errorf("more than %d check runs on this head", RunCheckLimit)
    }
internal/wake/run.go:171:
    count(c.Name, bucket("CheckRun", c.Status, c.Conclusion, ""))
internal/wake/run.go:174:
    count(s.Context, bucket("StatusContext", "", "", s.State))
internal/wake/run.go:186-196:
    func IsFinalRun(value string) bool {
        p := Decompose(value)
        if len(p) == 2 && p[0] == "unreadable" {
            return false
        }
        p = fields(p, 4)
        fail, _ := strconv.Atoi(p[0])
        pending, _ := strconv.Atoi(p[1])
        pass, _ := strconv.Atoi(p[2])
        return pending == 0 && fail+pass > 0
    }
internal/wake/run.go:106-108:
    if len(r.Names) > 0 && bad == len(r.Names) {
        _, reason := Unreadable(out[0].value)
        return res, fmt.Errorf("every watched head is unreadable: %s", reason)
    }
GUARDED-BY cmd/nova-wake/events_test.go:259 TestARunIsAnEntryWithoutAState
GREPS: grep -rn "entry-interval\|RunIntervalFloor\|fail:runs" --include='*.go' ., grep -rn "func Test.*15" --include='*_test.go' internal/wake/
Left owed: none
git status --short

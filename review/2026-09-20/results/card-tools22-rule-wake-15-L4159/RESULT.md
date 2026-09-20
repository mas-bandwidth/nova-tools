RESULT tools22-rule-wake-15-L4159 sha=5298f6be12ea
CONFORMS internal/wake/branch.go:76
SPEC docs/SPEC-WAKE.md:4159 rule 15
PKG internal/wake
ASK For each --ref branch, exactly one gh api read of repos/<owner>/<repo>/git/ref/heads/<name> per forge tick, whose answer is the head sha or `absent` on a 404, any other shape (an array) `unreadable:`, the line carrying both ends, and `fail:branches` when every branch is unreadable in one tick.
DECIDING internal/wake/branch.go:82-83 -- one gh api call: `gh(ctx, b.Timeout, &b.calls, "api", fmt.Sprintf("repos/%s/%s/git/ref/heads/%s", owner, rname, branch))`
DECIDING internal/wake/branch.go:85-88 -- 404 is `absent`, never an error: `if notFound(err) { return "absent", nil }`
DECIDING internal/wake/branch.go:96-101 -- an array fails the struct Unmarshal and is `unreadable`, never absent: `json.Unmarshal(raw, &one)` error -> `return "", fmt.Errorf("the forge answered a shape this source cannot read as one reference: ...")`
DECIDING internal/wake/branch.go:105 -- the sha value: `return one.Object.SHA, nil`
DECIDING internal/wake/branch.go:60 -- the `unreadable:` value: `value = Compose("unreadable", oneLineOf(err.Error()))`
DECIDING internal/wake/forge.go:80-94 -- both ends on the line: withWas composes `<now>|<was>`; source.go:201 prints `WAKE BRANCH %s head=%s was=%s`
DECIDING internal/wake/branch.go:69-71 -- `fail:branches` when every --ref is unreadable: `return res, fmt.Errorf("every --ref is unreadable: %s", ...)`, recorded as state key `fail:branches` by cmd/nova-wake/main.go:1371 via `w.st.Fail(src.Name(), ...)` (state.go:454 `key := "fail:" + source`)
GUARDED-BY cmd/nova-wake/events_test.go:313 TestABranchMovingIsItsTwoShas
GREPS grep -rn "func Test" --include='*_test.go' internal/wake/ (no branch test in the package); grep -rn "branches\|--ref\|test 16\|Test16" --include='*_test.go' .; grep -rn "func withWas\|func Compose\|func notFound\|func oneLineOf\|func gh\b\|func SplitRef\|func ownerRepo" --include='*.go' internal/wake/; grep -rn "fail:" --include='*.go' internal/wake/; grep -rn "WAKE BRANCH\|KindBranch\|head=" --include='*.go' internal/wake/ cmd/nova-wake/; grep -rn "Fail\|fail:" --include='*.go' cmd/nova-wake/
RUN GOMAXPROCS=8 go test ./cmd/nova-wake/ -count=1 -run TestABranchMovingIsItsTwoShas -> ok (0.250s)
Left owed
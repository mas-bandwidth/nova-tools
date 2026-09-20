"RESULT tools22-rule-version-1-L18 sha=5298f6be12ea — does the code at this base do what docs/SPEC-VERSION.md rule 1 says?
ABSENT
SPEC docs/SPEC-VERSION.md:18 rule 1
PKG cmd/nova-version
ASK The implementation must require all paths (--repo, --out, --bin) and revisions to come from explicit flags, refuse exit 2 on missing ones, and resolve revisions only within --repo.
I looked in cmd/nova-version/main.go, internal/update/cli.go (Run function), internal/update/snapverb.go, internal/update/diffverb.go, internal/release/cli.go. Grepped for "moved" as a verb, "apply --sha", "--sha", "--repo", "--out", "--bin" as nova-version flags, and TestMoved/TestApplySha tests. Neither the `moved` verb nor the `apply --sha` command — the two commands the section heading says rule 1 governs — exists in this tree. The `versionVerbs` constant (internal/update/cli.go:79-83) lists snapshot, diff, report, send, help — not moved. The Run function (internal/update/cli.go:135-355) dispatches snapshot, diff, report, send, help, version — not moved. No `moved` or `apply --sha` handler exists in internal/update/*.go or internal/release/*.go. No TestMoved* or TestApplySha* test exists.
Left owed none.
git status --short — nothing
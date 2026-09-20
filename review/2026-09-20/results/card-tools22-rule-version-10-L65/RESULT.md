RESULT tools22-rule-version-10-L65 sha=5298f6be12ea — does the code at this base do what docs/SPEC-VERSION.md rule 10 says?
ABSENT
SPEC docs/SPEC-VERSION.md:65 rule 10
PKG cmd/nova-version
ASK `nova-version moved` must parse each built binary's help text for its flag inventory, never a hand-written or PR-derived list, so it can announce only flags that actually exist in the built tool.
Searched: grep -rn \"moved\" --include='*.go' cmd/nova-version/ (no matches); grep -rn \"nova-version moved\|MOVED OK\|movedVerb\|moved.*from.*to.*repo.*out\" --include='*.go' . (no matches); read internal/update/cli.go (versionVerbs constant lists only snapshot, diff, report, send, help — moved is absent from the help block); read internal/update/cli.go verb dispatch (lines 165-201: no moved case); read cmd/nova-version/main.go (calls update.Main, which dispatches to cli.go).
No TestMoved* test exists in the tree (grep -rn \"func Test.*Moved\" --include='*_test.go' . returned zero matches for TestMoved).
Left owed: the spec defines `moved` at SPEC-VERSION.md:1-31 as a verb of nova-version with its own flags (--from, --to, --repo, --out) and output (MOVED OK line); that entire verb and the two rules that govern it (rules 2, 3, 4, 10, 12) are unimplemented at this base.
git status --short: (nothing to print, clean)
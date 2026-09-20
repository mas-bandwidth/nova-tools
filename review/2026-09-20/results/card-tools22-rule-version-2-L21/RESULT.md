RESULT tools22-rule-version-2-L21 sha=5298f6be12ea — does the code at this base do what docs/SPEC-VERSION.md rule 2 says?
ABSENT
SPEC docs/SPEC-VERSION.md:21 rule 2
PKG cmd/nova-version
ASK The implementation must build each cmd/* binary at two revisions, run `<tool> help` on each, parse the verbs and flags from the help output, inventory them per revision, and report added/deleted entries — never using a hand-written list.
NOT IMPLEMENTED: there is no `moved` verb in the code path. The `Run` function in internal/update/cli.go dispatches only snapshotVerb (line 169), diffVerb (line 175), and report/send — no case matches `"moved"`. A call like `nova-version moved ...` falls through to line 200–201 and returns a refusal ("unknown verb"). The versionVerbs constant (cli.go:79) lists only snapshot, diff, report, send, and help; the spec's help banner line `nova-version moved --from <sha> --to <sha> --repo <dir> --out <path>` has no corresponding code. No function named movedVerb, movedMain, or similar exists anywhere.
GREPS_RUN: grep -rn "moved" --include='*.go' cmd/nova-version/ internal/update/ | no results match a "moved" verb dispatch
grep -rn '"moved"' --include='*.go' . | no results
grep -rn 'movedVerb\|moved_main\|func moved' --include='*.go' . | no results
grep -rn 'func Test.*[Mm]oved' --include='*_test.go' . | no results
git status --short

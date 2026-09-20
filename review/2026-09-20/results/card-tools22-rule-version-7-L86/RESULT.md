RESULT tools22-rule-version-7-L86 sha=5298f6be12eaa0f7e6622334d2b6a1eb427649e3 — does the code at this base do what docs/SPEC-VERSION.md rule 7 says?
ABSENT
SPEC docs/SPEC-VERSION.md:86 rule 7
PKG cmd/nova-version
ASK An implementation must detect when one binary in a freshly built set reports devel while the rest report the target revision stamp, refuse with exit 2 naming the offending binary and both stamps, and install nothing.

Grep 1: grep -rn "TestApplyShaRefusesALostStamp" --include='*.go' . (no output)
Grep 2: grep -rn "devel" --include='*.go' cmd/nova-version/ (no output)
Grep 3: grep -rn "apply.*sha\|applySha\|ApplySha" --include='*.go' cmd/nova-version/ internal/update/ | head -20 (only found a suggestion message in snapverb.go:189 referencing "rebuild the set under one stamp with nova-update apply --sha", not actual implementation)
Grep 4: ls cmd/nova-version/ → examplelines_test.go, firstrun_test.go, friendsequence_test.go, main.go, testdata/, version_test.go
Grep 5: grep -rn 'func Test' --include='*_test.go' cmd/nova-version/ internal/update/ (no TestApplySha* test exists)

Where I looked:
- cmd/nova-version/main.go — only calls update.Main("nova-version", ...); Update.Dispatches snapshot, diff, report, check, watch verbs but no apply or apply--sha
- internal/update/cli.go Main() (line 135) — dispatches verb "snapshot" to snapshotVerb, verb "diff" to diffVerb; verbs "report"/"check"/"apply"/"watch" go to the UPDATE path, not applied to nova-version
- internal/update/snapverb.go — contains snapshotVerb which DOES reject mixed sets (line 187-191), but this is for the SNAPSHOT verb, not for apply--sha postflight verification
- internal/update/read.go line 53 — opaque() detects devel as a stamp, but there is no call site that gates apply--sha on it

The spec rules 4-11 (docs/SPEC-VERSION.md lines ~38-70) describe an apply --sha verb that builds the whole cmd/* set at one revision into one stamped directory, reads every binary back, verifies the stamp set, and switches the --bin link atomically. None of this functionality exists at commit 5298f6be12eaa0f7e6622334d2b6a1eb427649e3. The test TestApplyShaRefusesALostStamp described at line 86 also does not exist. Left owed

git status --short

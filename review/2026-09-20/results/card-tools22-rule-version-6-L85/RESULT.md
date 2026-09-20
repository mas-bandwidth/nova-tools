RESULT tools22-rule-version-6-L85 sha=5298f6be12eaa0f7e6622334d2b6a1eb427649e3 — does the code at this base do what docs/SPEC-VERSION.md rule 6 says?
ABSENT
SPEC docs/SPEC-VERSION.md:85 rule 6
PKG cmd/nova-version
ASK The code must implement `apply --sha` (a build mode where nova-update builds every cmd/* at a given revision stamp, reads back each binary's version, detects when two binaries report different stamps, and exits 2 naming both binaries and both stamps without installing anything or leaving --bin in a mixed state).

Grep results ran:
$ grep -rn 'func Test' --include='*_test.go' cmd/nova-version/ internal/update/ | grep -iE 'sha|apply.*sha|mixedset|mixed.*set'
→ only TestSnapshotRefusesAMixedSetNamingThePair (tests snapshot, not apply --sha)

$ grep -rn 'TestApplySha\|ApplySha' --include='*.go' .
→ no output: zero functions or tests mention ApplySha

$ grep -rn '"sha"\|"--sha"\|\.Lookup("sha"|Get("sha"' --include='*.go' cmd/nova-version/ cmd/nova-update/ internal/update/
→ no output: no --sha flag parsing exists anywhere

$ grep -rn 'APPLY SHA' --include='*.go' .
→ no output: the spec's one-line output format string appears nowhere in code

$ sed -n '65,111p' docs/SPEC-VERSION.md (rule context lines 75–92)
→ Red-tests section for "nova-version moved and nova-update apply --sha"; rules 5–8 and 10 name TestApplySha* but none are implemented

Where I looked (so "absent" != "not found"):
- cmd/nova-version/: 7 test files, 0 referencing apply --sha
- cmd/nova-update/: main.go delegates to internal/update; same code path
- internal/update/cli.go:421 `apply()` implements a manifest-based update apply (reads entries[], runs e.Apply[] command); no --sha flag, no build step, no stamp verification
- internal/update/snapverb.go:189 mixed-set refusal lives under SNAPSHOT verb, not apply
- internal/update/cli.go:147–210 verb dispatch has no "sha" branch; verbs are {help,version,snapshot,diff,report,check,apply,watch,release,adoption,send}

The spec section header (line 16) reads "nova-version moved and nova-update apply --sha": the apply --sha behaviour belongs to nova-update, not nova-version. Even granting shared internals via internal/update/, the entire apply --sha build-mode pipeline is unwritten: no flag parsing, no build invocation, no postflight stamp check, no mixed-set detection, no atomic install, and no rollback. The only mixed-set enforcement is snapverb.go:188–189, on the snapshot verb, whose error message even suggests using `apply --sha` to fix it.

Left owed
git status --short
?? .lease
?? .nova-sandbox-tmp/
?? RESULT.md
?? harness-output.log
?? opencode.json
?? repo/

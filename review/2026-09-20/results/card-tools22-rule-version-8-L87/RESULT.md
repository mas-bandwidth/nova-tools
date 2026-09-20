"RESULT tools22-rule-version-8-L87 sha=5298f6be12ea — does the code at this base do what docs/SPEC-VERSION.md rule 8 says?
ABSENT
SPEC docs/SPEC-VERSION.md:87 rule 8
PKG cmd/nova-version
ASK The apply --sha verb must detect when its build process exceeds --timeout, exit with code 2 naming the timeout, and ensure --bin remains unchanged (no partial directory).
Where I looked:
- cmd/nova-version/main.go (19 lines): delegates to update.Main("nova-version", ...), only handles snapshot/diff/send/report/help/version verbs (line 165-176 in internal/update/cli.go)
- internal/update/cli.go(421): function apply() implements the manifest-based single-name apply verb, not the --sha build mode described by the spec. No --sha flag exists anywhere in cli.go or any other file.
- internal/update/snapverb.go: implements snapshotVerb; line 189 mentions "nova-update apply --sha" as a remediation suggestion but contains no implementation
- grep -rn "--sha\|applySha\|ApplySha" --include='*.go' . : no matches for any --sha handling or apply-sha code
- grep -rn "TestApplyShaIsBoundedByTheClock" --include='*_test.go' . : no test exists
- grep -rn "func Test" --include='*_test.go' cmd/nova-version/ : five tests (TestVersionStampAndUsage, TestRefusalNamesVersionNotUpdate, TestVersionHelpDoesNotDemandAnApplyVerb, TestVersionReportFileUsageStatesShape, TestExecutableFirstRun, TestMissingIndependentFlagsAreTogether, TestFirstRunTranscriptIsWhatTheToolPrintsLineForLine, TestFriendSequenceSnapshotReport, TestHelpExampleLinesRunAsPrinted) — none cover apply --sha
- grep -rn "TestApplySha" --include='*_test.go' . : no results
The spec at docs/SPEC-VERSION.md:38-47 describes apply --sha as building all ./cmd/... packages at a revision under one stamp into a staging directory, reading back every binary's version, verifying the postflight set, then atomically switching the --bin link. The help banner at lines 10-13 lists nova-update apply --sha <sha> --repo <dir> --bin <dir> [--timeout <d>] as a documented command, but no code in cmd/nova-version, internal/update/cli.go, internal/update/snapverb.go, or any other file implements --sha mode of apply. The regular apply verb (cli.go:421) takes a name from a manifest file and runs that single entry's shell command; it has no --sha flag, no repo parameter, no cmd/* discovery, no staged-build-then-swap semantics, and no clock/budget bounds on the build step (cli.go:463 uses o.timeout but that bounds only the child process read-back, not the go build itself).

Rule 8 requires: injected clock + fake build sleeping past --timeout → exit 2 with timeout named + no partial --bin. None of this behavior exists because apply --sha itself does not exist.

Left owed

git status --short

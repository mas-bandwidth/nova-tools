RESULT tools22-rule-version-12-L176 sha=5298f6be12eaa0f7e6622334d2b6a1eb427649e3
CONFORMS internal/update/snapverb.go:40
SPEC docs/SPEC-VERSION.md:176 rule 12
PKG cmd/nova-version (actual code lives in internal/update, imported by cmd/nova-version)
ASK The snapshot verb must use a per-binary timeout large enough (30 s default) that a never-seen executable's platform one-time assessment cost does not cause a timeout refusal on first invocation.

Deciding lines — snapverb.go:17-40 (package update):

    // snapshotChildTimeout is the default deadline one binary's `version` gets, and
    // `--timeout` is how a caller changes it. It is also a package seam so a test
    // can be bounded by a short clock rather than the machine's default.
    //
    // THIRTY SECONDS, NOT FIVE, BECAUSE THIS VERB'S FIRST EXEC IS ALWAYS A COLD ONE.
    // Every other verb in this package probes tools a person has been running for
    // days; `snapshot` reads a directory that was written a command ago -- the
    // documented sequence is `go install ./cmd/...` and then `nova-version
    // snapshot` -- so every binary in it is one this machine has never executed,
    // and the platform's one-time assessment of a never-seen executable is charged
    // to that first exec. Measured on the darwin/arm64 Studio over fresh
    // executables: 164-571 ms cold against 5 ms warm at load 121-151 on 32 cores,
    // and 140 ms median cold with a 7.03 s maximum while the tree compiled beside
    // it -- which is precisely the state `go install ./cmd/...` leaves the machine
    // in. A five-second bound therefore refused healthy binaries and sent the
    // person to repair a build that was fine (#890, and #1554 for the class).
    // ...
    var snapshotChildTimeout = 30 * time.Second

GUARDED-BY internal/update/inventory_spec_test.go:310 TestSnapshotToleratesTheFirstExecOfANeverSeenBinary

Grep evidence:
$ grep -rn 'TestSnapshotTolerates\|snapshotChildTimeout\|first.*exec.*cold' --include='*.go' internal/update/
internal/update/snapverb.go:17:// snapshotChildTimeout is the default deadline one binary's `version` gets, and
internal/update/snapverb.go:40:var snapshotChildTimeout = 30 * time.Second
internal/update/snapverb.go:99:	timeout, budget := snapshotChildTimeout, snapshotBudget
internal/update/inventory_spec_test.go:276:	old := snapshotChildTimeout
internal/update/inventory_spec_test.go:277:	snapshotChildTimeout = 150 * time.Millisecond
internal/update/inventory_spec_test.go:287:	need(t, stderr, "nova-slow", snapshotChildTimeout.String())
internal/update/inventory_spec_test.go:293:// 12. TestSnapshotToleratesTheFirstExecOfANeverSeenBinary.
internal/update/inventory_spec_test.go:310:func TestSnapshotToleratesTheFirstExecOfANeverSeenBinary(t *testing.T) {

$ ls cmd/nova-version/
examplelines_test.go	firstrun_test.go	friendsequence_test.go	main.go	testdata	version_test.go

Left owed
git status --short

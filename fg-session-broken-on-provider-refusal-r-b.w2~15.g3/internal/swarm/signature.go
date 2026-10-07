package swarm

import (
	"path/filepath"
	"strings"
)

// A READ VERDICT OR A BRANCH WHOSE RUN CARRIES A KNOWN FAILURE SIGNATURE WAS NOT EARNED.
//
// A card runs `go test` as a gate and writes its verdict on RESULT.md line 1 or 2; when the
// run's own capture, `harness-output.log`, holds a mechanical failure — the Go toolchain not
// available, no packages named, the harness fence auto-rejecting a path, a permission denied
// — the verdict the card printed was not earned, and reading it sends a coordinator to the
// model for a fault the machine made (Glenn, 2026-09-16: reads on Space printed APPROVE
// while their go test tail said toolchain not available). Such a verdict is scored
// ABSTAIN reason=signature sig="<signature>" class=<toolchain|packages|fence|permission>,
// never done.
//
// The table lives in the spec and in this binary as one slice, and the two agree: a test
// reads docs/SPEC-SWARM.md and compares it against failureSignatures row for row.

// failureSignature is one row of the failure-signature table: the text that matches a
// mechanical failure in a run's harness-output.log, the class that names its kind, and the
// remedy a coordinator reads.
type failureSignature struct {
	signature string
	class     string
	remedy    string
}

// failureSignatures is the one table of known failure signatures, in the order the spec
// names them. A verdict whose run carries one of these is ABSTAIN reason=signature.
var failureSignatures = []failureSignature{
	{signature: "toolchain not available", class: "toolchain", remedy: "pin the Go toolchain in go.mod, or install it"},
	{signature: "no packages to test", class: "packages", remedy: "name the packages to test; an empty list proves nothing"},
	{signature: "auto-rejecting", class: "fence", remedy: "the harness fence auto-rejected a path; re-run walled"},
	{signature: "permission denied", class: "permission", remedy: "the sandbox refused a read or write; keep the work inside the job directory"},
	{signature: "command not found: go", class: "toolchain", remedy: "install Go and put it on PATH before the run"},
	{signature: "cannot find package", class: "packages", remedy: "the package path is wrong, or its module is not in go.mod"},
}

// findFailureSignature scans a run's capture for the first known failure signature and
// returns it — the matched text and its class — or ok=false when none is present.
func findFailureSignature(raw []byte) (sig, class string, ok bool) {
	text := string(raw)
	for _, s := range failureSignatures {
		if strings.Contains(text, s.signature) {
			return s.signature, s.class, true
		}
	}
	return "", "", false
}

// failureSignatureInFile reads the harness-output.log at path and returns the first known
// failure signature it carries, or ok=false. An absent or unreadable capture is no
// signature: it is read with readRegular, so a symlink or FIFO is read as no record at all.
func failureSignatureInFile(path string) (sig, class string, ok bool) {
	raw, err := readRegular(path)
	if err != nil {
		return "", "", false
	}
	return findFailureSignature(raw)
}

// harnessOutputBeside is the run's own capture beside a RESULT.md: `verify` scans RESULT.md
// and this file, the same name `native` writes walled or not (issue #608).
func harnessOutputBeside(resultPath string) string {
	return filepath.Join(filepath.Dir(resultPath), "harness-output.log")
}

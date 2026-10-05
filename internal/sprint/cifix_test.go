package sprint

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// ParseFailedLog reads go test's own output too (no gh job columns), puts a test of the
// module's root package at ".", and names no test for a failed check that ran none.
func TestParseFailedLogReadsPlainGoTestOutput(t *testing.T) {
	t.Parallel()
	log := "--- FAIL: TestRoot (0.00s)\n    root_test.go:3: boom\nFAIL\texample.com/m\t0.01s\n"
	got := ParseFailedLog(log, "example.com/m")
	require.Equal(t, []CIFailure{{Test: "TestRoot", Package: "example.com/m", Dir: ".", File: "root_test.go", Line: "root_test.go:3: boom"}}, got)
	require.Contains(t, CIFixBrief(CIFixReq{Repo: "o/m", Base: "sprint/x", Source: "dev at 1"}, got[0]), "TEST: . TestRoot\n")

	require.Empty(t, ParseFailedLog("build\tgo vet\t2026-10-05T12:00:00Z ./x.go:3:1: undefined: y\nFAIL\texample.com/m [build failed]\n", "example.com/m"))
}

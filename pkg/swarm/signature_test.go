package swarm

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// THE FAILURE-SIGNATURE TABLE IS ONE TABLE IN TWO PLACES, and the two agree: the slice in
// signature.go and the table in docs/SPEC-SWARM.md. This test reads the spec and compares
// its rows against the slice, row for row, so a signature added to one and not the other is
// red.
func TestSignatureTableMatchesSpec(t *testing.T) {
	t.Parallel()

	spec := readSpecSignatureTable(t)
	require.Len(t, spec, len(failureSignatures), "the spec table holds %d rows and the binary %d; they must agree", len(spec), len(failureSignatures))
	for i := range failureSignatures {
		assert.Equal(t, failureSignatures[i], spec[i], "row %d disagrees: spec %+v, binary %+v", i, spec[i], failureSignatures[i])
	}
}

// EACH SIGNATURE HAS ONE TEST: the row's own text is planted in a run's capture and must be
// the one detected, with its own class. A signature the binary lists but cannot see, or one
// that returns the wrong class, is a bug in the table.
func TestEachSignatureIsDetected(t *testing.T) {
	t.Parallel()

	for _, s := range failureSignatures {
		s := s
		t.Run(s.signature, func(t *testing.T) {
			sig, class, ok := findFailureSignature([]byte("a run's tail\n" + s.signature + "\nmore\n"))
			require.True(t, ok, "the signature %q was not detected", s.signature)
			assert.Equal(t, s.signature, sig, "detected sig=%q class=%q; want sig=%q class=%q", sig, class, s.signature, s.class)
			assert.Equal(t, s.class, class, "detected sig=%q class=%q; want sig=%q class=%q", sig, class, s.signature, s.class)
		})
	}
}

// verify SCANS RESULT.md AND harness-output.log: a matching RESULT whose run's capture
// carries a known failure signature is not the read verdict the card wrote; it is an
// ABSTAIN naming the signature and its class.
func TestVerifyScoresSignature(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	result := filepath.Join(dir, "RESULT.md")
	writeResult(t, result, contractLine, "BRANCH: rowan/swarm-signatures at abc123")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "harness-output.log"),
		[]byte("go: download go1.26 for linux/amd64: toolchain not available\n"), 0o644))
	got, err := CheckResult(result, Contract{Label: "card-142", ContractLine: contractLine})
	require.NoError(t, err)
	require.False(t, got.OK, "a verdict whose run carries a failure signature is not OK: %s", got.Line)
	require.Contains(t, got.Line, `reason=signature sig="toolchain not available" class=toolchain`, "the verify line names the signature and its class: %s", got.Line)
}

// readSpecSignatureTable reads the failure-signature table out of the spec: the one table of
// signature, class and remedy, which is the same shape as the slice.
func readSpecSignatureTable(t *testing.T) []failureSignature {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "SPEC-SWARM.md"))
	require.NoError(t, err, "the table is the spec's: %v", err)
	lines := strings.Split(string(raw), "\n")
	start := -1
	for i, line := range lines {
		if line == "| signature | class | remedy |" {
			start = i
			break
		}
	}
	require.GreaterOrEqual(t, start, 0, "the spec has no failure-signature table")
	var out []failureSignature
	for _, line := range lines[start+1:] {
		if !strings.HasPrefix(line, "|") {
			break
		}
		cells := tableCells(line)
		if len(cells) != 3 {
			continue
		}
		if strings.Trim(cells[0], "-") == "" {
			continue
		}
		out = append(out, failureSignature{signature: cells[0], class: cells[1], remedy: cells[2]})
	}
	require.NotEmpty(t, out, "the failure-signature table is empty")
	return out
}

// tableCells splits one markdown table row into its trimmed cells, with the surrounding
// backticks removed from each.
func tableCells(line string) []string {
	parts := strings.Split(line, "|")
	cells := make([]string, 0, len(parts))
	for _, p := range parts {
		cells = append(cells, strings.Trim(strings.TrimSpace(p), "`"))
	}
	if len(cells) > 0 && cells[0] == "" {
		cells = cells[1:]
	}
	if len(cells) > 0 && cells[len(cells)-1] == "" {
		cells = cells[:len(cells)-1]
	}
	return cells
}

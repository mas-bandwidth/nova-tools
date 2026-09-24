package swarm

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// THE FAILURE-SIGNATURE TABLE IS ONE TABLE IN TWO PLACES, and the two agree: the slice in
// signature.go and the table in docs/SPEC-SWARM.md. This test reads the spec and compares
// its rows against the slice, row for row, so a signature added to one and not the other is
// red.
func TestSignatureTableMatchesSpec(t *testing.T) {
	spec := readSpecSignatureTable(t)
	if len(spec) != len(failureSignatures) {
		t.Fatalf("the spec table holds %d rows and the binary %d; they must agree", len(spec), len(failureSignatures))
	}
	for i := range failureSignatures {
		if spec[i] != failureSignatures[i] {
			t.Errorf("row %d disagrees: spec %+v, binary %+v", i, spec[i], failureSignatures[i])
		}
	}
}

// EACH SIGNATURE HAS ONE TEST: the row's own text is planted in a run's capture and must be
// the one detected, with its own class. A signature the binary lists but cannot see, or one
// that returns the wrong class, is a bug in the table.
func TestEachSignatureIsDetected(t *testing.T) {
	for _, s := range failureSignatures {
		s := s
		t.Run(s.signature, func(t *testing.T) {
			sig, class, ok := findFailureSignature([]byte("a run's tail\n" + s.signature + "\nmore\n"))
			if !ok {
				t.Fatalf("the signature %q was not detected", s.signature)
			}
			if sig != s.signature || class != s.class {
				t.Errorf("detected sig=%q class=%q; want sig=%q class=%q", sig, class, s.signature, s.class)
			}
		})
	}
}

// A READ VERDICT WHOSE RUN CARRIES A KNOWN FAILURE SIGNATURE IS ABSTAIN reason=signature,
// never done. The card's RESULT.md carries a verdict=APPROVE contract line and a BRANCH
// disposition, and the run's own harness-output.log holds the go toolchain's own words, so
// the batch must score reason=signature and never print the BRANCH.
func TestBatchScoresSignature(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "root")
	tsv := writeCards(t, dir, [][2]string{
		{"a", "RESULT: READ pr-1 at abc123 verdict=APPROVE\nBRANCH: rowan/swarm-signatures at abc123"},
	})
	runner := runnerDoing(t, dir, "signature",
		runnerStep{Op: "mkdir", Path: "{job}"},
		runnerStep{Op: "write", Path: "{job}/harness-output.log",
			Body: "go: download go1.26 for linux/amd64: toolchain not available\n"},
		publishCard("{job}"),
	)
	code, out, _ := runBatch(t, tsv, root, runner, 10*time.Second)
	if code != 1 {
		t.Fatalf("a card whose run carries a failure signature abstains, exits 1, got %d:\n%s", code, out)
	}
	if !strings.Contains(out, `a slot=1: ABSTAIN reason=signature sig="toolchain not available" class=toolchain`) {
		t.Fatalf("a read verdict whose run carries a known failure signature scores reason=signature:\n%s", out)
	}
	if strings.Contains(out, "a slot=1: BRANCH") {
		t.Fatalf("the BRANCH is never printed beside a failure signature:\n%s", out)
	}
}

// verify SCANS RESULT.md AND harness-output.log: a matching RESULT whose run's capture
// carries a known failure signature is not the read verdict the card wrote; it is an
// ABSTAIN naming the signature and its class.
func TestVerifyScoresSignature(t *testing.T) {
	dir := t.TempDir()
	result := filepath.Join(dir, "RESULT.md")
	writeResult(t, result, contractLine, "BRANCH: rowan/swarm-signatures at abc123")
	if err := os.WriteFile(filepath.Join(dir, "harness-output.log"),
		[]byte("go: download go1.26 for linux/amd64: toolchain not available\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := CheckResult(result, Contract{Label: "card-142", ContractLine: contractLine})
	if err != nil {
		t.Fatal(err)
	}
	if got.OK {
		t.Fatalf("a verdict whose run carries a failure signature is not OK: %s", got.Line)
	}
	if !strings.Contains(got.Line, `reason=signature sig="toolchain not available" class=toolchain`) {
		t.Fatalf("the verify line names the signature and its class: %s", got.Line)
	}
}

// readSpecSignatureTable reads the failure-signature table out of the spec: the one table of
// signature, class and remedy, which is the same shape as the slice.
func readSpecSignatureTable(t *testing.T) []failureSignature {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "SPEC-SWARM.md"))
	if err != nil {
		t.Fatalf("the table is the spec's: %v", err)
	}
	lines := strings.Split(string(raw), "\n")
	start := -1
	for i, line := range lines {
		if line == "| signature | class | remedy |" {
			start = i
			break
		}
	}
	if start < 0 {
		t.Fatal("the spec has no failure-signature table")
	}
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
	if len(out) == 0 {
		t.Fatal("the failure-signature table is empty")
	}
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

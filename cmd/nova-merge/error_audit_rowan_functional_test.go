//go:build functional

package main

// Rowan's failure-guidance audit (2026-09-26): every "not a lane" refusal in
// nova-merge (internal/merge/state.go:245-273,533) and nova-review
// (main.go:169,1775, reads.go:753) names `nova-merge init --lane ...` as the
// way to make one, and nova-merge has no init verb: it answers `unknown
// subcommand "init"`. The remedy is a dead end.

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
)

func TestRowanAuditLaneRemedyVerbExists(t *testing.T) {
	t.Parallel()
	lane := filepath.Join(t.TempDir(), "no-lane")
	var out, errOut bytes.Buffer
	run([]string{"read", "--lane", lane, "--pr", "7", "--who", "rowan", "--head", strings.Repeat("a", 40), "--verdict", "approve"}, &out, &errOut, production())
	refusal := out.String() + errOut.String()
	i := strings.Index(refusal, "; nova-merge ") + len("; ")
	if i < len("; ") {
		t.Fatalf("refusal names no nova-merge remedy: %q", refusal)
	}
	verb := strings.Fields(refusal[i+len("nova-merge "):])[0]
	out.Reset()
	errOut.Reset()
	run([]string{verb, "-h"}, &out, &errOut, production())
	if strings.Contains(out.String()+errOut.String(), "unknown subcommand") {
		t.Fatalf("the not-a-lane refusal %q sends the coordinator to `nova-merge %s`, which answers %q", strings.TrimSpace(refusal), verb, strings.TrimSpace(errOut.String()))
	}
}

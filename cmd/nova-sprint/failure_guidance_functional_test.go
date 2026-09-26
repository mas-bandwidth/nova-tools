//go:build functional

package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
)

// Probes beside Stella's (error_audit_stella_functional_test.go): each
// failure names the operation, the cause, what changed and the next action.

// TestLandEvalPolicySyncFailureIsNamed: a policy that reads but cannot reach
// the store's ns_policy_set (a store without the library, the missing
// function case) refuses before evaluation, naming the base, the operation,
// that nothing was evaluated and which bases changed; exit 2.
func TestLandEvalPolicySyncFailureIsNamed(t *testing.T) {
	t.Parallel()
	addr := testutil.Start(t)
	path := filepath.Join(t.TempDir(), "policy.yml")
	if err := os.WriteFile(path, []byte("repo: nova-tools\nbases:\n  dev:\n    readers: 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	code := runLandEval(context.Background(), []string{"--redis", addr, "--sprint", sdSprint, "--repo", "nova-tools", "--policy", path, "--mirror", "none"}, &out, &errOut)
	for _, want := range []string{"policy " + path + ": sync base dev to the store (ns_policy_set)", "nothing evaluated", "bases - synced, dev not"} {
		if code != 2 || !strings.Contains(errOut.String(), want) {
			t.Fatalf("sync failure: exit=%d stdout=%q stderr=%q (want %q)", code, out.String(), errOut.String(), want)
		}
	}
}

// TestLandEvalReceiptNamesThePolicy: the EVAL receipt says which policy file
// ran (policy=<path>) or that none was found (policy=none), so a default
// discovery that found nothing is visible, never silent.
func TestLandEvalReceiptNamesThePolicy(t *testing.T) {
	t.Parallel()
	c, _ := sdStore(t)
	path := filepath.Join(t.TempDir(), "policy.yml")
	if err := os.WriteFile(path, []byte("repo: nova-tools\nbases:\n  dev:\n    readers: 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ policy, want string }{{path, "policy=" + path}, {"", "policy=none"}} {
		args := []string{"--redis", c.Options().Addr, "--sprint", sdSprint, "--repo", "nova-tools", "--mirror", "none"}
		if tc.policy != "" {
			args = append(args, "--policy", tc.policy)
		}
		var out, errOut bytes.Buffer
		if code := runLandEval(context.Background(), args, &out, &errOut); code != 0 || !strings.Contains(out.String(), " "+tc.want+"\n") {
			t.Fatalf("policy %q: exit=%d stdout=%q stderr=%q (want %q)", tc.policy, code, out.String(), errOut.String(), tc.want)
		}
	}
}

// TestHoldShowAbsentIsNotAnError keeps absent distinct from failed (finding
// 4's other half): a unit nobody ingested is "no unit" with the key that is
// absent and the ingest to run, exit 2, and never a store error.
func TestHoldShowAbsentIsNotAnError(t *testing.T) {
	t.Parallel()
	c, _ := sdStore(t)
	var out, errOut bytes.Buffer
	code := runHoldShow(context.Background(), []string{"--redis", c.Options().Addr, "--sprint", sdSprint, "nova-tools#8"}, &out, &errOut)
	for _, want := range []string{"no unit for nova-tools#8 in sprint " + sdSprint, "s:" + sdSprint + ":prunit:nova-tools:8 is absent", "nova-sprint hold ingest"} {
		if code != 2 || !strings.Contains(errOut.String(), want) {
			t.Fatalf("absent unit: exit=%d stderr=%q (want %q)", code, errOut.String(), want)
		}
	}
	if strings.Contains(errOut.String(), "unknown") {
		t.Fatalf("an absent unit reported as unknown: %q", errOut.String())
	}
}

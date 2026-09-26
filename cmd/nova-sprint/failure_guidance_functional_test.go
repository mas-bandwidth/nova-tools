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
// 4's other half): a unit nothing made is "no unit" with the key that is
// absent and the land eval that makes units (round 2: hold ingest resolves
// through the same key and refuses without it), exit 2, never a store error.
func TestHoldShowAbsentIsNotAnError(t *testing.T) {
	t.Parallel()
	c, _ := sdStore(t)
	var out, errOut bytes.Buffer
	code := runHoldShow(context.Background(), []string{"--redis", c.Options().Addr, "--sprint", sdSprint, "nova-tools#8"}, &out, &errOut)
	for _, want := range []string{"no unit for nova-tools#8 in sprint " + sdSprint, "s:" + sdSprint + ":prunit:nova-tools:8 is absent", "a unit is made only by land eval", "nova-sprint land eval --sprint " + sdSprint + " --repo nova-tools"} {
		if code != 2 || !strings.Contains(errOut.String(), want) {
			t.Fatalf("absent unit: exit=%d stderr=%q (want %q)", code, errOut.String(), want)
		}
	}
	if strings.Contains(errOut.String(), "unknown") {
		t.Fatalf("an absent unit reported as unknown: %q", errOut.String())
	}
}

// TestLandEvalBadDefaultPolicyRefusesAtTheCommand (round 2): the default
// discovery, fleet/land/<repo>.yml under the working directory, when the
// file is there and malformed, refuses through run() the same way an
// explicit --policy does, naming the path, the field and that nothing ran.
// The test cannot chdir (t.Parallel), so --repo carries the relative walk
// from this package directory to a temp fleet/land tree; the discovered path
// is still fleet/land/<repo>.yml joined and cleaned.
func TestLandEvalBadDefaultPolicyRefusesAtTheCommand(t *testing.T) {
	t.Parallel()
	c, _ := sdStore(t)
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "fleet", "land"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "fleet", "land", "nova-tools.yml"), []byte("repo: nova-tools\nbases:\n  dev:\n    land_bar: ten\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	rel, err := filepath.Rel(cwd, root)
	if err != nil {
		t.Fatal(err)
	}
	repo := filepath.ToSlash(filepath.Join("..", "..", rel, "fleet", "land", "nova-tools"))
	var out, errOut bytes.Buffer
	code := run([]string{"land", "eval", "--redis", c.Options().Addr, "--sprint", sdSprint, "--repo", repo, "--mirror", "none"}, &out, &errOut)
	for _, want := range []string{"(the default fleet/land/<repo>.yml) could not be used", "field land_bar: value \"ten\" is not a whole number", "nothing evaluated, store unchanged"} {
		if code != 2 || out.Len() != 0 || !strings.Contains(errOut.String(), want) {
			t.Fatalf("bad default policy: exit=%d stdout=%q stderr=%q (want %q)", code, out.String(), errOut.String(), want)
		}
	}
}

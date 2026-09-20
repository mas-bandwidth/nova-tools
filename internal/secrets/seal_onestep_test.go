package secrets

import (
	"strings"
	"testing"
)

// TestSealIsOneStep pins docs/SPEC-SECRETS.md:1040: "seal is one step (see #910):
// stdin or hidden prompt, PR, gate, merge, check, because a multi-step seal is a
// step somebody stops halfway." A single RunSeal call must carry the value from
// stdin through encrypt, commit, PR, merge, pull and seat check — every one of
// them, before the function returns — never leaving a step for the operator to
// finish by hand.
func TestSealIsOneStep(t *testing.T) {
	skipPOSIXFakesOnWindows(t)
	f := newSealFixture(t, "TARGET: oldvalue\nOTHER: keepme\n")

	checkCalled := false
	opts := f.options(t, "TARGET", "newvalue\n", false)
	opts.Check = func(storeDir, asName, keyPath, sopsPath string) error {
		checkCalled = true
		if storeDir != f.storeDir {
			t.Errorf("Check storeDir = %q, want %q", storeDir, f.storeDir)
		}
		if asName != "rowan" {
			t.Errorf("Check asName = %q, want rowan", asName)
		}
		return nil
	}

	line, err := RunSeal(opts)
	if err != nil {
		t.Fatalf("RunSeal: %v", err)
	}

	if !checkCalled {
		t.Error("seat check was not called after merge — seal stopped before verifying the seat decrypts")
	}

	argv := readMaybe(t, f.sopsArgs)
	if !strings.Contains(argv, "-d") {
		t.Errorf("sops decrypt was not called:\n%s", argv)
	}
	if !strings.Contains(argv, "-e") {
		t.Errorf("sops encrypt was not called:\n%s", argv)
	}

	git := readMaybe(t, f.gitArgs)
	for _, want := range []string{"checkout", "-b", "add", "commit", "push"} {
		if !strings.Contains(git, want) {
			t.Errorf("git missing %q — seal did not complete the commit chain:\n%s", want, git)
		}
	}

	gh := readMaybe(t, f.ghArgs)
	for _, want := range []string{"pr", "create", "pr", "view", "pr", "merge"} {
		if !strings.Contains(gh, want) {
			t.Errorf("gh missing %q — seal did not complete the PR chain:\n%s", want, gh)
		}
	}

	if !strings.Contains(line, "SEAL OK") || !strings.Contains(line, "merged") {
		t.Errorf("result does not end with the merged receipt: %s", line)
	}
	if !strings.Contains(line, "pr=#42") {
		t.Errorf("result does not name the PR number: %s", line)
	}
}

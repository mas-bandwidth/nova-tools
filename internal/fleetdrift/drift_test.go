package fleetdrift

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The two red tests of SPEC-FLEET-KUBE Part 5 that this slice turns green:
//
//   - terraform-plan-on-a-fixture-shows-exactly-the-expected-drift
//   - a-hand-edited-remote-file-is-shown-by-the-plan-not-by-the-declaration
//
// The third test proves the read is a real remote read: the observer runs the
// same `ssh <target> sha256sum <path>` the module's data "external" runs, with a
// fake ssh on PATH, so no test makes a network call.

func hashOf(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// fixtureManifest is a bench whose declaration names three files, two of them
// witnessed and one a null_resource trigger only (Witness false).
func fixtureManifest() Manifest {
	return Manifest{
		"bench-file-go-version": {
			Path:     "nova-bench/go.version",
			Declared: hashOf("go1.26.5\n"),
			Witness:  true,
		},
		"bench-file-runner-unit": {
			Path:     ".config/systemd/user/nova-runner-1.service",
			Declared: hashOf("[Service]\nKillMode=control-group\n"),
			Witness:  true,
		},
		"bench-file-trigger-only": {
			Path:     ".config/systemd/user/nova-mirror.timer",
			Declared: hashOf("[Timer]\nOnCalendar=daily\n"),
			Witness:  false,
		},
	}
}

// TestTerraformPlanOnAFixtureShowsExactlyTheExpectedDrift: a fixture bench whose
// declaration says one go version and whose observed state says another plans
// the one differing resource, and a clean fixture plans nothing.
func TestTerraformPlanOnAFixtureShowsExactlyTheExpectedDrift(t *testing.T) {
	m := fixtureManifest()

	// The remote state matches the declaration on two files and differs on the
	// go version: exactly one drift, and it is the go-version resource.
	observed := map[string]string{
		"nova-bench/go.version":                      hashOf("go1.25.0\n"),
		".config/systemd/user/nova-runner-1.service": m["bench-file-runner-unit"].Declared,
		".config/systemd/user/nova-mirror.timer":     hashOf("[Timer]\nOnCalendar=daily\n"),
	}
	drifts, err := Plan(context.Background(), m, func(_ context.Context, path string) (string, error) {
		return observed[path], nil
	})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if len(drifts) != 1 {
		t.Fatalf("Plan found %d drifts, want exactly 1: %+v", len(drifts), drifts)
	}
	if drifts[0].Resource != "bench-file-go-version" {
		t.Errorf("the one drift is %q, want bench-file-go-version", drifts[0].Resource)
	}
	if drifts[0].Declared != m["bench-file-go-version"].Declared || drifts[0].Observed != observed["nova-bench/go.version"] {
		t.Errorf("drift hashes = declared %q observed %q, want the declaration and the observed bytes", drifts[0].Declared, drifts[0].Observed)
	}

	// Clean fixture: every witnessed observation equals its declaration, so the
	// plan is empty (a plan that is not empty before an apply is the drift).
	clean, err := Plan(context.Background(), m, func(_ context.Context, path string) (string, error) {
		for _, r := range m {
			if r.Path == path {
				return r.Declared, nil
			}
		}
		return "", nil
	})
	if err != nil {
		t.Fatalf("Plan clean: %v", err)
	}
	if len(clean) != 0 {
		t.Fatalf("a clean fixture planned %d drifts, want none: %+v", len(clean), clean)
	}
}

// TestAHandEditedRemoteFileIsShownByThePlanNotByTheDeclaration: a fixture bench
// whose remote file is edited by hand while the declaration is unchanged plans
// that file, because the read-each-plan observer reports the observed hash. The
// same edit with only a null_resource trigger (Witness false) stays clean,
// proving the trigger is not the witness.
func TestAHandEditedRemoteFileIsShownByThePlanNotByTheDeclaration(t *testing.T) {
	edited := hashOf("hand-edited on the bench\n")

	// Witnessed: the observed hash is the hand edit and the declaration is
	// unchanged, so the plan shows the file.
	witnessed := Manifest{
		"bench-file-runner-unit": {
			Path:     ".config/systemd/user/nova-runner-1.service",
			Declared: hashOf("the declared bytes\n"),
			Witness:  true,
		},
	}
	drifts, err := Plan(context.Background(), witnessed, func(_ context.Context, _ string) (string, error) {
		return edited, nil
	})
	if err != nil {
		t.Fatalf("Plan witnessed: %v", err)
	}
	if len(drifts) != 1 || drifts[0].Resource != "bench-file-runner-unit" {
		t.Fatalf("a hand-edited witnessed file planned %+v, want the runner-unit drift", drifts)
	}

	// The same edit with a null_resource trigger only: the trigger hashes the
	// declaration, so the hand edit is invisible and the plan stays clean.
	triggerOnly := Manifest{
		"bench-file-runner-unit": {
			Path:     ".config/systemd/user/nova-runner-1.service",
			Declared: hashOf("the declared bytes\n"),
			Witness:  false,
		},
	}
	clean, err := Plan(context.Background(), triggerOnly, func(_ context.Context, _ string) (string, error) {
		return edited, nil
	})
	if err != nil {
		t.Fatalf("Plan trigger only: %v", err)
	}
	if len(clean) != 0 {
		t.Fatalf("a hand edit with only a trigger planned %+v; the trigger is not the witness", clean)
	}
}

// TestFleetPlanReadsTheRemoteFileOverFakeSSH proves the observer is a real
// remote read: it runs `ssh <target> sha256sum <path>` through the ssh program
// from the caller, and a fake ssh on PATH answers from the fixture bench tree.
func TestFleetPlanReadsTheRemoteFileOverFakeSSH(t *testing.T) {
	bin := t.TempDir()
	fake := filepath.Join(bin, "ssh")
	// The real argv is `ssh -o BatchMode=yes -o ConnectTimeout=5 <target>
	// sha256sum <path>`; the fake drops the transport options and the target and
	// runs the remote command locally, which is the remote read with no network.
	script := "#!/bin/sh\nshift 5\nexec \"$@\"\n"
	if err := os.WriteFile(fake, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	host := t.TempDir()
	remotePath := filepath.Join(host, "go.version")
	if err := os.WriteFile(remotePath, []byte("go1.26.5\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	m := Manifest{
		"bench-file-go-version": {
			Path:     remotePath,
			Declared: hashOf("go1.26.5\n"),
			Witness:  true,
		},
	}
	observer := SSHObserver("", "space", 0)
	drifts, err := Plan(context.Background(), m, observer)
	if err != nil {
		t.Fatalf("Plan over fake ssh: %v", err)
	}
	if len(drifts) != 0 {
		t.Fatalf("the matching remote file planned %+v, want clean", drifts)
	}

	// The hand edit is what the plan now shows, and the observer read it over
	// the fake transport rather than from the declaration.
	if err := os.WriteFile(remotePath, []byte("go1.25.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	drifts, err = Plan(context.Background(), m, observer)
	if err != nil {
		t.Fatalf("Plan over fake ssh after edit: %v", err)
	}
	if len(drifts) != 1 {
		t.Fatalf("after the hand edit Plan found %d drifts, want exactly 1: %+v", len(drifts), drifts)
	}
	if !strings.HasPrefix(drifts[0].Observed, hashOf("go1.25.0\n")) {
		t.Errorf("observed %q, want the edited file's hash", drifts[0].Observed)
	}
}

// TestFleetPlanRefusesAnUnreadableManifest: a module with no declared-files
// manifest is a refusal with a remedy, not a clean plan.
func TestFleetPlanRefusesAnUnreadableManifest(t *testing.T) {
	_, err := LoadManifest(filepath.Join(t.TempDir(), "files.json"))
	if err == nil {
		t.Fatal("LoadManifest on a missing manifest returned no error; a missing declaration is not a clean plan")
	}
}

// TestTheBenchModuleDeclaresEveryFileWithAReadEachPlanWitness finds the module
// the card ships (infra/terraform/modules/bench/files.json), loads it through
// the same LoadManifest the fleet plan verb uses, and proves the declaration is
// self-consistent: every managed file carries a witness, and every declared
// hash is the sha256 of the content the module writes. A managed file with no
// witness would be a null_resource trigger pretending to be a drift detector.
func TestTheBenchModuleDeclaresEveryFileWithAReadEachPlanWitness(t *testing.T) {
	path := ""
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 8; i++ {
		candidate := filepath.Join(dir, "infra", "terraform", "modules", "bench", "files.json")
		if _, err := os.Stat(candidate); err == nil {
			path = candidate
			break
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	if path == "" {
		t.Fatal("infra/terraform/modules/bench/files.json not found above the working directory")
	}
	m, err := LoadManifest(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(m) == 0 {
		t.Fatal("the bench module declares no managed files")
	}
	for name, r := range m {
		if !r.Witness {
			t.Errorf("%s has no read-each-plan witness; a null_resource trigger is not the drift detector", name)
		}
		if r.Path == "" {
			t.Errorf("%s declares no path", name)
		}
		if got := hashOf(r.Content); got != r.Declared {
			t.Errorf("%s declared sha256 %s, but sha256(content) is %s", name, r.Declared, got)
		}
	}
}

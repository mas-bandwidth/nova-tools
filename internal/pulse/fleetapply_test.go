package pulse

// `fleet standard --apply`, held to the three rules the remedies were written under:
// idempotent, nothing deleted, and the big one not automated.
//
// These tests RUN THE REMEDIES. The fake ssh of fleetverbs_test.go pipes the script into
// `bash -s`, so each remedy is applied to a temporary home on this machine, twice, and the
// test reads the files afterwards. That is the only way idempotence can be claimed: a remedy
// that a fake merely "accepted" is a remedy nobody has run.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// applyMachines writes a machines registry whose one Linux machine is the fake bench.
func applyMachines(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "machines.tsv")
	body := "worker-1\tnova@worker1\tlinux/x64\tbench,runner\t-\t16\tallow-shared=2026-09-18 the fake bench\n" +
		"studio\tglenn@studio\tdarwin/arm64\tcoordination\trowan\t24\t-\n"
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// applyHome is a machine in the state the fleet was in on the morning of 2026-09-18: a
// ~/.bashrc that returns before any PATH line for a non-interactive shell, nova-* shadows in
// ~/go/bin, and a runner .path with no Go on it. The git identity is answered by the fake
// git of newFleetVerbsFake, which is why this home does not write one.
func applyHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	// Ubuntu's own file, near enough: the guard is the second line.
	bashrc := "# ~/.bashrc: executed by bash(1) for non-login shells.\n" +
		"case $- in\n    *i*) ;;\n      *) return;;\nesac\n" +
		"PATH=\"$HOME/.local/bin:$PATH\"\n"
	if err := os.WriteFile(filepath.Join(home, ".bashrc"), []byte(bashrc), 0o644); err != nil {
		t.Fatal(err)
	}
	gobin := filepath.Join(home, "go", "bin")
	if err := os.MkdirAll(gobin, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, tool := range []string{"nova-merge", "nova-swarm", "nova-pulse"} {
		writeFleetVerbsExe(t, filepath.Join(gobin, tool), "#!/bin/sh\necho 'v0.15.3'\n")
	}
	// Something that is NOT a nova tool, to prove the remedy moves what it says it moves.
	writeFleetVerbsExe(t, filepath.Join(gobin, "staticcheck"), "#!/bin/sh\nexit 0\n")
	writeFleetVerbsExe(t, filepath.Join(home, "sdk", "go1.26.5", "bin", "go"),
		"#!/bin/sh\necho 'go version go1.26.5 linux/amd64'\n")
	runner := filepath.Join(home, "runner-nova-tools-1")
	if err := os.MkdirAll(runner, 0o755); err != nil {
		t.Fatal(err)
	}
	// space's sixteen, exactly: the bare distro PATH, no go anywhere on it.
	if err := os.WriteFile(filepath.Join(runner, ".path"), []byte("/usr/local/bin:/usr/bin:/bin\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return home
}

func runApply(t *testing.T, in ApplyInput) (string, string, ApplyOutcome) {
	t.Helper()
	var out, errs strings.Builder
	in.Stdout, in.Stderr = &out, &errs
	outcome := FleetStandardApply(in)
	return out.String(), errs.String(), outcome
}

// TestApplyRepairsTheFourFaultsOfTheHandPassAndSaysWhatItChanged is the whole verb against
// the machine the fleet actually was.
func TestApplyRepairsTheFourFaultsOfTheHandPassAndSaysWhatItChanged(t *testing.T) {
	fake := newFleetVerbsFake(t)
	home := applyHome(t)
	out, errs, outcome := runApply(t, ApplyInput{
		Machines: applyMachines(t), Name: "worker-1", SSH: fake.SSH, Home: home,
	})
	if outcome.Code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout:%s\nstderr:%s", outcome.Code, out, errs)
	}
	for _, item := range []string{"path-noninteractive", "gobin-shadow", "git-identity", "runner-path-go", "nova-stamp"} {
		if !strings.Contains(out, "STANDARD APPLY worker-1 "+item+" ") {
			t.Errorf("no line for %s:\n%s", item, out)
		}
	}
	for _, item := range []string{"path-noninteractive", "gobin-shadow", "runner-path-go"} {
		if !strings.Contains(out, "STANDARD APPLY worker-1 "+item+" changed") {
			t.Errorf("%s did not repair anything on a machine that carried the fault:\n%s", item, out)
		}
	}
	// The PATH block is ABOVE the interactive guard, which is the whole fault.
	bashrc := applyRead(t, filepath.Join(home, ".bashrc"))
	block := strings.Index(bashrc, "# >>> nova non-interactive PATH >>>")
	guard := strings.Index(bashrc, "return;;")
	if block < 0 {
		t.Fatalf("the marker block was not written:\n%s", bashrc)
	}
	if guard >= 0 && block > guard {
		t.Errorf("the block is BELOW the interactive guard, where no non-interactive shell reads it:\n%s", bashrc)
	}
	if !strings.Contains(bashrc, "# <<< nova non-interactive PATH <<<") {
		t.Errorf("the block is not closed by a marker, so it cannot be found or removed:\n%s", bashrc)
	}
	// Ubuntu's own file survives, whole.
	if !strings.Contains(bashrc, "# ~/.bashrc: executed by bash(1) for non-login shells.") {
		t.Errorf("the remedy overwrote the machine's own ~/.bashrc:\n%s", bashrc)
	}
	// The shadows are MOVED, never deleted, and what is not a nova tool is left alone.
	for _, tool := range []string{"nova-merge", "nova-swarm", "nova-pulse"} {
		if _, err := os.Stat(filepath.Join(home, "go", "bin", tool)); err == nil {
			t.Errorf("%s is still in ~/go/bin, ahead of the release on the same PATH", tool)
		}
	}
	if _, err := os.Stat(filepath.Join(home, "go", "bin", "staticcheck")); err != nil {
		t.Error("the remedy moved a binary that is not a nova tool")
	}
	moved, err := filepath.Glob(filepath.Join(home, "nova-bench", "stale-gobin-*", "nova-merge"))
	if err != nil || len(moved) != 1 {
		t.Errorf("the shadow binaries were not moved aside to ~/nova-bench/stale-gobin-<date>/: %v", moved)
	}
	// The runner's .path leads with the Go SDK, and keeps what it had.
	dotPath := applyRead(t, filepath.Join(home, "runner-nova-tools-1", ".path"))
	if !strings.HasPrefix(dotPath, filepath.Join(home, "sdk", "go1.26.5", "bin")+":") {
		t.Errorf(".path does not lead with the Go SDK: %q", dotPath)
	}
	if !strings.Contains(dotPath, "/usr/local/bin:/usr/bin:/bin") {
		t.Errorf(".path lost what the runner had: %q", dotPath)
	}
	if strings.Count(strings.TrimSpace(dotPath), "\n") != 0 {
		t.Errorf(".path is not one line any more: %q", dotPath)
	}
}

// TestApplyIsIdempotent: the second run changes nothing and says so. The loop runs this
// after every failure, and a remedy that appends its block each time is a ~/.bashrc nobody
// can read by Friday.
func TestApplyIsIdempotent(t *testing.T) {
	fake := newFleetVerbsFake(t)
	home := applyHome(t)
	machines := applyMachines(t)
	if _, errs, outcome := runApply(t, ApplyInput{Machines: machines, Name: "worker-1", SSH: fake.SSH, Home: home}); outcome.Code != 0 {
		t.Fatalf("the first apply exited %d\n%s", outcome.Code, errs)
	}
	out, errs, outcome := runApply(t, ApplyInput{Machines: machines, Name: "worker-1", SSH: fake.SSH, Home: home})
	if outcome.Code != 0 {
		t.Fatalf("the second apply exited %d\n%s%s", outcome.Code, out, errs)
	}
	if changed := outcome.Changed(); len(changed) != 0 {
		t.Errorf("the second apply changed %v; every remedy is idempotent", changed)
	}
	bashrc := applyRead(t, filepath.Join(home, ".bashrc"))
	if n := strings.Count(bashrc, "# >>> nova non-interactive PATH >>>"); n != 1 {
		t.Errorf("the marker block is in ~/.bashrc %d times, want 1:\n%s", n, bashrc)
	}
}

// TestApplyNeverRunsTheAdopt: a stale build is `nova-update release adopt`, which stops
// cards, swaps binaries and re-certifies. Apply names it and does not run it.
func TestApplyNeverRunsTheAdopt(t *testing.T) {
	fake := newFleetVerbsFake(t)
	out, _, outcome := runApply(t, ApplyInput{
		Machines: applyMachines(t), Name: "worker-1", SSH: fake.SSH, Home: applyHome(t),
		Items: []string{"nova-stamp"},
	})
	if outcome.Code != 0 {
		t.Fatalf("exit = %d, want 0\n%s", outcome.Code, out)
	}
	if !strings.Contains(out, "STANDARD APPLY worker-1 nova-stamp unchanged remedy=adopt") {
		t.Errorf("the stale-build item does not name the adopt as its remedy:\n%s", out)
	}
	if strings.Contains(fake.log(t, "ssh.log"), "worker1") {
		t.Errorf("the adopt item reached the machine:\n%s", fake.log(t, "ssh.log"))
	}
	if len(outcome.Changed()) != 0 {
		t.Errorf("the adopt item reported a change: %v", outcome.Changed())
	}
}

// TestApplyDryRunReachesNoMachine: --dry-run must exist and change nothing, because the
// first thing anybody does with a mutating verb is ask it what it would do.
func TestApplyDryRunReachesNoMachine(t *testing.T) {
	fake := newFleetVerbsFake(t)
	home := applyHome(t)
	before := applyRead(t, filepath.Join(home, ".bashrc"))
	out, _, outcome := runApply(t, ApplyInput{
		Machines: applyMachines(t), Name: "worker-1", SSH: fake.SSH, Home: home, DryRun: true,
	})
	if outcome.Code != 0 {
		t.Fatalf("exit = %d, want 0\n%s", outcome.Code, out)
	}
	if !strings.Contains(out, "STANDARD APPLY worker-1 path-noninteractive would") {
		t.Errorf("--dry-run does not say what it would do:\n%s", out)
	}
	if log := fake.log(t, "ssh.log"); strings.TrimSpace(log) != "" {
		t.Errorf("--dry-run reached the machine:\n%s", log)
	}
	if after := applyRead(t, filepath.Join(home, ".bashrc")); after != before {
		t.Errorf("--dry-run changed ~/.bashrc:\n%s", after)
	}
	if len(outcome.Changed()) != 0 {
		t.Errorf("--dry-run reported changes: %v", outcome.Changed())
	}
}

// TestApplyRefusesAnUnknownMachineOrItemBeforeAnySSH: --apply CHANGES a machine, so every
// refusal it can make it makes before it reaches one.
func TestApplyRefusesAnUnknownMachineOrItemBeforeAnySSH(t *testing.T) {
	fake := newFleetVerbsFake(t)
	machines := applyMachines(t)
	for _, tc := range []struct {
		name  string
		in    ApplyInput
		wants string
	}{
		{"no machine", ApplyInput{Machines: machines, SSH: fake.SSH}, "--machine"},
		{"unknown machine", ApplyInput{Machines: machines, Name: "potato", SSH: fake.SSH}, "potato"},
		{"unknown item", ApplyInput{Machines: machines, Name: "worker-1", SSH: fake.SSH, Items: []string{"reboot-it"}}, "reboot-it"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, errs, outcome := runApply(t, tc.in)
			if outcome.Code != 2 {
				t.Fatalf("exit = %d, want 2\n%s", outcome.Code, errs)
			}
			if !strings.Contains(errs, tc.wants) {
				t.Errorf("the refusal does not name %q:\n%s", tc.wants, errs)
			}
			if log := fake.log(t, "ssh.log"); strings.TrimSpace(log) != "" {
				t.Errorf("a refusal reached the machine:\n%s", log)
			}
		})
	}
}

// TestARemedyThatSaysNothingIsAFailureAndNotAPass: the verdict comes from what the machine
// said, never from the exit code alone (memory: fakes strict like the real tool).
func TestARemedyThatSaysNothingIsAFailureAndNotAPass(t *testing.T) {
	fake := newFleetVerbsFake(t)
	// An ssh that runs nothing and exits 0: the shape of a remedy that silently did not run.
	writeFleetVerbsExe(t, fake.SSH, "#!/bin/sh\ncat > /dev/null\nexit 0\n")
	out, errs, outcome := runApply(t, ApplyInput{
		Machines: applyMachines(t), Name: "worker-1", SSH: fake.SSH, Home: applyHome(t),
		Items: []string{"git-identity"},
	})
	if outcome.Code != 3 {
		t.Fatalf("exit = %d, want 3\n%s%s", outcome.Code, out, errs)
	}
	if !strings.Contains(errs, "STANDARD APPLY worker-1 git-identity failed") {
		t.Errorf("a remedy that printed nothing was not a failure:\n%s", errs)
	}
}

// TestEveryRemedyRepairsACheckOfTheProvisioningStandard: an item that names no check is a
// repair for nothing, and a class that maps to it can never be certified by it.
func TestEveryRemedyRepairsACheckOfTheProvisioningStandard(t *testing.T) {
	names := map[string]bool{}
	for _, goos := range []string{"linux", "darwin"} {
		for _, c := range FleetStandardChecks(goos, "go1.26.5", "abc123", 25) {
			names[c.Name] = true
		}
	}
	for _, goos := range []string{"linux", "darwin"} {
		for _, r := range StandardRemedies(goos, "Rowan", "rowan@mas-bandwidth.com") {
			if !names[r.Item] {
				t.Errorf("the %s remedy %s repairs no check of the provisioning standard", goos, r.Item)
			}
		}
	}
}

// TestNoRemedyDeletesAnything is a class rule with a cost behind it: the shadow binaries are
// the ONLY copy of a tool somebody may still need, and a remedy loop with an `rm` in it is
// one bad glob away from a machine with no tools at all (memory: deletion is a verb over a
// validated path below a root, and a remedy is not that verb).
func TestNoRemedyDeletesAnything(t *testing.T) {
	for _, goos := range []string{"linux", "darwin"} {
		for _, r := range StandardRemedies(goos, "Rowan", "rowan@mas-bandwidth.com") {
			for _, bad := range []string{"rm ", "rm -", "rmdir", "shred", "truncate", "> /dev/sd"} {
				if strings.Contains(r.Script, bad) {
					t.Errorf("the %s remedy for %s carries %q; a remedy moves things aside and never deletes:\n%s",
						goos, r.Item, bad, r.Script)
				}
			}
		}
	}
}

// TestNoRemedyUsesCase is the same class rule the probes carry, for the same reason: bash
// 3.2 is the /bin/bash on every Mac in this fleet, and a `case` inside a `$( )` truncates
// everything after it in silence.
func TestNoRemedyUsesCase(t *testing.T) {
	for _, goos := range []string{"linux", "darwin"} {
		for _, r := range StandardRemedies(goos, "Rowan", "rowan@mas-bandwidth.com") {
			if strings.Contains(r.Script, "case ") || strings.Contains(r.Script, "esac") {
				t.Errorf("the %s remedy for %s uses `case`:\n%s", goos, r.Item, r.Script)
			}
		}
	}
}

// applyRead is one file, read back after a remedy ran.
func applyRead(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

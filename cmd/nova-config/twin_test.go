package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/goenv"
)

// twinStep runs one command against realDeps(), returning exit code, stdout, and stderr.
func twinStep(args []string) (int, string, string) {
	var out, errb bytes.Buffer
	code := run(args, &out, &errb, realDeps())
	return code, out.String(), errb.String()
}

// TestFileTwinLifecycle tests the full configuration lifecycle against a fresh
// file twin (--pg file:<path>) without PostgreSQL or Redis.
func TestFileTwinLifecycle(t *testing.T) {
	dir := t.TempDir()
	twinPath := filepath.Join(dir, "config.json")
	pgFlag := "--pg=file:" + twinPath

	// 1. machine add
	code, out, errs := twinStep([]string{"machine", "add", "m1", "--user", "glenn", "--seat", "seat-a", "--slots", "2", "--runners", "1", "--as", "boss", pgFlag})
	if code != 0 || errs != "" {
		t.Fatalf("machine add: exit %d, stderr %q\nstdout: %s", code, errs, out)
	}
	if !strings.Contains(out, "CONFIG ADD kind=machine name=m1 rev=1") {
		t.Fatalf("machine add output mismatch: %s", out)
	}

	// Verify file was created on disk
	if _, err := os.Stat(twinPath); err != nil {
		t.Fatalf("twin file was not created: %v", err)
	}

	// 2. machine list
	code, out, errs = twinStep([]string{"machine", "list", pgFlag})
	if code != 0 || errs != "" {
		t.Fatalf("machine list: exit %d, stderr %q\nstdout: %s", code, errs, out)
	}
	if !strings.Contains(out, "MACHINE name=m1 user=glenn seat=seat-a slots=2 runners=1") || !strings.Contains(out, "CONFIG LIST kind=machine rows=1") {
		t.Fatalf("machine list output mismatch: %s", out)
	}

	// 3. machine width (runs cold without Redis when no friend carries slots)
	code, out, errs = twinStep([]string{"machine", "width", "m1", pgFlag})
	if code != 0 || errs != "" {
		t.Fatalf("machine width: exit %d, stderr %q\nstdout: %s", code, errs, out)
	}
	if !strings.Contains(out, "CONFIG WIDTH machine=m1 width=2 slots=2 charged=0 member=true") {
		t.Fatalf("machine width output mismatch: %s", out)
	}

	// 4. friend add
	code, out, errs = twinStep([]string{"friend", "add", "f1", "--slots", "1", "--tiers", "flash,pro", "--as", "boss", pgFlag})
	if code != 0 || errs != "" {
		t.Fatalf("friend add: exit %d, stderr %q\nstdout: %s", code, errs, out)
	}
	if !strings.Contains(out, "CONFIG ADD kind=friend name=f1 rev=2") {
		t.Fatalf("friend add output mismatch: %s", out)
	}

	// 5. friend list
	code, out, errs = twinStep([]string{"friend", "list", pgFlag})
	if code != 0 || errs != "" {
		t.Fatalf("friend list: exit %d, stderr %q\nstdout: %s", code, errs, out)
	}
	if !strings.Contains(out, "FRIEND name=f1 slots=1 tiers=flash,pro roles=-") || !strings.Contains(out, "CONFIG LIST kind=friend rows=1") {
		t.Fatalf("friend list output mismatch: %s", out)
	}

	// 6. status
	code, out, errs = twinStep([]string{"status", pgFlag})
	if code != 0 || errs != "" {
		t.Fatalf("status: exit %d, stderr %q\nstdout: %s", code, errs, out)
	}
	if !strings.Contains(out, "CONFIG STATUS pg=file:"+twinPath) || !strings.Contains(out, "schema=5") || !strings.Contains(out, "machine=1 machine_rev=1") || !strings.Contains(out, "friend=1 friend_rev=2") || !strings.Contains(out, "redis=-") {
		t.Fatalf("status output mismatch: %s", out)
	}

	// 7. show verbs
	// machine show
	code, out, errs = twinStep([]string{"machine", "show", "m1", pgFlag})
	if code != 0 || errs != "" {
		t.Fatalf("machine show: exit %d, stderr %q\nstdout: %s", code, errs, out)
	}
	if !strings.Contains(out, "MACHINE name=m1 user=glenn seat=seat-a slots=2 runners=1") {
		t.Fatalf("machine show output mismatch: %s", out)
	}

	// friend show
	code, out, errs = twinStep([]string{"friend", "show", "f1", pgFlag})
	if code != 0 || errs != "" {
		t.Fatalf("friend show: exit %d, stderr %q\nstdout: %s", code, errs, out)
	}
	if !strings.Contains(out, "FRIEND name=f1 slots=1 tiers=flash,pro roles=-") {
		t.Fatalf("friend show output mismatch: %s", out)
	}

	// fleet show (singleton)
	code, out, errs = twinStep([]string{"fleet", "show", pgFlag})
	if code != 0 || errs != "" {
		t.Fatalf("fleet show: exit %d, stderr %q\nstdout: %s", code, errs, out)
	}
	if !strings.Contains(out, "FLEET name=fleet store=- coordinator=-") {
		t.Fatalf("fleet show output mismatch: %s", out)
	}

	// sprint show (singleton)
	code, out, errs = twinStep([]string{"sprint", "show", pgFlag})
	if code != 0 || errs != "" {
		t.Fatalf("sprint show: exit %d, stderr %q\nstdout: %s", code, errs, out)
	}
	if !strings.Contains(out, "SPRINT name=sprint coordinator=-") {
		t.Fatalf("sprint show output mismatch: %s", out)
	}

	// 8. fleet set & sprint set
	code, out, errs = twinStep([]string{"fleet", "set", "--store", "m1", "--coordinator", "m1", "--as", "boss", pgFlag})
	if code != 0 || errs != "" {
		t.Fatalf("fleet set: exit %d, stderr %q\nstdout: %s", code, errs, out)
	}
	if !strings.Contains(out, "CONFIG SET kind=fleet name=fleet rev=3 changed=coordinator,store") {
		t.Fatalf("fleet set output mismatch: %s", out)
	}

	code, out, errs = twinStep([]string{"sprint", "set", "--coordinator", "f1", "--as", "boss", pgFlag})
	if code != 0 || errs != "" {
		t.Fatalf("sprint set: exit %d, stderr %q\nstdout: %s", code, errs, out)
	}
	if !strings.Contains(out, "CONFIG SET kind=sprint name=sprint rev=4 changed=coordinator") {
		t.Fatalf("sprint set output mismatch: %s", out)
	}

	// Verify fleet show and sprint show reflect changes
	code, out, errs = twinStep([]string{"fleet", "show", pgFlag})
	if code != 0 || errs != "" || !strings.Contains(out, "FLEET name=fleet store=m1 coordinator=m1") {
		t.Fatalf("fleet show after set mismatch: %s", out)
	}
	code, out, errs = twinStep([]string{"sprint", "show", pgFlag})
	if code != 0 || errs != "" || !strings.Contains(out, "SPRINT name=sprint coordinator=f1") {
		t.Fatalf("sprint show after set mismatch: %s", out)
	}

	// 9. apply --check
	code, out, errs = twinStep([]string{"apply", "--check", pgFlag})
	if code != 0 || errs != "" {
		t.Fatalf("apply --check: exit %d, stderr %q\nstdout: %s", code, errs, out)
	}
	for _, expected := range []string{
		"CHECK ADD kind=machine name=m1",
		"CONFIG CHECK kind=machine add=1 set=0 remove=0 rev=1 applied=0",
		"CHECK SET kind=fleet name=fleet changed=store,coordinator",
		"CONFIG CHECK kind=fleet add=0 set=1 remove=0 rev=3 applied=0",
		"CHECK ADD kind=friend name=f1",
		"CONFIG CHECK kind=friend add=1 set=0 remove=0 rev=2 applied=0",
		"CHECK SET kind=sprint name=sprint changed=coordinator",
		"CONFIG CHECK kind=sprint add=0 set=1 remove=0 rev=4 applied=0",
	} {
		if !strings.Contains(out, expected) {
			t.Errorf("apply --check missing expected line %q\nfull output:\n%s", expected, out)
		}
	}

	// 10. inventory
	code, out, errs = twinStep([]string{"inventory", pgFlag})
	if code != 0 || errs != "" {
		t.Fatalf("inventory: exit %d, stderr %q\nstdout: %s", code, errs, out)
	}
	if !strings.Contains(out, "bench-m1") && !strings.Contains(out, "m1") {
		t.Fatalf("inventory output mismatch: %s", out)
	}

	// 11. history
	code, out, errs = twinStep([]string{"machine", "history", "m1", pgFlag})
	if code != 0 || errs != "" {
		t.Fatalf("machine history: exit %d, stderr %q\nstdout: %s", code, errs, out)
	}
	if !strings.Contains(out, "CONFIG HISTORY kind=machine name=m1 changes=1") {
		t.Fatalf("machine history output mismatch: %s", out)
	}

	// 12. Cross-process persistence test:
	// Reset sprint and fleet references before deleting f1 and m1
	code, _, errs = twinStep([]string{"sprint", "set", "--coordinator", "", "--as", "boss", pgFlag})
	if code != 0 {
		t.Fatalf("clear sprint: %s", errs)
	}
	code, _, errs = twinStep([]string{"fleet", "set", "--store", "", "--coordinator", "", "--as", "boss", pgFlag})
	if code != 0 {
		t.Fatalf("clear fleet: %s", errs)
	}

	code, out, errs = twinStep([]string{"friend", "remove", "f1", "--as", "boss", pgFlag})
	if code != 0 || errs != "" {
		t.Fatalf("friend remove: exit %d, stderr %q\nstdout: %s", code, errs, out)
	}
	code, out, errs = twinStep([]string{"machine", "remove", "m1", "--as", "boss", pgFlag})
	if code != 0 || errs != "" {
		t.Fatalf("machine remove: exit %d, stderr %q\nstdout: %s", code, errs, out)
	}

	// Verify lists are empty now
	code, out, _ = twinStep([]string{"machine", "list", pgFlag})
	if !strings.Contains(out, "rows=0") {
		t.Fatalf("expected rows=0 after remove, got: %s", out)
	}
	code, out, _ = twinStep([]string{"friend", "list", pgFlag})
	if !strings.Contains(out, "rows=0") {
		t.Fatalf("expected rows=0 after remove, got: %s", out)
	}
}

// TestFileTwinSubprocess verifies persistence across separate OS processes
// using the built binary.
func TestFileTwinSubprocess(t *testing.T) {
	tmpDir := t.TempDir()
	binPath := filepath.Join(tmpDir, "nova-config")

	buildCmd := exec.Command("go", "build", "-o", binPath, ".")
	buildCmd.Env = goenv.Clean(os.Environ())
	if out, err := buildCmd.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, string(out))
	}

	twinPath := filepath.Join(tmpDir, "twin.json")
	pgArg := "--pg=file:" + twinPath

	// Process 1: machine add
	cmd1 := exec.Command(binPath, "machine", "add", "box1", "--user", "glenn", "--seat", "seat-a", "--slots", "4", "--as", "boss", pgArg)
	cmd1.Env = goenv.Clean(os.Environ())
	out1, err := cmd1.CombinedOutput()
	if err != nil {
		t.Fatalf("cmd1 failed: %v\n%s", err, string(out1))
	}

	// Process 2: machine list
	cmd2 := exec.Command(binPath, "machine", "list", pgArg)
	cmd2.Env = goenv.Clean(os.Environ())
	out2, err := cmd2.CombinedOutput()
	if err != nil {
		t.Fatalf("cmd2 failed: %v\n%s", err, string(out2))
	}
	if !strings.Contains(string(out2), "name=box1") || !strings.Contains(string(out2), "rows=1") {
		t.Fatalf("cmd2 output mismatch: %s", string(out2))
	}

	// Process 3: status
	cmd3 := exec.Command(binPath, "status", pgArg)
	cmd3.Env = goenv.Clean(os.Environ())
	out3, err := cmd3.CombinedOutput()
	if err != nil {
		t.Fatalf("cmd3 failed: %v\n%s", err, string(out3))
	}
	if !strings.Contains(string(out3), "machine=1") || !strings.Contains(string(out3), "redis=-") {
		t.Fatalf("cmd3 output mismatch: %s", string(out3))
	}

	// Process 4: apply --check
	cmd4 := exec.Command(binPath, "apply", "--check", pgArg)
	cmd4.Env = goenv.Clean(os.Environ())
	out4, err := cmd4.CombinedOutput()
	if err != nil {
		t.Fatalf("cmd4 failed: %v\n%s", err, string(out4))
	}
	if !strings.Contains(string(out4), "CHECK ADD kind=machine name=box1") {
		t.Fatalf("cmd4 output mismatch: %s", string(out4))
	}
}

// TestFileTwinRefusals tests that invalid twin paths and malformed files are refused cleanly.
func TestFileTwinRefusals(t *testing.T) {
	// 1. Bare "file"
	code, _, errs := twinStep([]string{"machine", "list", "--pg", "file"})
	if code != 2 || !strings.Contains(errs, "a twin is a file: --pg file:<path>") {
		t.Fatalf("bare file: code %d, stderr %q", code, errs)
	}

	// 2. Empty path "file:"
	code, _, errs = twinStep([]string{"machine", "list", "--pg", "file:"})
	if code != 2 || !strings.Contains(errs, "a twin is a file: --pg file:<path>") {
		t.Fatalf("empty file: code %d, stderr %q", code, errs)
	}

	// 3. Whitespace path "file:   "
	code, _, errs = twinStep([]string{"machine", "list", "--pg", "file:   "})
	if code != 2 || !strings.Contains(errs, "a twin is a file: --pg file:<path>") {
		t.Fatalf("whitespace file: code %d, stderr %q", code, errs)
	}

	// 4. Directory path
	dir := t.TempDir()
	code, _, errs = twinStep([]string{"machine", "list", "--pg", "file:" + dir})
	if code != 2 || !strings.Contains(errs, "is a directory; want --pg file:<path>") {
		t.Fatalf("directory file: code %d, stderr %q", code, errs)
	}

	// 5. Corrupted snapshot file
	corruptFile := filepath.Join(dir, "corrupt.json")
	if err := os.WriteFile(corruptFile, []byte("{not-valid-json"), 0o600); err != nil {
		t.Fatal(err)
	}
	code, _, errs = twinStep([]string{"machine", "list", "--pg", "file:" + corruptFile})
	if code != 2 || !strings.Contains(errs, "not a twin snapshot") || !strings.Contains(errs, "it is left as it is") {
		t.Fatalf("corrupted file: code %d, stderr %q", code, errs)
	}

	// 6. Wrong snapshot version
	wrongVerFile := filepath.Join(dir, "wrong_ver.json")
	if err := os.WriteFile(wrongVerFile, []byte(`{"version":99,"schema":5,"rows":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	code, _, errs = twinStep([]string{"machine", "list", "--pg", "file:" + wrongVerFile})
	if code != 2 || !strings.Contains(errs, "this build reads version 1") || !strings.Contains(errs, "it is left as it is") {
		t.Fatalf("wrong ver file: code %d, stderr %q", code, errs)
	}
}

// TestFileTwinEnvVar tests that setting NOVA_PG_DSN=file:<path> works without --pg flag.
func TestFileTwinEnvVar(t *testing.T) {
	twinPath := filepath.Join(t.TempDir(), "env_config.json")
	t.Setenv("NOVA_PG_DSN", "file:"+twinPath)
	t.Setenv("NOVA_FRIEND", "boss")

	code, out, errs := twinStep([]string{"machine", "add", "node1", "--user", "glenn", "--seat", "seat-a", "--slots", "3"})
	if code != 0 || errs != "" {
		t.Fatalf("machine add via env: exit %d, stderr %q\nstdout: %s", code, errs, out)
	}

	code, out, errs = twinStep([]string{"machine", "list"})
	if code != 0 || errs != "" {
		t.Fatalf("machine list via env: exit %d, stderr %q\nstdout: %s", code, errs, out)
	}
	if !strings.Contains(out, "name=node1") {
		t.Fatalf("machine list via env missing node1: %s", out)
	}
}

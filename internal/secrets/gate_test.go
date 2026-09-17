package secrets

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const (
	gateRecoveryKey = "age1s6kpww894xpuylmck9f2g5kz2007a8nuy6guqrjj39s0gaqf6pkqydlata"
	gateSeatKey     = "age158lrf2hlptfwl6fh280y6pq58vdmumnqzhk5vd669aqf37ca3sus9mcazh"
)

var gateThirdKey = "age1" + strings.Repeat("q", 58)

func gateGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v failed: %v, out: %s", args, err, out)
	}
	return string(out)
}

// gateStart returns a store directory whose HEAD is the base commit.
func gateStart(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	gateGit(t, dir, "init", "-b", "main")
	gateGit(t, dir, "config", "user.name", "Test")
	gateGit(t, dir, "config", "user.email", "test@example.com")
	if err := os.WriteFile(filepath.Join(dir, "recovery.pub"), []byte(gateRecoveryKey+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("the store\n"), 0600); err != nil {
		t.Fatal(err)
	}
	gateGit(t, dir, "add", "-A")
	gateGit(t, dir, "commit", "-m", "base")
	return dir
}

// gateCommit writes files, commits, and returns the new HEAD sha.
func gateCommit(t *testing.T, dir string, files map[string]string) string {
	t.Helper()
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	gateGit(t, dir, "add", "-A")
	gateGit(t, dir, "commit", "-m", "change")
	return strings.TrimSpace(gateGit(t, dir, "rev-parse", "HEAD"))
}

func gateGoodSops(seatKey string, recipients ...string) string {
	all := append([]string{seatKey}, recipients...)
	return "creation_rules:\n" +
		"  - path_regex: ^rowan\\.yaml$\n" +
		"    age: " + strings.Join(all, ",") + "\n"
}

func gateSealedFile() string {
	return "sops:\n" +
		"    age:\n" +
		"        - recipient: " + gateSeatKey + "\n" +
		"          enc: |\n" +
		"            -----BEGIN AGE ENCRYPTED FILE-----\n" +
		"    lastmodified: \"2026-09-17T00:00:00Z\"\n" +
		"    mac: ENC[AES256_GCM,data:aaaaaaaa,iv:bbbbbbbb,tag:cccccccc,type:str]\n" +
		"GH_TOKEN: ENC[AES256_GCM,data:xyz,iv:abc,tag:def,type:str]\n"
}

func TestGateApprovesAGoodSeatPR(t *testing.T) {
	dir := gateStart(t)
	base := strings.TrimSpace(gateGit(t, dir, "rev-parse", "HEAD"))
	head := gateCommit(t, dir, map[string]string{
		".sops.yaml": gateGoodSops(gateSeatKey, gateRecoveryKey),
		"rowan.yaml": gateSealedFile(),
	})
	line, code := RunGate(dir, base, head)
	if code != 0 {
		t.Fatalf("RunGate = (%q, %d), want APPROVE at exit 0", line, code)
	}
	if line != "GATE APPROVE files=2" {
		t.Fatalf("RunGate line = %q, want %q", line, "GATE APPROVE files=2")
	}
}

func TestGateRefusesARuleWithThreeRecipients(t *testing.T) {
	dir := gateStart(t)
	base := strings.TrimSpace(gateGit(t, dir, "rev-parse", "HEAD"))
	head := gateCommit(t, dir, map[string]string{
		".sops.yaml": gateGoodSops(gateSeatKey, gateRecoveryKey, gateThirdKey),
		"rowan.yaml": gateSealedFile(),
	})
	line, code := RunGate(dir, base, head)
	if code != 2 {
		t.Fatalf("RunGate code = %d, want 2 (line=%q)", code, line)
	}
	if !strings.Contains(line, "GATE REFUSE rule=1") {
		t.Fatalf("RunGate line = %q, want REFUSE naming rule=1", line)
	}
}

func TestGateRefusesAPlaintextValue(t *testing.T) {
	dir := gateStart(t)
	base := strings.TrimSpace(gateGit(t, dir, "rev-parse", "HEAD"))
	head := gateCommit(t, dir, map[string]string{
		".sops.yaml": gateGoodSops(gateSeatKey, gateRecoveryKey),
		"rowan.yaml": gateSealedFile() + "GH_TOKEN: sk-live-notencrypted\n",
	})
	line, code := RunGate(dir, base, head)
	if code != 2 {
		t.Fatalf("RunGate code = %d, want 2 (line=%q)", code, line)
	}
	if !strings.Contains(line, "GATE REFUSE") || !strings.Contains(line, "rowan.yaml") {
		t.Fatalf("RunGate line = %q, want REFUSE naming rowan.yaml", line)
	}
}

func TestGateRefusesAChangeToAnotherFile(t *testing.T) {
	dir := gateStart(t)
	base := strings.TrimSpace(gateGit(t, dir, "rev-parse", "HEAD"))
	head := gateCommit(t, dir, map[string]string{
		".sops.yaml": gateGoodSops(gateSeatKey, gateRecoveryKey),
		"rowan.yaml": gateSealedFile(),
		"notes.txt":  "a change outside the gate\n",
	})
	line, code := RunGate(dir, base, head)
	if code != 2 {
		t.Fatalf("RunGate code = %d, want 2 (line=%q)", code, line)
	}
	if !strings.Contains(line, "GATE REFUSE") || !strings.Contains(line, "notes.txt") {
		t.Fatalf("RunGate line = %q, want REFUSE naming notes.txt", line)
	}
}

package secrets

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	gateRecoveryKey = "age1s6kpww894xpuylmck9f2g5kz2007a8nuy6guqrjj39s0gaqf6pkqydlata"
	gateSeatKey     = "age158lrf2hlptfwl6fh280y6pq58vdmumnqzhk5vd669aqf37ca3sus9mcazh"
)

// gateMarkLine is the clear root key every verb-written seat file carries (SPEC-SECRETS "gate").
const gateMarkLine = "NOVA_SECRETS_WRITTEN_BY: seal dev\n"

var gateThirdKey = "age1" + strings.Repeat("q", 58)

func gateGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "git %v failed: %v, out: %s", args, err, out)
	return string(out)
}

// gateStart returns a store directory whose HEAD is the base commit.
func gateStart(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	gateGit(t, dir, "init", "-b", "main")
	gateGit(t, dir, "config", "user.name", "Test")
	gateGit(t, dir, "config", "user.email", "test@example.com")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "recovery.pub"), []byte(gateRecoveryKey+"\n"), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "README.md"), []byte("the store\n"), 0600))
	gateGit(t, dir, "add", "-A")
	gateGit(t, dir, "commit", "-m", "base")
	return dir
}

// gateCommit writes files, commits, and returns the new HEAD sha.
func gateCommit(t *testing.T, dir string, files map[string]string) string {
	t.Helper()
	for name, content := range files {
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(content), 0600))
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
		"        - recipient: " + gateRecoveryKey + "\n" +
		"          enc: |\n" +
		"            -----BEGIN AGE ENCRYPTED FILE-----\n" +
		"    lastmodified: \"2026-09-17T00:00:00Z\"\n" +
		"    mac: ENC[AES256_GCM,data:aaaaaaaa,iv:bbbbbbbb,tag:cccccccc,type:str]\n" +
		"GH_TOKEN: ENC[AES256_GCM,data:xyz,iv:abc,tag:def,type:str]\n" +
		gateMarkLine
}

func TestGateApprovesAGoodSeatPR(t *testing.T) {
	t.Parallel()

	dir := gateStart(t)
	base := strings.TrimSpace(gateGit(t, dir, "rev-parse", "HEAD"))
	head := gateCommit(t, dir, map[string]string{
		".sops.yaml": gateGoodSops(gateSeatKey, gateRecoveryKey),
		"rowan.yaml": gateSealedFile(),
	})
	line, code := RunGate(GateInput{StoreDir: dir, Base: base, Head: head})
	require.Equal(t, 0, code, "RunGate = (%q, %d), want APPROVE at exit 0", line, code)
	require.Equal(t, "GATE APPROVE files=2 machines=-", line, "RunGate line = %q, want %q", line, "GATE APPROVE files=2 machines=-")
}

func TestGateRefusesARuleWithThreeRecipients(t *testing.T) {
	t.Parallel()

	dir := gateStart(t)
	base := strings.TrimSpace(gateGit(t, dir, "rev-parse", "HEAD"))
	head := gateCommit(t, dir, map[string]string{
		".sops.yaml": gateGoodSops(gateSeatKey, gateRecoveryKey, gateThirdKey),
		"rowan.yaml": gateSealedFile(),
	})
	line, code := RunGate(GateInput{StoreDir: dir, Base: base, Head: head})
	require.Equal(t, 1, code, "RunGate code = %d, want 1 (line=%q)", code, line)
	require.Contains(t, line, "GATE FAILED rule=1", "RunGate line = %q, want FAILED naming rule=1", line)
}

func TestGateRefusesAPlaintextValue(t *testing.T) {
	t.Parallel()

	dir := gateStart(t)
	base := strings.TrimSpace(gateGit(t, dir, "rev-parse", "HEAD"))
	head := gateCommit(t, dir, map[string]string{
		".sops.yaml": gateGoodSops(gateSeatKey, gateRecoveryKey),
		"rowan.yaml": gateSealedFile() + "GH_TOKEN: sk-live-notencrypted\n",
	})
	line, code := RunGate(GateInput{StoreDir: dir, Base: base, Head: head})
	require.Equal(t, 1, code, "RunGate code = %d, want 1 (line=%q)", code, line)
	require.Contains(t, line, "GATE FAILED", "RunGate line = %q, want FAILED naming rowan.yaml", line)
	require.Contains(t, line, "rowan.yaml", "RunGate line = %q, want FAILED naming rowan.yaml", line)
}

// TestGateVerdictExitsOneFailedAndCouldNotRunExitsTwoRefused pins skeleton contract 1.2
// at the gate (STANDARD §2, exit codes): a verdict of no is the gate that ran and judged
// the diff, GATE FAILED at exit 1; a gate that could not run -- a ref that names no commit
// -- is SECRETS GATE REFUSED at exit 2. A CI step reads the two apart, where before both
// were exit 2 and a broken change was indistinguishable from a gate that never ran.
func TestGateVerdictExitsOneFailedAndCouldNotRunExitsTwoRefused(t *testing.T) {
	t.Parallel()

	t.Run("a rule verdict is GATE FAILED at exit 1", func(t *testing.T) {
		t.Parallel()
		dir := gateStart(t)
		base := strings.TrimSpace(gateGit(t, dir, "rev-parse", "HEAD"))
		head := gateCommit(t, dir, map[string]string{
			".sops.yaml": gateGoodSops(gateSeatKey, gateRecoveryKey, gateThirdKey),
			"rowan.yaml": gateSealedFile(),
		})
		line, code := RunGate(GateInput{StoreDir: dir, Base: base, Head: head})
		assert.Equal(t, 1, code, "a rule verdict must exit 1 (line=%q, code=%d)", line, code)
		assert.True(t, strings.HasPrefix(line, "GATE FAILED"), "a rule verdict must lead with FAILED: %q", line)
		assert.NotContains(t, line, "GATE REFUSE", "the verdict still prints the old word: %q", line)
	})

	t.Run("a gate that could not run is SECRETS GATE REFUSED at exit 2", func(t *testing.T) {
		t.Parallel()
		dir := gateStart(t)
		base := strings.TrimSpace(gateGit(t, dir, "rev-parse", "HEAD"))
		line, code := RunGate(GateInput{StoreDir: dir, Base: base, Head: "no-such-branch"})
		assert.Equal(t, 2, code, "a ref that names no commit is could-not-run and must exit 2 (line=%q, code=%d)", line, code)
		assert.True(t, strings.HasPrefix(line, "SECRETS GATE REFUSED"), "could-not-run must lead with REFUSED: %q", line)
		assert.Contains(t, line, "run: nova-secrets gate -h", "could-not-run must name the next command: %q", line)
	})
}

func TestGateRefusesAChangeToAnotherFile(t *testing.T) {
	t.Parallel()

	dir := gateStart(t)
	base := strings.TrimSpace(gateGit(t, dir, "rev-parse", "HEAD"))
	head := gateCommit(t, dir, map[string]string{
		".sops.yaml": gateGoodSops(gateSeatKey, gateRecoveryKey),
		"rowan.yaml": gateSealedFile(),
		"notes.txt":  "a change outside the gate\n",
	})
	line, code := RunGate(GateInput{StoreDir: dir, Base: base, Head: head})
	require.Equal(t, 1, code, "RunGate code = %d, want 1 (line=%q)", code, line)
	require.Contains(t, line, "GATE FAILED", "RunGate line = %q, want FAILED naming notes.txt", line)
	require.Contains(t, line, "notes.txt", "RunGate line = %q, want FAILED naming notes.txt", line)
}

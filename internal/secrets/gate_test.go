package secrets

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/testgit"
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
	cmd.Env = testgit.Environ()
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

// gateRuleWith is one creation rule carrying an unencrypted_regex: "" writes none, so the
// rule keeps every key encrypted.
func gateRuleWith(seatFile, regex string) string {
	r := gateRule(seatFile, gateSeatKey, gateRecoveryKey)
	if regex != "" {
		r += "    unencrypted_regex: " + regex + "\n"
	}
	return r
}

// The read of 2026-10-07: a pull request that widened a rule's unencrypted_regex to `.*`
// and put a cleartext GH_TOKEN in the seat file was approved, because the gate judged each
// rule in the head alone. The rule's clear keys are fixed at the base; the one sanctioned
// change is admitting the mark key on a rule that lacked it, which seal and seat inject
// commit beside the file they write (SPEC-SECRETS "gate", the mark). Each row is a probe
// and, for a refusal, the line it must print.
func TestGateRefusesAWidenedUnencryptedRegex(t *testing.T) {
	t.Parallel()

	const mark = "^NOVA_SECRETS_WRITTEN_BY$"
	sealed := "OTHER: ENC[AES256_GCM,data:q,iv:w,tag:e,type:str]\n"
	cases := []struct {
		name      string
		baseRegex string
		headRegex string
		extra     string
		wantCode  int
		want      string
	}{
		{"a widened regex lets a cleartext value through", mark, ".*", "GH_TOKEN: sk-live-notencrypted\n", 1,
			`GATE FAILED rule=1 check=1 file=.sops.yaml: unencrypted_regex for rowan.yaml changes from "^NOVA_SECRETS_WRITTEN_BY$" to ".*"; a rule may not widen the keys it keeps in the clear`},
		{"an absent regex where the base had one", mark, "", "", 1,
			`GATE FAILED rule=1 check=1 file=.sops.yaml: unencrypted_regex for rowan.yaml changes from "^NOVA_SECRETS_WRITTEN_BY$" to ""; a rule may not widen the keys it keeps in the clear`},
		{"another clear key where the base had none", "", "^space_user$", "space_user: rowan\n", 1,
			`GATE FAILED rule=1 check=1 file=.sops.yaml: unencrypted_regex for rowan.yaml changes from "" to "^space_user$"; a rule may not widen the keys it keeps in the clear`},
		{"admitting the mark on a rule that lacked it is the sanctioned change", "", mark, sealed, 0,
			"GATE APPROVE files=2 machines=-"},
		{"the same regex is unchanged", mark, mark, sealed, 0,
			"GATE APPROVE files=1 machines=-"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			dir := gateStart(t)
			gateCommit(t, dir, map[string]string{
				".sops.yaml": gateSops(gateRuleWith("rowan.yaml", c.baseRegex)),
				"rowan.yaml": gateSealedFile(),
			})
			base := strings.TrimSpace(gateGit(t, dir, "rev-parse", "HEAD"))
			head := gateCommit(t, dir, map[string]string{
				".sops.yaml": gateSops(gateRuleWith("rowan.yaml", c.headRegex)),
				"rowan.yaml": gateSealedFile() + c.extra,
			})
			line, code := RunGate(GateInput{StoreDir: dir, Base: base, Head: head})
			assert.Equal(t, c.wantCode, code, line)
			assert.Equal(t, c.want, line)
		})
	}
}

// The read of 2026-10-07: a `.yaml` under a subdirectory (`sub/evil.yaml`) passed as a seat
// file, because isSeatYAML recognized any `*.yaml` that was not `.sops.yaml`, and its rule in
// the unchanged .sops.yaml matched it. A seat file is a root-level `<seat>.yaml` only; a
// nested one is no seat file, so check 3 refuses a change to it as a change outside the three
// kinds. The base already carries the nested file and its rule, so .sops.yaml does not change
// and check 3 is the only finding.
func TestGateRefusesASeatFileInASubdirectory(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		file string
		want string
	}{
		{"a yaml under a subdirectory", "sub/evil.yaml",
			"GATE FAILED rule=0 check=3 file=sub/evil.yaml: only .sops.yaml, README.md and seat .yaml files may change"},
		{"a yaml two levels down", "a/b/evil.yaml",
			"GATE FAILED rule=0 check=3 file=a/b/evil.yaml: only .sops.yaml, README.md and seat .yaml files may change"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			dir := gateStart(t)
			require.NoError(t, os.MkdirAll(filepath.Join(dir, filepath.Dir(c.file)), 0700))
			gateCommit(t, dir, map[string]string{
				".sops.yaml": gateSops(gateRule(c.file, gateSeatKey, gateRecoveryKey)),
				c.file:       gateSealedFile(),
			})
			base := strings.TrimSpace(gateGit(t, dir, "rev-parse", "HEAD"))
			head := gateCommit(t, dir, map[string]string{
				c.file: gateSealedFile() + "OTHER: ENC[AES256_GCM,data:q,iv:w,tag:e,type:str]\n",
			})
			line, code := RunGate(GateInput{StoreDir: dir, Base: base, Head: head})
			assert.Equal(t, 1, code, line)
			assert.Equal(t, c.want, line)
		})
	}
}

// The read of 2026-10-07: a cleartext value nested under an indented map key passed,
// because plainValues skipped every indented line whole. A key nested in an indented map is
// read like a root-level key; only the indented `sops:` metadata block is skipped, because
// its keys are the envelope's and not the seat's.
func TestGateRefusesCleartextUnderAnIndentedMapKey(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		body string
		want string
	}{
		{"a cleartext value one level down", "parent:\n  GH_TOKEN: sk-live-notencrypted\n",
			"GATE FAILED rule=1 check=2 file=rowan.yaml: key GH_TOKEN is a plain value, not encrypted"},
		{"a cleartext value two levels down", "parent:\n    child:\n        GH_TOKEN: sk-live-notencrypted\n",
			"GATE FAILED rule=1 check=2 file=rowan.yaml: key GH_TOKEN is a plain value, not encrypted"},
		{"a sealed value under an indented key is fine", "parent:\n  OTHER: ENC[AES256_GCM,data:q,iv:w,tag:e,type:str]\n",
			"GATE APPROVE files=2 machines=-"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			dir := gateStart(t)
			base := strings.TrimSpace(gateGit(t, dir, "rev-parse", "HEAD"))
			head := gateCommit(t, dir, map[string]string{
				".sops.yaml": gateGoodSops(gateSeatKey, gateRecoveryKey),
				"rowan.yaml": gateSealedFile() + c.body,
			})
			line, code := RunGate(GateInput{StoreDir: dir, Base: base, Head: head})
			wantCode := 1
			if strings.HasPrefix(c.want, "GATE APPROVE") {
				wantCode = 0
			}
			assert.Equal(t, wantCode, code, line)
			assert.Equal(t, c.want, line)
		})
	}
}

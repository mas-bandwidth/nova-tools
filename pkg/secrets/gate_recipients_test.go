package secrets

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The second key a PR would introduce. A recipient key is public, but the gate never prints
// one: a refusal names the SEAT, because the seat is what a reader has to go and check.
var gateNewSeatKey = "age1" + strings.Repeat("r", 58)

// gateRule is one creation rule, written the way the store writes them.
func gateRule(seatFile, seatKey, recovery string) string {
	return "  - path_regex: ^" + strings.ReplaceAll(seatFile, ".", `\.`) + "$\n" +
		"    age: " + seatKey + "," + recovery + "\n"
}

func gateSops(rules ...string) string {
	return "creation_rules:\n" + strings.Join(rules, "")
}

// gateSealedFor is a sealed seat file naming its seat key and the recovery key.
func gateSealedFor(seatKey string) string {
	return "sops:\n" +
		"    age:\n" +
		"        - recipient: " + seatKey + "\n" +
		"          enc: |\n" +
		"            -----BEGIN AGE ENCRYPTED FILE-----\n" +
		"        - recipient: " + gateRecoveryKey + "\n" +
		"          enc: |\n" +
		"            -----BEGIN AGE ENCRYPTED FILE-----\n" +
		"    lastmodified: \"2026-09-18T00:00:00Z\"\n" +
		"    mac: ENC[AES256_GCM,data:aaaaaaaa,iv:bbbbbbbb,tag:cccccccc,type:str]\n" +
		"GH_TOKEN: ENC[AES256_GCM,data:xyz,iv:abc,tag:def,type:str]\n" +
		gateMarkLine
}

// gateMachines writes a machines registry: name<TAB>ssh<TAB>os/arch<TAB>roles<TAB>seat<TAB>cores<TAB>notes
func gateMachines(t *testing.T, rows ...string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "machines.tsv")
	body := "# the fleet\n" + strings.Join(rows, "\n") + "\n"
	require.NoError(t, os.WriteFile(path, []byte(body), 0600))
	return path
}

func gateRow(name, seat string) string {
	return strings.Join([]string{name, name, "linux/x64", "bench", seat, "8", "-"}, "\t")
}

// gateRemove deletes files, commits, and returns the new HEAD sha.
func gateRemove(t *testing.T, dir string, names ...string) string {
	t.Helper()
	for _, n := range names {
		require.NoError(t, os.Remove(filepath.Join(dir, n)))
	}
	gateGit(t, dir, "add", "-A")
	gateGit(t, dir, "commit", "-m", "remove")
	return strings.TrimSpace(gateGit(t, dir, "rev-parse", "HEAD"))
}

// THE RULE (Glenn 2026-09-18): a seat is added to the store with NO human approval, so the
// thing that used to be a human's question -- "whose key is this, and does that machine
// exist?" -- has to be a machine's question. The machines registry is the answer: a new
// recipient is permitted only for a seat the fleet already says it has.
func TestGateRefusesANewRecipientForASeatTheRegistryDoesNotName(t *testing.T) {
	t.Parallel()

	dir := gateStart(t)
	base := strings.TrimSpace(gateGit(t, dir, "rev-parse", "HEAD"))
	head := gateCommit(t, dir, map[string]string{
		".sops.yaml": gateSops(gateRule("air.yaml", gateNewSeatKey, gateRecoveryKey)),
		"air.yaml":   gateSealedFor(gateNewSeatKey),
	})
	machines := gateMachines(t, gateRow("air", "swarm-air"), gateRow("hulk", "swarm-hulk"))

	line, code := RunGate(GateInput{StoreDir: dir, Base: base, Head: head, MachinesPath: machines})
	require.Equal(t, 1, code, "RunGate code = %d, want 1 (line=%q)", code, line)
	require.Contains(t, line, "GATE FAILED rule=1", "RunGate line = %q, want FAILED naming rule=1 and air.yaml", line)
	require.Contains(t, line, "air.yaml", "RunGate line = %q, want FAILED naming rule=1 and air.yaml", line)
	require.NotContains(t, line, gateNewSeatKey, "RunGate line = %q, must name the seat, not the key", line)
}

func TestGateApprovesANewRecipientForASeatTheRegistryNames(t *testing.T) {
	t.Parallel()

	dir := gateStart(t)
	base := strings.TrimSpace(gateGit(t, dir, "rev-parse", "HEAD"))
	head := gateCommit(t, dir, map[string]string{
		".sops.yaml":     gateSops(gateRule("swarm-air.yaml", gateNewSeatKey, gateRecoveryKey)),
		"swarm-air.yaml": gateSealedFor(gateNewSeatKey),
	})
	machines := gateMachines(t, gateRow("air", "swarm-air"))

	line, code := RunGate(GateInput{StoreDir: dir, Base: base, Head: head, MachinesPath: machines})
	require.Equal(t, 0, code, "RunGate = (%q, %d), want APPROVE at exit 0", line, code)
	require.True(t, strings.HasPrefix(line, "GATE APPROVE files=2 machines="), "RunGate line = %q, want APPROVE naming the registry it read", line)
	require.Contains(t, line, machines, "RunGate line = %q, want APPROVE naming the registry it read", line)
}

// A key already in the store is not a grant: reselaing an existing seat, or editing its rule,
// asks the registry nothing.
func TestGateApprovesARuleEditThatAddsNoRecipient(t *testing.T) {
	t.Parallel()

	dir := gateStart(t)
	gateCommit(t, dir, map[string]string{
		".sops.yaml": gateSops(gateRule("rowan.yaml", gateSeatKey, gateRecoveryKey)),
		"rowan.yaml": gateSealedFor(gateSeatKey),
	})
	base := strings.TrimSpace(gateGit(t, dir, "rev-parse", "HEAD"))
	head := gateCommit(t, dir, map[string]string{
		".sops.yaml": gateSops(gateRule("rowan.yaml", gateSeatKey, gateRecoveryKey)) +
			"  # the store's own note\n",
		"rowan.yaml": gateSealedFor(gateSeatKey) + "OTHER: ENC[AES256_GCM,data:q,iv:w,tag:e,type:str]\n",
	})
	machines := gateMachines(t, gateRow("air", "swarm-air"))

	line, code := RunGate(GateInput{StoreDir: dir, Base: base, Head: head, MachinesPath: machines})
	require.Equal(t, 0, code, "RunGate = (%q, %d), want APPROVE at exit 0", line, code)
}

// Without a registry the rule is dormant, not silently satisfied: the gate says so on the
// line, so nobody reads an APPROVE as the registry having vouched.
func TestGateWithoutAMachinesRegistrySaysTheRuleDidNotRun(t *testing.T) {
	t.Parallel()

	dir := gateStart(t)
	base := strings.TrimSpace(gateGit(t, dir, "rev-parse", "HEAD"))
	head := gateCommit(t, dir, map[string]string{
		".sops.yaml": gateSops(gateRule("air.yaml", gateNewSeatKey, gateRecoveryKey)),
		"air.yaml":   gateSealedFor(gateNewSeatKey),
	})

	line, code := RunGate(GateInput{StoreDir: dir, Base: base, Head: head})
	require.Equal(t, 0, code, "RunGate = (%q, %d), want APPROVE at exit 0", line, code)
	require.Contains(t, line, "machines=-", "RunGate line = %q, want it to say machines=- (no registry read)", line)
}

func TestGateReadsTheNamedCommitsWithoutAWorkingCopySopsFile(t *testing.T) {
	t.Parallel()

	dir := gateStart(t)
	base := strings.TrimSpace(gateGit(t, dir, "rev-parse", "HEAD"))
	line, code := RunGate(GateInput{StoreDir: dir, Base: base, Head: base})
	require.Equal(t, 0, code, "RunGate = (%q, %d), want approval for an empty commit diff", line, code)
	require.Contains(t, line, "GATE APPROVE files=0", "RunGate line = %q, want the empty diff", line)
}

func TestGateRefusesAnUnreadableMachinesRegistry(t *testing.T) {
	t.Parallel()

	dir := gateStart(t)
	base := strings.TrimSpace(gateGit(t, dir, "rev-parse", "HEAD"))
	head := gateCommit(t, dir, map[string]string{
		".sops.yaml":     gateSops(gateRule("swarm-air.yaml", gateNewSeatKey, gateRecoveryKey)),
		"swarm-air.yaml": gateSealedFor(gateNewSeatKey),
	})

	line, code := RunGate(GateInput{StoreDir: dir, Base: base, Head: head, MachinesPath: filepath.Join(dir, "no-such.tsv")})
	require.Equal(t, 2, code, "RunGate code = %d, want 2 (line=%q)", code, line)
	require.Contains(t, line, "SECRETS GATE REFUSED", "RunGate line = %q, want the could-not-run refusal", line)
}

// Keep what exists: a seat file that was in the store must still be there. Removing one is
// how a seat would lose its credentials silently, and it is never part of adding a seat.
func TestGateRefusesARemovedSeatFile(t *testing.T) {
	t.Parallel()

	dir := gateStart(t)
	gateCommit(t, dir, map[string]string{
		".sops.yaml": gateSops(gateRule("rowan.yaml", gateSeatKey, gateRecoveryKey)),
		"rowan.yaml": gateSealedFor(gateSeatKey),
	})
	base := strings.TrimSpace(gateGit(t, dir, "rev-parse", "HEAD"))
	head := gateRemove(t, dir, "rowan.yaml")

	line, code := RunGate(GateInput{StoreDir: dir, Base: base, Head: head})
	require.Equal(t, 1, code, "RunGate code = %d, want 1 (line=%q)", code, line)
	require.Contains(t, line, "GATE FAILED", "RunGate line = %q, want FAILED naming rowan.yaml", line)
	require.Contains(t, line, "rowan.yaml", "RunGate line = %q, want FAILED naming rowan.yaml", line)
}

// The registry is read WHOLE and validated whole, as pkg/fleet demands: a malformed
// line is a refusal, never a half-read registry whose read half lets a recipient through.
func TestGateRefusesAMalformedMachinesRegistry(t *testing.T) {
	t.Parallel()

	dir := gateStart(t)
	base := strings.TrimSpace(gateGit(t, dir, "rev-parse", "HEAD"))
	head := gateCommit(t, dir, map[string]string{
		".sops.yaml":     gateSops(gateRule("swarm-air.yaml", gateNewSeatKey, gateRecoveryKey)),
		"swarm-air.yaml": gateSealedFor(gateNewSeatKey),
	})
	machines := gateMachines(t, "air\tair\tlinux/x64\tbench")

	line, code := RunGate(GateInput{StoreDir: dir, Base: base, Head: head, MachinesPath: machines})
	require.Equal(t, 2, code, "RunGate code = %d, want 2 (line=%q)", code, line)
	require.Contains(t, line, "SECRETS GATE REFUSED", "RunGate line = %q, want the could-not-run refusal", line)
}

func TestGateMachinesRefusesPlaceTableFormat(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "machines.tsv")
	require.NoError(t, os.WriteFile(path, []byte("node\tnode\t/home/node\t-\n"), 0o600))

	_, err := gateFleetSeats(path)
	require.Error(t, err, "gate must refuse the place machine table")
	require.Contains(t, err.Error(), "wants 7 tab-separated fields", "refusal = %q", err)
}

func TestGateCollectsEveryIndependentSeatFinding(t *testing.T) {
	t.Parallel()

	dir := gateStart(t)
	base := strings.TrimSpace(gateGit(t, dir, "rev-parse", "HEAD"))
	head := gateCommit(t, dir, map[string]string{
		".sops.yaml": gateSops(
			gateRule("alpha.yaml", gateSeatKey, gateRecoveryKey),
			gateRule("beta.yaml", gateSeatKey, gateRecoveryKey),
		),
		"alpha.yaml": strings.TrimSuffix(gateSealedFor(gateSeatKey), gateMarkLine) + "TOKEN: synthetic-alpha\nSECOND_TOKEN: synthetic-alpha-two\n",
		"beta.yaml":  strings.TrimSuffix(gateSealedFor(gateSeatKey), gateMarkLine) + "TOKEN: synthetic-beta\n",
	})

	line, code := RunGate(GateInput{StoreDir: dir, Base: base, Head: head})
	require.Equal(t, 1, code, "RunGate code = %d, want 1 (line=%q)", code, line)
	assert.Contains(t, line, "alpha.yaml", "refusal = %q, want the first finding", line)
	assert.Contains(t, line, "beta.yaml", "refusal = %q, want every independent finding", line)
	assert.Contains(t, line, "SECOND_TOKEN", "refusal = %q, want every plaintext key in alpha.yaml", line)
	assert.Contains(t, line, "was not written", "refusal = %q, want the independent mark finding", line)
	assert.NotContains(t, line, "synthetic-alpha", "refusal must name findings without exposing values")
	assert.NotContains(t, line, "synthetic-beta", "refusal must name findings without exposing values")
}

func TestGateCollectsIndependentInputProblems(t *testing.T) {
	t.Parallel()

	dir := gateStart(t)
	gateCommit(t, dir, map[string]string{
		".sops.yaml": gateSops(gateRule("seat.yaml", gateSeatKey, gateRecoveryKey)),
	})
	machines := gateMachines(t, "malformed place-table row")
	line, code := RunGate(GateInput{
		StoreDir: dir, Base: "missing-base", Head: "missing-head", MachinesPath: machines,
	})
	require.Equal(t, 2, code, "RunGate code = %d, want 2 (line=%q)", code, line)
	require.Contains(t, line, "--base", "refusal = %q, want the base problem", line)
	require.Contains(t, line, "--head", "refusal = %q, want the head problem", line)
	require.Contains(t, line, "machines", "refusal = %q, want the registry problem", line)

	line, code = RunGate(GateInput{StoreDir: dir, Head: "HEAD", MachinesPath: machines})
	require.Equal(t, 2, code, "RunGate with a missing base = (%q, %d), want refusal", line, code)
	require.Contains(t, line, "missing --base", "refusal = %q, want the missing base", line)
	require.Contains(t, line, "machines", "refusal = %q, want the independent registry problem", line)
}

func TestGateCollectsIndependentHeadInputProblems(t *testing.T) {
	t.Parallel()

	dir := gateStart(t)
	gateCommit(t, dir, map[string]string{
		".sops.yaml": gateSops(gateRule("seat.yaml", gateSeatKey, gateRecoveryKey)),
	})
	base := strings.TrimSpace(gateGit(t, dir, "rev-parse", "HEAD"))
	require.NoError(t, os.Remove(filepath.Join(dir, "recovery.pub")))
	head := gateCommit(t, dir, map[string]string{".sops.yaml": "creation_rules: [\n"})
	line, code := RunGate(GateInput{StoreDir: dir, Base: base, Head: head})
	require.Equal(t, 1, code, "RunGate code = %d, want 1 (line=%q)", code, line)
	require.Contains(t, line, "recovery.pub", "refusal = %q, want the recovery input problem", line)
	require.Contains(t, line, ".sops.yaml", "refusal = %q, want the rule input problem", line)
}

// A machine that carries no seat says `-`; that is an answer, not a seat name, and a rule
// for a file called `-.yaml` must not be vouched for by it.
func TestGateDoesNotTreatADashAsASeatName(t *testing.T) {
	t.Parallel()

	dir := gateStart(t)
	base := strings.TrimSpace(gateGit(t, dir, "rev-parse", "HEAD"))
	head := gateCommit(t, dir, map[string]string{
		".sops.yaml": gateSops(gateRule("-.yaml", gateNewSeatKey, gateRecoveryKey)),
		"-.yaml":     gateSealedFor(gateNewSeatKey),
	})
	machines := gateMachines(t, gateRow("mini", "-"))

	line, code := RunGate(GateInput{StoreDir: dir, Base: base, Head: head, MachinesPath: machines})
	require.Equal(t, 1, code, "RunGate code = %d, want 1 (line=%q)", code, line)
}

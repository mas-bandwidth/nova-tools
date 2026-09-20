package secrets

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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

// gateSealedFor is a sealed seat file naming one recipient.
func gateSealedFor(seatKey string) string {
	return "sops:\n" +
		"    age:\n" +
		"        - recipient: " + seatKey + "\n" +
		"          enc: |\n" +
		"            -----BEGIN AGE ENCRYPTED FILE-----\n" +
		"    lastmodified: \"2026-09-18T00:00:00Z\"\n" +
		"    mac: ENC[AES256_GCM,data:aaaaaaaa,iv:bbbbbbbb,tag:cccccccc,type:str]\n" +
		"GH_TOKEN: ENC[AES256_GCM,data:xyz,iv:abc,tag:def,type:str]\n"
}

// gateMachines writes a machines registry: name<TAB>ssh<TAB>os/arch<TAB>roles<TAB>seat<TAB>cores<TAB>notes
func gateMachines(t *testing.T, rows ...string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "machines.tsv")
	body := "# the fleet\n" + strings.Join(rows, "\n") + "\n"
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func gateRow(name, seat string) string {
	return strings.Join([]string{name, name, "linux/x64", "bench", seat, "8", "-"}, "\t")
}

// gateRemove deletes files, commits, and returns the new HEAD sha.
func gateRemove(t *testing.T, dir string, names ...string) string {
	t.Helper()
	for _, n := range names {
		if err := os.Remove(filepath.Join(dir, n)); err != nil {
			t.Fatal(err)
		}
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
	dir := gateStart(t)
	base := strings.TrimSpace(gateGit(t, dir, "rev-parse", "HEAD"))
	head := gateCommit(t, dir, map[string]string{
		".sops.yaml": gateSops(gateRule("air.yaml", gateNewSeatKey, gateRecoveryKey)),
		"air.yaml":   gateSealedFor(gateNewSeatKey),
	})
	machines := gateMachines(t, gateRow("air", "swarm-air"), gateRow("hulk", "swarm-hulk"))

	line, code := RunGate(GateInput{StoreDir: dir, Base: base, Head: head, MachinesPath: machines})
	if code != 2 {
		t.Fatalf("RunGate code = %d, want 2 (line=%q)", code, line)
	}
	if !strings.Contains(line, "GATE REFUSE rule=1") || !strings.Contains(line, "air.yaml") {
		t.Fatalf("RunGate line = %q, want REFUSE naming rule=1 and air.yaml", line)
	}
	if strings.Contains(line, gateNewSeatKey) {
		t.Fatalf("RunGate line = %q, must name the seat, not the key", line)
	}
}

func TestGateApprovesANewRecipientForASeatTheRegistryNames(t *testing.T) {
	dir := gateStart(t)
	base := strings.TrimSpace(gateGit(t, dir, "rev-parse", "HEAD"))
	head := gateCommit(t, dir, map[string]string{
		".sops.yaml":     gateSops(gateRule("swarm-air.yaml", gateNewSeatKey, gateRecoveryKey)),
		"swarm-air.yaml": gateSealedFor(gateNewSeatKey),
	})
	machines := gateMachines(t, gateRow("air", "swarm-air"))

	line, code := RunGate(GateInput{StoreDir: dir, Base: base, Head: head, MachinesPath: machines})
	if code != 0 {
		t.Fatalf("RunGate = (%q, %d), want APPROVE at exit 0", line, code)
	}
	if !strings.HasPrefix(line, "GATE APPROVE files=2 machines=") || !strings.Contains(line, machines) {
		t.Fatalf("RunGate line = %q, want APPROVE naming the registry it read", line)
	}
}

// A key already in the store is not a grant: reselaing an existing seat, or editing its rule,
// asks the registry nothing.
func TestGateApprovesARuleEditThatAddsNoRecipient(t *testing.T) {
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
	if code != 0 {
		t.Fatalf("RunGate = (%q, %d), want APPROVE at exit 0", line, code)
	}
}

// Without a registry the rule is dormant, not silently satisfied: the gate says so on the
// line, so nobody reads an APPROVE as the registry having vouched.
func TestGateWithoutAMachinesRegistrySaysTheRuleDidNotRun(t *testing.T) {
	dir := gateStart(t)
	base := strings.TrimSpace(gateGit(t, dir, "rev-parse", "HEAD"))
	head := gateCommit(t, dir, map[string]string{
		".sops.yaml": gateSops(gateRule("air.yaml", gateNewSeatKey, gateRecoveryKey)),
		"air.yaml":   gateSealedFor(gateNewSeatKey),
	})

	line, code := RunGate(GateInput{StoreDir: dir, Base: base, Head: head})
	if code != 0 {
		t.Fatalf("RunGate = (%q, %d), want APPROVE at exit 0", line, code)
	}
	if !strings.Contains(line, "machines=-") {
		t.Fatalf("RunGate line = %q, want it to say machines=- (no registry read)", line)
	}
}

func TestGateRefusesAnUnreadableMachinesRegistry(t *testing.T) {
	dir := gateStart(t)
	base := strings.TrimSpace(gateGit(t, dir, "rev-parse", "HEAD"))
	head := gateCommit(t, dir, map[string]string{
		".sops.yaml":     gateSops(gateRule("swarm-air.yaml", gateNewSeatKey, gateRecoveryKey)),
		"swarm-air.yaml": gateSealedFor(gateNewSeatKey),
	})

	line, code := RunGate(GateInput{StoreDir: dir, Base: base, Head: head, MachinesPath: filepath.Join(dir, "no-such.tsv")})
	if code != 2 {
		t.Fatalf("RunGate code = %d, want 2 (line=%q)", code, line)
	}
	if !strings.Contains(line, "GATE REFUSE") {
		t.Fatalf("RunGate line = %q, want a refusal", line)
	}
}

// Keep what exists: a seat file that was in the store must still be there. Removing one is
// how a seat would lose its credentials silently, and it is never part of adding a seat.
func TestGateRefusesARemovedSeatFile(t *testing.T) {
	dir := gateStart(t)
	gateCommit(t, dir, map[string]string{
		".sops.yaml": gateSops(gateRule("rowan.yaml", gateSeatKey, gateRecoveryKey)),
		"rowan.yaml": gateSealedFor(gateSeatKey),
	})
	base := strings.TrimSpace(gateGit(t, dir, "rev-parse", "HEAD"))
	head := gateRemove(t, dir, "rowan.yaml")

	line, code := RunGate(GateInput{StoreDir: dir, Base: base, Head: head})
	if code != 2 {
		t.Fatalf("RunGate code = %d, want 2 (line=%q)", code, line)
	}
	if !strings.Contains(line, "GATE REFUSE") || !strings.Contains(line, "rowan.yaml") {
		t.Fatalf("RunGate line = %q, want REFUSE naming rowan.yaml", line)
	}
}

// The registry is read WHOLE and validated whole, as internal/fleet demands: a malformed
// line is a refusal, never a half-read registry whose read half lets a recipient through.
func TestGateRefusesAMalformedMachinesRegistry(t *testing.T) {
	dir := gateStart(t)
	base := strings.TrimSpace(gateGit(t, dir, "rev-parse", "HEAD"))
	head := gateCommit(t, dir, map[string]string{
		".sops.yaml":     gateSops(gateRule("swarm-air.yaml", gateNewSeatKey, gateRecoveryKey)),
		"swarm-air.yaml": gateSealedFor(gateNewSeatKey),
	})
	machines := gateMachines(t, "air\tair\tlinux/x64\tbench")

	line, code := RunGate(GateInput{StoreDir: dir, Base: base, Head: head, MachinesPath: machines})
	if code != 2 {
		t.Fatalf("RunGate code = %d, want 2 (line=%q)", code, line)
	}
	if !strings.Contains(line, "GATE REFUSE") {
		t.Fatalf("RunGate line = %q, want a refusal", line)
	}
}

// A machine that carries no seat says `-`; that is an answer, not a seat name, and a rule
// for a file called `-.yaml` must not be vouched for by it.
func TestGateDoesNotTreatADashAsASeatName(t *testing.T) {
	dir := gateStart(t)
	base := strings.TrimSpace(gateGit(t, dir, "rev-parse", "HEAD"))
	head := gateCommit(t, dir, map[string]string{
		".sops.yaml": gateSops(gateRule("-.yaml", gateNewSeatKey, gateRecoveryKey)),
		"-.yaml":     gateSealedFor(gateNewSeatKey),
	})
	machines := gateMachines(t, gateRow("mini", "-"))

	line, code := RunGate(GateInput{StoreDir: dir, Base: base, Head: head, MachinesPath: machines})
	if code != 2 {
		t.Fatalf("RunGate code = %d, want 2 (line=%q)", code, line)
	}
}

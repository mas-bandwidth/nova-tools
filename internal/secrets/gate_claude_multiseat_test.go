package secrets

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func claudeGateManifest(t *testing.T, account, profile, seat string) string {
	return claudeGateManifestMachines(t, "mini", "mini", account, profile, seat)
}

func claudeGateManifestMachines(t *testing.T, sourceMachine, targetMachine, account, profile, seat string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "accounts.tsv")
	line := strings.Join([]string{account, "Account", sourceMachine, "source-" + account, targetMachine, profile, seat, "2026-10-08T00:00:00Z"}, "\t")
	body := "account_id\tlabel\tsource_machine\tsource_seat\tmachine\tprofile_id\ttarget_seat\tissued_at\n" + line + "\n"
	require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
	return path
}

func TestGateRequiresExactMachineAccountProfileSeatForExtraRecipient(t *testing.T) {
	t.Parallel()
	dir := gateStart(t)
	base := strings.TrimSpace(gateGit(t, dir, "rev-parse", "HEAD"))
	head := gateCommit(t, dir, map[string]string{
		".sops.yaml":       gateSops(gateRule("target-seat.yaml", gateNewSeatKey, gateRecoveryKey)),
		"target-seat.yaml": gateSealedFor(gateNewSeatKey),
	})
	registry := gateMachines(t, gateRow("mini", "primary-seat")+"\t"+`[{"account_id":"account-one","source_seat":"source-account-one"},{"account_id":"account-one","profile_id":"profile-one","target_seat":"target-seat"}]`)
	valid := claudeGateManifest(t, "account-one", "profile-one", "target-seat")
	input := GateInput{StoreDir: dir, Base: base, Head: head, MachinesPath: registry}
	line, code := RunGate(input)
	require.Equal(t, 1, code, line)
	assert.Contains(t, line, "check=4")
	input.ClaudeAccountsPath = valid
	line, code = RunGate(input)
	require.Equal(t, 0, code, line)
	assert.Contains(t, line, "GATE APPROVE")
	assert.Contains(t, line, " accounts="+valid)
	raw, err := os.ReadFile(valid)
	require.NoError(t, err)
	assert.Contains(t, line, " accounts_sha256="+ClaudeTokenFleetSHA256(raw))
	for _, mismatch := range []struct{ account, profile, seat string }{
		{"account-two", "profile-one", "target-seat"},
		{"account-one", "profile-two", "target-seat"},
		{"account-one", "profile-one", "other-seat"},
	} {
		input.ClaudeAccountsPath = claudeGateManifest(t, mismatch.account, mismatch.profile, mismatch.seat)
		line, code = RunGate(input)
		assert.Equal(t, 2, code, line)
		assert.Contains(t, line, "no exact binding")
	}
}

func TestGateDoesNotTreatARecipientUsedByAnotherSeatAsExistingGrant(t *testing.T) {
	t.Parallel()
	dir := gateStart(t)
	base := strings.TrimSpace(gateGit(t, dir, "rev-parse", "HEAD"))
	head := gateCommit(t, dir, map[string]string{
		".sops.yaml":  gateSops(gateRule("first.yaml", gateSeatKey, gateRecoveryKey), gateRule("second.yaml", gateSeatKey, gateRecoveryKey)),
		"first.yaml":  gateSealedFor(gateSeatKey),
		"second.yaml": gateSealedFor(gateSeatKey),
	})
	line, code := RunGate(GateInput{StoreDir: dir, Base: base, Head: head})
	require.Equal(t, 1, code, line)
	assert.Contains(t, line, "reuses a non-recovery recipient")
}

func TestGateRejectsRecipientReuseWhenOnlySeatFileChanges(t *testing.T) {
	t.Parallel()
	dir := gateStart(t)
	base := gateCommit(t, dir, map[string]string{
		".sops.yaml": gateSops(gateRule("rowan.yaml", gateSeatKey, gateRecoveryKey), gateRule("second.yaml", gateSeatKey, gateRecoveryKey)),
	})
	head := gateCommit(t, dir, map[string]string{"second.yaml": gateSealedFor(gateSeatKey)})
	line, code := RunGate(GateInput{StoreDir: dir, Base: base, Head: head})
	require.Equal(t, 1, code, line)
	assert.Contains(t, line, "reuses a non-recovery recipient")
}

func TestGateRejectsExistingRuleThatBecomesBroadWhenSeatFileIsAdded(t *testing.T) {
	t.Parallel()
	dir := gateStart(t)
	base := gateCommit(t, dir, map[string]string{
		".sops.yaml": "creation_rules:\n  - path_regex: ^rowan.*\\.yaml$\n    age: " + gateSeatKey + "," + gateRecoveryKey + "\n",
		"rowan.yaml": gateSealedFor(gateSeatKey),
	})
	head := gateCommit(t, dir, map[string]string{"rowan-next.yaml": gateSealedFor(gateSeatKey)})
	line, code := RunGate(GateInput{StoreDir: dir, Base: base, Head: head})
	require.Equal(t, 1, code, line)
	assert.Contains(t, line, "path_regex names 2 seat files")
}

func TestGateChecksGrantWhenOnlySeatFileIsAddedUnderExactRule(t *testing.T) {
	t.Parallel()
	dir := gateStart(t)
	base := gateCommit(t, dir, map[string]string{
		".sops.yaml": gateSops(gateRule("target-seat.yaml", gateNewSeatKey, gateRecoveryKey)),
	})
	head := gateCommit(t, dir, map[string]string{"target-seat.yaml": gateSealedFor(gateNewSeatKey)})
	registry := gateMachines(t, gateRow("mini", "primary-seat")+"\t"+`[{"account_id":"account-one","source_seat":"source-account-one"},{"account_id":"account-one","profile_id":"profile-one","target_seat":"target-seat"}]`)
	input := GateInput{StoreDir: dir, Base: base, Head: head, MachinesPath: registry}
	line, code := RunGate(input)
	require.Equal(t, 1, code, line)
	assert.Contains(t, line, "check=4")
	input.ClaudeAccountsPath = claudeGateManifest(t, "account-two", "profile-one", "target-seat")
	line, code = RunGate(input)
	require.Equal(t, 2, code, line)
	assert.Contains(t, line, "no exact binding")
	input.ClaudeAccountsPath = claudeGateManifest(t, "account-one", "profile-one", "target-seat")
	line, code = RunGate(input)
	require.Equal(t, 0, code, line)
	assert.Contains(t, line, " accounts="+input.ClaudeAccountsPath)
}

func TestGateAdmitsSourceSeatOnlyOnItsOwnAccountAndMachine(t *testing.T) {
	t.Parallel()
	dir := gateStart(t)
	base := strings.TrimSpace(gateGit(t, dir, "rev-parse", "HEAD"))
	head := gateCommit(t, dir, map[string]string{
		".sops.yaml":              gateSops(gateRule("source-account-one.yaml", gateNewSeatKey, gateRecoveryKey)),
		"source-account-one.yaml": gateSealedFor(gateNewSeatKey),
	})
	registry := gateMachines(t,
		gateRow("studio", "studio-primary")+"\t"+`[{"account_id":"account-one","source_seat":"source-account-one"}]`,
		gateRow("mini", "mini-primary")+"\t"+`[{"account_id":"account-one","profile_id":"profile-one","target_seat":"target-seat"}]`)
	input := GateInput{StoreDir: dir, Base: base, Head: head, MachinesPath: registry}
	line, code := RunGate(input)
	require.Equal(t, 1, code, line)
	assert.Contains(t, line, "check=4")
	input.ClaudeAccountsPath = claudeGateManifestMachines(t, "mini", "mini", "account-one", "profile-one", "target-seat")
	line, code = RunGate(input)
	require.Equal(t, 2, code, line)
	assert.Contains(t, line, "source seat")
	input.ClaudeAccountsPath = claudeGateManifestMachines(t, "studio", "mini", "account-one", "profile-one", "target-seat")
	line, code = RunGate(input)
	require.Equal(t, 0, code, line)
	assert.Contains(t, line, " accounts="+input.ClaudeAccountsPath)
}

func TestGateAdmitsTwoAccountSourceAndTargetSeatsOnCanonicalHosts(t *testing.T) {
	t.Parallel()
	dir := gateStart(t)
	base := strings.TrimSpace(gateGit(t, dir, "rev-parse", "HEAD"))
	keys := []string{gateSeatKey, gateNewSeatKey, gateThirdKey, "age1" + strings.Repeat("t", 58)}
	names := []string{"source-a", "source-b", "target-a", "target-b"}
	files := map[string]string{}
	var rules []string
	for i, name := range names {
		rules = append(rules, gateRule(name+".yaml", keys[i], gateRecoveryKey))
		files[name+".yaml"] = gateSealedFor(keys[i])
	}
	files[".sops.yaml"] = gateSops(rules...)
	head := gateCommit(t, dir, files)
	studio := gateRow("studio", "studio-primary") + "\t" + `[{"account_id":"account-a","source_seat":"source-a"},{"account_id":"account-b","source_seat":"source-b"}]`
	mini := gateRow("mini", "mini-primary") + "\t" + `[{"account_id":"account-a","profile_id":"profile-a","target_seat":"target-a"},{"account_id":"account-b","profile_id":"profile-b","target_seat":"target-b"}]`
	registry := gateMachines(t, studio, mini)
	manifestPath := filepath.Join(t.TempDir(), "accounts.tsv")
	header := "account_id\tlabel\tsource_machine\tsource_seat\tmachine\tprofile_id\ttarget_seat\tissued_at\n"
	rowA := "account-a\tAccount A\tstudio\tsource-a\tmini\tprofile-a\ttarget-a\t2026-10-08T00:00:00Z\n"
	rowB := "account-b\tAccount B\tstudio\tsource-b\tmini\tprofile-b\ttarget-b\t2026-10-08T00:00:00Z\n"
	require.NoError(t, os.WriteFile(manifestPath, []byte(header+rowA+rowB), 0o600))
	input := GateInput{StoreDir: dir, Base: base, Head: head, MachinesPath: registry, ClaudeAccountsPath: manifestPath}
	line, code := RunGate(input)
	require.Equal(t, 0, code, line)
	assert.Contains(t, line, " accounts="+manifestPath)
	wrongStudio := gateRow("studio", "studio-primary") + "\t" + `[{"account_id":"account-a","source_seat":"source-a"},{"account_id":"account-a","source_seat":"source-b"}]`
	input.MachinesPath = gateMachines(t, wrongStudio, mini)
	line, code = RunGate(input)
	require.Equal(t, 2, code, line)
	assert.Contains(t, line, "account-b source seat source-b has no exact binding")
}

package secrets

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestClaudeTokenFleetKeepsTwoAccountsSeparateOnOneMachine(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	store := filepath.Join(dir, "secrets")
	require.NoError(t, os.MkdirAll(filepath.Join(store, ".git"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(store, ".sops.yaml"), []byte("creation_rules: []\n"), 0o600))
	for _, seat := range []string{"source-a", "source-b", "target-a", "target-b"} {
		body := "OTHER_KEY: ENC[AES256_GCM,data:other,type:str]\nsops:\n  age: []\n"
		if strings.HasPrefix(seat, "source") {
			body = "CLAUDE_CODE_OAUTH_TOKEN: ENC[AES256_GCM,data:fixture-" + seat + ",type:str]\nsops:\n  age: []\n"
		}
		require.NoError(t, os.WriteFile(filepath.Join(store, seat+".yaml"), []byte(body), 0o600))
	}
	manifest := filepath.Join(dir, "bindings.tsv")
	data := claudeFleetHeader + "\n" +
		"account-a\tAccount A\tstudio\tsource-a\tmini\tfriend-a\ttarget-a\t\n" +
		"account-b\tAccount B\tstudio\tsource-b\tmini\tfriend-b\ttarget-b\t\n"
	require.NoError(t, os.WriteFile(manifest, []byte(data), 0o600))
	bindings, err := ReadClaudeTokenFleetManifest(manifest)
	require.NoError(t, err)
	plan, err := PlanClaudeTokenFleet(store, bindings, time.Date(2026, 10, 9, 0, 0, 0, 0, time.UTC))
	require.NoError(t, err)
	require.Len(t, plan.Accounts, 2)
	assert.Equal(t, 2, plan.Targets)
	assert.Equal(t, "source-a", plan.Accounts[0].Token.Source.Seat)
	assert.Equal(t, "studio", plan.Accounts[0].SourceMachine)
	assert.Equal(t, "target-a", plan.Accounts[0].Targets[0].Seat)
	assert.Equal(t, "friend-a", plan.Accounts[0].Targets[0].Profile)
	assert.Equal(t, "source-b", plan.Accounts[1].Token.Source.Seat)
	assert.Equal(t, "target-b", plan.Accounts[1].Targets[0].Seat)
	assert.Equal(t, "friend-b", plan.Accounts[1].Targets[0].Profile)
	assert.Equal(t, "mini", plan.Accounts[0].Targets[0].Machine)
	assert.Equal(t, "mini", plan.Accounts[1].Targets[0].Machine)
}

func TestClaudeTokenFleetAllowsOneAccountProfileOnTwoMachines(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "bindings.tsv")
	data := claudeFleetHeader + "\n" +
		"account-a\tAccount A\tstudio\tsource-a\tmini\tfriend-a\ttarget-mini\t\n" +
		"account-a\tAccount A\tstudio\tsource-a\tstudio\tfriend-a\ttarget-studio\t\n"
	require.NoError(t, os.WriteFile(path, []byte(data), 0o600))
	manifest, err := ReadClaudeTokenFleetManifest(path)
	require.NoError(t, err)
	require.Len(t, manifest.Accounts, 1)
	assert.Equal(t, []ClaudeTokenTargetBinding{
		{Machine: "mini", Profile: "friend-a", Seat: "target-mini"},
		{Machine: "studio", Profile: "friend-a", Seat: "target-studio"},
	}, manifest.Accounts[0].Targets)
}

func TestClaudeTokenFleetPublicBytePinUsesTheParsedBytes(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "bindings.tsv")
	raw := []byte(claudeFleetHeader + "\naccount-a\tAccount A\tstudio\tsource-a\tmini\tfriend-a\ttarget-a\t\n")
	require.NoError(t, os.WriteFile(path, raw, 0o600))
	read, err := ReadClaudeTokenFleetManifestBytes(path)
	require.NoError(t, err)
	assert.Equal(t, raw, read)
	parsed, err := ParseClaudeTokenFleetManifest(read)
	require.NoError(t, err)
	fromPath, err := ReadClaudeTokenFleetManifest(path)
	require.NoError(t, err)
	assert.Equal(t, fromPath, parsed)
	assert.NotEqual(t, ClaudeTokenFleetSHA256(raw), ClaudeTokenFleetSHA256(append(append([]byte(nil), raw...), '\n')))
	assert.Equal(t, "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad", ClaudeTokenFleetSHA256([]byte("abc")))
}

func TestClaudeTokenFleetRefusesUnknownOrCollidingBindings(t *testing.T) {
	t.Parallel()
	base := claudeFleetHeader + "\n" +
		"account-a\tAccount A\tstudio\tsource-a\tmini\tfriend-a\ttarget-a\t\n"
	for _, tc := range []struct{ name, tail, want string }{
		{"target seat reused", "account-b\tAccount B\tstudio\tsource-b\tmini\tfriend-b\ttarget-a\t\n", "target seat"},
		{"profile reused", "account-b\tAccount B\tstudio\tsource-b\tmini\tfriend-a\ttarget-b\t\n", "profile"},
		{"same machine profile twice", "account-a\tAccount A\tstudio\tsource-a\tmini\tfriend-a\ttarget-b\t\n", "bound twice"},
		{"source reused", "account-b\tAccount B\tstudio\tsource-a\tmini\tfriend-b\ttarget-b\t\n", "source seat"},
		{"source is target", "account-b\tAccount B\tstudio\tsource-b\tmini\tfriend-b\tsource-a\t\n", "also a target"},
		{"unknown source machine", "account-b\tAccount B\t\tsource-b\tmini\tfriend-b\ttarget-b\t\n", "explicit safe"},
		{"unknown target machine", "account-b\tAccount B\tstudio\tsource-b\t\tfriend-b\ttarget-b\t\n", "explicit safe"},
		{"overlong account ID", strings.Repeat("a", 65) + "\tAccount B\tstudio\tsource-b\tmini\tfriend-b\ttarget-b\t\n", "explicit safe"},
		{"account metadata changed", "account-a\tOther label\tstudio\tsource-a\tmini\tfriend-b\ttarget-b\t\n", "changes its label"},
		{"source machine changed", "account-a\tAccount A\tother-studio\tsource-a\tstudio\tfriend-b\ttarget-b\t\n", "changes its label"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(t.TempDir(), "bindings.tsv")
			require.NoError(t, os.WriteFile(path, []byte(base+tc.tail), 0o600))
			_, err := ReadClaudeTokenFleetManifest(path)
			assert.ErrorContains(t, err, tc.want)
		})
	}
	t.Run("malformed header", func(t *testing.T) {
		t.Parallel()
		path := filepath.Join(t.TempDir(), "bindings.tsv")
		require.NoError(t, os.WriteFile(path, []byte("account\tseat\n"+base), 0o600))
		_, err := ReadClaudeTokenFleetManifest(path)
		assert.ErrorContains(t, err, "exact eight-column header")
	})
	t.Run("symlink", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, "real.tsv"), []byte(base), 0o600))
		require.NoError(t, os.Symlink(filepath.Join(dir, "real.tsv"), filepath.Join(dir, "link.tsv")))
		_, err := ReadClaudeTokenFleetManifest(filepath.Join(dir, "link.tsv"))
		assert.ErrorContains(t, err, "real regular file")
	})
	t.Run("symlinked ancestor", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		realDir := filepath.Join(dir, "real")
		require.NoError(t, os.Mkdir(realDir, 0o700))
		require.NoError(t, os.WriteFile(filepath.Join(realDir, "bindings.tsv"), []byte(base), 0o600))
		require.NoError(t, os.Symlink(realDir, filepath.Join(dir, "linkdir")))
		_, err := ReadClaudeTokenFleetManifest(filepath.Join(dir, "linkdir", "bindings.tsv"))
		assert.ErrorContains(t, err, "real regular file")
	})
}

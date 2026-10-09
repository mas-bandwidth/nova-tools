package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/onboarding"
)

func TestClaudeTokenCLIPlansWithoutValuesOrMutation(t *testing.T) {
	t.Parallel()
	store := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(store, ".git"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(store, ".sops.yaml"), []byte("creation_rules: []\n"), 0o600))
	const sentinel = "never-print-this-token-value"
	for name, body := range map[string]string{
		"source": "CLAUDE_CODE_OAUTH_TOKEN: ENC[AES256_GCM,data:" + sentinel + ",type:str]\nsops:\n  age: []\n",
		"target": "OTHER_KEY: ENC[AES256_GCM,data:other,type:str]\nsops:\n  age: []\n",
	} {
		require.NoError(t, os.WriteFile(filepath.Join(store, name+".yaml"), []byte(body), 0o600))
	}
	var stdout, stderr strings.Builder
	code := run([]string{"claude-token", "--store", store, "--from", "source", "--to", "target", "--json"}, strings.NewReader(""), &stdout, &stderr)
	require.Equal(t, 0, code, stderr.String())
	assert.Contains(t, stdout.String(), `"token_expiry":"unknown"`)
	assert.Contains(t, stdout.String(), `"source_state":"sealed_name_present_unverified"`)
	assert.Contains(t, stdout.String(), `"state":"missing"`)
	assert.NotContains(t, stdout.String(), sentinel)
	assert.Empty(t, stderr.String())
	entries, err := os.ReadDir(store)
	require.NoError(t, err)
	assert.Len(t, entries, 4) // no receipt, token copy, or write
}

func TestClaudeTokenToolMeetsTheStandard(t *testing.T) {
	t.Parallel()
	assert.Empty(t, claudeTokenTool().Problems())
}

func TestClaudeTokenHelpExampleRuns(t *testing.T) {
	t.Parallel()
	const example = "nova-secrets claude-token --store ./secrets --from ada --to bo"
	var help, ignored strings.Builder
	require.Equal(t, 0, run([]string{"claude-token", "-h"}, strings.NewReader(""), &help, &ignored))
	assert.Contains(t, help.String(), "example: "+example)
	store := filepath.Join(t.TempDir(), "secrets")
	require.NoError(t, os.MkdirAll(filepath.Join(store, ".git"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(store, ".sops.yaml"), []byte("creation_rules: []\n"), 0o600))
	for _, seat := range []string{"ada", "bo"} {
		require.NoError(t, os.WriteFile(filepath.Join(store, seat+".yaml"), []byte("OTHER_KEY: ENC[AES256_GCM,data:x,type:str]\nsops:\n  age: []\n"), 0o600))
	}
	var out, errOut strings.Builder
	code := run([]string{"claude-token", "--store", store, "--from", "ada", "--to", "bo"}, strings.NewReader(""), &out, &errOut)
	require.Equal(t, 0, code, errOut.String())
	step := onboarding.Step{Line: "$ " + example, Want: []string{
		"CLAUDE-TOKEN OK source=ada source_state=missing targets=1 token_expiry=unknown issued_at=- renew_by=- renewal=unknown store_freshness=not_checked provider_turn=not_checked",
		"CLAUDE-TOKEN SEAT name=bo state=missing",
		`CLAUDE-TOKEN NEXT action=issue seat=ada command=claude\x20setup-token`,
		`CLAUDE-TOKEN NEXT action=seal_hidden_prompt seat=ada command=nova-secrets\x20seal\x20-h`,
		`CLAUDE-TOKEN NEXT action=inject_or_refresh seat=bo command=nova-secrets\x20seat\x20inject\x20-h`,
		`CLAUDE-TOKEN NEXT action=check_on_target seat=bo command=nova-secrets\x20check\x20-h`,
		"CLAUDE-TOKEN NOTE Read-only advisory: a sealed key name and recorded date do not prove token validity, entitlement, or an actual provider reply.",
	}}
	for _, problem := range onboarding.Compare(step, onboarding.Result{Code: code, Stdout: out.String(), Stderr: errOut.String()}, nil) {
		t.Error(problem)
	}
}

func TestClaudeTokenFleetHelpExampleRuns(t *testing.T) {
	t.Parallel()
	const example = "nova-secrets claude-token-fleet --store ./secrets --manifest ./claude-token-fleet.tsv"
	var help, ignored strings.Builder
	require.Equal(t, 0, run([]string{"claude-token-fleet", "-h"}, strings.NewReader(""), &help, &ignored))
	assert.Contains(t, help.String(), "example: "+example)
	dir := t.TempDir()
	store := filepath.Join(dir, "secrets")
	require.NoError(t, os.MkdirAll(filepath.Join(store, ".git"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(store, ".sops.yaml"), []byte("creation_rules: []\n"), 0o600))
	for _, seat := range []string{"ada", "bo"} {
		require.NoError(t, os.WriteFile(filepath.Join(store, seat+".yaml"), []byte("OTHER_KEY: ENC[AES256_GCM,data:x,type:str]\nsops:\n  age: []\n"), 0o600))
	}
	manifest := filepath.Join(dir, "claude-token-fleet.tsv")
	require.NoError(t, os.WriteFile(manifest, []byte("account_id\tlabel\tsource_machine\tsource_seat\tmachine\tprofile_id\ttarget_seat\tissued_at\nada\tAccount A\tstudio\tada\tmini\tfriend\tbo\t\n"), 0o600))
	var out, errOut strings.Builder
	code := run([]string{"claude-token-fleet", "--store", store, "--manifest", manifest}, strings.NewReader(""), &out, &errOut)
	require.Equal(t, 0, code, errOut.String())
	step := onboarding.Step{Line: "$ " + example, Want: []string{
		"CLAUDE-TOKEN-FLEET OK accounts=1 targets=1 token_expiry=unknown recipient_registry=not_checked provider_turn=not_checked",
		`CLAUDE-TOKEN-FLEET ACCOUNT id=ada label=Account\x20A source_machine=studio source_seat=ada source_state=missing renew_by=- renewal=unknown`,
		"CLAUDE-TOKEN-FLEET TARGET account=ada machine=mini profile=friend seat=bo state=missing",
		"CLAUDE-TOKEN-FLEET NOTE Read-only advisory: distinct seat names do not establish recipient registry admission, token validity, entitlement, or a provider reply.",
	}}
	for _, problem := range onboarding.Compare(step, onboarding.Result{Code: code, Stdout: out.String(), Stderr: errOut.String()}, nil) {
		t.Error(problem)
	}
}

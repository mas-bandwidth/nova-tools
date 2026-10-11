package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/pkg/swarm"
)

// A LIMITED ROUTE'S PROVIDER IS GIVEN ITS TABLE BASE; AN UNLIMITED ONE IS NOT TOUCHED
// (providerbudget.go, THE GLOBAL RATE BUDGET; nova-sprint tla/RateBudget.tla). The limited case
// opens the proxy's listener, so it is in jobconfig_limited_functional_test.go; the two
// cases here open no socket.

// writtenJobConfig is the opencode.json writeJobConfig wrote under dataHome.
func writtenJobConfig(t *testing.T, dataHome string) []byte {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(dataHome, ".config", "opencode", "opencode.json"))
	require.NoError(t, err, "writeJobConfig wrote the job's opencode.json")
	return body
}

// A fleet member's inception job with no limit (no --budget-server) is written exactly as
// it was before the limited route's base was seeded: the fence, the declared model and the
// read options, and no baseURL, so no proxy.
func TestAnUnlimitedInceptionJobConfigIsUnchanged(t *testing.T) {
	t.Parallel()
	dataHome, jobDir := t.TempDir(), t.TempDir()
	cfg := nativeRunConfig{model: "inception/mercury-3-preview-1002", label: "c1"}

	var notes bytes.Buffer
	sha, reason, proxy := writeJobConfig(cfg, "inception", dataHome, jobDir, nil, &notes)
	require.Empty(t, reason, "the config is written")
	require.NotEmpty(t, sha, "the config's sha8 is recorded")

	before, merged := swarm.MergeFencePermission(nil, jobDir, nil)
	require.True(t, merged, "the fence merges into an empty config")
	before, ok := swarm.DeclareRouteModel(before, "inception", "mercury-3-preview-1002")
	require.True(t, ok, "the route's model is declared")
	before = swarm.ApplyProviderReadDeadline(before, "inception")

	assert.Nil(t, proxy, "an unlimited route opens no proxy")
	assert.Equal(t, string(before), string(writtenJobConfig(t, dataHome)), "the unlimited job's config is byte-identical to the one written before this change")
	assert.Empty(t, swarm.ProviderBaseURL(writtenJobConfig(t, dataHome), "inception"), "no baseURL is written for an unlimited route")
	assert.Empty(t, notes.String(), "an unlimited route writes no NATIVE NOTE")
}

// A limited route whose provider has no base in the table keeps today's NATIVE NOTE: no
// baseURL is invented and no proxy opens.
func TestALimitedProviderWithNoTableBaseKeepsTheNote(t *testing.T) {
	t.Parallel()
	require.Empty(t, swarm.ProviderTableBaseURL("opencode"), "the table names no base for opencode")
	dataHome := t.TempDir()
	cfg := nativeRunConfig{model: "opencode/deepseek-v4-flash", label: "c1", budgetServer: "127.0.0.1:1", budgetAs: "m1"}

	var notes bytes.Buffer
	_, reason, proxy := writeJobConfig(cfg, "opencode", dataHome, t.TempDir(), nil, &notes)
	require.Empty(t, reason, "the config is written")

	assert.Nil(t, proxy, "no proxy opens without a base to stand in front of")
	assert.Empty(t, swarm.ProviderBaseURL(writtenJobConfig(t, dataHome), "opencode"), "no baseURL is invented")
	assert.Contains(t, notes.String(), "has no http baseURL the proxy can stand in front of", "the NATIVE NOTE is written as before")
}

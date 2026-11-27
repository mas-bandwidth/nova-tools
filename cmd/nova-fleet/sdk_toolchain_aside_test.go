package main

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// sdk_toolchain_aside_test.go pins how the shared SDK is kept to what the play installs (roles/sdk-toolchain/tasks/
// aside.yml, and the SHARE-HELD remedy in link.yml): a hand entry fails the run unless nova_sdk_aside_dir is given;
// with it, entries are moved there only after a root-read in-use scan, never under --check. The scripts themselves run
// in sdk_toolchain_aside_functional_test.go.

func TestSDKAsideIsOptInAndEmptyByDefault(t *testing.T) {
	t.Parallel()
	d := readSDKDefaultsMap(t)
	assert.Equal(t, "", d["nova_sdk_aside_dir"], "nothing moves unless a run names the directory")
}

func TestSDKAsideRunsFirstInLinkAndFailsWithoutADirectory(t *testing.T) {
	t.Parallel()
	link := taskFileOf(t, sdkToolchainRole+"/tasks/link.yml")
	inc := runnerTask(t, link, "the shared SDK holds what the play installs (aside.yml)")
	assert.Equal(t, "aside.yml", asText(inc["ansible.builtin.include_tasks"]))
	assert.Less(t, taskIndex(link, "the shared SDK holds what the play installs"), taskIndex(link, "everything in the shared SDK is readable"),
		"moved before the readability check reads it")

	aside := taskFileOf(t, sdkToolchainRole+"/tasks/aside.yml")
	fail := runnerTask(t, aside, "a hand entry in the shared SDK fails the run (no nova_sdk_aside_dir)")
	assert.Equal(t, "sdk_unpinned_hand | length > 0 and nova_sdk_aside_dir | length == 0", asText(fail["when"]))
	assert.Contains(t, asText(fail["ansible.builtin.fail"].(map[string]any)["msg"]), "SDK-TOOLCHAIN UNPINNED")

	list := runnerTask(t, aside, "what the shared SDK holds that no pin names")
	assert.Equal(t, false, list["check_mode"], "read under --check too, so --check fails on a hand entry")
}

func TestSDKAsideMovesOnlyAfterARootInUseScanAndNeverUnderCheck(t *testing.T) {
	t.Parallel()
	aside := taskFileOf(t, sdkToolchainRole+"/tasks/aside.yml")
	scan := runnerTask(t, aside, "no process of the login runs from an entry to move aside")
	assert.NotContains(t, scan, "become", "the SDK is the login's")
	move := runnerTask(t, aside, "each entry is moved aside under its own name")
	assert.Contains(t, asText(move["when"]), "not ansible_check_mode")
	script := asText(move["vars"].(map[string]any)["sdk_aside_script"])
	assert.NotContains(t, script, "rm ", "nothing is deleted")
	assert.Contains(t, script, `[ "$(stat -c %d "$src")" = "$(stat -c %d "$dst")" ]`, "a rename inside one filesystem, never a copy and delete")
	assert.Less(t, taskIndex(aside, "no process of the login runs from an entry"), taskIndex(aside, "each entry is moved aside"))
	assert.Less(t, taskIndex(aside, "an entry in use is not moved"), taskIndex(aside, "each entry is moved aside"))
}

func TestSDKShareHeldIsMovedAsideOnlyWithADirectory(t *testing.T) {
	t.Parallel()
	link := taskFileOf(t, sdkToolchainRole+"/tasks/link.yml")
	held := runnerTask(t, link, "an old SDK holding what the shared one lacks is kept")
	assert.Equal(t, "sdk_dir_extra.stdout_lines | default([]) | length > 0 and nova_sdk_aside_dir | length == 0", asText(held["when"]))
	move := runnerTask(t, link, "what the old SDK holds that the shared one lacks is moved aside (nova_sdk_aside_dir)")
	assert.Contains(t, asText(move["when"]), "nova_sdk_aside_dir | length > 0")
	assert.Contains(t, asText(move["when"]), "not ansible_check_mode")
	// the old SDK's in-use scan runs before anything is moved out of it, and the link is made after
	inUse := runnerTask(t, link, "the old SDK is in use by no process before anything moves out of it (nova_sdk_aside_dir)")
	assert.Equal(t, "in-use.yml", asText(inUse["ansible.builtin.include_tasks"]))
	assert.Less(t, taskIndex(link, "the old SDK is in use by no process before"), taskIndex(link, "what the old SDK holds that the shared one lacks is moved aside"))
	assert.Less(t, taskIndex(link, "what the old SDK holds that the shared one lacks is moved aside"), taskIndex(link, "the login's own SDK is moved aside and nova_sdk_dir names"))
}

func readSDKDefaultsMap(t *testing.T) map[string]any {
	t.Helper()
	b, err := os.ReadFile(sdkToolchainRole + "/defaults/main.yml")
	require.NoError(t, err)
	m := map[string]any{}
	require.NoError(t, yaml.Unmarshal(b, &m))
	require.NotEmpty(t, m)
	return m
}

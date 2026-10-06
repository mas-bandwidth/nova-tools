package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// install writes the harness's settings before the agent, refuses a
// symlinked directory with nothing written or loaded, and check --settings
// names the drift (docs/SPEC-FRIEND.md, Harness settings).
func TestInstallWritesTheHarnessSettingsAndCheckNamesTheDrift(t *testing.T) {
	t.Parallel()
	r := newRig(t, "ada", "bob")
	r.env["CODEX_HOME"] = "/w/codex"
	cli := r.cli()
	config := "/w/codex/config.toml"

	cli.Do(t, "check", "--settings", "--as", "bob", "--harness", "codex", "--dir", "/w/bob").Exit(1).
		Err("CHECK DRIFT harness=codex settings=3 drift=1",
			`CHECK DRIFT harness=codex file=`+config+` name=sandbox_workspace_write.writable_roots want="/w/bob" have="[]"`,
			"NOTE install again writes them: nova-friend install --as bob --harness codex --dir /w/bob")
	cli.Do(t, "install", "--as", "bob", "--harness", "codex", "--dir", "/w/bob", "--dry-run").Exit(0).
		Out(`INSTALL PLAN command="write ` + config + ` sandbox_workspace_write.writable_roots=/w/bob"`)
	_, err := r.fs.ReadFile(config)
	require.ErrorIs(t, err, os.ErrNotExist, "a dry run writes nothing")

	cli.Do(t, "install", "--as", "bob", "--harness", "codex", "--dir", "/w/bob").Exit(0).
		Out(`INSTALL WROTE harness=codex file=` + config + ` name=sandbox_workspace_write.writable_roots value="/w/bob"`)
	raw, err := r.fs.ReadFile(config)
	require.NoError(t, err)
	assert.Contains(t, string(raw), `writable_roots = ["/w/bob"]`)
	cli.Do(t, "check", "--settings", "--as", "bob", "--harness", "codex", "--dir", "/w/bob").Exit(0).
		Out("CHECK OK harness=codex settings=3 drift=0")

	// a symlinked directory: refused, nothing written, no agent loaded
	r.fs.Symlink("/Volumes/nova/ai/carol", "/w/carol")
	r.launchctl = nil
	cli.Do(t, "install", "--as", "carol", "--harness", "codex", "--dir", "/w/carol").Exit(2).
		Err("INSTALL REFUSED", "not a real directory", `/w/carol (dir) is symlink to /Volumes/nova/ai/carol`)
	assert.Empty(t, r.launchctl)
	assert.NoFileExists(t, filepath.Join(r.home, "Library", "LaunchAgents", "com.nova.friend-carol.plist"))
	cli.Do(t, "check", "--settings", "--as", "carol", "--harness", "codex", "--dir", "/w/carol").Exit(1).
		Err(`CHECK DRIFT harness=codex file=/w/carol name=dir want="directory" have="symlink to /Volumes/nova/ai/carol"`)
}

func TestInstallNamesTheGrokWakeFileAndTheClaudeConfigDirInTheAgent(t *testing.T) {
	t.Parallel()
	r := newRig(t, "ada", "bob")
	cli := r.cli()
	plist := filepath.Join(r.home, "Library", "LaunchAgents", "com.nova.friend-bob.plist")
	wake := filepath.Join(r.home, ".nova-friend", "bob", "bob.wake")

	// grok by a dry run: a real install runs the delivery check, and grok's reads the host's ~/.grok
	cli.Do(t, "install", "--as", "bob", "--harness", "grok", "--dir", "/w/bob", "--dry-run").Exit(0).
		Out(`INSTALL PLAN command="write `+filepath.Dir(wake)+` wake-directory=directory"`, `INSTALL PLAN command="write `+wake+` wake-file=file"`, "NOTE monitor `tail -n 0 -F "+wake+"`")
	_, err := r.fs.ReadFile(wake)
	require.ErrorIs(t, err, os.ErrNotExist, "a dry run writes nothing")

	// claude is a headless lane harness (fr-go-runners-r.w1): install writes its config directory
	// into the agent, and the check's prompt goes in on stdin
	cli.Do(t, "install", "--as", "bob", "--harness", "claude", "--dir", "/w/bob", "--config-dir", "/w/bob-claude").Exit(0).
		Out("INSTALL OK label=com.nova.friend-bob", "INSTALL WROTE harness=claude file=/w/bob-claude name=CLAUDE_CONFIG_DIR")
	assert.FileExists(t, plist)
}

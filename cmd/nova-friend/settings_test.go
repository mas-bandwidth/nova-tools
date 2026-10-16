package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A Codex one-shot install may plan settings without a session route; an
// incomplete named session route still refuses before settings writes.
func TestCodexOneShotInstallAndRefusedSessionPreserveSettingsInspection(t *testing.T) {
	t.Parallel()
	r := newRig(t, "ada", "bob")
	r.env["CODEX_HOME"] = "/w/codex"
	cli := r.cli()
	config := "/w/codex/config.toml"

	cli.Do(t, "check", "--settings", "--as", "bob", "--harness", "codex", "--dir", "/w/bob").Exit(1).
		Err("CHECK DRIFT harness=codex settings=3 drift=1",
			`CHECK DRIFT harness=codex file=`+config+` name=sandbox_workspace_write.writable_roots want="/w/bob" have="[]"`,
			"one-shot card runner", "--adapter folder --session <real-session> --delivery-dir <existing-watched-folder>")
	cli.Do(t, "install", "--as", "bob", "--harness", "codex", "--dir", "/w/bob", "--dry-run").Exit(0).
		Out("INSTALL PLAN")
	cli.Do(t, "install", "--as", "bob", "--harness", "codex", "--dir", "/w/bob", "--session", "real-thread", "--dry-run").Exit(2).
		Err("codex native push is unavailable")
	cli.Do(t, "install", "--as", "bob", "--harness", "codex", "--dir", "/w/bob", "--session", "real-thread").Exit(2).
		Err("codex native push is unavailable")
	_, err := r.fs.ReadFile(config)
	require.ErrorIs(t, err, os.ErrNotExist, "a refused install writes no harness settings")
	assert.Empty(t, r.launchctl)
	assert.NoFileExists(t, filepath.Join(r.home, "Library", "LaunchAgents", "com.nova.friend-bob.plist"))
	// Inspection remains available without selecting an unavailable delivery route.
	r.fs.Symlink("/Volumes/nova/ai/carol", "/w/carol")
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

	// cursor has no deliver command: install refuses it before anything is written (the push proof)
	cli.Do(t, "install", "--as", "bob", "--harness", "cursor", "--dir", "/w/bob", "--config-dir", "/w/bob-claude").Exit(2).
		Err("INSTALL REFUSED: no deliver command for cursor")
	assert.NoFileExists(t, plist)
	// claude runs each card as a process of its own: no session to push into, so install goes on
	cli.Do(t, "install", "--as", "bob", "--harness", "claude", "--dir", "/w/bob", "--config-dir", "/w/bob-claude", "--dry-run").Exit(0).
		Out("INSTALL PLAN")
}

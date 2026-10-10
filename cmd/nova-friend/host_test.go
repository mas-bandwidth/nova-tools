package main

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/pkg/friend"
	"github.com/mas-bandwidth/nova-tools/pkg/testkit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// tmuxCalls is a tmux behind the world's Exec seam: has-session answers by exists, and every argv is kept.
func tmuxCalls(exists bool, calls *[]string) friend.Exec {
	return func(_ context.Context, _, name string, args []string, _ string) (string, int, error) {
		*calls = append(*calls, name+" "+strings.Join(args, " "))
		if args[0] == "has-session" && !exists {
			return "can't find session", 1, nil
		}
		return "", 0, nil
	}
}

func hostCLI(r *rig, exec friend.Exec) testkit.Main {
	w := r.world()
	w.exec = exec
	return testkit.Main(func(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
		return run(args, stdin, stdout, stderr, w)
	})
}

func TestHostStartsTheSessionAndSavesItsState(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	var calls []string
	state := filepath.Join(t.TempDir(), "bob")
	cli := hostCLI(r, tmuxCalls(false, &calls))
	out := cli.Do(t, "host", "--as", "bob", "--harness", "aider", "--dir", "/w/bob", "--state-dir", state, "--", "aider", "--no-auto-commits").Exit(0)
	assert.Contains(t, out.Stdout, `HOST OK session=friend-bob dir=/w/bob attach="tmux attach -t friend-bob"`)
	assert.Equal(t, []string{"tmux has-session -t friend-bob", "tmux new-session -d -s friend-bob -c /w/bob -- aider --no-auto-commits"}, calls)
	raw, err := os.ReadFile(filepath.Join(state, friend.HostFile))
	require.NoError(t, err)
	assert.Contains(t, string(raw), `"session": "friend-bob"`)
}

func TestHostRefusesASessionThatRunsAtExit1(t *testing.T) {
	t.Parallel()
	var calls []string
	out := hostCLI(newRig(t), tmuxCalls(true, &calls)).Do(t, "host", "--as", "bob", "--harness", "aider", "--dir", "/w/bob", "--state-dir", t.TempDir(), "--", "aider").Exit(1)
	assert.Contains(t, out.Stderr, "HOST REFUSED: friend-bob runs already; run: tmux attach -t friend-bob")
	assert.Equal(t, []string{"tmux has-session -t friend-bob"}, calls)
}

func TestHostDryRunPrintsTheCommandAndStartsNothing(t *testing.T) {
	t.Parallel()
	var calls []string
	state := t.TempDir()
	out := hostCLI(newRig(t), tmuxCalls(false, &calls)).Do(t, "host", "--as", "bob", "--harness", "grok", "--dir", "/w/bob", "--state-dir", state, "--dry-run", "--", "grok").Exit(0)
	assert.Contains(t, out.Stdout, "HOST DRY-RUN")
	assert.Contains(t, out.Stdout, "tmux new-session -d -s friend-bob -c /w/bob -- grok")
	assert.Empty(t, calls)
	assert.NoFileExists(t, filepath.Join(state, friend.HostFile))
}

func TestHostWantsALaunchCommandAndAKnownPrompt(t *testing.T) {
	t.Parallel()
	cli := hostCLI(newRig(t), tmuxCalls(false, new([]string)))
	cli.Do(t, "host", "--as", "bob", "--harness", "aider", "--dir", "/w/bob", "--state-dir", t.TempDir()).Exit(2)
	cli.Do(t, "host", "--as", "bob", "--harness", "aider", "--dir", "/w/bob", "--state-dir", t.TempDir(), "--").Exit(2)
	cli.Do(t, "host", "--as", "bob", "--harness", "nosuch", "--dir", "/w/bob", "--state-dir", t.TempDir(), "--", "x").Exit(2)
	cli.Do(t, "host", "--as", "bob", "--harness", "nosuch", "--prompt", `^\$ $`, "--dir", "/w/bob", "--state-dir", t.TempDir(), "--dry-run", "--", "x").Exit(0)
}

// The host verb's help names every flag, output line, JSON field and exit
// code, runs one example as written, and docs/CLI.md carries the same text.
func TestHostHelpAndCommandReferenceNameEveryFlagLineFieldAndExit(t *testing.T) {
	t.Parallel()
	help := newRig(t).cli().Do(t, "host", "-h").Exit(0).Stdout
	raw, err := os.ReadFile("../../docs/CLI.md")
	require.NoError(t, err)
	doc := string(raw)
	for _, text := range []string{
		"--as", "--harness", "--dir", "--prompt", "--dry-run", "--json", "--state-dir",
		`HOST OK session=friend-<name> dir=<d> attach="tmux attach -t friend-<name>"`,
		"HOST REFUSED: friend-<name> runs already; run: tmux attach -t friend-<name>",
		"HOST DRY-RUN session= dir= command=",
		"session, dir, attach", "Exit 0 started, 1 refused, 2 could not run",
		"--harness tmux", "nova-friend host --as bob --harness aider --dir ./bob --dry-run -- aider",
	} {
		require.Contains(t, help, text)
		require.Contains(t, doc, text, "docs/CLI.md carries the help's text")
	}
}

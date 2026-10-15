package notice

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGrokCommandQuotesShellMeta(t *testing.T) {
	t.Parallel()
	for _, frag := range []string{";", "&", "|", "\\", "'"} {
		t.Run(frag, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			marker := filepath.Join(dir, "marker")
			argsPath := filepath.Join(dir, "args")
			script := filepath.Join(dir, "hook.sh")
			require.NoError(t, os.WriteFile(script, []byte("#!/bin/sh\nprintf '%s\\n' \"$1\" \"$2\" > '"+argsPath+"'\n"), 0o755))
			state := dir + "/s" + frag + "touch " + marker
			home, err := GrokHome(state, nil)
			require.NoError(t, err)
			cmd := grokCommand(script, "busy", home).Command
			require.NoError(t, exec.Command("sh", "-c", cmd).Run(), cmd)
			_, err = os.Stat(marker)
			assert.True(t, os.IsNotExist(err), cmd)
			got, err := os.ReadFile(argsPath)
			require.NoError(t, err)
			assert.Equal(t, "busy\n"+home+"\n", string(got))
		})
	}
}

func TestGrokQuotedCommandPublishesOneSnapshot(t *testing.T) {
	t.Parallel()
	home := filepath.Join(t.TempDir(), "a;b&c|d\\e")
	_, err := InstallGrok(home)
	require.NoError(t, err)
	raw, err := os.ReadFile(filepath.Join(home, "hooks", "nova-hook.json"))
	require.NoError(t, err)
	var doc grokHookFile
	require.NoError(t, json.Unmarshal(raw, &doc))
	require.NoError(t, exec.Command("sh", "-c", doc.Hooks.UserPromptSubmit[0].Hooks[0].Command).Run())
	require.NoError(t, exec.Command("sh", "-c", doc.Hooks.Stop[0].Hooks[0].Command).Run())
	state, err := os.ReadFile(filepath.Join(home, "nova-hook", "state"))
	require.NoError(t, err)
	assert.Equal(t, "1 idle\n", string(state))
	_, err = os.Stat(filepath.Join(home, "nova-hook", "turn"))
	assert.True(t, os.IsNotExist(err))
	idle, gen, err := readGrokSample(home)
	require.NoError(t, err)
	assert.True(t, idle)
	assert.Equal(t, 1, gen)
}

// TestGrokConsumesOnlyAFinishedTurn pins Observe (tla/GrokHookNotice.tla): a
// busy then idle pair between two samples finishes the turn, a busy sample
// does not, and an idle word with no new generation does not.
func TestGrokConsumesOnlyAFinishedTurn(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		words []string
		want  bool
	}{
		{"busy then idle between samples", []string{"busy", "idle"}, true},
		{"a busy sample", []string{"busy"}, false},
		{"idle with no new generation", nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			home := t.TempDir()
			wake, err := InstallGrok(home)
			require.NoError(t, err)
			g := &Grok{Home: home, Wake: wake}
			kind, err := g.Push(t.Context(), Notice{Path: "/private/events/n1.json"})
			require.NoError(t, err)
			assert.Equal(t, "wake", kind, "an idle session is woken")
			for _, w := range tc.words {
				require.NoError(t, exec.Command(filepath.Join(home, "hooks", "nova-hook-grok.sh"), w, filepath.Join(home, "nova-hook")).Run())
			}
			done, err := g.Consumed(t.Context(), Notice{})
			require.NoError(t, err)
			assert.Equal(t, tc.want, done)
		})
	}
}

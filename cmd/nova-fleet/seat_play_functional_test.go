//go:build functional

package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// seat_play_functional_test.go runs seat.yml's own tasks that read the seat key's public half (from "the seat's
// public half" up to the key's receipt), copied into a localhost playbook, against a key directory in the test's temp
// dir with the real age-keygen; and renders join.yml's check receipt through the templar. A key file age-keygen cannot
// read (a 0-byte file, which `creates:` keeps) used to come back as an empty public half: state no-key, exit 0.

// playTasks is the play's tasks from the one named first up to, not including, the one named last (to the play's end
// when last is empty).
func playTasks(t *testing.T, file, first, last string) []map[string]any {
	t.Helper()
	b, err := os.ReadFile(file)
	require.NoError(t, err)
	var plays []map[string]any
	require.NoError(t, yaml.Unmarshal(b, &plays))
	for _, play := range plays {
		tasks := mapsOf(t, play["tasks"])
		from := -1
		for i, task := range tasks {
			name := asText(task["name"])
			if from >= 0 && last != "" && strings.HasPrefix(name, last) {
				return tasks[from:i]
			}
			if from < 0 && strings.HasPrefix(name, first) {
				from = i
			}
		}
		if from >= 0 && last == "" {
			return tasks[from:]
		}
	}
	t.Fatalf("%s: no tasks from %q to %q", file, first, last)
	return nil
}

func runLocalPlay(t *testing.T, tasks []map[string]any, vars map[string]any, extra ...string) (string, error) {
	t.Helper()
	dir := t.TempDir()
	play := []map[string]any{{"hosts": "localhost", "connection": "local", "gather_facts": false, "vars": vars, "tasks": tasks}}
	b, err := yaml.Marshal(play)
	require.NoError(t, err)
	file := filepath.Join(dir, "play.yml")
	require.NoError(t, os.WriteFile(file, b, 0o644))
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "ansible-playbook", append(append([]string{"-i", "localhost,"}, extra...), file)...)
	cmd.Env = append(os.Environ(), "ANSIBLE_NOCOLOR=1", "ANSIBLE_LOCAL_TEMP="+filepath.Join(dir, "tmp"))
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func TestFunctionalSeatRefusesAKeyAgeKeygenCannotRead(t *testing.T) {
	t.Parallel()
	keygen, err := exec.LookPath("age-keygen")
	if err != nil {
		t.Skip("age-keygen is not installed on this machine")
	}
	if _, err := exec.LookPath("ansible-playbook"); err != nil {
		t.Skip("ansible-core is not installed on this machine")
	}
	tasks := playTasks(t, "seat.yml", "the seat's public half", "the store's rules")
	run := func(t *testing.T, key []byte, extra ...string) (string, error) {
		dir := t.TempDir()
		if key != nil {
			require.NoError(t, os.WriteFile(filepath.Join(dir, "m1.key"), key, 0o600))
		}
		return runLocalPlay(t, tasks, map[string]any{"nova_secrets_key_dir": dir, "nova_seat": "m1", "nova_age_keygen": keygen}, extra...)
	}

	t.Run("empty key file", func(t *testing.T) {
		t.Parallel()
		out, err := run(t, []byte{})
		require.Error(t, err, out)
		assert.Contains(t, out, "SEAT STOP")
		assert.Contains(t, out, "m1.key")
		assert.Contains(t, out, "no identities found", "the refusal carries age-keygen's own cause")
		assert.NotContains(t, out, "SEAT PUB m1 none")
	})
	t.Run("missing key outside check mode", func(t *testing.T) {
		t.Parallel()
		out, err := run(t, nil)
		require.Error(t, err, out)
		assert.Contains(t, out, "SEAT STOP")
	})
	t.Run("missing key in check mode", func(t *testing.T) {
		t.Parallel()
		out, err := run(t, nil, "--check")
		require.NoError(t, err, out)
		assert.Contains(t, out, "SEAT PUB m1 WOULD-MAKE")
	})
	t.Run("a real key", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		key := filepath.Join(dir, "k")
		require.NoError(t, exec.Command(keygen, "-o", key).Run())
		b, err := os.ReadFile(key)
		require.NoError(t, err)
		out, err := run(t, b)
		require.NoError(t, err, out)
		assert.Contains(t, out, "SEAT PUB m1 age1")
	})
}

// TestFunctionalJoinCheckReportsTheSeatItFound: the join's check receipt reports seat.yml's seat_state, never a
// fixed seat=present.
func TestFunctionalJoinCheckReportsTheSeatItFound(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("ansible-playbook"); err != nil {
		t.Skip("ansible-core is not installed on this machine")
	}
	receipt := playTasks(t, "join.yml", "the check's receipt", "")
	require.Len(t, receipt, 1)
	for state, want := range map[string]string{"present": "seat=present", "add": "seat=added", "": "seat=none"} {
		vars := map[string]any{"check_peer": map[string]any{"Online": true}}
		if state != "" {
			vars["seat_state"] = state
		}
		out, err := runLocalPlay(t, receipt, vars)
		require.NoError(t, err, out)
		assert.Contains(t, out, want, "seat_state %q", state)
	}
}

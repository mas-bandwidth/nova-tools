//go:build functional

package fleet

import (
	"context"
	"encoding/json"
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

// loops_play_functional_test.go renders fleet/loops.yml's Linux unit state
// through ansible-core's own templar. It carries //go:build functional
// (docs/STANDARD.md section 8) so the unit tier does not compile the ansible
// render.
//
// The render runs the task as the play holds it: its own vars block and its
// module state argument, copied into a one-task localhost playbook, fed the
// inputs the play feeds it (loop_units, loop_timers, loop_restart, item). The
// four truth-table inputs are held to restarted, restarted, started and stopped.

// stateRenderPlay is a one-task localhost playbook that prints what the Linux
// state task would hand systemd: the task's own vars and its state argument.
func stateRenderPlay(t *testing.T, task map[string]any) []byte {
	t.Helper()
	systemd, _ := task["ansible.builtin.systemd_service"].(map[string]any)
	require.NotNil(t, systemd, "the Linux state task has no systemd_service args")
	state := asText(systemd["state"])
	require.NotEmpty(t, state, "the Linux state task has no state argument")
	debug := map[string]any{"ansible.builtin.debug": map[string]any{"msg": state}}
	if vars, ok := task["vars"]; ok {
		debug["vars"] = vars
	}
	play := []map[string]any{{
		"hosts":        "localhost",
		"connection":   "local",
		"gather_facts": false,
		"tasks":        []map[string]any{debug},
	}}
	b, err := yaml.Marshal(play)
	require.NoError(t, err)
	return b
}

// renderState runs the render playbook with the given extra vars.
func renderState(t *testing.T, playbook, extraVars string) (string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "ansible-playbook",
		"-i", "localhost,",
		"-e", extraVars,
		playbook,
	)
	cmd.Env = append(os.Environ(), "ANSIBLE_NOCOLOR=1")
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func TestLoopsPlayStateExpressionRendersThroughTheTemplar(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("ansible-playbook"); err != nil {
		t.Skip("ansible-core is not installed on this machine; the structural test TestLoopsPlayStateExpressionIsBuiltOutsideTheModuleArgs still runs")
	}

	playbook := filepath.Join(t.TempDir(), "render.yml")
	require.NoError(t, os.WriteFile(playbook, stateRenderPlay(t, linuxStateTask(t, loopsPlays(t))), 0o644))

	result := func(name string, changed bool) map[string]any {
		return map[string]any{"item": map[string]any{"name": name}, "changed": changed}
	}
	for _, tc := range []struct {
		name    string
		enabled bool
		changed bool
		named   bool
		want    string
	}{
		{"disabled", false, true, true, "stopped"},
		{"enabled and changed", true, true, false, "restarted"},
		{"enabled and named by nova_loop_restart", true, false, true, "restarted"},
		{"enabled, unchanged and not restarted", true, false, false, "started"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			restart := []string{}
			if tc.named {
				restart = append(restart, "loop-a")
			}
			extra, err := json.Marshal(map[string]any{
				"item":         map[string]any{"name": "loop-a", "enabled": tc.enabled},
				"loop_restart": restart,
				"loop_units":   map[string]any{"results": []any{result("loop-a", tc.changed), result("loop-b", true)}},
				"loop_timers":  map[string]any{"results": []any{}},
			})
			require.NoError(t, err)
			out, err := renderState(t, playbook, string(extra))
			require.NoErrorf(t, err, "ansible refused the Linux state task:\n%s", out)
			assert.Contains(t, out, `"msg": "`+tc.want+`"`,
				"the templar yielded no %s for %s:\n%s", tc.want, tc.name, out)
		})
	}
}

// timerTask is the task that writes a loop's timer.
func timerTask(t *testing.T) map[string]any {
	t.Helper()
	for _, play := range loopsPlays(t) {
		for _, task := range flattenTasks(t, tasksOf(t, play)) {
			if _, ok := task["ansible.builtin.template"].(map[string]any); ok &&
				asText(task["name"]) == "one timer per record that runs every n seconds (linux)" {
				return task
			}
		}
	}
	require.FailNow(t, "no task writes the Linux timers")
	return nil
}

// A kept-alive row renders no timer and an `every` row renders exactly one: the
// timer task's loop, templated by ansible-core over both kinds of row.
func TestLoopsPlayTimersOnlyForEveryRows(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("ansible-playbook"); err != nil {
		t.Skip("ansible-core is not installed on this machine")
	}
	loop := asText(timerTask(t)["loop"])
	require.NotEmpty(t, loop)
	play := []map[string]any{{
		"hosts": "localhost", "connection": "local", "gather_facts": false,
		"tasks": []map[string]any{{"ansible.builtin.debug": map[string]any{
			"msg": "TIMERS={{ (" + strings.Trim(strings.TrimSpace(loop), "{} ") + ") | map(attribute='name') | join(',') }}"}}},
	}}
	b, err := yaml.Marshal(play)
	require.NoError(t, err)
	playbook := filepath.Join(t.TempDir(), "timers.yml")
	require.NoError(t, os.WriteFile(playbook, b, 0o644))
	extra, err := json.Marshal(map[string]any{"loop_records": []any{
		map[string]any{"name": "kept", "keepalive": true, "every": 0},
		map[string]any{"name": "periodic", "keepalive": false, "every": 60},
	}})
	require.NoError(t, err)
	out, err := renderState(t, playbook, string(extra))
	require.NoErrorf(t, err, "ansible refused the timer loop:\n%s", out)
	assert.Contains(t, out, `TIMERS=periodic"`, out)
	assert.NotContains(t, out, "kept", out)
}

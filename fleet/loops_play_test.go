package fleet

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// loops_play_test.go pins fleet/loops.yml's Linux unit state (docs/FLEET.md, "loops.yml").
// The state of ansible.builtin.systemd_service is built before the module call,
// because the ansible-core templar stops at a parenthesised ternary chained to a
// second `if ... else` ("expected token ')', got 'else'"): loop_unit_changed,
// loop_unit_wants_restart and loop_unit_state are task vars, and the module
// reads `state: "{{ loop_unit_state }}"`.

// loopsPlays reads fleet/loops.yml as the list of plays it holds.
func loopsPlays(t *testing.T) []map[string]any {
	t.Helper()
	b, err := os.ReadFile("loops.yml")
	require.NoError(t, err)
	var plays []map[string]any
	require.NoError(t, yaml.Unmarshal(b, &plays))
	return plays
}

// linuxStateTask is the task that starts or stops a Linux unit.
func linuxStateTask(t *testing.T, plays []map[string]any) map[string]any {
	t.Helper()
	for _, play := range plays {
		tasks := flattenTasks(t, tasksOf(t, play))
		for _, task := range tasks {
			if _, ok := task["ansible.builtin.systemd_service"].(map[string]any); !ok {
				continue
			}
			if asText(task["name"]) == "start the enabled, stop the disabled (linux)" {
				return task
			}
		}
	}
	require.FailNow(t, "no systemd_service task starts the Linux units")
	return nil
}

// nestedIf is the shape that broke: a parenthesised ternary closed by `)`
// immediately before a second `if`, or any expression chaining more than one
// `if` keyword.
var nestedIf = regexp.MustCompile(`\)\s*if `)

// holdsInlineConditional says expr is a state argument with an inline
// conditional nested in parentheses: it closes a `)` into a second `if`, or it
// carries more than one `if` keyword.
func holdsInlineConditional(expr string) bool {
	return nestedIf.MatchString(expr) || strings.Count(expr, " if ") > 1
}

// loopUnitState is the truth table the task's loop_unit_state expression writes
// down, the same four outcomes the module saw before: a disabled record is
// stopped; an enabled record whose unit file changed, or one nova_loop_restart
// names, is restarted; any other enabled record is started.
func loopUnitState(enabled, changed, restart bool) string {
	if !enabled {
		return "stopped"
	}
	if changed || restart {
		return "restarted"
	}
	return "started"
}

func TestLoopsPlayStateExpressionIsBuiltOutsideTheModuleArgs(t *testing.T) {
	t.Parallel()
	plays := loopsPlays(t)
	task := linuxStateTask(t, plays)

	// The module call reads one var: no inline conditional is built inside the
	// module args, where the templar stopped at the `else`.
	systemd, ok := task["ansible.builtin.systemd_service"].(map[string]any)
	require.True(t, ok, "the Linux state task is not a systemd_service task")
	state := asText(systemd["state"])
	assert.Equal(t, "{{ loop_unit_state }}", state,
		"the module's state is not the loop_unit_state var built before the call")
	assert.False(t, holdsInlineConditional(state),
		"the module's state holds an inline conditional nested in parentheses: %q", state)

	// The branches are short named task vars.
	changed := taskVar(t, task, "loop_unit_changed")
	assert.Contains(t, changed, "selectattr('changed')",
		"loop_unit_changed is not the selectattr('changed') chain")
	assert.Contains(t, changed, "loop_timers.results | default([])",
		"loop_unit_changed does not fold the timers' results in")

	wants := taskVar(t, task, "loop_unit_wants_restart")
	assert.Contains(t, wants, "loop_unit_changed",
		"loop_unit_wants_restart does not read loop_unit_changed")
	assert.Contains(t, wants, "item.name in loop_restart",
		"loop_unit_wants_restart does not read nova_loop_restart")

	stateExpr := taskVar(t, task, "loop_unit_state")
	assert.False(t, nestedIf.MatchString(stateExpr),
		"loop_unit_state closes a `)` into a second `if`: %q", stateExpr)
	assert.Contains(t, stateExpr, "'stopped' if not item.enabled",
		"loop_unit_state does not stop a disabled record")
	assert.Contains(t, stateExpr, "'restarted'")
	assert.Contains(t, stateExpr, "'started'")

	// The four outcomes, disabled / enabled changed / enabled named / enabled
	// unchanged, are the same as before the split.
	for _, tc := range []struct {
		name                      string
		enabled, changed, restart bool
		want                      string
	}{
		{"disabled", false, false, false, "stopped"},
		{"enabled and changed", true, true, false, "restarted"},
		{"enabled and named by nova_loop_restart", true, false, true, "restarted"},
		{"enabled, unchanged and not restarted", true, false, false, "started"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, loopUnitState(tc.enabled, tc.changed, tc.restart))
		})
	}
}

// The play and its templates name no tool this repository no longer ships.
func TestFleetFilesNameNoRetiredTool(t *testing.T) {
	t.Parallel()
	for _, f := range []string{"loops.yml", "templates/nova-loop.plist.j2", "templates/nova-loop.service.j2",
		"templates/nova-loop.timer.j2", "group_vars/all.yml", "nova-tools-install.yml"} {
		b, err := os.ReadFile(f)
		require.NoError(t, err)
		for _, word := range []string{"nova-swarm", "nova-sprint", "nova-update", "disk-guard", "nova-card"} {
			assert.NotContains(t, string(b), word, "%s", f)
		}
	}
}

// The install defaults: the six tools, and retiring off until asked.
func TestInstallRoleDefaults(t *testing.T) {
	t.Parallel()
	b, err := os.ReadFile("roles/nova-tools-install/defaults/main.yml")
	require.NoError(t, err)
	var d map[string]any
	require.NoError(t, yaml.Unmarshal(b, &d))
	g, err := os.ReadFile("group_vars/all.yml")
	require.NoError(t, err)
	var gv map[string]any
	require.NoError(t, yaml.Unmarshal(g, &gv))
	assert.Equal(t, []any{"nova-bus", "nova-cairn", "nova-config", "nova-sandbox", "nova-secrets", "nova-reserve"}, gv["nova_tools_names"])
	assert.Equal(t, false, d["nova_tools_remove_enabled"])
	names, _ := gv["nova_tools_names"].([]any)
	for _, r := range d["nova_tools_remove"].([]any) {
		assert.NotContains(t, names, r)
	}
}

// The default retire list is the rows of retired-tools.txt that say "retired"
// or "rowan-tools", and no other row: another product's binaries are never in it.
func TestRetireListIsOnlyRetiredAndRowanTools(t *testing.T) {
	t.Parallel()
	b, err := os.ReadFile("roles/nova-tools-install/defaults/main.yml")
	require.NoError(t, err)
	var d map[string]any
	require.NoError(t, yaml.Unmarshal(b, &d))
	var want []any
	txt, err := os.ReadFile("retired-tools.txt")
	require.NoError(t, err)
	for _, line := range strings.Split(string(txt), "\n") {
		f := strings.Split(line, "\t")
		if len(f) < 3 || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(f[2], "retired") || f[2] == "rowan-tools" {
			want = append(want, f[0])
		}
	}
	assert.ElementsMatch(t, want, d["nova_tools_remove"])
	for _, other := range []string{"nova-sprint", "nova-card", "nova-work", "nova-swarm", "nova-hook", "nova-friend"} {
		assert.NotContains(t, d["nova_tools_remove"], other)
	}
}

// tasksOf is a play's top-level tasks.
func tasksOf(t *testing.T, play map[string]any) []map[string]any {
	t.Helper()
	return mapsOf(t, play["tasks"])
}

// flattenTasks is tasks in document order, each block's tasks inlined.
func flattenTasks(t *testing.T, tasks []map[string]any) []map[string]any {
	t.Helper()
	out := []map[string]any{}
	for _, task := range tasks {
		out = append(out, task)
		if block := mapsOf(t, task["block"]); len(block) > 0 {
			out = append(out, flattenTasks(t, block)...)
		}
	}
	return out
}

// mapsOf is the []map[string]any a YAML task list decodes to.
func mapsOf(t *testing.T, v any) []map[string]any {
	t.Helper()
	l, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]map[string]any, 0, len(l))
	for _, e := range l {
		m, ok := e.(map[string]any)
		require.True(t, ok, "a task is not a mapping: %T", e)
		out = append(out, m)
	}
	return out
}

func asText(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

// taskVar is a task's own vars key, set on the task itself (not a play var).
func taskVar(t *testing.T, task map[string]any, key string) string {
	t.Helper()
	vars, _ := task["vars"].(map[string]any)
	if v, ok := vars[key]; ok {
		return asText(v)
	}
	require.FailNow(t, "no task var "+key)
	return ""
}

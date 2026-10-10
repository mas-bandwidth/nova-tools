package fleet

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// tools_test.go pins the friend step of fleet/tools.yml (docs/FLEET.md, the
// tools play; fleet/seat-friend.yml): the adopt reinstalls the friend daemons
// the friends table places on a host, the ones launchd holds, and removes the
// unit of a friend who no longer lives there. A friend who moved hosts leaves
// her old unit installed and not loaded — live lists it stale with
// why="not loaded" (cmd/nova-sprint/adopt_live.go, liveAgent.Stale) — so the
// step separates the stale friends launchd holds (reinstalled with
// seat-friend.yml) from the stale friends it does not (their unit removed with
// `nova-friend uninstall --as <friend>`, never reinstalled on the host she
// left).

// friendPlay is one friend step of tools.yml: the play's name, the fact that
// names the stale friends the host runs (selectattr('loaded')), the fact that
// names the stale friends it does not (rejectattr('loaded')), and the fact that
// names the seat hold.
type friendPlay struct {
	play     string
	ran      string // the friends the host runs: reinstalled
	gone     string // the friends who no longer live here: their units removed
	holdFact string
}

func TestFriendStepRemovesAMovedFriendsUnit(t *testing.T) {
	t.Parallel()
	plays := readTools(t)
	for _, fp := range []friendPlay{
		{play: "the seat adopts the build", ran: "seat_friends", gone: "seat_friends_gone", holdFact: "nova_seat_hold"},
		{play: "the friends of the fleet hosts reinstalled onto the installed build", ran: "ff_stale", gone: "ff_gone", holdFact: "nova_seat_hold"},
	} {
		t.Run(fp.play, func(t *testing.T) {
			t.Parallel()
			play := playNamed(t, plays, fp.play)
			tasks := flattenTasks(t, tasksOf(t, play))

			// The stale friends are split by what launchd holds: the ones it
			// holds are reinstalled, the ones it does not are the units of a
			// friend who no longer lives here and are removed.
			ran := varOf(t, play, tasks, fp.ran)
			assert.Contains(t, ran, "selectattr('stale')", "%s is not the stale friends", fp.ran)
			assert.Contains(t, ran, "selectattr('role', 'equalto', 'friend')", "%s is not the friend daemons", fp.ran)
			assert.Contains(t, ran, "selectattr('loaded')", "%s is not the friends launchd holds, whom the host runs", fp.ran)
			assert.Contains(t, ran, fp.holdFact, "%s does not keep the held labels out", fp.ran)

			gone := varOf(t, play, tasks, fp.gone)
			assert.Contains(t, gone, "selectattr('stale')", "%s is not the stale friends", fp.gone)
			assert.Contains(t, gone, "rejectattr('loaded')", "%s is not the friends launchd does not hold, who no longer live here", fp.gone)

			// The unit of a moved friend is removed with nova-friend
			// uninstall, keyed on the friend live named.
			removed := taskMatching(t, tasks, func(tool string, argv []string, loop string) bool {
				return tool == "uninstall" && strings.Contains(loop, fp.gone)
			})
			require.NotNil(t, removed, "%s: no uninstall of %s: a moved friend's unit stays", fp.play, fp.gone)
			command := strings.Join(argvOf(removed), " ")
			assert.Contains(t, command, "nova-friend", "the unit is removed with nova-friend, not by hand")
			assert.Contains(t, command, "uninstall", "the unit is removed, not reloaded")
			assert.Contains(t, command, "--as", "uninstall names the friend")
			assert.Contains(t, command, "item.friend", "uninstall names the friend live reported")

			// A friend the host runs is reinstalled, never removed.
			reinstall := taskMatching(t, tasks, func(task string, argv []string, loop string) bool {
				return task == "include_tasks" && strings.Contains(loop, fp.ran)
			})
			require.NotNil(t, reinstall, "%s: no reinstall of %s: a stale daemon the host runs is left stale", fp.play, fp.ran)
		})
	}
}

// readTools reads fleet/tools.yml as the list of plays it holds.
func readTools(t *testing.T) []map[string]any {
	t.Helper()
	b, err := os.ReadFile("tools.yml")
	require.NoError(t, err)
	var plays []map[string]any
	require.NoError(t, yaml.Unmarshal(b, &plays))
	return plays
}

// playNamed is the play whose name is exactly name.
func playNamed(t *testing.T, plays []map[string]any, name string) map[string]any {
	t.Helper()
	for _, p := range plays {
		if p["name"] == name {
			return p
		}
	}
	require.FailNow(t, "no play named "+name)
	return nil
}

// tasksOf is a play's top-level tasks.
func tasksOf(t *testing.T, play map[string]any) []map[string]any {
	t.Helper()
	return mapsOf(t, play["tasks"])
}

// flattenTasks is tasks in document order, each block's tasks inlined, so a
// step nested in a block is found where it stands.
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

// varOf is the value a play gives key: its play vars, else a set_fact task.
func varOf(t *testing.T, play map[string]any, tasks []map[string]any, key string) string {
	t.Helper()
	if vars, ok := play["vars"].(map[string]any); ok {
		if v, ok := vars[key]; ok {
			return asText(v)
		}
	}
	for _, task := range tasks {
		sf, _ := task["ansible.builtin.set_fact"].(map[string]any)
		if sf == nil {
			continue
		}
		if v, ok := sf[key]; ok {
			return asText(v)
		}
	}
	require.FailNow(t, "no play var or set_fact of "+key)
	return ""
}

// taskMatching is the first task where match says which tool it runs, its argv
// and its loop expression. tool is the command's last word (uninstall,
// install), or include_tasks for the include.
func taskMatching(t *testing.T, tasks []map[string]any, match func(tool string, argv []string, loop string) bool) map[string]any {
	t.Helper()
	for _, task := range tasks {
		tool, argv := "", []string{}
		if cmd, ok := task["ansible.builtin.command"].(map[string]any); ok {
			argv = argvOf(map[string]any{"ansible.builtin.command": cmd})
			joined := strings.Join(argv, " ")
			switch {
			case strings.Contains(joined, "uninstall"):
				tool = "uninstall"
			case strings.Contains(joined, "install"):
				tool = "install"
			}
		}
		if _, ok := task["ansible.builtin.include_tasks"]; ok {
			tool = "include_tasks"
		}
		if tool == "" {
			continue
		}
		loop := asText(task["loop"])
		if match(tool, argv, loop) {
			return task
		}
	}
	return nil
}

// argvOf is a command task's argv, the list of words it runs.
func argvOf(task map[string]any) []string {
	cmd, _ := task["ansible.builtin.command"].(map[string]any)
	if cmd == nil {
		return nil
	}
	return stringList(cmd["argv"])
}

func stringList(v any) []string {
	switch t := v.(type) {
	case []string:
		return t
	case []any:
		out := make([]string, 0, len(t))
		for _, e := range t {
			out = append(out, asText(e))
		}
		return out
	case string:
		return []string{t}
	default:
		return nil
	}
}

func asText(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

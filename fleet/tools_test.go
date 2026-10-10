package fleet

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// tools_test.go pins the tools play's rule that every tool in a machine's bin
// directory is the published release's bytes, installed from it and never
// compiled on the machine (docs/SPEC-RELEASE.md, install skip; dogfood
// 2026-10-10: nova-friend on mini-m5 reported go1.27.1 while every other
// v1.2.5 tool reported go1.26.6, a local build beside the release's assets).
// The play is read as YAML; the comparison itself is ansible's, and it is
// measured by the sha256 of the files (a build prints the release's version
// and holds other bytes).

// toolsPlays reads fleet/tools.yml.
func toolsPlays(t *testing.T) []map[string]any {
	t.Helper()
	b, err := os.ReadFile("tools.yml")
	require.NoError(t, err)
	var plays []map[string]any
	require.NoError(t, yaml.Unmarshal(b, &plays))
	return plays
}

// playIndex is the position of the play whose name starts with prefix, else -1.
func playIndex(plays []map[string]any, prefix string) int {
	for i, p := range plays {
		if n, _ := p["name"].(string); strings.HasPrefix(n, prefix) {
			return i
		}
	}
	return -1
}

// walkTasks calls f on every task of a task list, descending blocks, rescues and always.
func walkTasks(tasks any, f func(task map[string]any)) {
	list, _ := tasks.([]any)
	for _, e := range list {
		task, ok := e.(map[string]any)
		if !ok {
			continue
		}
		f(task)
		for _, k := range []string{"block", "rescue", "always"} {
			walkTasks(task[k], f)
		}
	}
}

// TestToolsPlayNeverCompilesATool: no task of the play runs the Go toolchain.
// A tool is built once, by `nova-update release build` on the machine running
// the play, and every machine installs that build (the header of tools.yml).
func TestToolsPlayNeverCompilesATool(t *testing.T) {
	for _, p := range toolsPlays(t) {
		walkTasks(p["tasks"], func(task map[string]any) {
			for _, mod := range []string{"ansible.builtin.command", "ansible.builtin.shell", "ansible.builtin.raw", "command"} {
				v, ok := task[mod]
				if !ok {
					continue
				}
				var argv []string
				switch m := v.(type) {
				case string:
					argv = strings.Fields(m)
				case map[string]any:
					switch a := m["argv"].(type) {
					case []any:
						for _, x := range a {
							argv = append(argv, strings.TrimSpace(toString(x)))
						}
					case string:
						argv = strings.Fields(a)
					}
					if c, ok := m["cmd"].(string); ok {
						argv = strings.Fields(c)
					}
				}
				for _, w := range argv {
					assert.NotContains(t, []string{"go", "gofmt"}, w, "task %q runs the Go toolchain; tools are installed from the release, never built on a machine", task["name"])
				}
			}
		})
	}
}

func toString(x any) string {
	s, _ := x.(string)
	return s
}

// TestToolsPlayChecksEveryInstalledToolAgainstTheRelease: after the install and
// the friends' reinstall, a play on every host lists the bin directory with the
// sha256 of each file, compares it with the release's SHA256SUMS, and refuses
// the host whose tool differs, naming the step.
func TestToolsPlayChecksEveryInstalledToolAgainstTheRelease(t *testing.T) {
	plays := toolsPlays(t)
	id := playIndex(plays, "every tool in the bin directory holds the release's bytes")
	require.GreaterOrEqual(t, id, 0, "tools.yml has no play that checks the installed tools' bytes against the release")
	play := plays[id]

	assert.Equal(t, "all", play["hosts"], "a local build can be on any machine, the coordinator's included")
	assert.Contains(t, play["tags"], "seat", "it runs wherever the friends' reinstall does, which assumes the build is installed")

	// It runs after everything that installs or reinstalls a tool.
	install := playIndex(plays, "the build in ~/.local/bin on every machine")
	seat := playIndex(plays, "the seat adopts the build")
	friends := playIndex(plays, "the friends of the fleet hosts reinstalled")
	require.True(t, install >= 0 && seat >= 0 && friends >= 0, "the install, seat and friends plays are in tools.yml")
	assert.Greater(t, id, install)
	assert.Greater(t, id, seat)
	assert.Greater(t, id, friends)

	var listing, refusal map[string]any
	var tasks int
	walkTasks(play["tasks"], func(task map[string]any) {
		tasks++
		if f, ok := task["ansible.builtin.find"].(map[string]any); ok {
			listing = f
		}
		if a, ok := task["ansible.builtin.assert"].(map[string]any); ok {
			if msg, _ := a["fail_msg"].(string); strings.Contains(msg, "step=identity") {
				refusal = a
			}
		}
	})
	require.NotNil(t, listing, "the play lists the bin directory")
	assert.Equal(t, "{{ nova_bin_dir }}", listing["paths"])
	assert.Equal(t, true, listing["get_checksum"])
	assert.Equal(t, "sha256", listing["checksum_algorithm"], "find's default is sha1, and SHA256SUMS holds sha256")

	require.NotNil(t, refusal, "the play refuses a host whose tool differs, naming step=identity")
	assert.Contains(t, refusal["fail_msg"], "TOOLS REFUSED step=identity")
	assert.Contains(t, refusal["that"], "id_differ | length == 0", "the refusal is on the tools that differ")
	assert.Contains(t, refusal["that"], "ansible_check_mode", "--check names what would be replaced and refuses nothing")

	// The release's side is the build's own SHA256SUMS for the host's platform.
	vars, _ := play["vars"].(map[string]any)
	assert.Contains(t, toString(vars["id_built"]), "nova_release_out")
	assert.Contains(t, toString(vars["id_built"]), "nova_platform")
	assert.Contains(t, toString(vars["id_sums"]), "SHA256SUMS")
	assert.Contains(t, toString(vars["id_want"]), "lookup('ansible.builtin.file', id_sums)")
	assert.GreaterOrEqual(t, tasks, 4)
}

package fleet

import (
	"fmt"
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

			// The removal runs only on a real adopt, never under --check, which
			// reports it instead: the loop is the gone list guarded by
			// `not ansible_check_mode`, and the --check report is its twin.
			assert.True(t, removesOnlyOutsideCheck(asText(removed["loop"]), fp.gone),
				"%s: the removal loop %q is not %s guarded by `if not ansible_check_mode else []`", fp.play, asText(removed["loop"]), fp.gone)
			var reported bool
			for _, task := range tasks {
				if strings.Contains(asText(task["loop"]), fp.gone) && task["when"] == "ansible_check_mode" {
					reported = true
				}
			}
			assert.True(t, reported, "%s: no --check report (WOULD-REMOVE) of %s", fp.play, fp.gone)

			// A friend the host runs is reinstalled, never removed.
			reinstall := taskMatching(t, tasks, func(task string, argv []string, loop string) bool {
				return task == "include_tasks" && strings.Contains(loop, fp.ran)
			})
			require.NotNil(t, reinstall, "%s: no reinstall of %s: a stale daemon the host runs is left stale", fp.play, fp.ran)
		})
	}
}

// removesOnlyOutsideCheck says loop is the list gone, emptied under --check:
// `{{ gone if not ansible_check_mode else [] }}`. The inverse (removing only
// under --check) and a bare `{{ gone }}` (removing under --check too) are not.
func removesOnlyOutsideCheck(loop, gone string) bool {
	return strings.Join(strings.Fields(loop), " ") == "{{ "+gone+" if not ansible_check_mode else [] }}"
}

func TestRemovalGuardIsTheRightWayRound(t *testing.T) {
	t.Parallel()
	assert.True(t, removesOnlyOutsideCheck("{{ ff_gone if not ansible_check_mode else [] }}", "ff_gone"))
	assert.False(t, removesOnlyOutsideCheck("{{ ff_gone if ansible_check_mode else [] }}", "ff_gone"), "removes only under --check")
	assert.False(t, removesOnlyOutsideCheck("{{ ff_gone }}", "ff_gone"), "removes under --check too")
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

// readGroupVars reads fleet/group_vars/all.yml as its key/value mapping.
func readGroupVars(t *testing.T) map[string]any {
	t.Helper()
	b, err := os.ReadFile("group_vars/all.yml")
	require.NoError(t, err)
	var vars map[string]any
	require.NoError(t, yaml.Unmarshal(b, &vars))
	return vars
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

// TestSeatWindowWaitsOnlyForTheAgentsItStopped pins the first half of the
// adopt-window-and-dry-run-fixes rule (docs/SPEC-SPRINT.md, "Adopting a build";
// fleet/tools.yml, the seat play): the window's live wait holds until the
// agents the window itself stopped (seat_stopped_labels) no longer run, never
// until every nova process on the host is gone.
func TestSeatWindowWaitsOnlyForTheAgentsItStopped(t *testing.T) {
	t.Parallel()
	play := playNamed(t, readTools(t), "the seat adopts the build")
	tasks := flattenTasks(t, tasksOf(t, play))

	wait := taskNamed(t, tasks, "window: the agents the window stopped run no longer (through live)")
	assert.Contains(t, asText(wait["until"]),
		"selectattr('label', 'in', seat_stopped_labels) | selectattr('holds')",
		"the window's wait reads the agents it stopped, not every process on the host")
	assert.NotContains(t, asText(wait["until"]), ".processes",
		"the window's wait reads every nova process on the host, not only the agents it stopped")

	refused := taskNamed(t, tasks, "window: nothing the window stopped runs")
	runs := taskVar(t, refused, "seat_runs")
	assert.Contains(t, runs, "selectattr('label', 'in', seat_stopped_labels)",
		"the refusal names the agents the window stopped")
	assert.NotContains(t, runs, ".processes",
		"the refusal reads every nova process on the host, not only the agents it stopped")
}

// TestSeatWindowDryRunNamesEachStep pins the second half: under --check each
// step of the window says what it would do (stop, migrate, load, install,
// restart), instead of one would for the whole window.
func TestSeatWindowDryRunNamesEachStep(t *testing.T) {
	t.Parallel()
	play := playNamed(t, readTools(t), "the seat adopts the build")
	tasks := flattenTasks(t, tasksOf(t, play))

	for _, step := range []struct{ name, want string }{
		{"window: what --check would stop", "WOULD-STOP"},
		{"window: what --check would migrate", "WOULD-MIGRATE"},
		{"window: what --check would load", "WOULD-LOAD"},
		{"window: what --check would install", "WOULD-INSTALL"},
	} {
		t.Run(step.name, func(t *testing.T) {
			t.Parallel()
			task := taskNamed(t, tasks, step.name)
			assert.Contains(t, debugMsg(task), step.want, "the --check dry run names the %s step", step.want)
			assert.Equal(t, "seat_window and ansible_check_mode", asText(task["when"]),
				"%s reports only under --check", step.name)
		})
	}

	restart := taskNamed(t, tasks, "server: each agent to start again and why")
	assert.Contains(t, debugMsg(restart), "WOULD-RESTART", "the --check dry run names the restart step")
}

// debugMsg is a debug task's msg.
func debugMsg(task map[string]any) string {
	d, _ := task["ansible.builtin.debug"].(map[string]any)
	return asText(d["msg"])
}

// taskNamed is the first task whose name is exactly name.
func readSeatFriend(t *testing.T) []map[string]any {
	content, err := os.ReadFile("seat-friend.yml")
	require.NoError(t, err)
	var tasks []map[string]any
	require.NoError(t, yaml.Unmarshal(content, &tasks))
	return tasks
}

func findTaskNamed(t *testing.T, tasks []map[string]any, name string) map[string]any {
	for _, task := range tasks {
		if task["name"] == name {
			return task
		}
	}
	t.Fatalf("task %q not found", name)
	return nil
}

func taskNamed(t *testing.T, tasks []map[string]any, name string) map[string]any {
	t.Helper()
	for _, task := range tasks {
		if task["name"] == name {
			return task
		}
	}
	require.FailNow(t, "no task named "+name)
	return nil
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

// tools_test.go also pins the identity rule of fleet/tools.yml
// (docs/SPEC-RELEASE.md, install skip): every tool in a machine's bin
// directory is the published release's bytes, installed from it and never
// compiled on the machine (dogfood 2026-10-10: one machine's nova-friend
// reported go1.27.1 while every other v1.2.5 tool reported go1.26.6, a local
// build beside the release's assets). The play is read as YAML; the comparison
// itself is ansible's, measured by the sha256 of the files, because a build
// prints the release's version and holds other bytes.

// playIndex is the position of the play whose name starts with prefix, else -1.
func playIndex(plays []map[string]any, prefix string) int {
	for i, p := range plays {
		if n, _ := p["name"].(string); strings.HasPrefix(n, prefix) {
			return i
		}
	}
	return -1
}

// walkTasks calls f on every task of a task list, descending blocks, rescues
// and always, so a command nested anywhere is found where it stands.
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
	t.Parallel()
	for _, p := range readTools(t) {
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
							argv = append(argv, strings.TrimSpace(asText(x)))
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

// TestFriendHostInstallsAdoptedBuild: the friend host step of the friends play
// installs the adopted build named in the config: it runs nova-friend install
// from {{ nova_bin_dir }}/nova-friend.
func TestFriendHostInstallsAdoptedBuild(t *testing.T) {
	t.Parallel()
	plays := readTools(t)
	play := playNamed(t, plays, "the friends of the fleet hosts reinstalled onto the installed build")
	tasks := flattenTasks(t, tasksOf(t, play))

	// Find the include_tasks that references seat-friend.yml for the friends reinstall.
	found := false
	for _, task := range tasks {
		if include, ok := task["ansible.builtin.include_tasks"].(string); ok {
			if include == "seat-friend.yml" {
				found = true
				break
			}
		}
	}
	require.True(t, found, "the friend host step does not include seat-friend.yml")

	// Read seat-friend.yml and assert the install command uses {{ nova_bin_dir }}/nova-friend.
	seat := readSeatFriend(t)
	installTask := findTaskNamed(t, seat, "friends: {{ seat_friend.friend }} is reinstalled with the flags its plist records")
	cmd, ok := installTask["ansible.builtin.command"].(map[string]any)
	require.True(t, ok, "seat-friend.yml reinstall task has no ansible.builtin.command")
	argv, ok := cmd["argv"].(string)
	require.True(t, ok, "seat-friend.yml reinstall task has no argv")
	assert.Contains(t, argv, "nova_bin_dir", "seat-friend.yml install does not reference nova_bin_dir")
	assert.Contains(t, argv, "nova-friend", "seat-friend.yml install does not reference nova-friend")
}

// TestToolsPlayChecksEveryInstalledToolAgainstTheRelease: after the install and
// the friends' reinstall, a play on every host lists the bin directory with the
// sha256 of each file, compares it with the release's SHA256SUMS, and refuses
// the host whose tool differs, naming the step.
func TestToolsPlayChecksEveryInstalledToolAgainstTheRelease(t *testing.T) {
	t.Parallel()
	plays := readTools(t)
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
	assert.Contains(t, asText(vars["id_built"]), "nova_release_out")
	assert.Contains(t, asText(vars["id_built"]), "nova_platform")
	assert.Contains(t, asText(vars["id_sums"]), "SHA256SUMS")
	assert.Contains(t, asText(vars["id_want"]), "lookup('ansible.builtin.file', id_sums)")
	assert.GreaterOrEqual(t, tasks, 4)
}

// taskContaining is the first task whose name contains name (a play names its
// steps with a variable word, e.g. a friend's name, in the middle).
func taskContaining(t *testing.T, tasks []map[string]any, name string) map[string]any {
	t.Helper()
	for _, task := range tasks {
		if strings.Contains(asText(task["name"]), name) {
			return task
		}
	}
	require.FailNow(t, "no task named like "+name)
	return nil
}

// runsInstalled is the check the plays encode for one running daemon: it must
// be loaded and not stale, where the manifest's stale means the running process
// is not the installed binary (docs/SPEC-RELEASE.md, "Adopting a build"). A
// process on an older binary is stale and fails; the installed one passes.
func runsInstalled(loaded, stale bool) bool { return loaded && !stale }

// TestRestartSelectsEveryReplacedDaemon pins the restart selection: the set is
// every loaded member, reader and friend whose binary the install replaced (the
// manifest's stale, role agent for members and readers, friend for the friend
// daemons), built from seat_replaced and the base's stale-with-tool set. Bug 13
// was exactly a member outside the old selection, so a fix that adds no restart
// path does not reach it.
func TestRestartSelectsEveryReplacedDaemon(t *testing.T) {
	t.Parallel()
	seat := playNamed(t, readTools(t), "the seat adopts the build")
	replaced := varOf(t, seat, flattenTasks(t, tasksOf(t, seat)), "seat_replaced")
	restart := varOf(t, seat, flattenTasks(t, tasksOf(t, seat)), "seat_restart")

	assert.Contains(t, replaced, "'agent'", "the replaced set names the manifest's role for members and readers (agent)")
	assert.Contains(t, replaced, "'friend'", "the replaced set names the friend daemons")
	assert.Contains(t, replaced, "stale")
	assert.NotContains(t, replaced, "'member'", "the manifest names no role member: live emits agent")
	assert.NotContains(t, replaced, "'reader'", "the manifest names no role reader: live emits agent")
	assert.NotContains(t, replaced, "seat_restart")
	assert.Contains(t, restart, "seat_replaced", "the restart set is built from the replaced set")
	assert.Contains(t, restart, "selectattr('tool', 'defined')", "the base's stale-with-tool restart is kept beside the replaced set")
	assert.Equal(t, 3, strings.Count(restart, "else []"),
		"each of the three seat_restart terms carries the else [] fallback, so a closed window yields [], never undefined")
}

// TestInstallPlayRestartsReplacedMembersAndReaders pins the restart in the
// every-machine install play (not the coordinator's seat play): after `release
// install` replaces a tool, the play restarts every member and reader whose
// binary it replaced and fails a host whose running member still reports an
// older version. Bug 13's six members were fleet hosts this play installed with
// no restart at all.
func TestInstallPlayRestartsReplacedMembersAndReaders(t *testing.T) {
	t.Parallel()
	install := playNamed(t, readTools(t), "the build in ~/.local/bin on every machine")
	task := taskContaining(t, flattenTasks(t, tasksOf(t, install)), "each member and reader whose binary the install replaced")
	raw := fmt.Sprint(task)

	assert.Contains(t, raw, "selectattr('role', 'equalto', 'agent')", "the restart names the manifest's member and reader role (agent)")
	assert.Contains(t, raw, "tools_agents | length > 0", "the play fails the host when the manifest lists no member or reader")
	assert.Contains(t, raw, "selectattr('stale')", "the restart names the agents whose binary was replaced")
	assert.Contains(t, raw, "bootout", "the restart drains through launchd bootout")
	assert.Contains(t, raw, "bootstrap", "the restart loads through launchd bootstrap")
	assert.Contains(t, raw, "systemd_service", "the restart drains through systemd on linux")
	assert.Contains(t, raw, "item.target", "the linux restart's scope comes from each agent's target, never one fleet-wide scope")
	assert.NotContains(t, raw, "nova_systemd_scope", "the linux restart runs in one fleet-wide scope, never the agent's own")
	assert.Contains(t, raw, "restarted", "the restart is a drain-restart, never a kill")
	assert.Contains(t, raw, "rejectattr('stale')", "the check reads the running process, not the binary on disk")
	assert.NotContains(t, raw, "item.binary", "the check never runs the binary file's version verb")
	assert.Contains(t, raw, "tools_manifest.rc == 0", "the manifest read is asserted before the restart, never skipped when it fails")

	when := fmt.Sprint(task["when"])
	assert.Contains(t, when, "not in (groups['coordinator']", "the restart runs on the fleet hosts, not the coordinator")
	assert.Contains(t, when, "RELEASE INSTALLED .*tools=[1-9]", "the restart runs only after the install replaced a tool")

	assert.False(t, runsInstalled(true, true), "a running member on an older binary passed the check")
	assert.True(t, runsInstalled(true, false), "a running member on the installed binary failed the check")
}

// TestVersionCheckFailsAnOlderRunningVersion pins the post-install check: the
// running process's own state (the manifest's stale, set when the process is not
// the installed binary) is what the play reads, never the binary file's `version`
// verb. A member left on an older binary is stale and fails; the installed one is
// not stale and passes.
func TestVersionCheckFailsAnOlderRunningVersion(t *testing.T) {
	t.Parallel()
	seat := playNamed(t, readTools(t), "the seat adopts the build")
	check := taskContaining(t, flattenTasks(t, tasksOf(t, seat)), "manifest after, until each one started")

	assert.Contains(t, fmt.Sprint(check), "rejectattr('stale')", "the check reads the running process, not the binary on disk")
	assert.NotContains(t, fmt.Sprint(flattenTasks(t, tasksOf(t, seat))), "{{ item.binary }} version", "no seat check runs the binary file's version verb")

	assert.False(t, runsInstalled(true, true), "an older running version passed the check")
	assert.True(t, runsInstalled(true, false), "the installed version failed the check")
}

// TestFriendVersionCheckedAfterReinstall pins the friend half: the reinstall
// reads the reinstalled daemon's running process (the manifest's stale in the
// beat check), never the binary file's `version` verb. Bug 13 left friends
// version-unread; their only post-install check was a beat, which now requires
// not stale.
func TestFriendVersionCheckedAfterReinstall(t *testing.T) {
	t.Parallel()
	b, err := os.ReadFile("seat-friend.yml")
	require.NoError(t, err)
	var tasks []map[string]any
	require.NoError(t, yaml.Unmarshal(b, &tasks))
	check := taskContaining(t, tasks, "is there exactly once, not stale")
	assert.Contains(t, fmt.Sprint(check), "stale", "the friend's running process is checked, not the binary file")
	raw := string(b)
	assert.NotContains(t, raw, "version", "the friend's check never runs the binary file's version verb")
	assert.Less(t, strings.Index(raw, "is reinstalled with the flags its plist records"),
		strings.Index(raw, "is there exactly once, not stale"), "the running-process check follows the reinstall")
}

// TestAdoptionEndsWithOneTable pins the closing table: the last play prints one
// row per host with the host, the version running, the members and readers up,
// the harness present and the disk free. The play ended in per-step debug
// receipts before this. The disk free names the capture group, so the column is
// the Available number, never the whole df row.
func TestAdoptionEndsWithOneTable(t *testing.T) {
	t.Parallel()
	plays := readTools(t)
	last := plays[len(plays)-1]
	assert.Equal(t, "the adoption table", fmt.Sprint(last["name"]), "the adoption does not end with its table")
	table := fmt.Sprint(tasksOf(t, last))
	assert.Contains(t, table, "ADOPT TABLE")
	for _, col := range []string{"host=", "version=", "members_readers_up=", "harness=", "disk_free_kb="} {
		assert.Contains(t, table, col, "the table has no %s column", col)
	}
	assert.Contains(t, table, "regex_findall", "the disk_free_kb column does not findall, so an empty df crashes the row")
	assert.Contains(t, table, "default('?')", "the disk_free_kb column has no default, so an empty or skipped df crashes the row")
}

// TestRefusalCausesAreNotTruncated pins the card's rule that a refusal's cause
// is printed whole: neither play cuts a cause at a fixed width.
func TestRefusalCausesAreNotTruncated(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"tools.yml", "seat-friend.yml"} {
		b, err := os.ReadFile(name)
		require.NoError(t, err)
		assert.NotContains(t, string(b), "truncate(", "%s cuts a refusal's cause", name)
	}
}

// TestSeatFriendAcceptanceRunFailsOnNonZeroExit pins the acceptance step in
// seat-friend.yml: each friend placed on the host has a command task that runs
// a probe card, registered with failed_when: false, followed by a separate
// ansible.builtin.assert whose fail_msg names the friend, the exit code and the
// log line when the lane exits non-zero.
func TestSeatFriendAcceptanceRunFailsOnNonZeroExit(t *testing.T) {
	t.Parallel()
	b, err := os.ReadFile("seat-friend.yml")
	require.NoError(t, err)
	var tasks []map[string]any
	require.NoError(t, yaml.Unmarshal(b, &tasks))

	// Find the acceptance task (one that runs a probe card under the lane wall)
	acceptanceTask := taskContaining(t, tasks, "acceptance")
	require.NotNil(t, acceptanceTask, "seat-friend.yml has no acceptance task for each friend")

	// The acceptance task should be a command that registers its result
	cmd, ok := acceptanceTask["ansible.builtin.command"].(map[string]any)
	require.True(t, ok, "acceptance task is not a command")

	// The argv appends the plist's recorded flags WITHOUT the install verb:
	// seat_friend.install is the whole install command including its verb (the
	// reinstall step runs [<bin>/nova-friend] + seat_friend.install), so the
	// acceptance argv appends seat_friend.install[1:] and its element 2 is a flag
	// (--as), never the install positional the tool refuses at exit 2
	// (docs/SPEC-FRIEND.md, the acceptance run: the flags the daemon runs with).
	argv, ok := cmd["argv"].(string)
	require.True(t, ok, "acceptance task has no argv")
	const tail = "'acceptance'] + "
	i := strings.Index(argv, tail)
	require.GreaterOrEqual(t, i, 0, "acceptance argv does not append the recorded flags after the acceptance verb: %q", argv)
	rest := argv[i+len(tail):]
	assert.True(t, strings.HasPrefix(rest, "seat_friend.install[1:]"),
		"acceptance argv appends the recorded flags without dropping the install verb, so element 2 is the install positional: %q", rest)

	// Should register the result to read rc later
	assert.Contains(t, acceptanceTask, "register", "acceptance task does not register its result")

	// The acceptance task should have failed_when: false so it doesn't immediately fail
	assert.Contains(t, acceptanceTask, "failed_when", "acceptance task has no failed_when")
	assert.Equal(t, false, acceptanceTask["failed_when"], "acceptance task should not fail immediately on non-zero exit")

	// There should be a separate assert task that checks the acceptance
	assertTask := taskContaining(t, tasks, "acceptance: the lane ran")
	require.NotNil(t, assertTask, "seat-friend.yml has no separate assert for the acceptance run")

	// The assert should have a fail_msg that names the friend, exit code and log line
	assertMsg, ok := assertTask["ansible.builtin.assert"].(map[string]any)
	require.True(t, ok, "acceptance assert is not an assert task")
	failMsg, ok := assertMsg["fail_msg"].(string)
	require.True(t, ok, "acceptance assert has no fail_msg")
	assert.Contains(t, failMsg, "seat_friend.friend", "acceptance fail_msg does not name the friend")
	assert.Contains(t, failMsg, "seat_acceptance.rc", "acceptance fail_msg does not name the exit code")
	assert.Contains(t, failMsg, "stdout", "acceptance fail_msg does not name the output line")
	assert.Contains(t, failMsg, "stderr", "acceptance fail_msg does not name the output line")
}

// TestLiveManifestFlagsArePerPlatform pins the one live manifest command per
// platform (docs/SPEC-RELEASE.md, "Adopting a build"): on Darwin live reads the
// launchd agents directory and the system-domain daemons directory; on Linux it
// reads the systemd units directly, so neither directory is named. The flags are
// defined once (fleet/group_vars/all.yml, nova_seat_live_flags) and every play
// that reads live uses them.
func TestLiveManifestFlagsArePerPlatform(t *testing.T) {
	t.Parallel()
	all := readGroupVars(t)
	darwin := stringList(all["nova_seat_live_flags_darwin"])
	linux := stringList(all["nova_seat_live_flags_linux"])
	require.NotEmpty(t, darwin, "no Darwin live flags")
	require.NotEmpty(t, linux, "no Linux live flags")
	assert.Contains(t, darwin, "--agents-dir", "the Darwin live read does not name the launchd agents directory")
	assert.Contains(t, darwin, "--daemons-dir", "the Darwin live read does not name the system-domain daemons directory")
	assert.NotContains(t, linux, "--agents-dir", "the Linux live read names an agents directory; live reads systemd units")
	assert.NotContains(t, linux, "--daemons-dir", "the Linux live read names a daemons directory; live reads systemd units")

	plays := readTools(t)
	for _, read := range []struct{ play, key string }{
		{"the build in ~/.local/bin on every machine", "tools_live"},
		{"the friends of the fleet hosts reinstalled onto the installed build", "seat_live"},
		{"the adoption table", "seat_live"},
	} {
		t.Run(read.play, func(t *testing.T) {
			t.Parallel()
			play := playNamed(t, plays, read.play)
			assert.Contains(t, varOf(t, play, flattenTasks(t, tasksOf(t, play)), read.key), "nova_seat_live_flags",
				"%s does not use the one per-platform live flags", read.play)
		})
	}
}

// TestInstallPlayLoopsStepAcceptsAlreadyOnTheBuild pins the loops step's success
// case (docs/SPEC-RELEASE.md, "Adopting a build"): when the manifest lists a
// member or reader and none is stale, the step prints LOOPS OK and restarts
// nothing; it refuses only when no member or reader is listed at all, naming the
// manager.
func TestInstallPlayLoopsStepAcceptsAlreadyOnTheBuild(t *testing.T) {
	t.Parallel()
	install := playNamed(t, readTools(t), "the build in ~/.local/bin on every machine")
	task := taskContaining(t, flattenTasks(t, tasksOf(t, install)), "each member and reader whose binary the install replaced")
	raw := fmt.Sprint(task)

	assert.Contains(t, raw, "LOOPS OK host=", "the loops step has no success line for a host already on the build")
	assert.Contains(t, raw, "already on the installed build", "the LOOPS OK line does not say the host is already on the build")
	assert.Contains(t, raw, "tools_agents | length > 0", "the refusal is not on a manifest with no member or reader")
	assert.Contains(t, raw, "manager", "the no-agent refusal does not name the manager")

	// The old assert (tools_replaced | length > 0) is gone: a host already on
	// the build has an empty replaced set and must not refuse.
	for _, t2 := range flattenTasks(t, tasksOf(t, install)) {
		if a, ok := t2["ansible.builtin.assert"].(map[string]any); ok {
			assert.NotEqual(t, "tools_replaced | length > 0", asText(a["that"]),
				"an assert still refuses a host whose members are already on the build")
		}
	}
}

// TestLoopsRestartFollowsEachTarget pins the restart (docs/SPEC-RELEASE.md,
// "Adopting a build"): a launchd agent in the system domain is booted out and
// bootstrapped with become; a systemd agent's scope comes from its target's
// prefix (user/ or system/), never one fleet-wide scope.
func TestLoopsRestartFollowsEachTarget(t *testing.T) {
	t.Parallel()
	install := playNamed(t, readTools(t), "the build in ~/.local/bin on every machine")
	tasks := flattenTasks(t, tasksOf(t, install))

	for _, name := range []string{
		"loops: boot out each replaced member and reader (launchd)",
		"loops: bootstrap each from its plist",
	} {
		task := taskNamed(t, tasks, name)
		assert.Contains(t, fmt.Sprint(task["become"]), "system/",
			"%s does not run with become keyed on the target's system domain", name)
	}

	restart := taskNamed(t, tasks, "loops: restart each replaced member and reader (systemd)")
	raw := fmt.Sprint(restart)
	assert.Contains(t, raw, "item.target", "the linux restart does not read the scope from each agent's target")
	assert.NotContains(t, raw, "nova_systemd_scope", "the linux restart uses one fleet-wide scope, not the agent's own")
	assert.Contains(t, raw, "become", "the linux restart never becomes root for a system unit")
}

// TestAdoptionDiskFreeReadsThroughCheck pins the check run's disk read
// (docs/SPEC-RELEASE.md, "Adopting a build"): "table: the disk free under the
// home" is a read and runs in check mode, and the row's regex_findall with a
// default prints ? for an empty or skipped df instead of crashing.
func TestAdoptionDiskFreeReadsThroughCheck(t *testing.T) {
	t.Parallel()
	table := playNamed(t, readTools(t), "the adoption table")
	tasks := flattenTasks(t, tasksOf(t, table))

	df := taskNamed(t, tasks, "table: the disk free under the home")
	assert.Equal(t, false, df["check_mode"], "the disk free read is skipped in check mode, so the row crashes")

	row := taskNamed(t, tasks, "table: one row")
	msg := debugMsg(row)
	assert.Contains(t, msg, "regex_findall", "the disk_free_kb column does not findall, so an empty df crashes")
	assert.Contains(t, msg, "| first", "the disk_free_kb column does not take the first match")
	assert.Contains(t, msg, "default('?')", "the disk_free_kb column has no default for an empty or skipped df")
}

// TestInstallPlayStartsWithOneCanaryBatch pins the staged rollout
// (docs/SPEC-RELEASE.md, "Adopting a build"): the install play runs the canary
// alone first and every other fleet host after it, and it stops on the canary's
// failure so the other hosts are never touched. nova_adopt_canary=none runs the
// play in one batch, as before. The canary and its wait bound are group vars.
func TestInstallPlayStartsWithOneCanaryBatch(t *testing.T) {
	t.Parallel()
	install := playNamed(t, readTools(t), "the build in ~/.local/bin on every machine")
	serial := asText(install["serial"])
	assert.Contains(t, serial, "[1,", "the install play's first batch is not one host (the canary)")
	assert.Contains(t, serial, "nova_adopt_canary", "the batch split is not keyed on nova_adopt_canary")
	assert.Contains(t, serial, "100%", "the second batch is not every other fleet host")
	assert.Contains(t, serial, "else '100%'", "nova_adopt_canary=none does not run the play in one batch")
	assert.Equal(t, 0, install["max_fail_percentage"], "the play does not stop on the canary's failure (max_fail_percentage is not 0)")

	all := readGroupVars(t)
	assert.Contains(t, asText(all["nova_adopt_canary"]), "group", "nova_adopt_canary has no default host from the fleet group")
	_, ok := all["nova_adopt_canary"]
	assert.True(t, ok, "nova_adopt_canary is not a group var")
	_, ok = all["nova_adopt_canary_wait"]
	assert.True(t, ok, "nova_adopt_canary_wait is not a group var")
}

// TestInstallPlayCanaryWaitsOnItsMemberLog pins the wait (docs/SPEC-RELEASE.md,
// "Adopting a build"): after the loops step the canary reads its own member's
// loop log for one "finish ... ok=true exit=0" line, bounded by
// nova_adopt_canary_wait and polled every 30 s, and prints CANARY OK with the
// host and the card.
func TestInstallPlayCanaryWaitsOnItsMemberLog(t *testing.T) {
	t.Parallel()
	install := playNamed(t, readTools(t), "the build in ~/.local/bin on every machine")
	tasks := flattenTasks(t, tasksOf(t, install))

	wait := taskContaining(t, tasks, "the member's log since the restart")
	raw := fmt.Sprint(wait)
	assert.Contains(t, raw, "finish .*ok=true exit=0", "the wait does not read the log for an ok finish")
	assert.Contains(t, raw, "exit=1", "the wait does not stop on a refused finish")
	assert.Contains(t, raw, "nova-loop-", "the wait does not read the member's own loop log")
	assert.Contains(t, raw, "nova_adopt_canary_wait", "the wait is not bounded by nova_adopt_canary_wait")
	assert.Equal(t, 30, wait["delay"], "the wait does not poll every 30 s")

	ok := taskNamed(t, tasks, "canary: the member finished ok")
	assert.Contains(t, debugMsg(ok), "CANARY OK host=", "the wait prints no CANARY OK host line")
	assert.Contains(t, debugMsg(ok), "card=", "the CANARY OK line names no card")
}

// TestInstallPlayCanaryRollsBackTheBuildFactVersion pins the rollback
// (docs/SPEC-RELEASE.md, "Adopting a build"): a canary that fails installs the
// version the build fact held before, from the same release directory, then
// restarts the loops; a host whose build fact held no version is not rolled
// back. The rescue is on the canary block, so only the canary is touched.
func TestInstallPlayCanaryRollsBackTheBuildFactVersion(t *testing.T) {
	t.Parallel()
	install := playNamed(t, readTools(t), "the build in ~/.local/bin on every machine")

	block := taskNamed(t, tasksOf(t, install), "canary: the first host's member finishes, or the fleet waits")
	_, hasRescue := block["rescue"]
	require.True(t, hasRescue, "the canary block has no rescue: a failed wait does not roll back")

	var rollback map[string]any
	var restarts bool
	walkTasks(block["rescue"], func(task map[string]any) {
		if cmd, ok := task["ansible.builtin.command"].(map[string]any); ok {
			argv := strings.Join(stringList(cmd["argv"]), " ")
			if strings.Contains(argv, "release install") && strings.Contains(argv, "--version") && strings.Contains(argv, "tools_was_version") {
				rollback = task
			}
		}
		if _, ok := task["ansible.builtin.systemd_service"].(map[string]any); ok {
			restarts = true
		}
		if _, ok := task["ansible.builtin.command"].(map[string]any); ok {
			if strings.Contains(fmt.Sprint(task["name"]), "boot out each replaced member and reader") {
				restarts = true
			}
		}
	})
	require.NotNil(t, rollback, "the canary's failure installs no build fact version: nothing is rolled back")
	assert.Contains(t, fmt.Sprint(rollback["when"]), "tools_was_version != ''", "a host with no build fact is rolled back anyway")
	assert.True(t, restarts, "the rollback does not restart the loops on either platform")

	var refusal bool
	walkTasks(block["rescue"], func(task map[string]any) {
		if f, ok := task["ansible.builtin.fail"].(map[string]any); ok {
			if msg, _ := f["msg"].(string); strings.Contains(msg, "ADOPT REFUSED step=canary") {
				refusal = true
			}
		}
	})
	assert.True(t, refusal, "the canary's failure ends with no ADOPT REFUSED step=canary line")
}

// TestInstallPlayCanaryNoneSkipsTheWait pins nova_adopt_canary=none: the wait
// runs only when a canary is named and only on that host, and none says the
// play installs every host in one batch (docs/SPEC-RELEASE.md, "Adopting a
// build").
func TestInstallPlayCanaryNoneSkipsTheWait(t *testing.T) {
	t.Parallel()
	install := playNamed(t, readTools(t), "the build in ~/.local/bin on every machine")
	block := taskNamed(t, tasksOf(t, install), "canary: the first host's member finishes, or the fleet waits")
	when := asText(block["when"])
	assert.Contains(t, when, "nova_adopt_canary != 'none'", "the wait runs with no canary named")
	assert.Contains(t, when, "inventory_hostname == nova_adopt_canary", "the wait does not run on the canary alone")

	var none, wouldWait bool
	walkTasks(install["tasks"], func(task map[string]any) {
		if debugMsg(task) == "" {
			return
		}
		if strings.Contains(debugMsg(task), "CANARY none") {
			none = true
		}
		if strings.Contains(debugMsg(task), "WOULD-WAIT") {
			wouldWait = true
		}
	})
	assert.True(t, none, "nova_adopt_canary=none does not say the play runs in one batch")
	assert.True(t, wouldWait, "--check does not name the canary and the wait it would do")
}

// TestInstallPlayCanaryWaitReadsOnlyLinesAfterTheRestart pins the Darwin half of
// the wait (docs/SPEC-RELEASE.md, "Adopting a build"): the loop log appends and
// its lines carry no timestamp, so the wait must read only the lines the
// restarted member wrote, by snapshotting the log's length at the moment the
// restart begins, never the whole file. On Darwin a stale `finish ... ok=true
// exit=0` line from an earlier run would otherwise pass the wait instantly.
func TestInstallPlayCanaryWaitReadsOnlyLinesAfterTheRestart(t *testing.T) {
	t.Parallel()
	install := playNamed(t, readTools(t), "the build in ~/.local/bin on every machine")
	tasks := flattenTasks(t, tasksOf(t, install))

	before := taskContaining(t, tasks, "the member's log length at the moment the restart begins")
	beforeRaw := fmt.Sprint(before)
	assert.Contains(t, beforeRaw, "wc", "the restart's moment does not snapshot the log's length")
	assert.Contains(t, beforeRaw, "nova-loop-", "the snapshot does not read the member's own loop log")
	assert.Contains(t, asText(before["when"]), "Darwin", "the snapshot is not the Darwin log, whose lines carry no timestamp")

	wait := taskContaining(t, tasks, "the member's log since the restart")
	waitRaw := fmt.Sprint(wait)
	assert.Contains(t, waitRaw, "tail", "the Darwin wait does not read only the lines after the snapshot")
	assert.Contains(t, waitRaw, "tools_canary_log_before", "the Darwin wait is not bounded by the log length at the restart")
}

// TestInstallPlayCanaryReportsTheElapsedWait pins the CANARY OK duration
// (docs/SPEC-RELEASE.md, "Adopting a build"): it names the time the wait
// actually took, from the moment the restart began to the finish it saw, never
// the configured bound nova_adopt_canary_wait.
func TestInstallPlayCanaryReportsTheElapsedWait(t *testing.T) {
	t.Parallel()
	install := playNamed(t, readTools(t), "the build in ~/.local/bin on every machine")
	tasks := flattenTasks(t, tasksOf(t, install))

	seen := taskContaining(t, tasks, "the moment the member's finish was seen")
	assert.Contains(t, fmt.Sprint(seen), "date", "the wait does not read the clock when the finish was seen")

	ok := taskNamed(t, tasks, "canary: the member finished ok")
	msg := debugMsg(ok)
	assert.Contains(t, msg, "tools_canary_at", "the CANARY OK line does not measure from the restart")
	assert.Contains(t, msg, "tools_canary_ok_at", "the CANARY OK line does not measure to the finish seen")
	assert.NotContains(t, msg, "nova_adopt_canary_wait", "the CANARY OK line prints the wait bound, not the elapsed wait")
}

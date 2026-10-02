package ci

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// The runtime half of the functional-image class rule (see
// functional_image_class_test.go and docs/SPEC-CI.md, `functional-image`): the
// container-runtime role that installs rootless podman on a runner, the README
// that says how a run is made, and the Containerfile's build steps. These read
// the files as data, in the unit tier: no ansible, no container.

type roleTask map[string]any

func roleTasks(t *testing.T, rel string) []roleTask {
	t.Helper()
	var tasks []roleTask
	text := readFile(t, filepath.Join(repoRoot(t), filepath.FromSlash(containerRuntimeRole+"/"+rel)))
	err := yaml.Unmarshal([]byte(text), &tasks)
	require.NoError(t, err, "%s/%s is not a list of tasks: %v", containerRuntimeRole, rel, err)
	return tasks
}

func (k roleTask) name() string { s, _ := k["name"].(string); return s }

// module returns the task's module argument map (or string) by fully
// qualified name.
func (k roleTask) module(fqcn string) (any, bool) { v, ok := k[fqcn]; return v, ok }

// args returns a module's argument map, whichever map type the decoder gave.
func (k roleTask) args(fqcn string) map[string]any {
	switch m := k[fqcn].(type) {
	case roleTask:
		return m
	case map[string]any:
		return m
	}
	return nil
}

func (k roleTask) text() string {
	b, _ := yaml.Marshal(map[string]any(k))
	return string(b)
}

// TestContainerRuntimeRefusesRootBeforeItsFirstChange: the role's default user
// is the connecting user, so a connection made as root (or become in
// ansible.cfg) names root. One assert refuses root and system uids, reading the
// account's uid from the getent fact and not from a variable an extra variable
// can override; it cannot be skipped, ignored or failed softly; and only reads
// (stat, getent, set_fact, assert) come before it: no become, no include or
// import of a file that changes something.
func TestContainerRuntimeRefusesRootBeforeItsFirstChange(t *testing.T) {
	t.Parallel()
	const uid = "ansible_facts['getent_passwd'][container_runtime_user][1] | int"
	wantThat := []string{
		uid + " != 0",
		uid + " >= container_runtime_min_uid | int",
		"container_runtime_uid | int == " + uid,
	}
	tasks := roleTasks(t, "tasks/main.yml")
	guard := -1
	for i, k := range tasks {
		if k.name() == "the runner user is an ordinary user, never root" {
			guard = i
			break
		}
	}
	require.GreaterOrEqual(t, guard, 0, "%s/tasks/main.yml has no task \"the runner user is an ordinary user, never root\"", containerRuntimeRole)
	g := tasks[guard]
	for key := range g {
		if key != "name" && key != "ansible.builtin.assert" {
			assert.Fail(t, fmt.Sprintf("%s/tasks/main.yml: the root guard has a %q key; it takes no when, ignore_errors, failed_when, become or loop", containerRuntimeRole, key))
		}
	}
	m := g.args("ansible.builtin.assert")
	var got []string
	if l, ok := m["that"].([]any); ok {
		for _, e := range l {
			got = append(got, fold(e.(string)))
		}
	}
	require.Equal(t, len(wantThat), len(got), "%s/tasks/main.yml: the root guard asserts %v, want exactly %v", containerRuntimeRole, got, wantThat)
	for i := range wantThat {
		assert.Equal(t, fold(wantThat[i]), got[i], "%s/tasks/main.yml: guard line %d is %q, want %q", containerRuntimeRole, i+1, got[i], fold(wantThat[i]))
	}
	allowed := []string{"ansible.builtin.stat", "ansible.builtin.assert", "ansible.builtin.getent", "ansible.builtin.set_fact"}
	for i, k := range tasks[:guard] {
		modules := 0
		for key := range k {
			switch key {
			case "name", "register", "when", "loop", "loop_control":
			default:
				ok := false
				for _, a := range allowed {
					ok = ok || key == a
				}
				if !ok {
					assert.Fail(t, fmt.Sprintf("%s/tasks/main.yml: task %d (%q) before the root guard has %q; only stat, getent, set_fact and assert, with no become, ignore_errors, block, include or import, may come before it", containerRuntimeRole, i+1, k.name(), key))
				}
				modules++
			}
		}
		assert.Equal(t, 1, modules, "%s/tasks/main.yml: task %d (%q) before the root guard is not one plain read", containerRuntimeRole, i+1, k.name())
	}
	defaults := readFile(t, filepath.Join(repoRoot(t), filepath.FromSlash(containerRuntimeRole+"/defaults/main.yml")))
	dm := regexp.MustCompile(`(?m)^container_runtime_min_uid:\s*(\d+)\s*$`).FindStringSubmatch(defaults)
	assert.True(t, dm != nil && dm[1] != "0", "%s/defaults/main.yml: container_runtime_min_uid is missing or 0; it is the floor for the runner's uid and root is never allowed", containerRuntimeRole)
}

// TestContainerRuntimeSubidsNeverReuseARange: a missing subordinate id row is
// allocated after the highest range in the file, never at the fixed start; an
// existing row that overlaps another user's is a failure; no row is found by a
// regex built from the user name.
func TestContainerRuntimeSubidsNeverReuseARange(t *testing.T) {
	t.Parallel()
	main := roleTasks(t, "tasks/main.yml")
	included := false
	for _, k := range main {
		if v, ok := k.module("ansible.builtin.include_tasks"); ok && v == "subid.yml" {
			included = true
		}
		if v, ok := k.module("ansible.builtin.lineinfile"); ok {
			assert.Fail(t, fmt.Sprintf("%s/tasks/main.yml: %q writes a line itself (%v); subordinate id rows are written by tasks/subid.yml, which checks every range first", containerRuntimeRole, k.name(), v))
		}
	}
	require.True(t, included, "%s/tasks/main.yml does not include subid.yml", containerRuntimeRole)
	text := readFile(t, filepath.Join(repoRoot(t), filepath.FromSlash(containerRuntimeRole+"/tasks/main.yml")))
	assert.True(t, strings.Contains(text, "- subuid") && strings.Contains(text, "- subgid"), "%s/tasks/main.yml: subid.yml is not included for both subuid and subgid", containerRuntimeRole)
	sub := roleTasks(t, "tasks/subid.yml")
	var writes, fits, overlaps, parsed int
	for _, k := range sub {
		body := k.text()
		if v, ok := k.module("ansible.builtin.lineinfile"); ok {
			writes++
			assert.False(t, strings.Contains(body, "container_runtime_subid_start") || !strings.Contains(body, "container_runtime_subid_new_start"), "%s/tasks/subid.yml: the row is written at the fixed start (%v); it must start at container_runtime_subid_new_start, after every other user's range", containerRuntimeRole, v)
			assert.Contains(t, body, "container_runtime_subid.mine | length == 0", "%s/tasks/subid.yml: the row is written when the user already has one", containerRuntimeRole)
			assert.NotContains(t, body, "regexp", "%s/tasks/subid.yml: a row is matched by a regexp; the user name is data, never a pattern", containerRuntimeRole)
		}
		if strings.Contains(body, "others_end") && strings.Contains(body, "container_runtime_subid_start") {
			fits++
		}
		if strings.Contains(body, "clash | length == 0") {
			overlaps++
		}
		if strings.Contains(body, "[^:]+:[0-9]+:[0-9]+") {
			parsed++
		}
	}
	assert.True(t, writes == 1 && fits == 1 && overlaps == 1 && parsed == 1, "%s/tasks/subid.yml: want one row write, one start after others_end, one overlap assert and one row-format assert; have %d, %d, %d, %d", containerRuntimeRole, writes, fits, overlaps, parsed)
	assert.Contains(t, readFile(t, filepath.Join(repoRoot(t), filepath.FromSlash(containerRuntimeRole+"/tasks/subid.yml"))), "container_runtime_uid | string", "%s/tasks/subid.yml: rows written by numeric uid are not read as the user's", containerRuntimeRole)
}

// The allocation expressions of tasks/subid.yml, pinned as text (whitespace
// folded). The unit tier has no ansible and no Jinja engine, so the logic cannot
// be evaluated here; it is held instead by the exact text that was evaluated,
// with ansible's own template engine, on constructed subuid files (another
// user's range at the fixed start, rows by numeric uid, a user with a row in one
// file only, comments, CRLF, prefix names, regexp characters, zero counts,
// ranges at the 2^32 edge): the expected row was never another user's range and
// never changed another user's row. A change to any of these expressions is a
// change to that logic: evaluate it again on such files, then change the pin.
var subidPins = map[string]string{
	"container_runtime_subid_lines":     `{{ ((container_runtime_subid_slurp.content | default('') | b64decode).splitlines() if container_runtime_subid_stat.stat.exists else []) | map('trim') | reject('equalto', '') | reject('match', '#') | list }}`,
	"container_runtime_subid":           `{%- set names = [container_runtime_user, container_runtime_uid | string] -%} {%- set rows = container_runtime_subid_lines | map('split', ':') | list -%} {%- set mine = rows | selectattr('0', 'in', names) | list -%} {%- set others = rows | rejectattr('0', 'in', names) | list -%} {%- set ns = namespace(end=0, clash=[]) -%} {%- for o in others -%} {%- set o_end = o[1] | int + o[2] | int -%} {%- if o_end > ns.end -%}{%- set ns.end = o_end -%}{%- endif -%} {%- for m in mine -%} {%- if o[2] | int > 0 and m[2] | int > 0 and o[1] | int < m[1] | int + m[2] | int and m[1] | int < o_end -%} {%- set ns.clash = ns.clash + [o | join(':')] -%} {%- endif -%} {%- endfor -%} {%- endfor -%} {{ {'mine': mine | map('join', ':') | list, 'longest': (mine | map(attribute=2) | map('int') | list | max) if mine | length > 0 else 0, 'others_end': ns.end, 'clash': ns.clash | unique | list} }}`,
	"container_runtime_subid_new_start": `{{ [container_runtime_subid_start | int, container_runtime_subid.others_end | int] | max }}`,
}

var subidAssertPins = map[string]string{
	"every row is name:start:count":                    `item is match('^[^:]+:[0-9]+:[0-9]+$')`,
	"the user's existing rows overlap no other user's": `container_runtime_subid.clash | length == 0`,
	"an existing subordinate id range is large enough": `container_runtime_subid.mine | length == 0 or container_runtime_subid.longest | int >= container_runtime_subid_count | int`,
	"a new range fits in the 32-bit id space":          `container_runtime_subid_new_start | int + container_runtime_subid_count | int < 4294967296`,
}

func fold(s string) string { return strings.Join(strings.Fields(s), " ") }

// TestContainerRuntimeSubidExpressionsArePinned: see subidPins. Turning the
// `max` of the range end into `min`, inverting the overlap test or reading the
// end from the first row only each changes a pinned expression and is red.
func TestContainerRuntimeSubidExpressionsArePinned(t *testing.T) {
	t.Parallel()
	facts := map[string]string{}
	asserts := map[string]string{}
	for _, k := range roleTasks(t, "tasks/subid.yml") {
		for name, v := range k.args("ansible.builtin.set_fact") {
			if s, ok := v.(string); ok {
				facts[name] = fold(s)
			}
		}
		if m := k.args("ansible.builtin.assert"); m != nil {
			if s, ok := m["that"].(string); ok {
				asserts[k.name()] = fold(s)
			}
		}
	}
	for name, want := range subidPins {
		got := facts[name]
		assert.Equal(t, fold(want), got, "%s/tasks/subid.yml: the expression of %s is not the one that was evaluated on constructed files.\n got: %s\nwant: %s\nevaluate the change with ansible's template engine on files where another user holds the fixed start and where ranges touch 2^32, then update subidPins", containerRuntimeRole, name, got, fold(want))
	}
	for name, want := range subidAssertPins {
		got := asserts[name]
		assert.Equal(t, fold(want), got, "%s/tasks/subid.yml: the assert %q is %q, want the pinned %q", containerRuntimeRole, name, got, fold(want))
	}
}

// roleKeywords are the task keys that are not modules. Every other key of a
// task must be a module written with its full name, `ansible.builtin.<name>`:
// the tests above read modules by that name, so a short-form `lineinfile:` or
// `copy:` would be a task they cannot see.
var roleKeywords = map[string]bool{
	"name": true, "register": true, "when": true, "loop": true, "loop_control": true,
	"become": true, "become_user": true, "environment": true, "changed_when": true,
	"failed_when": true, "check_mode": true,
}

// subidTasks is tasks/subid.yml in order: task name, module, and the `when` of
// the task ("" for none). A task added, removed, reordered or given a `when`
// is a change to the allocation and is red until it is evaluated and pinned.
var subidTasks = [][3]string{
	{"/etc/{{ container_runtime_subid_file }} exists", "stat", ""},
	{"the rows of /etc/{{ container_runtime_subid_file }}", "slurp", "container_runtime_subid_stat.stat.exists"},
	{"the rows, as name:start:count lines", "set_fact", ""},
	{"every row is name:start:count", "assert", ""},
	{"the user's rows against everyone else's", "set_fact", ""},
	{"the user's existing rows overlap no other user's", "assert", ""},
	{"an existing subordinate id range is large enough", "assert", ""},
	{"the start of the range a missing row gets", "set_fact", "container_runtime_subid.mine | length == 0"},
	{"a new range fits in the 32-bit id space", "assert", "container_runtime_subid.mine | length == 0"},
	{"a row for the user in /etc/{{ container_runtime_subid_file }}", "lineinfile", "container_runtime_subid.mine | length == 0"},
}

// TestContainerRuntimeTasksCannotHideAWeakening: the role's tasks are read in
// full. A short-form module name, `ignore_errors` anywhere, a `block`, `rescue`
// or `always`, a `failed_when`, `vars`, `check_mode` or `changed_when` in
// tasks/subid.yml, and any task or `when` of subid.yml other than the pinned
// list are red, so a task cannot be added, softened or switched off without
// this test changing.
func TestContainerRuntimeTasksCannotHideAWeakening(t *testing.T) {
	t.Parallel()
	for _, file := range []string{"tasks/main.yml", "tasks/subid.yml"} {
		for i, k := range roleTasks(t, file) {
			for key := range k {
				switch {
				case roleKeywords[key], strings.HasPrefix(key, "ansible.builtin."):
				case key == "ignore_errors" || key == "block" || key == "rescue" || key == "always":
					assert.Fail(t, fmt.Sprintf("%s/%s: task %d (%q) has %q; the role fails or it does not run, and no task hides others", containerRuntimeRole, file, i+1, k.name(), key))
				default:
					assert.Fail(t, fmt.Sprintf("%s/%s: task %d (%q) has the key %q, which is not a keyword or an ansible.builtin.<module>; a short-form module name is a task the class tests cannot read", containerRuntimeRole, file, i+1, k.name(), key))
				}
			}
			if file == "tasks/subid.yml" {
				for _, key := range []string{"failed_when", "changed_when", "check_mode", "vars"} {
					if _, ok := k[key]; ok {
						assert.Fail(t, fmt.Sprintf("%s/%s: task %d (%q) has %q; the allocation's tasks take no such key", containerRuntimeRole, file, i+1, k.name(), key))
					}
				}
			}
		}
	}
	got := roleTasks(t, "tasks/subid.yml")
	require.Equal(t, len(subidTasks), len(got), "%s/tasks/subid.yml has %d tasks, the pinned list has %d; evaluate the change on constructed files and update subidTasks", containerRuntimeRole, len(got), len(subidTasks))
	for i, k := range got {
		module := ""
		for key := range k {
			if m, ok := strings.CutPrefix(key, "ansible.builtin."); ok {
				module = m
			}
		}
		when := ""
		if w, ok := k["when"]; ok {
			when = fold(fmt.Sprint(w))
		}
		want := subidTasks[i]
		assert.True(t, k.name() == want[0] && module == want[1] && when == want[2], "%s/tasks/subid.yml: task %d is (%q, %s, when %q), the pinned task is (%q, %s, when %q)", containerRuntimeRole, i+1, k.name(), module, when, want[0], want[1], want[2])
	}
	// main.yml writes only what the role means to write: one drop-in directory
	// and one drop-in file, no other file, copy, template or line.
	for _, k := range roleTasks(t, "tasks/main.yml") {
		if v, ok := k["ansible.builtin.include_tasks"]; ok && v != "subid.yml" {
			assert.Fail(t, fmt.Sprintf("%s/tasks/main.yml: %q includes %v; the only file the role includes is subid.yml, which is pinned above", containerRuntimeRole, k.name(), v))
		}
	}
	counts := map[string]int{}
	for _, k := range roleTasks(t, "tasks/main.yml") {
		for key := range k {
			switch key {
			case "ansible.builtin.file", "ansible.builtin.copy", "ansible.builtin.template", "ansible.builtin.lineinfile", "ansible.builtin.blockinfile", "ansible.builtin.replace":
				counts[key]++
			}
		}
	}
	want := map[string]int{"ansible.builtin.file": 1, "ansible.builtin.copy": 1}
	for _, key := range []string{"ansible.builtin.file", "ansible.builtin.copy", "ansible.builtin.template", "ansible.builtin.lineinfile", "ansible.builtin.blockinfile", "ansible.builtin.replace"} {
		assert.Equal(t, want[key], counts[key], "%s/tasks/main.yml has %d %s tasks, want %d (the drop-in directory and the drop-in file only; subordinate id rows are written by subid.yml)", containerRuntimeRole, counts[key], key, want[key])
	}
}

// TestContainerRuntimeDropInHasItsDirectory: copy does not create parent
// directories, so a directory task precedes the delegation drop-in, and the
// drop-in is for the runner's manager (user@<uid>.service.d), not the template
// of every user's.
func TestContainerRuntimeDropInHasItsDirectory(t *testing.T) {
	t.Parallel()
	tasks := roleTasks(t, "tasks/main.yml")
	dirAt, copyAt := -1, -1
	var dirPath, dest string
	for i, k := range tasks {
		if m := k.args("ansible.builtin.file"); m != nil && m["state"] == "directory" {
			dirPath, _ = m["path"].(string)
			dirAt = i
		}
		if m := k.args("ansible.builtin.copy"); m != nil {
			if d, _ := m["dest"].(string); strings.Contains(d, "/etc/systemd/system/") {
				dest, copyAt = d, i
			}
		}
	}
	require.True(t, copyAt >= 0 && dirAt >= 0, "%s/tasks/main.yml: want a directory task and a copy of the drop-in; found directory at %d, copy at %d", containerRuntimeRole, dirAt+1, copyAt+1)
	assert.LessOrEqual(t, dirAt, copyAt, "%s/tasks/main.yml: the drop-in directory task (%d) comes after the copy (%d)", containerRuntimeRole, dirAt+1, copyAt+1)
	assert.Equal(t, dirPath, filepath.Dir(dest), "%s/tasks/main.yml: the drop-in is copied to %q but the directory task makes %q", containerRuntimeRole, dest, dirPath)
	assert.False(t, strings.Contains(dest, "user@.service.d") || !strings.Contains(dest, "user@{{ container_runtime_uid }}.service.d"), "%s/tasks/main.yml: the drop-in %q is not for the runner's manager only (user@<uid>.service.d)", containerRuntimeRole, dest)
	dm, cm := tasks[dirAt].args("ansible.builtin.file"), tasks[copyAt].args("ansible.builtin.copy")
	for _, c := range []struct {
		what string
		got  map[string]any
		mode string
	}{{"directory", dm, "0755"}, {"drop-in", cm, "0644"}} {
		assert.True(t, c.got["owner"] == "root" && c.got["group"] == "root" && c.got["mode"] == c.mode, "%s/tasks/main.yml: the %s is owner %v group %v mode %v; want root, root, %s (it configures every service of a user's manager, and an existing one with another owner is corrected)", containerRuntimeRole, c.what, c.got["owner"], c.got["group"], c.got["mode"], c.mode)
	}
	assert.True(t, strings.Contains(tasks[dirAt].text(), "container_runtime_controllers") && strings.Contains(tasks[copyAt].text(), "container_runtime_controllers"), "%s/tasks/main.yml: the directory and the drop-in are made under different conditions", containerRuntimeRole)
}

// probeTask returns the probe container's task, its argv and its script (the
// last argv element): what the probe runs, without the comments around it.
func probeTask(t *testing.T) (roleTask, []string, string) {
	t.Helper()
	for _, k := range roleTasks(t, "tasks/main.yml") {
		if !strings.HasPrefix(k.name(), "the probe container") {
			continue
		}
		var argv []string
		if l, ok := k.args("ansible.builtin.command")["argv"].([]any); ok {
			for _, e := range l {
				argv = append(argv, fmt.Sprint(e))
			}
		}
		require.NotEmpty(t, argv, "%s/tasks/main.yml: the probe task has no argv", containerRuntimeRole)
		return k, argv, argv[len(argv)-1]
	}
	require.Fail(t, fmt.Sprintf("%s/tasks/main.yml: no probe container task", containerRuntimeRole))
	return nil, nil, ""
}

// readmeRunCommand returns the README's run command: the indented block that
// starts `podman run --rm --name nova-functional-run`, joined on one line.
func readmeRunCommand(t *testing.T) string {
	t.Helper()
	readme := readFile(t, filepath.Join(repoRoot(t), filepath.FromSlash(functionalImageReadme)))
	lines := strings.Split(readme, "\n")
	for i, l := range lines {
		if !strings.HasPrefix(strings.TrimSpace(l), "podman run --rm --name nova-functional-run") {
			continue
		}
		var cmd []string
		for _, m := range lines[i:] {
			if strings.TrimSpace(m) == "" {
				break
			}
			cmd = append(cmd, strings.TrimSuffix(strings.TrimSpace(m), "\\"))
		}
		return fold(strings.Join(cmd, " "))
	}
	require.Fail(t, fmt.Sprintf("%s: no run command (`podman run --rm --name nova-functional-run ...`)", functionalImageReadme))
	return ""
}

// TestFunctionalImageReadmeRunCommandCarriesEveryFlag: the flags are read from
// the run command itself, not from the flags table below it, so deleting one
// from the command is red.
func TestFunctionalImageReadmeRunCommandCarriesEveryFlag(t *testing.T) {
	t.Parallel()
	cmd := readmeRunCommand(t)
	for _, want := range []string{
		"--network none", "--ipc private", "--pids-limit ", "--memory ", "--memory-swap ", "--cpus ",
		"--read-only", "--tmpfs /tmp:", "--timeout ", "--init", "--security-opt no-new-privileges", "--cap-drop all",
		"-v \"$PWD\":/src:ro", "-v nova-gomod:/gomodcache:ro",
	} {
		assert.Contains(t, cmd+" ", want, "%s: the run command lacks %q: %s", functionalImageReadme, want, cmd)
	}
}

// TestContainerRuntimeProbeValuesAreNumbers: cpu.max is computed as an integer
// however the variable is given ("2" from -e, 1.5), and the probe container
// itself (its argv and its script, not the comments around them) carries and
// reads back every flag of the README's run command that a limit or a
// restriction depends on.
func TestContainerRuntimeProbeValuesAreNumbers(t *testing.T) {
	t.Parallel()
	task, argv, script := probeTask(t)
	wantKeys := map[string]bool{"name": true, "become": true, "become_user": true, "environment": true, "ansible.builtin.command": true, "register": true, "changed_when": true, "when": true}
	for key := range task {
		assert.True(t, wantKeys[key], "%s/tasks/main.yml: the probe task has the key %q; it takes only %v (no failed_when, loop, ignore_errors or the like that could make a failed probe pass)", containerRuntimeRole, key, wantKeys)
	}
	w := fold(fmt.Sprint(task["when"]))
	assert.Equal(t, "not ansible_check_mode", w, "%s/tasks/main.yml: the probe runs when %q; it runs whenever the play is not in check mode, and always then", containerRuntimeRole, w)
	assert.Equal(t, false, task["changed_when"], "%s/tasks/main.yml: the probe's changed_when is %v, want false", containerRuntimeRole, task["changed_when"])
	joined := strings.Join(argv[:len(argv)-1], " ")
	line := regexp.MustCompile(`WANT_CPU=\{\{ (.*?) \}\} 100000`).FindStringSubmatch(joined)
	require.NotNil(t, line, "%s/tasks/main.yml: no WANT_CPU argument", containerRuntimeRole)
	assert.True(t, strings.Contains(line[1], "| float") && strings.Contains(line[1], "| int") && strings.Contains(line[1], "round"), "%s/tasks/main.yml: WANT_CPU is %q; a string times 100000 repeats the string and a fraction gives 150000.0, so it must be (cpus | float * 100000) | round | int", containerRuntimeRole, line[1])
	have := map[string]bool{}
	for _, a := range argv {
		have[a] = true
	}
	for _, want := range []string{"--security-opt", "no-new-privileges", "--cap-drop", "all", "--memory", "--memory-swap", "--pids-limit", "--cpus", "--read-only", "--network", "--ipc", "--timeout"} {
		assert.True(t, have[want], "%s/tasks/main.yml: the probe's argv lacks %q", containerRuntimeRole, want)
	}
	for _, want := range []string{"pids.max", "memory.max", "memory.swap.max", "cpu.max", "/sys/class/net", "/proc/self/mountinfo", "touch /etc/probe-rootfs", "/tmp/probe-tmp", "NoNewPrivs", "CapBnd"} {
		assert.Contains(t, script, want, "%s/tasks/main.yml: the probe's script never reads %q", containerRuntimeRole, want)
	}
	assert.NotContains(t, script, "touch /probe-rootfs", "%s/tasks/main.yml: the probe writes to / to prove the root filesystem read-only; / is refused to root with every capability dropped whatever the mount is, so the check always passes. Read the mount from /proc/self/mountinfo and write to /etc", containerRuntimeRole)
	for code := 11; code <= 19; code++ {
		assert.Contains(t, script, fmt.Sprintf("exit %d", code), "%s/tasks/main.yml: the probe's script has no `exit %d`; one of its checks was deleted", containerRuntimeRole, code)
	}
	cmd := readmeRunCommand(t)
	for _, want := range []string{"--security-opt no-new-privileges", "--cap-drop all", "--memory-swap "} {
		assert.Contains(t, cmd, want, "%s: the run command lacks %s, which the runtime probe proves", functionalImageReadme, want)
	}
}

// TestFunctionalImageReadmeKeepsTheBuildCachePerTrustDomain: the test code in a
// run writes the build cache, so the README's run command mounts a volume named
// for its trust domain, never one shared name, and states the rule and a bound.
func TestFunctionalImageReadmeKeepsTheBuildCachePerTrustDomain(t *testing.T) {
	t.Parallel()
	readme := readFile(t, filepath.Join(repoRoot(t), filepath.FromSlash(functionalImageReadme)))
	for _, m := range regexp.MustCompile(`-v (nova-gocache\S*):/gocache`).FindAllStringSubmatch(readme, -1) {
		assert.True(t, strings.HasPrefix(m[1], "nova-gocache-"), "%s: a run mounts %q, one cache name for every run; name it for its trust domain (nova-gocache-<domain>)", functionalImageReadme, m[1])
	}
	for _, want := range []string{"trust domain", "one writer at a time", "du -sm /gocache", "podman volume rm", "unreviewed"} {
		assert.Contains(t, readme, want, "%s: the build cache section does not say %q", functionalImageReadme, want)
	}
}

// TestFunctionalImageBuildLeavesNothingBehind: the Containerfile's build steps
// keep the image clean and the snapshot instant enforced.
func TestFunctionalImageBuildLeavesNothingBehind(t *testing.T) {
	t.Parallel()
	src := readFile(t, filepath.Join(repoRoot(t), filepath.FromSlash(functionalImageFile)))
	ins := containerInstructions(src)
	stage := 0
	frontend := map[int]bool{}
	runsApt := map[int]bool{}
	for _, in := range ins {
		switch {
		case strings.HasPrefix(in, "FROM "):
			stage++
		case strings.HasPrefix(in, "ENV ") && strings.Contains(in, "DEBIAN_FRONTEND"):
			assert.Fail(t, fmt.Sprintf("%s: %q puts DEBIAN_FRONTEND in the image's environment; use ARG DEBIAN_FRONTEND=noninteractive in each stage that runs apt", functionalImageFile, in))
		case strings.HasPrefix(in, "ARG DEBIAN_FRONTEND="):
			frontend[stage] = true
		case strings.HasPrefix(in, "RUN ") && strings.Contains(in, "apt-get"):
			runsApt[stage] = true
		}
		if strings.HasPrefix(in, "RUN ") && strings.Contains(in, "Verify-Peer=false") {
			first := in[:strings.Index(in, "Verify-Peer=false")]
			after := in[strings.LastIndex(in, "Verify-Peer=false"):]
			after = after[strings.Index(after, " "):]
			assert.Contains(t, first, "dpkg-query -W", "%s: the RUN that reads the snapshot without TLS checks does not record the installed versions before it", functionalImageFile)
			for _, want := range []string{"apt-get update", "comm -13", "apt-cache policy", "Candidate:", "exit 1"} {
				assert.Contains(t, after, want, "%s: after the call without TLS checks the RUN lacks %q; it must refresh the index with TLS on and fail when any package that call installed differs from the verified candidate", functionalImageFile, want)
			}
		}
		if strings.HasPrefix(in, "RUN ") && strings.Contains(in, "postgresql-16") {
			assert.Contains(t, in, "ssl-cert-snakeoil.key", "%s: the RUN that installs postgresql-16 leaves the generated snakeoil TLS private key in the layer", functionalImageFile)
		}
	}
	for st := range runsApt {
		assert.True(t, frontend[st], "%s: stage %d runs apt-get with no ARG DEBIAN_FRONTEND=noninteractive", functionalImageFile, st)
	}
}

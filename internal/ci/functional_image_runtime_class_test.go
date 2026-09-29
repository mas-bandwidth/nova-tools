package ci

import (
	"path/filepath"
	"regexp"
	"strings"
	"testing"

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
	if err := yaml.Unmarshal([]byte(text), &tasks); err != nil {
		t.Fatalf("%s/%s is not a list of tasks: %v", containerRuntimeRole, rel, err)
	}
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
// ansible.cfg) names root. An assert that root and system uids are refused
// comes before every task that changes the host or includes one that does.
func TestContainerRuntimeRefusesRootBeforeItsFirstChange(t *testing.T) {
	t.Parallel()
	tasks := roleTasks(t, "tasks/main.yml")
	guard, first := -1, -1
	for i, k := range tasks {
		if _, ok := k.module("ansible.builtin.assert"); ok && strings.Contains(k.text(), "container_runtime_uid") &&
			strings.Contains(k.text(), "!= 0") && strings.Contains(k.text(), "container_runtime_min_uid") {
			if guard < 0 {
				guard = i
			}
		}
		_, becomes := k["become"]
		_, includes := k.module("ansible.builtin.include_tasks")
		if (becomes || includes) && first < 0 {
			first = i
		}
	}
	if guard < 0 {
		t.Fatalf("%s/tasks/main.yml has no assert that container_runtime_uid is not 0 and is at least container_runtime_min_uid", containerRuntimeRole)
	}
	if first >= 0 && first < guard {
		t.Errorf("%s/tasks/main.yml: %q (task %d) changes or includes a change before the uid assert (task %d); the role must refuse root before its first change", containerRuntimeRole, tasks[first].name(), first+1, guard+1)
	}
	defaults := readFile(t, filepath.Join(repoRoot(t), filepath.FromSlash(containerRuntimeRole+"/defaults/main.yml")))
	m := regexp.MustCompile(`(?m)^container_runtime_min_uid:\s*(\d+)\s*$`).FindStringSubmatch(defaults)
	if m == nil || m[1] == "0" {
		t.Errorf("%s/defaults/main.yml: container_runtime_min_uid is missing or 0; it is the floor for the runner's uid and root is never allowed", containerRuntimeRole)
	}
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
			t.Errorf("%s/tasks/main.yml: %q writes a line itself (%v); subordinate id rows are written by tasks/subid.yml, which checks every range first", containerRuntimeRole, k.name(), v)
		}
	}
	if !included {
		t.Fatalf("%s/tasks/main.yml does not include subid.yml", containerRuntimeRole)
	}
	text := readFile(t, filepath.Join(repoRoot(t), filepath.FromSlash(containerRuntimeRole+"/tasks/main.yml")))
	if !strings.Contains(text, "- subuid") || !strings.Contains(text, "- subgid") {
		t.Errorf("%s/tasks/main.yml: subid.yml is not included for both subuid and subgid", containerRuntimeRole)
	}
	sub := roleTasks(t, "tasks/subid.yml")
	var writes, fits, overlaps, parsed int
	for _, k := range sub {
		body := k.text()
		if v, ok := k.module("ansible.builtin.lineinfile"); ok {
			writes++
			if strings.Contains(body, "container_runtime_subid_start") || !strings.Contains(body, "container_runtime_subid_new_start") {
				t.Errorf("%s/tasks/subid.yml: the row is written at the fixed start (%v); it must start at container_runtime_subid_new_start, after every other user's range", containerRuntimeRole, v)
			}
			if !strings.Contains(body, "container_runtime_subid.mine | length == 0") {
				t.Errorf("%s/tasks/subid.yml: the row is written when the user already has one", containerRuntimeRole)
			}
			if strings.Contains(body, "regexp") {
				t.Errorf("%s/tasks/subid.yml: a row is matched by a regexp; the user name is data, never a pattern", containerRuntimeRole)
			}
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
	if writes != 1 || fits != 1 || overlaps != 1 || parsed != 1 {
		t.Errorf("%s/tasks/subid.yml: want one row write, one start after others_end, one overlap assert and one row-format assert; have %d, %d, %d, %d", containerRuntimeRole, writes, fits, overlaps, parsed)
	}
	if !strings.Contains(readFile(t, filepath.Join(repoRoot(t), filepath.FromSlash(containerRuntimeRole+"/tasks/subid.yml"))), "container_runtime_uid | string") {
		t.Errorf("%s/tasks/subid.yml: rows written by numeric uid are not read as the user's", containerRuntimeRole)
	}
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
		if got := facts[name]; got != fold(want) {
			t.Errorf("%s/tasks/subid.yml: the expression of %s is not the one that was evaluated on constructed files.\n got: %s\nwant: %s\nevaluate the change with ansible's template engine on files where another user holds the fixed start and where ranges touch 2^32, then update subidPins", containerRuntimeRole, name, got, fold(want))
		}
	}
	for name, want := range subidAssertPins {
		if got := asserts[name]; got != fold(want) {
			t.Errorf("%s/tasks/subid.yml: the assert %q is %q, want the pinned %q", containerRuntimeRole, name, got, fold(want))
		}
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
	if copyAt < 0 || dirAt < 0 {
		t.Fatalf("%s/tasks/main.yml: want a directory task and a copy of the drop-in; found directory at %d, copy at %d", containerRuntimeRole, dirAt+1, copyAt+1)
	}
	if dirAt > copyAt {
		t.Errorf("%s/tasks/main.yml: the drop-in directory task (%d) comes after the copy (%d)", containerRuntimeRole, dirAt+1, copyAt+1)
	}
	if filepath.Dir(dest) != dirPath {
		t.Errorf("%s/tasks/main.yml: the drop-in is copied to %q but the directory task makes %q", containerRuntimeRole, dest, dirPath)
	}
	if strings.Contains(dest, "user@.service.d") || !strings.Contains(dest, "user@{{ container_runtime_uid }}.service.d") {
		t.Errorf("%s/tasks/main.yml: the drop-in %q is not for the runner's manager only (user@<uid>.service.d)", containerRuntimeRole, dest)
	}
	if !strings.Contains(tasks[dirAt].text(), "container_runtime_controllers") || !strings.Contains(tasks[copyAt].text(), "container_runtime_controllers") {
		t.Errorf("%s/tasks/main.yml: the directory and the drop-in are made under different conditions", containerRuntimeRole)
	}
}

// TestContainerRuntimeProbeValuesAreNumbers: cpu.max is computed as an integer
// however the variable is given ("2" from -e, 1.5), and the probe proves the
// no-new-privileges and capability flags the README's run command carries.
func TestContainerRuntimeProbeValuesAreNumbers(t *testing.T) {
	t.Parallel()
	tasks := readFile(t, filepath.Join(repoRoot(t), filepath.FromSlash(containerRuntimeRole+"/tasks/main.yml")))
	line := regexp.MustCompile(`WANT_CPU=\{\{ (.*?) \}\} 100000`).FindStringSubmatch(tasks)
	if line == nil {
		t.Fatalf("%s/tasks/main.yml: no WANT_CPU line", containerRuntimeRole)
	}
	if !strings.Contains(line[1], "| float") || !strings.Contains(line[1], "| int") || !strings.Contains(line[1], "round") {
		t.Errorf("%s/tasks/main.yml: WANT_CPU is %q; a string times 100000 repeats the string and a fraction gives 150000.0, so it must be (cpus | float * 100000) | round | int", containerRuntimeRole, line[1])
	}
	for _, want := range []string{"- --security-opt", "- no-new-privileges", "- --cap-drop", "- all", "NoNewPrivs", "CapBnd"} {
		if !strings.Contains(tasks, want) {
			t.Errorf("%s/tasks/main.yml: the probe lacks %q", containerRuntimeRole, want)
		}
	}
	readme := readFile(t, filepath.Join(repoRoot(t), filepath.FromSlash(functionalImageReadme)))
	for _, want := range []string{"--security-opt no-new-privileges", "--cap-drop all"} {
		if !strings.Contains(readme, want) {
			t.Errorf("%s: the run command lacks %s, which the runtime probe proves", functionalImageReadme, want)
		}
	}
}

// TestFunctionalImageReadmeKeepsTheBuildCachePerTrustDomain: the test code in a
// run writes the build cache, so the README's run command mounts a volume named
// for its trust domain, never one shared name, and states the rule and a bound.
func TestFunctionalImageReadmeKeepsTheBuildCachePerTrustDomain(t *testing.T) {
	t.Parallel()
	readme := readFile(t, filepath.Join(repoRoot(t), filepath.FromSlash(functionalImageReadme)))
	for _, m := range regexp.MustCompile(`-v (nova-gocache\S*):/gocache`).FindAllStringSubmatch(readme, -1) {
		if !strings.HasPrefix(m[1], "nova-gocache-") {
			t.Errorf("%s: a run mounts %q, one cache name for every run; name it for its trust domain (nova-gocache-<domain>)", functionalImageReadme, m[1])
		}
	}
	for _, want := range []string{"trust domain", "one writer at a time", "du -sm /gocache", "podman volume rm", "unreviewed"} {
		if !strings.Contains(readme, want) {
			t.Errorf("%s: the build cache section does not say %q", functionalImageReadme, want)
		}
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
			t.Errorf("%s: %q puts DEBIAN_FRONTEND in the image's environment; use ARG DEBIAN_FRONTEND=noninteractive in each stage that runs apt", functionalImageFile, in)
		case strings.HasPrefix(in, "ARG DEBIAN_FRONTEND="):
			frontend[stage] = true
		case strings.HasPrefix(in, "RUN ") && strings.Contains(in, "apt-get"):
			runsApt[stage] = true
		}
		if strings.HasPrefix(in, "RUN ") && strings.Contains(in, "Verify-Peer=false") {
			after := in[strings.LastIndex(in, "Verify-Peer=false"):]
			after = after[strings.Index(after, " "):]
			if !strings.Contains(after, "apt-get update") || !strings.Contains(after, "--reinstall") {
				t.Errorf("%s: the RUN that reads the snapshot without TLS checks does not then update and --reinstall with them on; the snapshot instant is not enforced for what the first call installed", functionalImageFile)
			}
		}
		if strings.HasPrefix(in, "RUN ") && strings.Contains(in, "postgresql-16") && !strings.Contains(in, "ssl-cert-snakeoil.key") {
			t.Errorf("%s: the RUN that installs postgresql-16 leaves the generated snakeoil TLS private key in the layer", functionalImageFile)
		}
	}
	for st := range runsApt {
		if !frontend[st] {
			t.Errorf("%s: stage %d runs apt-get with no ARG DEBIAN_FRONTEND=noninteractive", functionalImageFile, st)
		}
	}
}

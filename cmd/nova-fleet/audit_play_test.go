package main

import (
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// audit_play_test.go pins the audit play (cmd/nova-fleet/README.md, "audit") and the file it reads,
// runner-requirements.tsv: every requirement is one line of four fields with an arch the play understands and a play
// that installs it, and the play only reads: it changes nothing on a machine, under --check or not.

// requirementLines are the requirements of runner-requirements.tsv, split into their fields.
func requirementLines(t *testing.T) [][]string {
	t.Helper()
	b, err := os.ReadFile("runner-requirements.tsv")
	require.NoError(t, err)
	var out [][]string
	for _, l := range strings.Split(string(b), "\n") {
		if strings.TrimSpace(l) == "" || strings.HasPrefix(l, "#") {
			continue
		}
		out = append(out, strings.Split(l, "\t"))
	}
	return out
}

func TestRunnerRequirementsAreOneLineEach(t *testing.T) {
	t.Parallel()
	lines := requirementLines(t)
	require.Greater(t, len(lines), 40, "the file is the fleet's CI toolchain, not a stub")
	installs := []string{"runner-packages", "sdk-toolchain", "github-runners", "base", "none"}
	seen := map[string]bool{}
	for _, f := range lines {
		require.Len(t, f, 4, "%q: name, arch, install and check, tab-separated", strings.Join(f, "\t"))
		name, arch, install, check := f[0], f[1], f[2], f[3]
		assert.False(t, seen[name], "%s is listed twice", name)
		seen[name] = true
		var archs int
		for _, a := range strings.Split(arch, ",") {
			assert.Contains(t, []string{"any", "amd64", "arm64", "not-wsl", "vm-tier"}, a, "%s: arch %q", name, arch)
			if a != "not-wsl" && a != "vm-tier" {
				archs++
			}
		}
		assert.Positive(t, archs, "%s: arch %q names no architecture", name, arch)
		for _, p := range strings.Split(install, ",") {
			assert.Contains(t, installs, p, "%s: install %q is not a play", name, install)
			if p != "base" && p != "none" {
				_, err := os.Stat(p + ".yml")
				assert.NoError(t, err, "%s: install names a play nova-fleet does not have", name)
			}
		}
		assert.NotEmpty(t, strings.TrimSpace(check), "%s has no check", name)
	}
	for _, want := range []string{"tla2tools.jar", "GNU time", "MSan libc++", "clang-format-18", "s390x libraries", "s390x libstdc++", "VM tier kernel image", "VM tier kvm", "cppcheck 2.19.0", "go >= 1.26.6"} {
		assert.True(t, seen[want], "the requirement %s", want)
	}
}

// The jar's pin in the requirements is the one the play installs and the repository's model checks pin.
func TestRunnerRequirementsJarIsThePinnedJar(t *testing.T) {
	t.Parallel()
	pinned, err := os.ReadFile("../../tests/tla/tla2tools.sha256")
	require.NoError(t, err)
	sum, _, _ := strings.Cut(string(pinned), " ")
	for _, f := range requirementLines(t) {
		if f[0] == "tla2tools.jar" {
			assert.Contains(t, f[3], sum+"  /opt/tla/tla2tools.jar")
			return
		}
	}
	t.Fatal("no tla2tools.jar requirement")
}

// The play reads and reports: no module that writes, the one command it runs changes nothing and runs under --check,
// and a machine with a gap fails naming it.
func TestAuditPlayOnlyReads(t *testing.T) {
	t.Parallel()
	b, err := os.ReadFile("audit.yml")
	require.NoError(t, err)
	assert.NotEmpty(t, header(string(b), "purpose"))
	assert.True(t, slices.Contains(plays(tree), "audit"), "nova-fleet lists the play")
	var docs []map[string]any
	require.NoError(t, yaml.Unmarshal(b, &docs))
	require.Len(t, docs, 1)
	assert.Equal(t, "{{ ('bench:runner:&' ~ nova_join_host) if nova_join_host is defined else 'bench:runner' }}", docs[0]["hosts"])
	assert.NotContains(t, docs[0], "become", "the checks run as the runner login, never as root")
	readOnly := []string{"ansible.builtin.debug", "ansible.builtin.meta", "ansible.builtin.assert", "ansible.builtin.set_fact",
		"ansible.builtin.getent", "ansible.builtin.command", "ansible.builtin.fail"}
	failed, memlock, job := false, false, false
	for _, task := range mapsOf(t, docs[0]["tasks"]) {
		name := asText(task["name"])
		var module string
		for k := range task {
			if strings.HasPrefix(k, "ansible.") {
				module = k
			}
		}
		assert.Contains(t, readOnly, module, "task %q: the audit writes nothing", name)
		if module == "ansible.builtin.command" {
			assert.Equal(t, false, task["changed_when"], "task %q", name)
			assert.Equal(t, false, task["check_mode"], "task %q runs under --check too", name)
			text := taskText(t, task)
			if strings.Contains(text, "audit_job_probe") {
				job = true
				assert.Contains(t, text, "cat \"/proc/$pid/environ\"", "the job runs with a runner's own environment")
				assert.Contains(t, text, "$wd/.env", "and what the runner adds to every job")
				assert.Contains(t, text, "the unit has no User=", "never as root")
				assert.Contains(t, text, "sudo -n -u \"$login\"", "as that unit's login")
				assert.Contains(t, text, "env -i", "and nothing of the audit's own")
				continue
			}
			if strings.Contains(text, "Runner") {
				memlock = true
				assert.NotContains(t, text, "sudo", "memlock is read as the fleet login's own ssh session")
				assert.Contains(t, text, "run --dir \"$d\" --wait 60s", "a slot of its own, never the machine's, and bounded")
				assert.Contains(t, text, "LimitMEMLOCK")
				assert.Contains(t, text, "/proc/$p/limits")
				continue
			}
			assert.Contains(t, text, "sudo', '-n', '-u', audit_login", "the checks run as the runner login")
			assert.Contains(t, text, "timeout 120 bash -c", "each check is bounded")
		}
		if module == "ansible.builtin.fail" && strings.Contains(taskText(t, task), "FLEET-AUDIT FAILED host=") {
			failed = true
			assert.Contains(t, asText(task["when"]), "audit_gaps | length > 0")
		}
	}
	assert.True(t, failed, "a machine with a gap fails by name")
	assert.True(t, memlock, "the locked-memory limits of ssh, nova-reserve and the runners are read")
	assert.Contains(t, string(b), "item=memlock ssh={{ audit_memlock.ssh }}")
	assert.True(t, job, "the first steps of a CI job are run")
	assert.Contains(t, string(b), "item=job-{{ item.split(' ')[0] }}")
	assert.Contains(t, string(b), "FLEET-AUDIT NOT-APPLICABLE host={{ inventory_hostname }} item=job reason=no active github-runner unit")
	for _, step := range []string{"step rustup", "step go", "step cmake", "step cache-dirs"} {
		assert.Contains(t, string(b), step)
	}
	assert.Contains(t, string(b), "FLEET-AUDIT MISSING host={{ inventory_hostname }} item={{ item.name }} install={{ item.install }} check={{ item.check }}")
	assert.Contains(t, string(b), "FLEET-AUDIT NOT-APPLICABLE host=")
}

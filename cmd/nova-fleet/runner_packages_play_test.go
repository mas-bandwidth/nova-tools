package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// runner_packages_play_test.go pins the runner-packages play (cmd/nova-fleet/README.md, "runner-packages"): the packages a CI
// runner's tests need, from nova_runner_packages, installed as packages and nothing more. The tests start their own
// PostgreSQL (initdb, pg_ctl), so the role starts no server, runs no `brew services` and enables no unit; and a package
// already on the machine is adopted as it is, so every install task works only on what the presence check found
// missing (mini-m5's postgresql@18 is adopted with changed=0).

const runnerPackagesRole = "roles/runner-packages"

// runnerPackagesTasks is every task of the role, from each of its task files, blocks inlined, keyed by file.
func runnerPackagesTasks(t *testing.T) map[string][]map[string]any {
	t.Helper()
	files, err := filepath.Glob(runnerPackagesRole + "/tasks/*.yml")
	require.NoError(t, err)
	require.NotEmpty(t, files, "the role has no task files")
	out := map[string][]map[string]any{}
	for _, f := range files {
		b, err := os.ReadFile(f)
		require.NoError(t, err)
		var raw []any
		require.NoError(t, yaml.Unmarshal(b, &raw), f)
		out[filepath.Base(f)] = flattenTasks(t, mapsOf(t, raw))
	}
	return out
}

// taskText is everything a task says, as YAML: its module arguments, loop and when.
func taskText(t *testing.T, task map[string]any) string {
	t.Helper()
	b, err := yaml.Marshal(task)
	require.NoError(t, err)
	return string(b)
}

func TestRunnerPackagesPlayTargetsTheBenchAndRunnerGroups(t *testing.T) {
	t.Parallel()
	b, err := os.ReadFile("runner-packages.yml")
	require.NoError(t, err)
	assert.NotEmpty(t, header(string(b), "purpose"), "the play's purpose line is what nova-fleet plays prints")
	var docs []map[string]any
	require.NoError(t, yaml.Unmarshal(b, &docs))
	require.Len(t, docs, 1)
	assert.Equal(t, "bench:runner", docs[0]["hosts"], "a bench runs the same gates as a runner, so it has the same packages")
	assert.NotContains(t, docs[0], "become", "brew refuses root: the play runs as the login, a task becomes root only where apt needs it")
	roles, _ := docs[0]["roles"].([]any)
	assert.Equal(t, []any{"runner-packages"}, roles)
	assert.True(t, slices.Contains(plays(tree), "runner-packages"), "nova-fleet lists the play")
}

func TestRunnerPackagesVarsLeaveLinuxUnchanged(t *testing.T) {
	t.Parallel()
	b, err := os.ReadFile("group_vars/all.yml")
	require.NoError(t, err)
	var vars struct {
		Packages  map[string][]string `yaml:"nova_runner_packages"`
		Foreign   []string            `yaml:"nova_runner_foreign_architectures"`
		VMKernel  *bool               `yaml:"nova_runner_vm_kernel"`
		PortsURI  string              `yaml:"nova_runner_ports_uri"`
		KernelDir string              `yaml:"nova_runner_vm_kernel_dir"`
	}
	require.NoError(t, yaml.Unmarshal(b, &vars))
	assert.Equal(t, map[string][]string{"darwin": {"postgresql@18"}, "debian": {}}, vars.Packages)
	require.NotNil(t, vars.Foreign, "nova_runner_foreign_architectures has a default")
	assert.Empty(t, vars.Foreign, "no foreign architecture by default")
	require.NotNil(t, vars.VMKernel, "nova_runner_vm_kernel has a default")
	assert.False(t, *vars.VMKernel, "the VM tier's kernel copies are off by default")
	assert.Equal(t, "http://ports.ubuntu.com/ubuntu-ports/", vars.PortsURI)
	assert.Equal(t, "{{ ansible_facts['user_dir'] }}/vm-kernels", vars.KernelDir)
}

// taskNamed is the one task of the role's file whose name is name.
func taskNamed(t *testing.T, file, name string) map[string]any {
	t.Helper()
	for _, task := range runnerPackagesTasks(t)[file] {
		if asText(task["name"]) == name {
			return task
		}
	}
	require.Failf(t, "no such task", "%s has no task %q", file, name)
	return nil
}

// A foreign architecture (libsodium-dev:s390x) is enabled in dpkg only where dpkg lacks it, fetched from its own ports
// source restricted to it, and the apt cache is refreshed after either changes; every step is skipped unless the
// list names one, and only on Debian/Ubuntu.
func TestRunnerPackagesForeignArchitectures(t *testing.T) {
	t.Parallel()
	b, err := os.ReadFile(runnerPackagesRole + "/tasks/main.yml")
	require.NoError(t, err)
	assert.Contains(t, string(b), "include_tasks: debian-arch.yml\n  when: rp_os == 'debian' and nova_runner_foreign_architectures | length > 0")

	add := taskNamed(t, "debian-arch.yml", "dpkg adds the architectures it lacks")
	text := taskText(t, add)
	assert.Contains(t, text, "dpkg --add-architecture")
	assert.Contains(t, text, "difference(rp_foreign_have.stdout_lines)", "only the architectures dpkg does not print")
	assert.Contains(t, asText(add["when"]), "not ansible_check_mode")

	src := taskNamed(t, "debian-arch.yml", "a ports source for each foreign architecture")
	tmpl, _ := src["ansible.builtin.template"].(map[string]any)
	require.NotNil(t, tmpl)
	assert.Equal(t, "/etc/apt/sources.list.d/ubuntu-ports-{{ item }}.sources", tmpl["dest"])
	assert.Equal(t, "nova_runner_foreign_architectures", strings.Trim(asText(src["loop"]), "{} "))

	refresh := taskNamed(t, "debian-arch.yml", "apt reads the changed sources")
	for _, r := range []string{"rp_native_restricted is changed", "rp_ports_source is changed", "rp_arch_added is changed"} {
		assert.Contains(t, asText(refresh["when"]), r)
	}
}

// A one-line /etc/apt/sources.list (Ubuntu 22.04's form) is never edited: an unrestricted line in it is refused, naming
// the fix. ubuntu.sources (deb822) is edited only when none of its sources names an architecture.
func TestRunnerPackagesNativeSourcesAreRestrictedSafely(t *testing.T) {
	t.Parallel()
	for _, task := range runnerPackagesTasks(t)["debian-arch.yml"] {
		for _, m := range []string{"ansible.builtin.replace", "ansible.builtin.lineinfile", "ansible.builtin.template", "ansible.builtin.copy"} {
			if args, ok := task[m].(map[string]any); ok {
				assert.NotEqual(t, "/etc/apt/sources.list", asText(args["path"])+asText(args["dest"]), "task %q edits the one-line sources", task["name"])
			}
		}
	}
	oneline := taskNamed(t, "debian-arch.yml", "the one-line sources name an architecture")
	assert.Contains(t, taskText(t, oneline), "rp_oneline_open | length == 0")
	mixed := taskNamed(t, "debian-arch.yml", "ubuntu.sources names an architecture in all of its sources or in none")
	assert.NotNil(t, mixed["ansible.builtin.assert"])
	restrict := taskNamed(t, "debian-arch.yml", "the native sources are restricted to the native architecture")
	assert.Equal(t, "rp_stanzas_open | length > 0", asText(restrict["when"]))
}

// The VM tier's kernel image: a postinst hook keeps a copy of every kernel the host installs, the run copies the
// running one's, and the login joins kvm; all of it only under nova_runner_vm_kernel, only on Debian/Ubuntu.
func TestRunnerPackagesVMKernel(t *testing.T) {
	t.Parallel()
	b, err := os.ReadFile(runnerPackagesRole + "/tasks/main.yml")
	require.NoError(t, err)
	assert.Contains(t, string(b), "include_tasks: debian-vm-kernel.yml\n  when: rp_os == 'debian' and nova_runner_vm_kernel | bool")

	hook := taskNamed(t, "debian-vm-kernel.yml", "the kernel hook that copies each image the host installs")
	tmpl, _ := hook["ansible.builtin.template"].(map[string]any)
	require.NotNil(t, tmpl)
	assert.Equal(t, "/etc/kernel/postinst.d/zz-nova-vm-kernel", tmpl["dest"])
	assert.Equal(t, "0755", tmpl["mode"])

	img := taskNamed(t, "debian-vm-kernel.yml", "the running kernel's image, readable by the login")
	cp, _ := img["ansible.builtin.copy"].(map[string]any)
	require.NotNil(t, cp)
	assert.Equal(t, "/boot/vmlinuz-{{ ansible_facts['kernel'] }}", cp["src"])
	assert.Equal(t, true, cp["remote_src"])
	assert.Equal(t, "{{ nova_runner_vm_kernel_dir }}/vmlinuz-{{ ansible_facts['kernel'] }}", cp["dest"])
	assert.Equal(t, "0600", cp["mode"])

	kvm := taskNamed(t, "debian-vm-kernel.yml", "the login is in kvm")
	user, _ := kvm["ansible.builtin.user"].(map[string]any)
	require.NotNil(t, user)
	assert.Equal(t, true, user["append"], "joining kvm keeps the login's other groups")
	assert.Equal(t, []any{"kvm"}, user["groups"])

	absent := taskNamed(t, "debian-vm-kernel.yml", "the running kernel has its image")
	msg := asText(absent["ansible.builtin.assert"].(map[string]any)["fail_msg"])
	assert.Contains(t, msg, "add linux-image-{{ ansible_facts['kernel'] }} to nova_runner_packages.debian", "the refusal names the package that provides it")
}

// TestRunnerPackagesStartsNoServer fails when a task starts, enables or registers a service: a service module, a
// LaunchAgent or unit file, `brew services`, pg_ctl, initdb or launchctl.
func TestRunnerPackagesStartsNoServer(t *testing.T) {
	t.Parallel()
	modules := []string{"ansible.builtin.service", "ansible.builtin.systemd", "ansible.builtin.systemd_service", "community.general.launchd",
		"ansible.builtin.cron", "community.general.homebrew_services", "ansible.windows.win_service"}
	// The files the role writes: apt sources, the kernel hook and the VM tier's kernel copies; never a unit or an agent.
	writes := []string{"/etc/apt/sources.list.d/", "/etc/kernel/postinst.d/", "{{ nova_runner_vm_kernel_dir }}/"}
	for file, tasks := range runnerPackagesTasks(t) {
		for _, task := range tasks {
			name := asText(task["name"])
			for _, m := range modules {
				assert.NotContains(t, task, m, "%s: task %q: the role installs packages and starts nothing", file, name)
			}
			for _, m := range []string{"ansible.builtin.template", "ansible.builtin.copy"} {
				if args, ok := task[m].(map[string]any); ok {
					dest := asText(args["dest"])
					assert.True(t, slices.ContainsFunc(writes, func(w string) bool { return strings.HasPrefix(dest, w) }),
						"%s: task %q writes %s: the role writes only apt sources, the kernel hook and kernel copies", file, name, dest)
				}
			}
			text := taskText(t, task)
			for _, word := range []string{"services", "pg_ctl", "initdb", "launchctl", "systemctl", "postgres -D", "enabled: true"} {
				assert.NotContains(t, text, word, "%s: task %q: the role installs packages and starts nothing", file, name)
			}
		}
	}
}

// TestRunnerPackagesInstallsOnlyWhatIsMissing fails on an install task that does not work from the presence check:
// each one loops over rp_missing (the wanted packages the check did not find), is skipped under --check, and comes
// after the task that registers what is present. The --check twin prints WOULD-INSTALL for the same list.
func TestRunnerPackagesInstallsOnlyWhatIsMissing(t *testing.T) {
	t.Parallel()
	installs, would := 0, 0
	for file, tasks := range runnerPackagesTasks(t) {
		checked := false
		for _, task := range tasks {
			name := asText(task["name"])
			text := taskText(t, task)
			if strings.Contains(text, "rp_present:") {
				checked = true
			}
			// a command that installs, or a package module given names (an apt task with no name only refreshes the cache)
			command := task["ansible.builtin.command"] != nil || task["ansible.builtin.shell"] != nil
			apt, _ := task["ansible.builtin.apt"].(map[string]any)
			installer := (command && (strings.Contains(text, " install ") || strings.Contains(text, " install\n"))) ||
				(apt != nil && apt["name"] != nil) || task["ansible.builtin.package"] != nil || task["community.general.homebrew"] != nil
			if strings.Contains(text, "WOULD-INSTALL") {
				would++
				assert.Contains(t, asText(task["when"]), "ansible_check_mode", "%s: task %q", file, name)
				assert.Contains(t, text, "rp_missing", "%s: task %q reports what is missing", file, name)
				continue
			}
			if !installer {
				continue
			}
			installs++
			assert.True(t, checked, "%s: task %q installs before the presence check", file, name)
			assert.Contains(t, text, "rp_missing", "%s: task %q installs only what the presence check found missing", file, name)
			assert.Contains(t, asText(task["when"]), "not ansible_check_mode", "%s: task %q changes nothing under --check", file, name)
			assert.NotContains(t, text, "upgrade", "%s: task %q never upgrades what is there", file, name)
			assert.NotContains(t, text, "reinstall", "%s: task %q never reinstalls what is there", file, name)
		}
	}
	assert.Equal(t, 2, installs, "one install task per OS (brew on darwin, apt on debian)")
	assert.Equal(t, 2, would, "one WOULD-INSTALL task per OS")
}

// TestRunnerPackagesReportsOneLinePerHost pins the receipt line.
func TestRunnerPackagesReportsOneLinePerHost(t *testing.T) {
	t.Parallel()
	found := 0
	for _, tasks := range runnerPackagesTasks(t) {
		for _, task := range tasks {
			if text := taskText(t, task); strings.Contains(text, "RUNNER-PACKAGES host=") {
				found++
				for _, field := range []string{"host={{ inventory_hostname }}", "wanted=", "present=", "installed="} {
					assert.Contains(t, text, field)
				}
				assert.Equal(t, false, task["changed_when"], "the receipt changes nothing")
			}
		}
	}
	assert.Equal(t, 1, found, "one RUNNER-PACKAGES line per host")
}

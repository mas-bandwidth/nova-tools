package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// With a runner login of its own the machine is locked down for it (roles/github-runner/tasks/lockdown.yml): the fleet
// login's home is 0750, and a scan run as the runner login over every local filesystem fails the run on any path
// it can write or owns outside its home, the kernel's and the named scratch places; nothing found is adopted.
func TestRunnerLoginLockdown(t *testing.T) {
	t.Parallel()
	login := roleTaskFile(t, "unix-login.yml")
	assert.Equal(t, "lockdown.yml", asText(runnerTask(t, login, "the machine, locked down for the runner login")["ansible.builtin.include_tasks"]))
	// WSL's interop runs wsl.exe -u root for any login: a runner login of its own is refused there, before it is made
	assert.Contains(t, asText(runnerTask(t, login, "WSL's interop is off")["ansible.builtin.shell"]), "/proc/sys/fs/binfmt_misc/WSLInterop*")
	wsl := runnerTask(t, login, "a runner login of its own is refused where WSL's interop is on")
	assert.Contains(t, asText(wsl["ansible.builtin.fail"].(map[string]any)["msg"]), "RUNNER-LOGIN-WSL-INTEROP")
	assert.Less(t, taskIndex(login, "a runner login of its own is refused where WSL's interop is on"), taskIndex(login, "the runner login (locked, its own group and kvm only)"))

	tasks := roleTaskFile(t, "lockdown.yml")
	home := runnerTask(t, tasks, "the fleet login's home is closed to other logins (0750)")
	assert.Contains(t, asText(home["ansible.builtin.shell"]), `chmod 0750 "$d"`)
	assert.Contains(t, asText(home["ansible.builtin.shell"]), "d={{ ansible_facts['user_dir'] | quote }}")

	for _, task := range tasks {
		assert.NotContains(t, asText(task["ansible.builtin.shell"]), "chown", "task %q: a file a job planted is never adopted", asText(task["name"]))
	}

	scan := runnerTask(t, tasks, "the runner login can write no system path")
	sh := asText(scan["ansible.builtin.shell"])
	assert.Contains(t, sh, "for m in $(findmnt --real -rn -o TARGET -t nonfs,nfs4,cifs,smb3,9p,fuse.sshfs); do", "every local filesystem")
	assert.Contains(t, sh, `[ -d "$m" ] || { echo "a mount the scan cannot start from: $m" >&2; exit 1; }`, "a mount find cannot start from never passes")
	assert.Contains(t, sh, "timeout 1800 runuser -u {{ github_runner_user | quote }} -- sh -c '")
	for _, pruned := range []string{"-path /proc", "-path /sys", "-path /dev", "-path /run", "-path /tmp", "-path /var/tmp", "-path /var/crash",
		"-path /var/lib/php/sessions", `-path "/var/mail/$me"`, `-path "$home"`, `-path "/run/user/$uid"`, "-path /run/lock", "-path /run/screen"} {
		assert.Contains(t, sh, pruned)
	}
	assert.Contains(t, sh, `-o \( -writable -o -user "$me" \) ! -type l -print`, "what it can write, and what it owns")
	assert.NotContains(t, sh, "-perm -1002", "no sticky directory is shared scratch but the named ones")
	assert.Contains(t, sh, `-o \( -writable -o -user "$me" \) ! -type l ! -type s -print`, "/run but its sockets")
	assert.Contains(t, sh, "for s in /run/docker.sock /run/podman/podman.sock /run/containerd/containerd.sock; do")
	assert.Contains(t, sh, "[ $r1 -le 1 ] && [ $r2 -le 1 ] && echo SCAN-DONE")
	assert.Equal(t, false, scan["check_mode"], "read under --check too")
	unfinished := runnerTask(t, tasks, "a scan that did not run to its end fails the run")
	assert.Contains(t, asText(unfinished["when"]), "!= 'SCAN-DONE'")
	writable := runnerTask(t, tasks, "a path the runner login can write or owns fails the run")
	assert.Contains(t, asText(writable["ansible.builtin.fail"].(map[string]any)["msg"]), "RUNNER-LOGIN-WRITABLE")
}

// The runner login's VM tier (roles/github-runner/tasks/vm-tier.yml): root's kernel copies outside its home, readable
// by its group; its ~/vm-kernels made as it; and rocketnet's scripts/vm-tier-missing conditions, checked as it, fail
// the run naming what is missing.
func TestRunnerLoginVMTier(t *testing.T) {
	t.Parallel()
	login := roleTaskFile(t, "unix-login.yml")
	tier := runnerTask(t, login, "the VM tier for the runner login")
	assert.Equal(t, "vm-tier.yml", asText(tier["ansible.builtin.include_tasks"]))
	assert.Equal(t, "nova_runner_vm_kernel | bool and ansible_facts['architecture'] == 'x86_64' and 'microsoft' not in (ansible_facts['kernel'] | lower)", asText(tier["when"]), "an x86_64 kernel, never WSL's")

	tasks := roleTaskFile(t, "vm-tier.yml")
	copies := asText(runnerTask(t, tasks, "the shared kernel copies and the running kernel's")["ansible.builtin.shell"])
	assert.Contains(t, copies, "dir={{ (nova_shared_dir ~ '/vm-kernels') | quote }}")
	assert.Contains(t, copies, `install -o root -g "$group" -m 0640 "/boot/vmlinuz-$r" "$dir/vmlinuz-$r"`)
	hook := asText(runnerTask(t, tasks, "the kernel hook that copies each image the host installs")["ansible.builtin.copy"].(map[string]any)["content"])
	assert.Contains(t, hook, "install -o root -g {{ github_runner_user | quote }} -m 0640")
	assert.Contains(t, hook, "getent group {{ github_runner_user | quote }} >/dev/null || exit 0", "a kernel package whose hook fails is left half configured")
	assert.NotContains(t, hook, "set -e", "the hook never fails")
	link := runnerTask(t, tasks, "the runner login's ~/vm-kernels names them")
	assert.Equal(t, "{{ github_runner_user }}", link["become_user"])

	check := asText(runnerTask(t, tasks, "the VM tier is whole for the runner login")["ansible.builtin.shell"])
	for _, want := range []string{"runuser -u {{ github_runner_user | quote }}", "command -v vng", "command -v qemu-system-x86_64",
		"[ -w /dev/kvm ]", `[ -r "$HOME/vm-kernels/vmlinuz-$r" ]`, `[ -d "/lib/modules/$r" ]`} {
		assert.Contains(t, check, want)
	}
	fail := runnerTask(t, tasks, "a VM tier that lacks anything fails the run")
	assert.Contains(t, asText(fail["ansible.builtin.fail"].(map[string]any)["msg"]), "VM-TIER-MISSING")
}

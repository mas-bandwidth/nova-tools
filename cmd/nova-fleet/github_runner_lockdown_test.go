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

package main

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// github_runner_shared_test.go pins what a runner login of its own runs outside the fleet login's home
// (cmd/nova-fleet/README.md, "The runner login"): root's copies of the tools in nova_shared_dir/bin, and the SDK in
// nova_shared_dir/sdk, the fleet login's, with both logins' ~/sdk naming it.

func taskFileOf(t *testing.T, path string) []map[string]any {
	t.Helper()
	b, err := os.ReadFile(path)
	require.NoError(t, err)
	var tasks []map[string]any
	require.NoError(t, yaml.Unmarshal(b, &tasks))
	return tasks
}

// The copies are root's (the runner login runs them and cannot change them), made from the fleet login's bin
// directory, each replaced whole and only when it differs; github-runners makes them, install refreshes them.
func TestSharedToolsAreRootsCopies(t *testing.T) {
	t.Parallel()
	shared := taskFileOf(t, "roles/nova-tools-install/tasks/shared.yml")
	require.Len(t, shared, 1)
	sh := asText(shared[0]["ansible.builtin.shell"])
	for _, want := range []string{
		`chown root:root "$shared" "$shared/bin"`,
		`src={{ nova_bin_dir | quote }}/$t`,
		`cmp -s "$src" "$shared/bin/$t" && continue`,
		`install -o root -g root -m 0755 "$src" "$shared/bin/.$t.new"`,
		`mv -f "$shared/bin/.$t.new" "$shared/bin/$t"`,
		`chmod 0755 "$shared/sdk"`,
	} {
		assert.Contains(t, sh, want)
	}
	assert.NotContains(t, sh, "chown -R", "nothing of the shared directory is changed through what it holds")
	assert.Equal(t, true, shared[0]["become"])

	install := taskFileOf(t, "roles/nova-tools-install/tasks/main.yml")
	refresh := runnerTask(t, install, "the shared tools are refreshed")
	assert.Equal(t, "shared.yml", asText(refresh["ansible.builtin.include_tasks"]))
	assert.Equal(t, "ansible_facts['system'] == 'Linux' and 'runner' in group_names and github_runner_user != ansible_facts['user_id']", asText(refresh["when"]))

	login := roleTaskFile(t, "unix-login.yml")
	assert.Equal(t, "{{ role_path }}/../nova-tools-install/tasks/shared.yml", asText(runnerTask(t, login, "the shared tools")["ansible.builtin.include_tasks"]))
	link := runnerTask(t, login, "the runner login's ~/sdk names it")
	assert.Equal(t, "{{ github_runner_user }}", link["become_user"], "the link is the runner login's, made as it")
	assert.Contains(t, asText(link["ansible.builtin.shell"]), "ln -sfn {{ (nova_shared_dir ~ '/sdk') | quote }} sdk")
}

// sdk-toolchain never becomes root: with nova_sdk_shared it checks the shared directory is the login's, moves the
// login's own SDK aside once, links nova_sdk_dir to the shared one (so the rest of the role installs there), and
// deletes the old one only at the end of the run.
func TestSDKToolchainSharesTheSDKAsTheLogin(t *testing.T) {
	t.Parallel()
	shared := taskFileOf(t, sdkToolchainRole+"/tasks/shared.yml")
	for _, task := range shared {
		assert.NotContains(t, task, "become", "task %q: the SDK is the login's", asText(task["name"]))
	}
	assert.Contains(t, asText(runnerTask(t, shared, "the shared SDK directory is the login's")["ansible.builtin.assert"].(map[string]any)["that"]),
		"sdk_shared_dir.stat.pw_name == ansible_facts['user_id']")
	// the old SDK stays in use until the shared one is whole: the link is made last, then the old one goes
	main := taskFileOf(t, sdkToolchainRole+"/tasks/main.yml")
	last := main[len(main)-1]
	assert.Equal(t, "link.yml", asText(last["ansible.builtin.include_tasks"]), "the link only once every task before it has run")
	assert.Equal(t, "nova_sdk_shared | bool", asText(last["when"]))
	link := taskFileOf(t, sdkToolchainRole+"/tasks/link.yml")
	for _, task := range link {
		assert.NotContains(t, task, "become", "task %q: the SDK is the login's", asText(task["name"]))
	}
	// another login runs from it: nothing in it writable by others, everything readable by them
	assert.Contains(t, asText(runnerTask(t, link, "nothing in the shared SDK is writable by other logins")["ansible.builtin.shell"]),
		`find "$d" -xdev ! -type l -perm -o+w -print -exec chmod o-w {} +`)
	readable := runnerTask(t, link, "everything in the shared SDK is readable by other logins")
	assert.Contains(t, asText(readable["ansible.builtin.shell"]), `\( ! -perm -o+r -o \( -type d ! -perm -o+x \) \)`)
	assert.Equal(t, "sdk_shared_unreadable.rc != 0 or sdk_shared_unreadable.stdout | length > 0", asText(readable["failed_when"]))
	// an old SDK holding what the shared one lacks stays in use, and the run fails naming it
	held := runnerTask(t, link, "an old SDK holding what the shared one lacks is kept")
	assert.Contains(t, asText(held["ansible.builtin.fail"].(map[string]any)["msg"]), "SDK-TOOLCHAIN SHARE-HELD")
	swap := runnerTask(t, link, "the login's own SDK is moved aside and nova_sdk_dir names the shared SDK")
	assert.Contains(t, asText(swap["ansible.builtin.shell"]), `mv -T "$sdk" "$sdk.home" && ln -s "$shared" "$sdk"`, "moved and linked in one command")
	assert.Less(t, taskIndex(link, "everything in the shared SDK is readable by other logins"), taskIndex(link, "the login's own SDK is moved aside and nova_sdk_dir names the shared SDK"))
	assert.Less(t, taskIndex(link, "an old SDK holding what the shared one lacks is kept"), taskIndex(link, "the login's own SDK is moved aside and nova_sdk_dir names the shared SDK"))
	assert.Less(t, taskIndex(link, "the login's own SDK is moved aside and nova_sdk_dir names the shared SDK"), taskIndex(link, "the SDK moved aside is deleted"))
	// compared below the top: a toolchain, a cargo tool or an opam package only the old SDK holds is held
	extra := asText(runnerTask(t, link, "what the login's own SDK holds that the shared one lacks")["ansible.builtin.shell"])
	assert.Contains(t, extra, `for e in * .[!.]* */* */.[!.]* rustup-*/cargo/bin rustup-*/toolchains/* rustup-*/cargo/bin/* opam/*/lib opam/*/bin opam/*/lib/* opam/*/bin/*; do`)
	assert.Contains(t, extra, `if [ "$up" = . ]; then echo "$e"; fi`, "each path once, at its highest missing level")
	// nothing is moved or deleted while a process runs from it, and nothing is stopped
	for _, step := range [][2]string{
		{"the login's own SDK is in use by no process", "the login's own SDK is moved aside and nova_sdk_dir names the shared SDK"},
		{"the SDK moved aside is in use by no process", "the SDK moved aside is deleted"},
	} {
		check := runnerTask(t, link, step[0])
		assert.Equal(t, "in-use.yml", asText(check["ansible.builtin.include_tasks"]))
		assert.Equal(t, taskIndex(link, step[0])+1, taskIndex(link, step[1]), "the check comes right before %q", step[1])
	}
	inUse := taskFileOf(t, sdkToolchainRole+"/tasks/in-use.yml")
	for _, task := range inUse {
		assert.NotContains(t, task, "become", "task %q: the SDK is the login's", asText(task["name"]))
	}
	scan := asText(runnerTask(t, inUse, "no process runs from {{ sdk_in_use_dir }}")["ansible.builtin.shell"])
	for _, want := range []string{`for f in "$p/exe" "$p/cwd" "$p"/fd/*; do`, `grep -q -F " $d/" "$p/maps"`} {
		assert.Contains(t, scan, want)
	}
	assert.NotContains(t, scan, "kill")
	assert.Contains(t, asText(runnerTask(t, inUse, "a directory in use is neither moved nor deleted")["ansible.builtin.fail"].(map[string]any)["msg"]), "SDK-TOOLCHAIN SDK-IN-USE")

	b, err := os.ReadFile(sdkToolchainRole + "/defaults/main.yml")
	require.NoError(t, err)
	var d map[string]any
	require.NoError(t, yaml.Unmarshal(b, &d))
	assert.Equal(t, "{{ ansible_facts['system'] == 'Linux' and 'runner' in group_names and github_runner_user != ansible_facts['user_id'] }}", d["nova_sdk_shared"], "shared wherever the runners are a login of their own")
}

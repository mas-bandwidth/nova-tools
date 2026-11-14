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
		`for t in {{ nova_tools_names | map('quote') | join(' ') }} sops age age-keygen; do`,
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
	// each runner's own rustup home (no two runners write one at once), the shared toolchains linked in, made as the
	// login before the runner's unit names it; the unit names the runner's homes, never the login's or the record's
	rust := roleTaskFile(t, "runner-rust.yml")
	rustup := runnerTask(t, rust, "this runner's rustup home names the shared toolchains")
	assert.Equal(t, "{{ github_runner_user }}", rustup["become_user"])
	rsh := asText(rustup["ansible.builtin.shell"])
	for _, want := range []string{
		`src=$(env -u RUSTUP_HOME -u CARGO_HOME "$shared_rustup" show home)`,
		`rh={{ github_runner_rustup_home | quote }}`,
		`ch={{ github_runner_cargo_home | quote }}`,
		`if [ "$(readlink "$l")" != "$t" ]; then ln -sfn "$t" "$l"; set=1; fi`,
		`cp "$src/update-hashes/$n" "$rh/update-hashes/$n"`,
		`if [ -L "$l" ] && [ ! -e "$l" ]; then rm -f "$l"; set=1; fi`,
		`cp "$src/settings.toml" "$rh/settings.toml"`,
		`if [ "$(readlink "$b")" != "$f" ]; then ln -sfn "$f" "$b"; set=1; fi`,
	} {
		assert.Contains(t, rsh, want)
	}
	assert.NotContains(t, rsh, "rm -rf", "only dangling links of its own are removed")
	runner := roleTaskFile(t, "unix-runner.yml")
	include := runnerTask(t, runner, "this runner's own rustup and cargo homes (Linux, a runner login of its own)")
	assert.Equal(t, "runner-rust.yml", asText(include["ansible.builtin.include_tasks"]))
	assert.Contains(t, asText(include["when"]), "github_runner_user != ansible_facts['user_id']")
	assert.Less(t, taskIndex(runner, "this runner's own rustup and cargo homes (Linux, a runner login of its own)"), taskIndex(runner, "the unit (Linux)"), "made before the unit names them")
	d := runnerDefaults(t)
	assert.Equal(t, "{{ github_runner_rust_homes }}/{{ github_runner_dir | basename }}/rustup", asText(d["github_runner_rustup_home"]), "one per runner")
	assert.Equal(t, "{{ github_runner_rust_homes }}/{{ github_runner_dir | basename }}/cargo", asText(d["github_runner_cargo_home"]), "one per runner")
	unit, err := os.ReadFile("roles/github-runner/templates/runner.service.j2")
	require.NoError(t, err)
	assert.Contains(t, string(unit), "Environment=RUSTUP_HOME={{ github_runner_rustup_home }}\n", "the runner's own, never the record's")
	assert.Contains(t, string(unit), "Environment=CARGO_HOME={{ github_runner_cargo_home }}\n")

	// the channel a job's dtolnay/rust-toolchain installs minimal and makes the default keeps clippy and rustfmt there
	channelName := "this runner's {{ github_runner_rust_channel }} toolchain carries {{ github_runner_rust_channel_components | join(' and ') }}"
	channel := runnerTask(t, rust, channelName)
	assert.Equal(t, "{{ github_runner_user }}", channel["become_user"], "in the runner's own rustup home, as the login")
	csh := asText(channel["ansible.builtin.shell"])
	for _, want := range []string{
		`RUSTUP_HOME={{ github_runner_rustup_home | quote }}`,
		`CARGO_HOME={{ github_runner_cargo_home | quote }}`,
		`rustup toolchain install "$ch" --profile minimal --no-self-update{% for c in github_runner_rust_channel_components %} --component {{ c | quote }}{% endfor %}`,
		`rustup component add --toolchain "$ch" $want`,
	} {
		assert.Contains(t, csh, want)
	}
	assert.NotContains(t, csh, "rustup default", "the default is the pinned one until a job names another")
	assert.Less(t, taskIndex(rust, "this runner's rustup home names the shared toolchains"), taskIndex(rust, channelName), "after the homes exist")
	assert.Equal(t, "stable", asText(d["github_runner_rust_channel"]))
	assert.Equal(t, []any{"clippy", "rustfmt"}, d["github_runner_rust_channel_components"])
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

	b, err := os.ReadFile(sdkToolchainRole + "/defaults/main.yml")
	require.NoError(t, err)
	var d map[string]any
	require.NoError(t, yaml.Unmarshal(b, &d))
	assert.Equal(t, "{{ ansible_facts['system'] == 'Linux' and 'runner' in group_names and github_runner_user != ansible_facts['user_id'] }}", d["nova_sdk_shared"], "shared wherever the runners are a login of their own")
}

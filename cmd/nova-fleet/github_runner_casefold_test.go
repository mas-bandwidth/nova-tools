package main

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// A Linux runner host's case-folding scratch volume (roles/github-runner/tasks/casefold.yml): root's ext4 image with
// casefold, loop-mounted from /etc/fstab, its scratch directory 1777 with chattr +F, and a probe made as the runner
// login that must fold Probe and probe to one entry, or the run fails naming the host. The scan of what the runner
// login can write prunes the volume as it does /tmp.
func TestRunnerCasefoldScratch(t *testing.T) {
	t.Parallel()
	b, err := os.ReadFile("roles/github-runner/defaults/main.yml")
	require.NoError(t, err)
	var d map[string]any
	require.NoError(t, yaml.Unmarshal(b, &d))
	assert.Equal(t, "/srv/casefold", d["github_runner_casefold_dir"])
	assert.Equal(t, "{{ nova_shared_dir }}/casefold.img", d["github_runner_casefold_image"])
	assert.Equal(t, "1G", d["github_runner_casefold_size"])

	main := roleTaskFile(t, "main.yml")
	inc := runnerTask(t, main, "the case-folding scratch volume (Linux)")
	assert.Equal(t, "casefold.yml", asText(inc["ansible.builtin.include_tasks"]))
	assert.Equal(t, "ansible_facts['system'] == 'Linux' and github_runner_casefold_dir | length > 0 and github_runner_state == 'present'", asText(inc["when"]))
	assert.Less(t, taskIndex(main, "the runner login of its own (Linux)"), taskIndex(main, "the case-folding scratch volume (Linux)"), "the runner login exists first")
	assert.Less(t, taskIndex(main, "the case-folding scratch volume (Linux)"), taskIndex(main, "every runner of the machine record"), "before any runner takes a job")

	tasks := roleTaskFile(t, "casefold.yml")
	img := asText(runnerTask(t, tasks, "the case-folding image (ext4, casefold)")["ansible.builtin.shell"])
	for _, want := range []string{
		`[ -e /sys/fs/ext4/features/casefold ] || {`,
		`mkfs.ext4 -q -F -O casefold -E encoding=utf8 -L nova-casefold "$img.new"`,
		`mv "$img.new" "$img"`,
		`*" casefold "*) ;;`,
	} {
		assert.Contains(t, img, want)
	}

	mount := runnerTask(t, tasks, "the case-folding image, mounted at boot")["ansible.posix.mount"].(map[string]any)
	assert.Equal(t, "{{ github_runner_casefold_dir }}", mount["path"])
	assert.Equal(t, "{{ github_runner_casefold_image }}", mount["src"])
	assert.Equal(t, "ext4", mount["fstype"])
	assert.Equal(t, "loop,nosuid,nodev,noexec", mount["opts"])
	assert.Equal(t, "mounted", mount["state"], "in /etc/fstab and mounted now")

	scratch := asText(runnerTask(t, tasks, "the shared case-folding scratch directory (root's, 1777, chattr +F)")["ansible.builtin.shell"])
	for _, want := range []string{
		`mountpoint -q "$top" || {`,
		`chattr +F "$d"`,
		`[ -z "$(ls -A "$d")" ] || {`,
		`[ "$(stat -c '%U:%G:%a' "$d")" = root:root:1777 ] || { chown root:root "$d"; chmod 1777 "$d"; set=1; }`,
	} {
		assert.Contains(t, scratch, want)
	}

	probe := runnerTask(t, tasks, "the scratch folds names for the runner login")
	sh := asText(probe["ansible.builtin.shell"])
	assert.Contains(t, sh, "runuser -u {{ github_runner_user | quote }} -- sh -c '")
	assert.Contains(t, sh, `if [ -d "$p/probe" ]; then r=FOLDS;`)
	fail := runnerTask(t, tasks, "a scratch that does not fold for the runner login fails the run")
	assert.Contains(t, asText(fail["ansible.builtin.fail"].(map[string]any)["msg"]), "CASEFOLD-MISSING")
	assert.Contains(t, asText(fail["when"]), "!= 'FOLDS'")

	scan := asText(runnerTask(t, roleTaskFile(t, "lockdown.yml"), "the runner login can write no system path")["ansible.builtin.shell"])
	assert.Contains(t, scan, `-o -path "$casefold"`, "the volume is shared scratch, as /tmp is")
	assert.Contains(t, scan, "{{ (github_runner_casefold_dir or '/nonexistent') | quote }} $mounts")
}

package main

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// A Linux runner host's case-folding scratch volume (roles/github-runner/tasks/casefold.yml): root's ext4 image with
// casefold, loop-mounted from /etc/fstab without holding up a boot, its scratch directory 1777 with chattr +F, and a
// probe made as the runner login that must fold Probe and probe to one entry, or the run fails naming the host. The
// kernel check runs under --check too. The scan of what the runner login can write prunes only <dir>/scratch, and
// only while <dir> is the image's own loop mount; a directory less than two levels below / is refused by name.
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

	// the directory: refused by name unless it is at least two levels below /, before the scan that prunes it runs
	own := runnerTask(t, main, "the case-folding directory is a path of its own (Linux)")
	assert.Less(t, taskIndex(main, "the case-folding directory is a path of its own (Linux)"), taskIndex(main, "the runner login of its own (Linux)"), "before the lockdown scan")
	assert.Equal(t, "ansible_facts['system'] == 'Linux' and github_runner_casefold_dir | length > 0", asText(own["when"]))
	ownAssert := own["ansible.builtin.assert"].(map[string]any)
	assert.Contains(t, asText(ownAssert["fail_msg"]), "CASEFOLD-DIR")
	that, _ := ownAssert["that"].([]any)
	require.Len(t, that, 1)
	m := regexp.MustCompile(`^github_runner_casefold_dir is match\('(.+)'\)$`).FindStringSubmatch(asText(that[0]))
	require.NotNil(t, m, "the assert is one match: %q", that[0])
	dirOK := regexp.MustCompile(m[1])
	for _, good := range []string{"/srv/casefold", "/var/lib/casefold", "/srv/case_fold-1"} {
		assert.True(t, dirOK.MatchString(good), "%s is a directory of its own", good)
	}
	for _, bad := range []string{"/", "/srv", "/srv/", "srv/casefold", "/srv/casefold/", "/srv/..", "/srv/../etc", "/srv/./casefold", "//srv", "/srv//casefold", "/srv/case fold"} {
		assert.False(t, dirOK.MatchString(bad), "%q is refused", bad)
	}

	tasks := roleTaskFile(t, "casefold.yml")
	for _, task := range tasks {
		_, ignored := task["ignore_errors"]
		assert.False(t, ignored, "task %q: no failure here is ignored", asText(task["name"]))
		if _, ok := task["failed_when"]; ok {
			assert.Equal(t, "the scratch folds names for the runner login", asText(task["name"]), "only the probe reports instead of failing; the task after it fails on what it reports")
		}
	}

	// the kernel: read under --check too, and a missing feature fails the run by name
	kernel := runnerTask(t, tasks, "the kernel has ext4 casefold")
	assert.Equal(t, "/sys/fs/ext4/features/casefold", kernel["ansible.builtin.stat"].(map[string]any)["path"])
	assert.Equal(t, false, kernel["check_mode"], "read under --check too")
	assert.NotContains(t, kernel, "when")
	noKernel := runnerTask(t, tasks, "a kernel without ext4 casefold fails the run")
	assert.Contains(t, asText(noKernel["ansible.builtin.fail"].(map[string]any)["msg"]), "CASEFOLD-KERNEL")
	assert.Equal(t, "not github_runner_casefold_kernel.stat.exists", asText(noKernel["when"]), "under --check too")
	assert.Less(t, taskIndex(tasks, "a kernel without ext4 casefold fails the run"), taskIndex(tasks, "the case-folding image (ext4, casefold)"))

	img := asText(runnerTask(t, tasks, "the case-folding image (ext4, casefold)")["ansible.builtin.shell"])
	for _, want := range []string{
		`mkfs.ext4 -q -F -O casefold -E encoding=utf8 -L nova-casefold "$img.new"`,
		`mv "$img.new" "$img"`,
		`*" casefold "*) ;;`,
	} {
		assert.Contains(t, img, want)
	}

	// the mount: in /etc/fstab, and a missing image never sends the host to emergency.target at boot
	mount := runnerTask(t, tasks, "the case-folding image, mounted at boot")["ansible.posix.mount"].(map[string]any)
	assert.Equal(t, "{{ github_runner_casefold_dir }}", mount["path"])
	assert.Equal(t, "{{ github_runner_casefold_image }}", mount["src"])
	assert.Equal(t, "ext4", mount["fstype"])
	assert.Equal(t, "loop,nosuid,nodev,noexec,nofail,x-systemd.device-timeout=10s", mount["opts"])
	opts := strings.Split(asText(mount["opts"]), ",")
	assert.Contains(t, opts, "nofail", "a missing image does not stop the boot")
	assert.Contains(t, opts, "x-systemd.device-timeout=10s", "nor hold it up for systemd's default 90 s")
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

	// the probe, whole: FOLDS only when probe is the entry Probe made, and the trap removes it however it ends
	probe := runnerTask(t, tasks, "the scratch folds names for the runner login")
	assert.Equal(t, `runuser -u {{ github_runner_user | quote }} -- sh -c '
  set -eu
  p=$(mktemp -d "$1/probe.XXXXXX")
  trap "rm -rf -- \"\$p\"" EXIT
  mkdir "$p/Probe"
  if [ -d "$p/probe" ]; then r=FOLDS; else r="Probe and probe are two entries"; fi
  echo "$r"' sh {{ (github_runner_casefold_dir ~ '/scratch') | quote }}
`, asText(probe["ansible.builtin.shell"]))
	fail := runnerTask(t, tasks, "a scratch that does not fold for the runner login fails the run")
	assert.Contains(t, asText(fail["ansible.builtin.fail"].(map[string]any)["msg"]), "CASEFOLD-MISSING")
	assert.Equal(t, "not ansible_check_mode and (github_runner_casefold_probe.rc != 0 or github_runner_casefold_probe.stdout | trim != 'FOLDS')",
		asText(fail["when"]), "a probe that failed, or printed anything but FOLDS, fails the run")

	// the scan prunes <dir>/scratch, and only while <dir> is the image's own loop mount
	scan := asText(runnerTask(t, roleTaskFile(t, "lockdown.yml"), "the runner login can write no system path")["ansible.builtin.shell"])
	for _, want := range []string{
		"cf={{ github_runner_casefold_dir | quote }}\n",
		"scratch=/nonexistent\n",
		`if [ -n "$cf" ] && mountpoint -q "$cf"; then`,
		`src=$(findmnt -n -o SOURCE --mountpoint "$cf")`,
		`/dev/loop*) if [ "$(losetup -n -O BACK-FILE "$src")" = {{ github_runner_casefold_image | quote }} ]; then scratch="$cf/scratch"; fi ;;`,
		"home=$1; scratch=$2; shift 2\n",
		`-o -path "$home" -o -path "$scratch" \`,
		`echo SCAN-DONE' sh {{ github_runner_home | quote }} "$scratch" $mounts`,
	} {
		assert.Contains(t, scan, want)
	}
	assert.Equal(t, 1, strings.Count(scan, `scratch="$cf/scratch"`), "the one prune is the scratch")
	assert.Equal(t, 1, strings.Count(scan, `-path "$scratch"`))
	assert.Equal(t, 3, strings.Count(scan, "scratch="), "set as the default, as the image's mount, and read as the find's argument; nothing else")
}

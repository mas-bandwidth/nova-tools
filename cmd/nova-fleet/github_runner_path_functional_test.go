//go:build functional

package main

import (
	"context"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// github_runner_path_functional_test.go renders the runner role's two service files through ansible-core's templar on
// localhost, with group_vars/all.yml and the role's defaults as the play reads them, and reads the PATH each gives the
// runner (github_runner_path_test.go is the structural half). HOME is a temp dir, so nova_bin_dir is <tmp>/.local/bin.

const runnerPathPlay = `- hosts: localhost
  connection: local
  gather_facts: true
  gather_subset: [min]
  vars_files: ["{{ fleet }}/group_vars/all.yml", "{{ fleet }}/roles/github-runner/defaults/main.yml"]
  vars:
    github_runner_unit: github-runner-o-r-1
    github_runner_label_id: github-runner.o-r-1
    github_runner_name: h-1
    github_runner_path: o/r
    github_runner_dir: /runners/o-r-1
  tasks:
    - ansible.builtin.copy:
        content: "{{ lookup('ansible.builtin.template', fleet ~ '/roles/github-runner/templates/' ~ item) }}"
        dest: "{{ out }}/{{ item }}"
      loop: [runner.service.j2, runner.plist.j2]
`

func renderRunnerUnits(t *testing.T, extra ...string) (service, plist, home string) {
	t.Helper()
	fleet, err := filepath.Abs(".")
	require.NoError(t, err)
	dir := t.TempDir()
	home = filepath.Join(dir, "home")
	require.NoError(t, os.MkdirAll(home, 0o755))
	play := filepath.Join(dir, "render.yml")
	require.NoError(t, os.WriteFile(play, []byte(runnerPathPlay), 0o644))
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	// the fleet login's runners unless a test names another login (Linux defaults to one of its own)
	me, err := user.Current()
	require.NoError(t, err)
	args := append([]string{"-i", "localhost,", "-e", "fleet=" + fleet, "-e", "out=" + dir, "-e", "github_runner_user=" + me.Username}, extra...)
	cmd := exec.CommandContext(ctx, "ansible-playbook", append(args, play)...)
	cmd.Env = append(os.Environ(), "ANSIBLE_NOCOLOR=1", "HOME="+home, "ANSIBLE_LOCAL_TEMP="+filepath.Join(dir, "tmp"))
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, string(out))
	s, err := os.ReadFile(filepath.Join(dir, "runner.service.j2"))
	require.NoError(t, err)
	p, err := os.ReadFile(filepath.Join(dir, "runner.plist.j2"))
	require.NoError(t, err)
	return string(s), string(p), home
}

func TestRunnerServiceFilesPutTheToolsFirstOnPath(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("ansible-playbook"); err != nil {
		t.Skip("ansible-core is not installed on this machine; TestRunnerServiceFilesSetPath still runs")
	}
	service, plist, home := renderRunnerUnits(t)
	bin := home + "/.local/bin"
	assert.Contains(t, service, "\nEnvironment=PATH="+bin+":/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin:/usr/games:/usr/local/games:/snap/bin\n")
	assert.Equal(t, 1, strings.Count(service, "Environment=PATH="))
	assert.Contains(t, plist, "<key>PATH</key>\n    <string>"+bin+":/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin</string>\n")
	assert.Equal(t, 1, strings.Count(plist, "<key>PATH</key>"))
	assert.Contains(t, service, "\nLimitMEMLOCK=infinity\n")
}

func TestRunnerEnvPathStillWins(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("ansible-playbook"); err != nil {
		t.Skip("ansible-core is not installed on this machine; TestRunnerServiceFilesSetPath still runs")
	}
	service, plist, _ := renderRunnerUnits(t, "-e", `{"github_runner_env": {"PATH": "/mine:/bin", "FOO": "bar"}}`)
	assert.Contains(t, service, "\nEnvironment=PATH=/mine:/bin\n")
	assert.Contains(t, service, "\nEnvironment=FOO=bar\n")
	assert.Equal(t, 1, strings.Count(service, "Environment=PATH="))
	assert.Contains(t, plist, "<key>PATH</key>\n    <string>/mine:/bin</string>\n")
	assert.Equal(t, 1, strings.Count(plist, "<key>PATH</key>"))
}

// The Linux unit's TMPDIR is the login's runner-tmp unless the environment names one; the environment is the machine
// record's (the inventory's nova_runner_env) when no -e replaces it. The plist sets none by default.
func TestRunnerTmpdirAndTheRecordsEnvironmentRender(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("ansible-playbook"); err != nil {
		t.Skip("ansible-core is not installed on this machine; TestRunnerServiceFilesSetTmpdir still runs")
	}
	service, plist, _ := renderRunnerUnits(t)
	assert.Regexp(t, `\nEnvironment=TMPDIR=/[^\n]*/\.cache/runner-tmp\n`, service, "the login's home (its passwd entry, ansible's user_dir)")
	assert.NotContains(t, plist, "TMPDIR")
	assert.NotContains(t, service, "nova-runner-login-net", "the fleet login's runners need no table of their own")

	service, plist, _ = renderRunnerUnits(t, "-e", `{"nova_runner_env": {"TMPDIR": "/scratch/t", "ImageOS": "ubuntu24", "PATH": "/sdk/bin:/bin"}}`)
	assert.Contains(t, service, "\nEnvironment=PATH=/sdk/bin:/bin\nEnvironment=TMPDIR=/scratch/t\nEnvironment=ImageOS=ubuntu24\n")
	assert.Equal(t, 1, strings.Count(service, "TMPDIR="))
	assert.Contains(t, plist, "<key>TMPDIR</key>\n    <string>/scratch/t</string>\n")

	service, _, _ = renderRunnerUnits(t, "-e", `{"nova_runner_env": {"ImageOS": "ubuntu24"}, "github_runner_env": {"FOO": "bar"}}`)
	assert.Contains(t, service, "\nEnvironment=FOO=bar\n")
	assert.NotContains(t, service, "ImageOS", "-e github_runner_env replaces the record's for the run")

	// a runner login of its own: the unit runs as it, and its TMPDIR is in its home (a name that is never the test's own
	// login, which on a Linux CI host is ghrunner)
	service, _, _ = renderRunnerUnits(t, "-e", "github_runner_user=ghrunner-t")
	assert.Contains(t, service, "\nUser=ghrunner-t\n")
	assert.Contains(t, service, "\nEnvironment=TMPDIR=/home/ghrunner-t/.cache/runner-tmp\n")
	assert.Contains(t, service, "\nEnvironment=PATH=/opt/nova/bin:/usr/local/sbin:")
	assert.Contains(t, service, "\nRequires=nova-runner-login-net.service\nAfter=nova-runner-login-net.service\n")
}

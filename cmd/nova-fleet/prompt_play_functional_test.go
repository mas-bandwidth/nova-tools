//go:build functional

package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// prompt_play_functional_test.go runs roles/prompt on localhost through ansible-core with HOME a temp dir, and the
// login shell given as a fact override. prompt_play_test.go is the structural half.

const promptPlay = `- hosts: localhost
  connection: local
  gather_facts: true
  gather_subset: [min]
  vars_files: ["{{ fleet }}/group_vars/all.yml"]
  tasks:
    - ansible.builtin.include_role: { name: prompt }
`

func runPromptPlay(t *testing.T, home, shell string, extra ...string) (string, error) {
	t.Helper()
	if _, err := exec.LookPath("ansible-playbook"); err != nil {
		t.Skip("ansible-core is not installed on this machine; prompt_play_test.go still runs")
	}
	fleet, err := filepath.Abs(".")
	require.NoError(t, err)
	dir := t.TempDir()
	play := filepath.Join(dir, "prompt.yml")
	require.NoError(t, os.WriteFile(play, []byte(promptPlay), 0o644))
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	args := append([]string{"-i", "localhost,", "-e", "fleet=" + fleet, "-e", "nova_prompt_shell=" + shell}, extra...)
	cmd := exec.CommandContext(ctx, "ansible-playbook", append(args, play)...)
	cmd.Env = append(os.Environ(), "ANSIBLE_NOCOLOR=1", "HOME="+home, "ANSIBLE_LOCAL_TEMP="+filepath.Join(dir, "tmp"),
		"ANSIBLE_ROLES_PATH="+filepath.Join(fleet, "roles"), "TMPDIR="+dir)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func promptKind() string {
	if runtime.GOOS == "darwin" {
		return "mac"
	}
	if b, err := os.ReadFile("/proc/sys/kernel/osrelease"); err == nil && strings.Contains(strings.ToLower(string(b)), "microsoft") {
		return "wsl2"
	}
	return "linux"
}

const promptBegin = "# BEGIN nova-fleet prompt (nova-tools cmd/nova-fleet prompt.yml)\n"
const promptEnd = "# END nova-fleet prompt (nova-tools cmd/nova-fleet prompt.yml)\n"

// An existing ~/.bashrc keeps every line it had and gains the block at its end; a zsh login's ~/.zshrc too; a second
// run changes nothing; --check on a fresh HOME writes nothing.
func TestFunctionalPromptManagedBlock(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	stock := "# ~/.bashrc\ncase $- in *i*) ;; *) return;; esac\nPS1='${debian_chroot:+($debian_chroot)}\\u@\\h:\\w\\$ '\n"
	require.NoError(t, os.WriteFile(filepath.Join(home, ".bashrc"), []byte(stock), 0o600))
	out, err := runPromptPlay(t, home, "/bin/zsh")
	require.NoError(t, err, out)
	kind := promptKind()
	assert.Contains(t, out, "PROMPT host=localhost kind="+kind+" files=.bashrc,.zshrc changed")

	b, err := os.ReadFile(filepath.Join(home, ".bashrc"))
	require.NoError(t, err)
	assert.Equal(t, stock+promptBegin+`PS1='\[\033[36m\]localhost [`+kind+`] \[\033[00m\]\w # '`+"\n"+promptEnd, string(b))
	fi, err := os.Stat(filepath.Join(home, ".bashrc"))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), fi.Mode().Perm(), "the file's mode is the login's")
	z, err := os.ReadFile(filepath.Join(home, ".zshrc"))
	require.NoError(t, err)
	assert.Equal(t, promptBegin+`PS1='%F{cyan}localhost [`+kind+`] %f%~ # '`+"\n"+promptEnd, string(z))

	out, err = runPromptPlay(t, home, "/bin/zsh")
	require.NoError(t, err, out)
	assert.Contains(t, out, "changed=0", "a second run changed something")
	out, err = runPromptPlay(t, home, "/bin/zsh", "--check", "--diff")
	require.NoError(t, err, out)
	assert.Contains(t, out, "changed=0", "--check on a current machine would change something")
	b2, err := os.ReadFile(filepath.Join(home, ".bashrc"))
	require.NoError(t, err)
	assert.Equal(t, string(b), string(b2))

	fresh := t.TempDir()
	out, err = runPromptPlay(t, fresh, "/bin/bash", "--check", "--diff")
	require.NoError(t, err, out)
	assert.Contains(t, out, "files=.bashrc unchanged (check: nothing changed)")
	assert.Contains(t, out, `\w # '`, "--diff shows the block")
	_, err = os.Stat(filepath.Join(fresh, ".bashrc"))
	assert.True(t, os.IsNotExist(err), "--check wrote ~/.bashrc")
}

// The block's prompt is what an interactive bash shows: bash -ic echoes the networknext PS1.
func TestFunctionalPromptBashReadsIt(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("no bash")
	}
	home := t.TempDir()
	out, err := runPromptPlay(t, home, "/bin/bash")
	require.NoError(t, err, out)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "bash", "-ic", `echo "$PS1"`)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "TERM=xterm-256color", "PATH=" + os.Getenv("PATH")}
	got, err := cmd.Output()
	require.NoError(t, err)
	assert.Equal(t, `\[\033[36m\]localhost [`+promptKind()+`] \[\033[00m\]\w # `+"\n", string(got))
}

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// job makes a resolved job directory with a data home inside it.
func job(t *testing.T) (dir, home string) {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	home = filepath.Join(dir, "home")
	require.NoError(t, os.Mkdir(home, 0o700))
	return dir, home
}

func baseInput(dir, home string) input {
	return input{writes: []string{dir}, home: home, argv: []string{"/bin/sh", "-c", "true"}, pathEnv: "/usr/bin:/bin"}
}

func reasons(bad []refusal) []string {
	var out []string
	for _, r := range bad {
		out = append(out, r.reason)
	}
	return out
}

func TestBuildDefaults(t *testing.T) {
	t.Parallel()
	dir, home := job(t)
	p, bad := build(baseInput(dir, home))
	require.Empty(t, bad)
	assert.Equal(t, dir, p.cwd)
	assert.Equal(t, filepath.Join(dir, tmpDirName), p.tmp)
	assert.DirExists(t, p.tmp)
	assert.Equal(t, home, p.home)
	assert.Equal(t, "/bin/sh", p.argv[0][len(p.argv[0])-7:])
}

func TestBuildRefusals(t *testing.T) {
	t.Parallel()
	dir, home := job(t)
	other, _ := job(t)
	file := filepath.Join(dir, "f")
	require.NoError(t, os.WriteFile(file, nil, 0o600))
	cases := []struct {
		name   string
		mutate func(*input)
		reason string
	}{
		{"no write", func(in *input) { in.writes = nil }, "bad_write"},
		{"no command", func(in *input) { in.argv = nil }, "no_command"},
		{"relative read", func(in *input) { in.reads = []string{"rel"} }, "bad_read"},
		{"missing read", func(in *input) { in.reads = []string{filepath.Join(dir, "none")} }, "bad_read"},
		{"read is a file", func(in *input) { in.reads = []string{file} }, "bad_read"},
		{"same in read and write", func(in *input) { in.reads = []string{dir} }, "bad_read"},
		{"same in noexec and write", func(in *input) { in.readsNoExec = []string{dir} }, "bad_read"},
		{"cwd outside", func(in *input) { in.cwd = other }, "bad_cwd"},
		{"tmp outside", func(in *input) { in.tmp = other }, "bad_write"},
		{"home unset", func(in *input) { in.home = "" }, "home_outside"},
		{"home outside", func(in *input) { in.home = other }, "home_outside"},
		{"home is a read", func(in *input) { in.writes = []string{other}; in.reads = []string{dir} }, "home_outside"},
		{"command not found", func(in *input) { in.argv = []string{"no-such-command-here"} }, "not_found"},
		{"command is a file without x", func(in *input) { in.argv = []string{file} }, "not_executable"},
		{"net-allow not loopback", func(in *input) { in.netAllow = []string{"example.com:80"} }, "bad_net"},
		{"net-allow no port", func(in *input) { in.netAllow = []string{"localhost"} }, "bad_net"},
		{"gpu unknown", func(in *input) { in.gpu = "cuda" }, "bad_gpu"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			in := baseInput(dir, home)
			c.mutate(&in)
			p, bad := build(in)
			assert.Nil(t, p)
			assert.Contains(t, reasons(bad), c.reason)
		})
	}
}

func TestRefusalMakesNothing(t *testing.T) {
	t.Parallel()
	dir, home := job(t)
	in := baseInput(dir, home)
	in.argv = []string{"no-such-command-here"}
	_, bad := build(in)
	require.NotEmpty(t, bad)
	assert.NoDirExists(t, filepath.Join(dir, tmpDirName), "a refused run leaves nothing behind")
}

func TestAllNotFoundIs127(t *testing.T) {
	t.Parallel()
	dir, home := job(t)
	var out strings.Builder
	rc := run([]string{"--write", dir, "--", "no-such-command-here"}, &out, &out, []string{"HOME=" + home, "PATH=/usr/bin:/bin"})
	assert.Equal(t, 127, rc)
	rc = run([]string{"--write", "rel", "--", "no-such-command-here"}, &out, &out, []string{"HOME=" + home})
	assert.Equal(t, 125, rc)
}

func TestHomeGuardOnCommandDirectory(t *testing.T) {
	t.Parallel()
	dir, home := job(t)
	callerHome, _ := job(t)
	cmd := filepath.Join(callerHome, "x.sh")
	require.NoError(t, os.WriteFile(cmd, []byte("#!/bin/sh\n"), 0o700))
	in := baseInput(dir, home)
	in.argv = []string{cmd}
	in.homes = []string{callerHome}
	_, bad := build(in)
	assert.Contains(t, reasons(bad), "bad_read", "a command in a home directory would make the home readable")
	in.reads = []string{callerHome} // the caller's own grant
	_, bad = build(in)
	assert.Empty(t, bad)
}

func TestBadPath(t *testing.T) {
	t.Parallel()
	assert.NotEmpty(t, badPath("darwin", `/a"b`))
	assert.NotEmpty(t, badPath("darwin", `/a(b)`))
	assert.NotEmpty(t, badPath("darwin", `/a\b`))
	assert.Empty(t, badPath("linux", `/a(b)\"`), "the metacharacters matter only where the path enters profile text")
	assert.NotEmpty(t, badPath("linux", "/a\nb"))
}

func TestAncestors(t *testing.T) {
	t.Parallel()
	assert.Equal(t, []string{"/a", "/a/b"}, ancestors("/a/b/c", "/a/b/"))
	assert.Empty(t, ancestors("/a"), `"/" is granted elsewhere`)
}

func TestChildEnv(t *testing.T) {
	t.Parallel()
	got := childEnv([]string{"A=1", "TMPDIR=/tmp", "SSH_AUTH_SOCK=/s", "SSH_AGENT_PID=1", "GPG_AGENT_INFO=x",
		"FOO_AGENT_SOCK=y", "AI_AGENT=1", "JAVA_TOOL_OPTIONS=-Xmx1g"}, "/w/tmp")
	assert.Contains(t, got, "A=1")
	assert.Contains(t, got, "AI_AGENT=1", "a name that addresses no agent passes")
	for _, gone := range []string{"SSH_AUTH_SOCK=/s", "SSH_AGENT_PID=1", "GPG_AGENT_INFO=x", "FOO_AGENT_SOCK=y", "TMPDIR=/tmp"} {
		assert.NotContains(t, got, gone)
	}
	assert.Contains(t, got, "TMPDIR=/w/tmp")
	assert.Contains(t, got, "TMPPREFIX=/w/tmp/zsh")
	assert.Contains(t, got, "JAVA_TOOL_OPTIONS=-Xmx1g -Djava.io.tmpdir=/w/tmp")
}

func TestParseArgs(t *testing.T) {
	t.Parallel()
	in, bad := parseArgs([]string{"--read", "/r", "--read", "/r2", "--read-noexec", "/n", "--write", "/w", "--cwd", "/w/c",
		"--tmp", "/w/t", "--net-deny", "--net-allow", "localhost:1", "--gpu", "metal", "--", "ls", "--read", "-l"})
	require.Empty(t, bad)
	assert.Equal(t, []string{"/r", "/r2"}, in.reads)
	assert.Equal(t, []string{"/n"}, in.readsNoExec)
	assert.Equal(t, []string{"/w"}, in.writes)
	assert.True(t, in.netDeny)
	assert.Equal(t, []string{"ls", "--read", "-l"}, in.argv, "everything after -- is the command, verbatim")

	_, bad = parseArgs([]string{"--write", "/w", "--bogus", "--", "ls"})
	assert.Equal(t, []string{"bad_flag"}, reasons(bad))
	_, bad = parseArgs([]string{"--write", "/w", "ls"})
	assert.Contains(t, reasons(bad), "bad_flag")
	assert.Contains(t, reasons(bad), "no_command")
	_, bad = parseArgs([]string{"--write"})
	assert.NotEmpty(t, bad)
}

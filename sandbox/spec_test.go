package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func reasons(bad []refusal) []string {
	var out []string
	for _, r := range bad {
		out = append(out, r.reason)
	}
	return out
}

func TestParseArgs(t *testing.T) {
	t.Parallel()
	s, bad := parseArgs([]string{"--read", "/r", "--read", "/r2", "--write", "/w", "--net-deny",
		"--net-allow", "localhost:1", "--gpu", "--unix", "--keep-fds", "--", "ls", "--read", "-l"})
	require.Empty(t, bad)
	assert.Equal(t, spec{reads: []string{"/r", "/r2"}, writes: []string{"/w"}, netAllow: []string{"localhost:1"},
		netDeny: true, gpu: true, unix: true, keepFDs: true, argv: []string{"ls", "--read", "-l"}}, s, "everything after -- is the command, verbatim")

	for _, c := range []struct {
		args []string
		want []string
	}{
		{[]string{"--write", "/w", "--bogus", "--", "ls"}, []string{"bad_flag"}},
		{[]string{"--cwd", "/w", "--", "ls"}, []string{"bad_flag", "bad_flag"}}, // a removed flag and its value
		{[]string{"--write", "/w", "ls"}, []string{"bad_flag", "no_command"}},
		{[]string{"--write"}, []string{"bad_flag", "no_command"}},
		{[]string{"--write", "/w", "--"}, []string{"no_command"}},
		{nil, []string{"no_command"}},
	} {
		_, bad := parseArgs(c.args)
		assert.Equal(t, c.want, reasons(bad), c.args)
	}
}

// dirs is a resolved scratch directory with a and b inside it.
func dirs(t *testing.T) (a, b string) {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	a, b = filepath.Join(root, "a"), filepath.Join(root, "b")
	require.NoError(t, os.Mkdir(a, 0o700))
	require.NoError(t, os.Mkdir(b, 0o700))
	return a, b
}

func TestBuild(t *testing.T) {
	t.Parallel()
	a, b := dirs(t)
	w, bad := build(spec{reads: []string{a}, writes: []string{b}, netDeny: true, netAllow: []string{"127.0.0.1:8080", "localhost:9"},
		argv: []string{"/bin/sh", "-c", "true"}}, "linux")
	require.Empty(t, bad)
	assert.Equal(t, []string{a}, w.reads)
	assert.Equal(t, []string{b}, w.writes)
	assert.Equal(t, []uint16{8080, 9}, w.netPorts)
	assert.True(t, filepath.IsAbs(w.command))
	assert.Equal(t, []string{"/bin/sh", "-c", "true"}, w.argv, "argv is verbatim; only w.command is resolved")

	_, bad = build(spec{argv: []string{"/bin/sh"}}, "linux")
	assert.Empty(t, bad, "no --write is a read-only run, not a refusal")
}

func TestBuildRefusals(t *testing.T) {
	t.Parallel()
	a, _ := dirs(t)
	file := filepath.Join(a, "f")
	require.NoError(t, os.WriteFile(file, nil, 0o600))
	script := filepath.Join(a, "x.sh")
	require.NoError(t, os.WriteFile(script, []byte("#!/bin/sh\n"), 0o700))
	cases := []struct {
		name   string
		goos   string
		s      spec
		reason string
	}{
		{"relative read", "linux", spec{reads: []string{"rel"}}, "bad_read"},
		{"missing write", "linux", spec{writes: []string{filepath.Join(a, "none")}}, "bad_write"},
		{"write is a file", "linux", spec{writes: []string{file}}, "bad_write"},
		{"net-allow not loopback", "linux", spec{netDeny: true, netAllow: []string{"example.com:80"}}, "bad_net"},
		{"net-allow no port", "linux", spec{netDeny: true, netAllow: []string{"localhost"}}, "bad_net"},
		{"net-allow port 0", "linux", spec{netDeny: true, netAllow: []string{"localhost:0"}}, "bad_net"},
		{"net-allow without net-deny", "linux", spec{netAllow: []string{"localhost:1"}}, "bad_net"},
		{"gpu off macOS", "linux", spec{gpu: true}, "bad_gpu"},
		{"command not found", "linux", spec{argv: []string{"no-such-command-here"}}, "not_found"},
		{"command not executable", "linux", spec{argv: []string{file}}, "not_executable"},
		{"command outside every grant", "linux", spec{argv: []string{script}}, "bad_read"},
		{"profile metacharacter", "darwin", spec{reads: []string{mkdir(t, a, `q"uote`)}}, "bad_read"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if c.s.argv == nil {
				c.s.argv = []string{"/bin/sh"}
			}
			w, bad := build(c.s, c.goos)
			assert.Nil(t, w)
			assert.Contains(t, reasons(bad), c.reason)
		})
	}
	w, bad := build(spec{reads: []string{a}, argv: []string{script}}, "linux")
	require.Empty(t, bad, "naming the command's directory is the caller's grant")
	assert.Equal(t, script, w.command)
}

func mkdir(t *testing.T, parent, name string) string {
	t.Helper()
	d := filepath.Join(parent, name)
	require.NoError(t, os.Mkdir(d, 0o700))
	return d
}

func TestBadPath(t *testing.T) {
	t.Parallel()
	for _, p := range []string{`/a"b`, `/a(b)`, `/a\b`} {
		assert.NotEmpty(t, badPath("darwin", p), p)
	}
	assert.Empty(t, badPath("linux", `/a(b)\"`), "the metacharacters matter only where the path enters profile text")
	assert.NotEmpty(t, badPath("linux", "/a\nb"))
}

func TestExitCodes(t *testing.T) {
	t.Parallel()
	var out strings.Builder
	env := []string{"PATH=/usr/bin:/bin"}
	assert.Equal(t, 0, run([]string{"help"}, &out, &out, env))
	assert.Contains(t, out.String(), "--net-allow")
	assert.Equal(t, 125, run(nil, &out, &out, env))
	assert.Equal(t, 125, run([]string{"--bogus", "--", "ls"}, &out, &out, env))
	assert.Equal(t, 127, run([]string{"--", "no-such-command-here"}, &out, &out, env))
	nox := filepath.Join(t.TempDir(), "noexec")
	require.NoError(t, os.WriteFile(nox, []byte("x"), 0o600))
	assert.Equal(t, 126, run([]string{"--", nox}, &out, &out, env), "a file that is not executable is 126")
	out.Reset()
	assert.Equal(t, 125, run([]string{"--write", "rel", "--", "no-such-command-here"}, &out, &out, env), "a refusal beside not_found is 125")
	assert.Equal(t, 2, strings.Count(out.String(), "SANDBOX REFUSED "), "every problem is reported at once")
}

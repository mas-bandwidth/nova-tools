package main

import (
	"bytes"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestVersionLineRetainsBuildIdentity(t *testing.T) {
	t.Parallel()
	saved := version
	t.Cleanup(func() { version = saved })
	version = "v1.2.3-rc1+build.7"

	var stdout, stderr bytes.Buffer
	code := cmdVersion(nil, &stdout, &stderr)
	require.Equal(t, 0, code, "cmdVersion exit = %d, stderr = %s", code, stderr.String())
	require.Equal(t, 0, stderr.Len(), "cmdVersion wrote stderr: %q", stderr.String())
	line := stdout.String()
	require.Equal(t, 1, strings.Count(line, "\n"), "version is not one line: %q", line)
	fields := strings.Fields(strings.TrimSuffix(line, "\n"))
	require.Len(t, fields, 4, "version fields = %d, want 4: %q", len(fields), line)
	require.Equal(t, "nova-secrets", fields[0], "version identity = %q, want nova-secrets %q", line, version)
	require.Equal(t, version, fields[1], "version identity = %q, want nova-secrets %q", line, version)
	require.Equal(t, runtime.GOOS+"/"+runtime.GOARCH, fields[2], "version platform = %q", line)
	require.Equal(t, runtime.Version(), fields[3], "version platform = %q", line)
}

func TestVersionVerbsNeedNoSecretsSetup(t *testing.T) {
	t.Parallel()
	bin := buildNovaSecrets(t)
	for _, verb := range []string{"version", "--version"} {
		t.Run(verb, func(t *testing.T) {
			stdout, stderr, code := runNovaSecrets(bin, verb)
			require.Equal(t, 0, code, "%s exit=%d stderr=%q", verb, code, stderr)
			require.Empty(t, stderr, "%s exit=%d stderr=%q", verb, code, stderr)
			fields := strings.Fields(strings.TrimSpace(stdout))
			require.Len(t, fields, 4, "%s output = %q", verb, stdout)
			require.Equal(t, "nova-secrets", fields[0], "%s output = %q", verb, stdout)
			require.NotEmpty(t, fields[1], "%s output = %q", verb, stdout)
		})
	}
}

func TestVersionRefusesArguments(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	code := cmdVersion([]string{"--store", "/never-opened"}, &stdout, &stderr)
	require.Equal(t, 2, code, "cmdVersion exit = %d, want 2", code)
	require.Equal(t, 0, stdout.Len(), "cmdVersion refusal stdout=%q stderr=%q", stdout.String(), stderr.String())
	require.Contains(t, stderr.String(), "takes no flags and no arguments", "cmdVersion refusal stdout=%q stderr=%q", stdout.String(), stderr.String())
}

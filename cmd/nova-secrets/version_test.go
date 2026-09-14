package main

import (
	"bytes"
	"runtime"
	"strings"
	"testing"
)

func TestVersionLineRetainsBuildIdentity(t *testing.T) {
	saved := version
	t.Cleanup(func() { version = saved })
	version = "v1.2.3-rc1+build.7"

	var stdout, stderr bytes.Buffer
	if code := cmdVersion(nil, &stdout, &stderr); code != 0 {
		t.Fatalf("cmdVersion exit = %d, stderr = %s", code, stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("cmdVersion wrote stderr: %q", stderr.String())
	}
	line := stdout.String()
	if strings.Count(line, "\n") != 1 {
		t.Fatalf("version is not one line: %q", line)
	}
	fields := strings.Fields(strings.TrimSuffix(line, "\n"))
	if len(fields) != 4 {
		t.Fatalf("version fields = %d, want 4: %q", len(fields), line)
	}
	if fields[0] != "nova-secrets" || fields[1] != version {
		t.Fatalf("version identity = %q, want nova-secrets %q", line, version)
	}
	if fields[2] != runtime.GOOS+"/"+runtime.GOARCH || fields[3] != runtime.Version() {
		t.Fatalf("version platform = %q", line)
	}
}

func TestVersionVerbsNeedNoSecretsSetup(t *testing.T) {
	bin := buildNovaSecrets(t)
	for _, verb := range []string{"version", "--version"} {
		t.Run(verb, func(t *testing.T) {
			stdout, stderr, code := runNovaSecrets(bin, verb)
			if code != 0 || stderr != "" {
				t.Fatalf("%s exit=%d stderr=%q", verb, code, stderr)
			}
			fields := strings.Fields(strings.TrimSpace(stdout))
			if len(fields) != 4 || fields[0] != "nova-secrets" || fields[1] == "" {
				t.Fatalf("%s output = %q", verb, stdout)
			}
		})
	}
}

func TestVersionRefusesArguments(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := cmdVersion([]string{"--store", "/never-opened"}, &stdout, &stderr); code != 2 {
		t.Fatalf("cmdVersion exit = %d, want 2", code)
	}
	if stdout.Len() != 0 || !strings.Contains(stderr.String(), "takes no flags and no arguments") {
		t.Fatalf("cmdVersion refusal stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
}

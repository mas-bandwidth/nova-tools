package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func takeArgs(dist, out, runner string) []string {
	return []string{"--os", runner, "--tool", "nova-check", "--stamp", "v0.0.0-dry-run", "--dist", dist, "--out", out}
}

func TestTakeShippedCopiesThePlatformBinaryAndRunsVersion(t *testing.T) {
	t.Parallel()
	for runner, name := range map[string]string{
		"ubuntu-latest":  "nova-check_v0.0.0-dry-run_linux_amd64",
		"macos-latest":   "nova-check_v0.0.0-dry-run_darwin_arm64",
		"windows-latest": "nova-check_v0.0.0-dry-run_windows_amd64.exe",
	} {
		t.Run(runner, func(t *testing.T) {
			t.Parallel()
			dist := t.TempDir()
			for _, n := range []string{"nova-check_v0.0.0-dry-run_linux_amd64", "nova-check_v0.0.0-dry-run_darwin_arm64", "nova-check_v0.0.0-dry-run_windows_amd64.exe"} {
				if err := os.WriteFile(filepath.Join(dist, n), []byte("bin:"+n), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			out := filepath.Join(t.TempDir(), "sub", "nova-check")
			var o, eb bytes.Buffer
			e := env{stdout: &o, stderr: &eb, getenv: func(string) string { return "" }}
			r := &fakeCmdRunner{}
			if code := takeShipped(e, r, takeArgs(dist, out, runner)); code != 0 {
				t.Fatalf("exit %d stdout %q stderr %q", code, o.String(), eb.String())
			}
			if b, _ := os.ReadFile(out); string(b) != "bin:"+name {
				t.Fatalf("copied %q, want the %s binary", b, name)
			}
			if fi, _ := os.Stat(out); fi.Mode().Perm()&0o100 == 0 {
				t.Fatalf("mode %v is not executable", fi.Mode())
			}
			if got := r.lines(); len(got) != 1 || got[0] != out+" version" {
				t.Fatalf("commands %q", got)
			}
		})
	}
}

func TestTakeShippedNeverPaperOverAMissingBinary(t *testing.T) {
	t.Parallel()
	dist := t.TempDir()
	if err := os.WriteFile(filepath.Join(dist, "other"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	var o, eb bytes.Buffer
	e := env{stdout: &o, stderr: &eb, getenv: func(string) string { return "" }}
	r := &fakeCmdRunner{}
	out := filepath.Join(t.TempDir(), "nova-check")
	if code := takeShipped(e, r, takeArgs(dist, out, "ubuntu-latest")); code != 1 {
		t.Fatalf("exit %d, want 1", code)
	}
	if !strings.Contains(o.String(), "nova-check_v0.0.0-dry-run_linux_amd64 is not in the artifact") || !strings.Contains(o.String(), "other") {
		t.Fatalf("stdout %q does not name the missing binary and list the artifact", o.String())
	}
	if _, err := os.Stat(out); err == nil {
		t.Fatal("a binary was put in place with none shipped")
	}
	if len(r.calls) != 0 {
		t.Fatalf("ran %q", r.lines())
	}
}

func TestTakeShippedRefusesARunnerWithNoTarget(t *testing.T) {
	t.Parallel()
	var o, eb bytes.Buffer
	e := env{stdout: &o, stderr: &eb, getenv: func(string) string { return "" }}
	if code := takeShipped(e, &fakeCmdRunner{}, takeArgs(t.TempDir(), "x", "plan9-latest")); code != 1 || !strings.Contains(o.String(), "no shipped target mapped for plan9-latest") {
		t.Fatalf("exit %d stdout %q", code, o.String())
	}
}

func TestTakeShippedUsageRefusals(t *testing.T) {
	t.Parallel()
	for name, args := range map[string][]string{
		"no flags":     nil,
		"unknown flag": {"--nope", "x"},
		"no value":     {"--os"},
		"missing out":  {"--os", "ubuntu-latest", "--tool", "t", "--stamp", "s", "--dist", "d"},
	} {
		var o, eb bytes.Buffer
		e := env{stdout: &o, stderr: &eb, getenv: func(string) string { return "" }}
		if code := takeShipped(e, &fakeCmdRunner{}, args); code != 2 {
			t.Errorf("%s: exit %d, want 2", name, code)
		}
	}
}

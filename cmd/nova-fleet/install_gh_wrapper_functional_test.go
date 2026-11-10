//go:build functional

package main

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// install_gh_wrapper_functional_test.go runs the install play from a local build on localhost (a runner, so no
// opencode) with a file at <bin>/gh, and checks what the play leaves there: the v1 wrapper goes, a real gh stays, and a
// wrapper a git credential helper names stays with a GH-WRAPPER KEPT line. install_gh_wrapper_test.go is the
// structural half.

const v1GhWrapper = "#!/usr/bin/env bash\n# Written by rowan-tools fleet/push-credential.yml\n" +
	"exec nova-secrets exec --only GH_PUSH_TOKEN -- /bin/sh -c 'GH_TOKEN=$GH_PUSH_TOKEN exec \"$0\" \"$@\"' /usr/bin/gh \"$@\"\n"

func TestInstallGhWrapper(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("ansible-playbook"); err != nil {
		t.Skip("ansible-core is not installed on this machine")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed on this machine")
	}
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("the local mode copies to Linux and macOS")
	}
	bin := buildFleet(t)
	dist := t.TempDir()
	asset := "nova-fleet_local_" + runtime.GOOS + "_" + runtime.GOARCH
	b, err := os.ReadFile(bin)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dist, asset), b, 0o755); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(b)
	if err := os.WriteFile(filepath.Join(dist, "SHA256SUMS"), []byte(hex.EncodeToString(sum[:])+"  "+asset+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	inv := filepath.Join(t.TempDir(), "hosts.ini")
	if err := os.WriteFile(inv, []byte("[runner]\nlocalhost ansible_connection=local ansible_python_interpreter=auto_silent\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	for _, c := range []struct {
		name, gh, helper string
		kept             bool
		says             string
	}{
		{name: "the v1 wrapper goes", gh: v1GhWrapper},
		{name: "a real gh stays", gh: "#!/bin/sh\necho gh version 2.46.0\n", kept: true},
		{name: "a wrapper a helper names stays", gh: v1GhWrapper, helper: "!{{gh}} auth git-credential", kept: true, says: "GH-WRAPPER KEPT host=localhost"},
	} {
		home := t.TempDir()
		binDir := filepath.Join(home, ".local", "bin")
		if err := os.MkdirAll(binDir, 0o755); err != nil {
			t.Fatal(err)
		}
		gh := filepath.Join(binDir, "gh")
		if err := os.WriteFile(gh, []byte(c.gh), 0o755); err != nil {
			t.Fatal(err)
		}
		if c.helper != "" {
			cfg := "[credential \"https://github.com\"]\n\thelper = " + strings.ReplaceAll(c.helper, "{{gh}}", gh) + "\n"
			if err := os.WriteFile(filepath.Join(home, ".gitconfig"), []byte(cfg), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		code, out, errs := runFleet(t, bin, "install", "--inventory", inv, "--", "-e", "nova_tools_version=local", "-e", "nova_tools_local_dir="+dist,
			"-e", "nova_bin_dir="+binDir, "-e", "nova_home="+home, "-e", `{"nova_tools_names":["nova-fleet"]}`)
		if code != 0 {
			t.Fatalf("%s: install: exit %d\n%s\n%s", c.name, code, out, errs)
		}
		got, err := os.ReadFile(gh)
		switch {
		case c.kept && (err != nil || string(got) != c.gh):
			t.Errorf("%s: <bin>/gh was changed or removed (%v)", c.name, err)
		case !c.kept && !os.IsNotExist(err):
			t.Errorf("%s: <bin>/gh is still there (%v)", c.name, err)
		}
		if c.says != "" && !strings.Contains(out, c.says) {
			t.Errorf("%s: the run does not say %q:\n%s", c.name, c.says, out)
		}
	}
}

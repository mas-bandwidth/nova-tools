package main

import (
	"bytes"
	"os/exec"
	"strings"
	"testing"
)

// runGit was publish_test.go's helper; native_stage_test.go still uses it after publish went (2026-09-24).
func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	var cmd *exec.Cmd
	if dir != "" {
		cmd = exec.Command("git", append([]string{"-C", dir}, args...)...)
	} else {
		cmd = exec.Command("git", args...)
	}
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, errb.String())
	}
	return out.String()
}

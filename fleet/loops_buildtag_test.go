package fleet

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestFleetTestsUsingSyscallStatAreUnixTagged(t *testing.T) {
	t.Parallel()

	names, err := filepath.Glob("*_test.go")
	require.NoError(t, err)
	targets := []string{"syscall.Stat_t", "syscall.Getuid", "syscall.Umask", "syscall.Mkfifo"}
	for _, name := range names {
		if name == "loops_buildtag_test.go" {
			continue
		}
		b, err := os.ReadFile(name)
		require.NoError(t, err)
		src := string(b)
		mentions := false
		for _, target := range targets {
			if strings.Contains(src, target) {
				mentions = true
				break
			}
		}
		if !mentions {
			continue
		}
		excluded := false
		for _, line := range strings.Split(src, "\n") {
			if strings.HasPrefix(line, "package ") {
				break
			}
			if strings.HasPrefix(line, "//go:build") && (strings.Contains(line, "unix") || strings.Contains(line, "!windows") || strings.Contains(line, "linux")) {
				excluded = true
			}
		}
		require.Truef(t, excluded, "%s mentions %v but carries no windows-excluding //go:build line before its package clause", name, targets)
	}
}

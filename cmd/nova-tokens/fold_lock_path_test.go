package main

import (
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"os"
	"path/filepath"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/tokens"
)

func TestFoldRefusesLinkedLockBeforeWriting(t *testing.T) {
	t.Parallel()
	for _, missing := range []bool{false, true} {
		name := "existing-target"
		if missing {
			name = "missing-target"
		}
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			out := mkdir(t, filepath.Join(dir, "out"))
			source := mkdir(t, filepath.Join(dir, "transcripts"))
			target := filepath.Join(dir, "unrelated")
			const body = "unrelated data must survive\n"
			if !missing {
				write(t, target, body)
			}
			link := filepath.Join(out, tokens.LockName)
			{
				err := os.Symlink(target, link)
				require.NoError(t, err, err)
			}
			result := invoke(t, "fold", "--out", out, "--day", "2026-09-11", "--repos", reposFile(t, dir), "--claude", "fixture="+source)
			wantExit(t, result, 2)
			wantContains(t, result.stderr, "symlink")
			wantContains(t, result.stderr, "fold.lock")
			raw, err := os.ReadFile(target)
			if missing {
				assert.True(t, os.IsNotExist(err), "target created: %q (%v)", raw, err)
			} else if err != nil || string(raw) != body {
				assert.Failf(t, "symlink target changed", "target changed: %q (%v)", raw, err)
			}
			entries, err := os.ReadDir(out)
			if err != nil || len(entries) != 1 || entries[0].Name() != tokens.LockName {
				assert.Failf(t, "unexpected output entries", "unexpected output entries: %v (%v)", entries, err)
			}
			info, err := os.Lstat(link)
			if err != nil || info == nil || info.Mode()&os.ModeSymlink == 0 {
				assert.Failf(t, "symlink changed", "link changed: %v (%v)", info, err)
			}
		})
	}
}

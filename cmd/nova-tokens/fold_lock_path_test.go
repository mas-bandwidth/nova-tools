package main

import (
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
			if err := os.Symlink(target, link); err != nil {
				t.Fatal(err)
			}
			result := invoke(t, "fold", "--out", out, "--day", "2026-09-11", "--repos", reposFile(t, dir), "--claude", "fixture="+source)
			wantExit(t, result, 2)
			wantContains(t, result.stderr, "symlink")
			wantContains(t, result.stderr, "fold.lock")
			raw, err := os.ReadFile(target)
			if missing {
				if !os.IsNotExist(err) {
					t.Errorf("target created: %q (%v)", raw, err)
				}
			} else if err != nil || string(raw) != body {
				t.Errorf("target changed: %q (%v)", raw, err)
			}
			entries, err := os.ReadDir(out)
			if err != nil || len(entries) != 1 || entries[0].Name() != tokens.LockName {
				t.Errorf("unexpected output entries: %v (%v)", entries, err)
			}
			info, err := os.Lstat(link)
			if err != nil || info.Mode()&os.ModeSymlink == 0 {
				t.Errorf("link changed: %v (%v)", info, err)
			}
		})
	}
}

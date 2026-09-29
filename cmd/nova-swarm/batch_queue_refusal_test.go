package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBatchRefusesDirectoryQueueBeforeWriting(t *testing.T) {
	t.Parallel()
	for _, flag := range []string{"pool", "tasks", "files", "label", "template", "max-input"} {
		t.Run(flag, func(t *testing.T) {
			root := t.TempDir()
			pool, tasks := filepath.Join(root, "pool"), filepath.Join(root, "tasks")
			for _, dir := range []string{pool, tasks} {
				if err := os.Mkdir(dir, 0700); err != nil {
					t.Fatal(err)
				}
			}
			card := filepath.Join(tasks, "one.md")
			const body = "One bounded task.\n"
			if err := os.WriteFile(card, []byte(body), 0600); err != nil {
				t.Fatal(err)
			}
			args := []string{"batch", "--" + flag, "1"}
			if flag == "pool" {
				args = []string{"batch", "--pool", pool, "--tasks", tasks, "--files", "1", "--tokens", "unmetered"}
			}
			code, stdout, stderr := runSwarm(t, args...)
			if code != 2 || stdout != "" || !strings.Contains(stderr, "flag provided but not defined") || !strings.Contains(stderr, "run: nova-swarm help") {
				t.Errorf("exit=%d stdout=%q stderr=%q; want unknown flag refusal", code, stdout, stderr)
			}
			entries, err := os.ReadDir(pool)
			if err != nil || len(entries) != 0 {
				t.Errorf("pool changed: %v, %v", entries, err)
			}
			raw, err := os.ReadFile(card)
			if err != nil || string(raw) != body {
				t.Errorf("task changed: %q, %v", raw, err)
			}
		})
	}
}

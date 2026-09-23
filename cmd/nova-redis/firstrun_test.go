package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/onboarding"
	"github.com/redis/go-redis/v9"
)

// TestFirstRunTranscriptIsWhatTheToolPrints runs every `$` line of the
// `### First run` under `## nova-redis` in docs/TESTS.md through run() and
// compares what it prints, word for word. The dial seam fails the test if it
// is reached: both lines are refusals made before the instance is dialled,
// which is the promise the transcript documents.
func TestFirstRunTranscriptIsWhatTheToolPrints(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "TESTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	lines, err := onboarding.FirstRun(string(raw), "nova-redis")
	if err != nil {
		t.Fatal(err)
	}
	d := deps{
		now: func() time.Time { return time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC) },
		dial: func(addr, password string) redis.Cmdable {
			t.Fatalf("a first-run refusal dialled %s; a refused write must never reach the instance", addr)
			return nil
		},
		getenv: func(string) string { return "" },
	}
	ran := 0
	for i := 0; i < len(lines); i++ {
		cmd, ok := strings.CutPrefix(lines[i], "$ nova-redis ")
		if !ok {
			continue
		}
		var want []string
		for j := i + 1; j < len(lines) && !strings.HasPrefix(lines[j], "$ "); j++ {
			if strings.TrimSpace(lines[j]) != "" {
				want = append(want, lines[j])
			}
		}
		var out, errb bytes.Buffer
		run(strings.Fields(cmd), &out, &errb, d)
		got := strings.TrimRight(out.String()+errb.String(), "\n")
		if got != strings.Join(want, "\n") {
			t.Errorf("`nova-redis %s` printed\n%s\nthe transcript says\n%s", cmd, got, strings.Join(want, "\n"))
		}
		ran++
	}
	if ran == 0 {
		t.Fatal("the nova-redis first run holds no `$ nova-redis` line; this test checked nothing")
	}
}

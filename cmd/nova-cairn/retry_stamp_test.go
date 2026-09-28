package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A retry is a receipt for the existing entry, not a new event. Exercise both
// store shapes through the CLI, with a clock different from the original append.
func TestAppendRetryReportsTheStoredStamp(t *testing.T) {
	t.Parallel()
	for _, bench := range []bool{false, true} {
		name := "nested"
		if bench {
			name = "bench"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			store := t.TempDir()
			path := filepath.Join(store, "entries", "s", "e.json")
			wantStamp := "2026-09-28T01:02:03.456Z"
			if bench {
				path = filepath.Join(store, "s.md")
				if err := os.WriteFile(path, []byte("# Session s\n"), 0600); err != nil {
					t.Fatal(err)
				}
				// Bench headings store whole seconds; the receipt must match that record.
				wantStamp = "2026-09-28T01:02:03Z"
			} else {
				runOK(t, "", "open", "--store", store, "--session", "s", "--publish", "manual", "--source", "original-session-source")
			}
			args := []string{"append", "--store", store, "--session", "s", "--entry", "e", "--text", "the original words", "--publish", "manual", "--now"}
			first, _ := runOK(t, "", append(append([]string{}, args...), "2026-09-28T01:02:03.456Z")...)
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			retryArgs := append(append([]string{}, args...), "2026-09-29T04:05:06Z")
			if !bench {
				retryArgs = append(retryArgs, "--source", "different-retry-source")
			}
			retry, _ := runOK(t, "", retryArgs...)
			for _, output := range []string{first, retry} {
				if !bench && !strings.Contains(output, "source=original-session-source ") {
					t.Errorf("receipt lost the stored source: %s", output)
				}
				if !strings.Contains(output, "stamp="+wantStamp+"\n") {
					t.Errorf("receipt does not report the stored stamp %s: %s", wantStamp, output)
				}
			}
			if !strings.Contains(retry, "duplicate=true") {
				t.Errorf("retry: %s", retry)
			}
			after, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if string(after) != string(before) {
				t.Fatal("retry changed the stored entry")
			}
			// Corrupt persisted time cannot be replaced by this invocation's clock.
			broken := strings.Replace(string(after), wantStamp, "2026-99-28T01:02:03Z", 1)
			if broken == string(after) {
				t.Fatal("fixture did not replace the stored stamp")
			}
			if err := os.WriteFile(path, []byte(broken), 0600); err != nil {
				t.Fatal(err)
			}
			code, out, errOut := runCode("", append(append([]string{}, args...), "2026-09-30T04:05:06Z")...)
			if code != 2 || out != "" || !strings.Contains(errOut, "invalid stamp") {
				t.Fatalf("corrupt stored stamp: code=%d out=%q err=%q", code, out, errOut)
			}
			kept, err := os.ReadFile(path)
			if err != nil || string(kept) != broken {
				t.Fatalf("refusal changed stored entry: %v", err)
			}
		})
	}
}

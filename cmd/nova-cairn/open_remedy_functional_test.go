//go:build functional && !windows

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestAppendOpenRemedyRoundTripsThroughShell(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, store, session string }{
		{"plain", "store", "s"},
		{"quotes", "my store's $HOME `literal`", "s'$HOME"},
		{"controls", "store\t\n", "s\u202eend"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			store := filepath.Join(root, tc.store)
			code, _, errOut := runCode("", "append", "--store", store, "--session", tc.session, "--entry", "e", "--text", "note", "--publish", "never")
			if code != 2 || strings.Count(errOut, "\n") != 1 {
				t.Fatalf("append=%d %q", code, errOut)
			}
			_, remedy, ok := strings.Cut(errOut, "open first: ")
			if !ok || !strings.HasSuffix(remedy, "; run: nova-cairn help\n") {
				t.Fatalf("no remedy: %q", errOut)
			}
			remedy = strings.TrimSuffix(remedy, "; run: nova-cairn help\n")
			stub := filepath.Join(root, "nova-cairn")
			if err := os.WriteFile(stub, []byte("#!/bin/sh\nprintf '%s\\000' \"$@\"\n"), 0700); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command("/bin/sh", "-c", remedy)
			cmd.Dir = root
			cmd.Env = []string{"PATH=" + root}
			raw, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("shell: %v: %s", err, raw)
			}
			args := strings.Split(strings.TrimSuffix(string(raw), "\x00"), "\x00")
			want := []string{"open", "--store", store, "--session", tc.session, "--publish", "never"}
			if !reflect.DeepEqual(args, want) {
				t.Fatalf("argv=%q want=%q", args, want)
			}
			runOK(t, "", args...)
			if _, err := os.Stat(filepath.Join(store, "sessions", tc.session+".md")); err != nil {
				t.Fatalf("session not opened: %v", err)
			}
		})
	}
}

// The remedy the refusal prints, run through a shell on a bench store, opens
// the named session as <id>.md and creates nothing else.
func TestAppendOpenRemedyOnABenchStoreCreatesOnlyTheSessionFile(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	store := filepath.Join(root, "cairns")
	if err := os.Mkdir(store, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"a", "b"} {
		if err := os.WriteFile(filepath.Join(store, id+".md"), []byte("# "+id+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	code, _, errOut := runCode("", "append", "--store", store, "--session", "NEW", "--entry", "e", "--text", "note", "--publish", "manual")
	_, remedy, ok := strings.Cut(errOut, "open first: ")
	if code != 2 || !ok {
		t.Fatalf("append=%d %q", code, errOut)
	}
	remedy = strings.TrimSuffix(remedy, "; run: nova-cairn help\n")
	args := strings.Fields(strings.ReplaceAll(remedy, "'", ""))[1:] // open --store <dir> --session NEW --publish manual
	runOK(t, "", args...)
	entries, err := os.ReadDir(store)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, e := range entries {
		got = append(got, e.Name())
	}
	if !reflect.DeepEqual(got, []string{"NEW.md", "a.md", "b.md"}) {
		t.Fatalf("store after the remedy: %v", got)
	}
}

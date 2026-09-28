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

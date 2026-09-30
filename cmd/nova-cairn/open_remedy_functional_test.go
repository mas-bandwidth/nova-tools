//go:build functional && !windows

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
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
			require.Equal(t, 2, code, "append=%d %q", code, errOut)
			require.Equal(t, 1, strings.Count(errOut, "\n"), "append=%d %q", code, errOut)
			_, remedy, ok := strings.Cut(errOut, "open first: ")
			require.True(t, ok, "no remedy: %q", errOut)
			require.True(t, strings.HasSuffix(remedy, "; run: nova-cairn help\n"), "no remedy: %q", errOut)
			remedy = strings.TrimSuffix(remedy, "; run: nova-cairn help\n")
			stub := filepath.Join(root, "nova-cairn")
			require.NoError(t, os.WriteFile(stub, []byte("#!/bin/sh\nprintf '%s\\000' \"$@\"\n"), 0700))
			cmd := exec.Command("/bin/sh", "-c", remedy)
			cmd.Dir = root
			cmd.Env = []string{"PATH=" + root}
			raw, err := cmd.CombinedOutput()
			require.NoError(t, err, "shell: %v: %s", err, raw)
			args := strings.Split(strings.TrimSuffix(string(raw), "\x00"), "\x00")
			want := []string{"open", "--store", store, "--session", tc.session, "--publish", "never"}
			require.Equal(t, want, args, "argv=%q want=%q", args, want)
			runOK(t, "", args...)
			_, err = os.Stat(filepath.Join(store, "sessions", tc.session+".md"))
			require.NoError(t, err, "session not opened")
		})
	}
}

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReadCommandsRefuseMissingStoreAndSession(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	for _, tc := range []struct{ store, session, want string }{
		{filepath.Join(root, "missing"), "s", "store"},
		{root, "missing", "session"},
		{root, "../escape", "session"},
	} {
		for _, verb := range []string{"index", "receipt"} {
			args := []string{verb, "--store", tc.store, "--session", tc.session}
			if verb == "receipt" {
				args = append(args, "--entry", "e")
			}
			code, out, errOut := runCode("", args...)
			if code != 2 || out != "" || !strings.Contains(errOut, tc.want) {
				t.Errorf("%q exit=%d out=%q err=%q", args, code, out, errOut)
			}
		}
	}
	code, out, errOut := runCode("", "index", "--store", filepath.Join(root, "missing"))
	if code != 2 || out != "" || !strings.Contains(errOut, "store") {
		t.Errorf("missing unfiltered store: %d %q %q", code, out, errOut)
	}
	out, _ = runOK(t, "", "index", "--store", root)
	require.Contains(t, out, "sessions=0 entries=0", "empty existing store: %q", out)
	runOK(t, "", "open", "--store", root, "--session", "empty", "--publish", "never")
	out, _ = runOK(t, "", "index", "--store", root, "--session", "empty")
	require.Contains(t, out, "entries=0", "empty existing session: %q", out)
}

func TestIndexAndReceiptReadFlatRecordsWithoutChangingThem(t *testing.T) {
	t.Parallel()
	store := t.TempDir()
	path := filepath.Join(store, "flat.md")
	require.NoError(t, os.WriteFile(path, []byte("# A session\n\nUnstructured opening prose.\n"), 0600))
	for _, id := range []string{"early", "late"} {
		stamp := "2026-09-28T01:02:03Z"
		if id == "late" {
			stamp = "2026-09-28T02:02:03Z"
		}
		runOK(t, "", "append", "--store", store, "--session", "flat", "--entry", id, "--text", "a note", "--now", stamp, "--publish", "manual", "--source", "not-stored")
	}
	before, err := os.ReadFile(path)
	require.NoError(t, err)
	out, _ := runOK(t, "", "index", "--store", store, "--max", "1")
	for _, want := range []string{"INDEX ENTRY session=flat entry=early stamp=2026-09-28T01:02:03Z bytes=6 source=-", "MORE", "sessions=1 entries=2 shown=1"} {
		assert.Contains(t, out, want, "index lacks %q: %s", want, out)
	}
	out, _ = runOK(t, "", "receipt", "--store", store, "--session", "flat", "--entry", "late")
	for _, want := range []string{"stamp=2026-09-28T02:02:03Z", "bytes=6 source=-", "publish=unknown"} {
		assert.Contains(t, out, want, "receipt lacks %q: %s", want, out)
	}
	after, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, string(before), string(after), "read changed the session")
	files, err := os.ReadDir(store)
	require.NoError(t, err)
	require.Len(t, files, 1, "read added sidecars: %v", files)
	code, _, errOut := runCode("", "receipt", "--store", store, "--session", "flat", "--entry", "absent")
	require.Equal(t, 2, code, "absent entry: %d %q", code, errOut)
	require.Contains(t, errOut, "entry", "absent entry: %d %q", code, errOut)
}

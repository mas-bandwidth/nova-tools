package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/testkit"
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
			refused(t, cli.Run(args...), tc.want)
		}
	}
	refused(t, cli.Run("index", "--store", filepath.Join(root, "missing")), "store")
	out := cli.OK(t, "index", "--store", root).Stdout
	require.Contains(t, out, "sessions=0 entries=0", "empty existing store: %q", out)
	cli.OK(t, "open", "--store", root, "--session", "empty", "--publish", "never")
	out = cli.OK(t, "index", "--store", root, "--session", "empty").Stdout
	require.Contains(t, out, "entries=0", "empty existing session: %q", out)
}

func TestIndexAndReceiptReadFlatRecordsWithoutChangingThem(t *testing.T) {
	t.Parallel()
	c := newRig(t)
	path := c.path("flat.md")
	testkit.WriteFile(t, path, "# A session\n\nUnstructured opening prose.\n")
	for _, id := range []string{"early", "late"} {
		stamp := "2026-09-28T01:02:03Z"
		if id == "late" {
			stamp = "2026-09-28T02:02:03Z"
		}
		c.ok("append", "--session", "flat", "--entry", id, "--text", "a note", "--now", stamp, "--publish", "manual", "--source", "not-stored")
	}
	before := testkit.ReadFile(t, path)
	printed(t, c.ok("index", "--max", "1"), "INDEX ENTRY session=flat entry=early stamp=2026-09-28T01:02:03Z bytes=6 source=-", "INDEX MORE kind=entry shown=1 total=2", "INDEX OK sessions=1 entries=2")
	printed(t, c.ok("receipt", "--session", "flat", "--entry", "late"), "stamp=2026-09-28T02:02:03Z", "bytes=6 source=-", "publish=unknown")
	printed(t, c.ok("receipt", "--session", "flat", "--entry", "late", "--text"), `text=a\x20note`)
	require.Equal(t, before, testkit.ReadFile(t, path), "read changed the session")
	files, err := os.ReadDir(c.store)
	require.NoError(t, err)
	require.Len(t, files, 1, "read added sidecars: %v", files)
	refused(t, c.run("receipt", "--session", "flat", "--entry", "absent"), "entry")
}

// refused asserts a refusal: exit 2, nothing on stdout, and stderr naming what
// was wrong. It asserts rather than requires, so a table row reports and the
// rows after it still run.
func refused(t *testing.T, r testkit.Result, names string) {
	t.Helper()
	assert.Equal(t, 2, r.Code, "want a refusal at exit 2: %+v", r)
	assert.Empty(t, r.Stdout, "a refusal wrote to stdout")
	assert.Contains(t, r.Stderr, names, "the refusal does not name %q", names)
}

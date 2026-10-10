package main

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/pkg/oneline"
)

// TestFriendcleanCoverFail covers friendClean.fail (friendclean.go:369): the
// failed counter increments and the FAILED line is said, every argument escaped
// by oneline.Escape so a newline or control in the error stays one line.
func TestFriendcleanCoverFail(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name   string
		friend string
		path   string
		err    error
	}{
		{
			name:   "a read error on an area directory",
			friend: "ada",
			path:   "/root/ada-working/inbox/job",
			err:    errors.New("permission denied"),
		},
		{
			name:   "a multi-line error is escaped to one line",
			friend: "bob",
			path:   "/home/bob-working/jobs/build",
			err:    errors.New("read error: first\nsecond"),
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			cl := &friendClean{}
			before := cl.failed
			cl.fail(c.friend, c.path, c.err)
			assert.Equal(t, before+1, cl.failed, "fail increments the failed counter")
			require.Len(t, cl.lines, 1)
			want := "FRIENDS-CLEAN FAILED friend=" + oneline.Escape(c.friend) +
				" path=" + oneline.Escape(c.path) + ": " + oneline.Escape(c.err.Error())
			assert.Equal(t, want, cl.lines[0])
		})
	}
}

// TestFriendcleanCoverFailFromReadError covers fail's refusal path: friend()
// calls fail when os.ReadDir fails with an error that is not fs.ErrNotExist.
// An area directory that cannot be read (chmod 0) triggers the failure; the
// FAILED line is said and report counts it as incomplete (exit 1).
func TestFriendcleanCoverFailFromReadError(t *testing.T) {
	t.Parallel()
	if os.Geteuid() == 0 {
		t.Skip("cannot make a directory unreadable as root")
	}
	if runtime.GOOS == "windows" {
		t.Skip("chmod 0 does not deny read on Windows")
	}
	ta := newTestApp(t)
	ta.a.friends = friendRows("ada")
	root := t.TempDir()
	w := filepath.Join(root, "ada-working")
	require.NoError(t, os.MkdirAll(filepath.Join(w, "inbox"), 0o755))
	require.NoError(t, os.Chmod(filepath.Join(w, "inbox"), 0o000))
	t.Cleanup(func() { _ = os.Chmod(filepath.Join(w, "inbox"), 0o755) })

	code, out, errs := ta.do("friend clean --root " + root)
	assert.Equal(t, 1, code, "a read failure makes the run incomplete")
	assert.Contains(t, out, "FRIENDS-CLEAN FAILED friend=ada path=")
	assert.Empty(t, errs, "failed lines go to stdout, not stderr")
}

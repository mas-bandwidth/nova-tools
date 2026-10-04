package main

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
)

// local_cover_test.go reaches the helpers in local.go that no existing test
// calls directly. Every case runs in-process: no sleep, no clock, no network,
// no subprocess, no Redis or Postgres.
//
// execLocal is the production localRunner seam: its refusal path (an empty
// command) returns before any subprocess is started and so is tested here.
// execLocal's main path — actually starting a command through subproc.Long —
// requires a subprocess and is out of scope for this card; it is noted in the
// report as not-done.

// TestLocalCoverExecLocalRefusal pins execLocal's check that rejects an empty
// command before touching subproc or os.Environ: nil and empty argv both
// return -1 and an "empty command" error.
func TestLocalCoverExecLocalRefusal(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		argv []string
	}{
		{"nil argv", nil},
		{"empty argv", []string{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			code, err := execLocal(localCmd{Argv: tc.argv})
			assert.Equal(t, -1, code, "want -1 for an empty command")
			assert.Error(t, err, "want an error for an empty command")
			assert.Contains(t, err.Error(), "empty command")
		})
	}
}

// TestLocalCoverLocalWhy covers all three branches of localWhy: a start error
// (err non-nil), an exit that left stderr (errText non-empty), and an exit
// with no error and no stderr (the default case).
func TestLocalCoverLocalWhy(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		code    int
		errText string
		err     error
		want    string
	}{
		{"start error", 0, "", errors.New("no such file or directory"), "no such file or directory"},
		{"exit with stderr", 128, "fatal: bad revision", nil, "exit 128: fatal: bad revision"},
		{"exit with no stderr", 2, "", nil, "exit 2"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, localWhy(tc.code, tc.errText, tc.err))
		})
	}
}

// TestLocalCoverLocalShort covers localWhy's main path — a sha longer than 12
// bytes is cut to twelve — and its edge, a sha of twelve or fewer bytes
// returned unchanged.
func TestLocalCoverLocalShort(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		sha  string
		want string
	}{
		{"long sha", "0123456789abcdef0123456789abcdef01234567", "0123456789ab"},
		{"exactly 12", "0123456789ab", "0123456789ab"},
		{"short sha", "abc123", "abc123"},
		{"empty", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, localShort(tc.sha))
		})
	}
}

// TestLocalCoverLocalKeep covers localKeep's main path — a single append that
// stays under 2*localOutputKept — and its trim, where the count crosses that
// ceiling and only the last localOutputKept lines survive.
func TestLocalCoverLocalKeep(t *testing.T) {
	t.Parallel()
	t.Run("appends without trimming", func(t *testing.T) {
		t.Parallel()
		got := localKeep([]string{"a", "b"}, "c")
		assert.Equal(t, []string{"a", "b", "c"}, got)
	})
	t.Run("trims when over capacity", func(t *testing.T) {
		t.Parallel()
		lines := make([]string, 2*localOutputKept)
		for i := range lines {
			lines[i] = "x"
		}
		got := localKeep(lines, "last")
		assert.Equal(t, localOutputKept, len(got), "want the last localOutputKept lines after trimming")
		assert.Equal(t, "last", got[len(got)-1], "the most recent line is last")
	})
}

// TestLocalCoverLocalTail covers localTail's main path — a slice within the
// kept window is returned as-is — and its trim, a longer slice that keeps only
// the last localOutputKept entries.
func TestLocalCoverLocalTail(t *testing.T) {
	t.Parallel()
	t.Run("within window", func(t *testing.T) {
		t.Parallel()
		in := []string{"a", "b"}
		assert.Equal(t, in, localTail(in))
	})
	t.Run("trims to window", func(t *testing.T) {
		t.Parallel()
		in := make([]string, localOutputKept+1)
		in[0] = "first"
		in[len(in)-1] = "last"
		got := localTail(in)
		assert.Equal(t, localOutputKept, len(got))
		assert.Equal(t, "last", got[len(got)-1])
		assert.NotContains(t, got, "first")
	})
}

// TestLocalCoverLocalResult covers localResult's main path — "pass" → "ok",
// "fail" → "FAILED" — and its default, where any other action string is
// returned unchanged (the "skip" case seen in a CI stream).
func TestLocalCoverLocalResult(t *testing.T) {
	t.Parallel()
	cases := []struct {
		action string
		want   string
	}{
		{"pass", "ok"},
		{"fail", "FAILED"},
		{"skip", "skip"},
		{"", ""},
	}
	for _, tc := range cases {
		t.Run(tc.action, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, localResult(tc.action))
		})
	}
}

// TestLocalCoverLocalLinesFlush covers localLines.flush's two paths: a
// remaining partial line is handed to the line callback and the buffer is
// cleared, and a call on an empty buffer is a no-op.
func TestLocalCoverLocalLinesFlush(t *testing.T) {
	t.Parallel()
	t.Run("flushes remaining buffer", func(t *testing.T) {
		t.Parallel()
		var got []string
		l := &localLines{line: func(b []byte) { got = append(got, string(b)) }}
		l.buf = []byte("partial line")
		l.flush()
		assert.Equal(t, []string{"partial line"}, got)
		assert.Nil(t, l.buf, "buffer should be cleared after flush")
	})
	t.Run("no-op on empty buffer", func(t *testing.T) {
		t.Parallel()
		called := false
		l := &localLines{line: func(b []byte) { called = true }}
		l.flush()
		assert.False(t, called, "line callback must not fire on an empty buffer")
		assert.Nil(t, l.buf)
	})
}

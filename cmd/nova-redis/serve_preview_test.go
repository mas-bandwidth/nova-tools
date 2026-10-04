package main

// serve_preview_test.go holds finding 6 of the nova-redis USE rating
// (docs/ratings/snapshots/0c5803c2de40/redis-use.md): serve creates the store
// directory and starts the service process, and an AI cannot see the plan it
// would take. serve --dry-run reports the same parsed launch options the real
// run consumes and has no effects: it creates no directory, reads no secret or
// environment, looks up no process and launches nothing. Real serve is
// unchanged and still launches through the seam.

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// previewHarness is serve over deps that record every effect the preview must
// not make: the environment and secret are unread, the process is never looked
// up and nothing ever launches. env, environ, lookPath and launch read as
// failure on any use, so a preview that reaches one turns the test red.
type previewHarness struct {
	t        *testing.T
	dir      string
	password string
	d        deps
}

func newPreviewHarness(t *testing.T, password string) *previewHarness {
	t.Helper()
	h := &previewHarness{t: t, password: password, dir: filepath.Join(t.TempDir(), "store")}
	h.d = deps{
		now: func() time.Time { return time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC) },
		getenv: func(k string) string {
			require.FailNowf(t, "", "the preview read the environment (%s); it must disclose no secret", k)
			return ""
		},
		environ: func() []string {
			require.FailNowf(t, "", "the preview read the environment list; it must touch nothing")
			return nil
		},
		lookPath: func(string) (string, error) {
			require.FailNowf(t, "", "the preview looked up the service process; it must launch nothing")
			return "", nil
		},
		launch: func(context.Context, launchSpec, io.Writer, io.Writer) error {
			require.FailNowf(t, "", "the preview launched a process; it must have no effects")
			return nil
		},
	}
	return h
}

func (h *previewHarness) run(args ...string) (int, string, string) {
	var out, errb bytes.Buffer
	code := run(args, &out, &errb, h.d)
	return code, out.String(), errb.String()
}

// TestServePreviewHasNoEffects: serve --dry-run prints the plan the real run
// would take, from the same parsed options, and writes nothing: a missing
// --dir stays absent, the password and environment go unread, no process is
// looked up and nothing launches. A malformed invocation refuses exactly as
// the real run does, with the same no-effects guarantee.
func TestServePreviewHasNoEffects(t *testing.T) {
	t.Parallel()

	const pw = "preview-must-not-read-this-secret"
	h := newPreviewHarness(t, pw)
	code, out, errb := h.run("serve", "--dry-run", "--bind", "127.0.0.1", "--port", "6379", "--dir", h.dir)
	require.Equal(t, 0, code, "serve --dry-run: exit %d stderr %q", code, errb)
	assert.Empty(t, errb, "serve --dry-run prints on stdout: %q", errb)
	for _, want := range []string{
		"SERVE OK",
		"bind=127.0.0.1",
		"port=6379",
		"auth=on",
		"persistence=aof",
		"eviction=none",
		"dir=" + h.dir,
		"dry_run=true",
	} {
		assert.Contains(t, out, want, "the preview does not report %q: %q", want, out)
	}
	assert.NotContains(t, out, pw, "the preview disclosed a secret: %q", out)

	_, statErr := os.Stat(h.dir)
	assert.ErrorIs(t, statErr, os.ErrNotExist, "the preview created the store directory %q", h.dir)

	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"bad bind", []string{"serve", "--dry-run", "--bind", "0.0.0.0", "--port", "6379", "--dir", h.dir}, `--bind "0.0.0.0" binds every interface`},
		{"bad port", []string{"serve", "--dry-run", "--bind", "127.0.0.1", "--port", "0", "--dir", h.dir}, `--port "0" needs a port`},
		{"relative dir", []string{"serve", "--dry-run", "--bind", "127.0.0.1", "--port", "6379", "--dir", "store"}, `--dir "store" is not absolute`},
	} {
		sub := tc
		t.Run(sub.name, func(t *testing.T) {
			t.Parallel()
			code, out, errb := h.run(sub.args...)
			assert.Equal(t, 2, code, "%s: exit %d (want 2)", sub.name, code)
			assert.Empty(t, out, "%s: a refusal writes nothing to stdout: %q", sub.name, out)
			assert.Contains(t, errb, sub.want, "%s: refusal %q does not name the problem", sub.name, errb)
			_, statErr := os.Stat(h.dir)
			assert.ErrorIs(t, statErr, os.ErrNotExist, "%s: the refused preview created the store directory %q", sub.name, h.dir)
		})
	}
}

// TestServeRealLaunchConsumesThePreviewedOptions: the real serve still launches
// through its seam with exactly the options the preview reports, so the plan
// and the run cannot drift.
func TestServeRealLaunchConsumesThePreviewedOptions(t *testing.T) {
	t.Parallel()

	h := newServeHarness(t, "pw-from-nova-secrets")
	preview := &previewHarness{t: t, dir: h.dir}
	code, out, errb := preview.run("serve", "--dry-run", "--bind", "127.0.0.1,100.100.1.2", "--port", "6380", "--dir", h.dir)
	require.Equal(t, 0, code, "preview: exit %d stderr %q", code, errb)
	assert.Contains(t, out, "bind=127.0.0.1,100.100.1.2", out)
	assert.Contains(t, out, "port=6380", out)
	assert.Contains(t, out, "dir="+h.dir, out)

	code, out, errb = h.run("serve", "--bind", "127.0.0.1,100.100.1.2", "--port", "6380", "--dir", h.dir)
	require.Equal(t, 0, code, "real serve: exit %d stderr %q", code, errb)
	require.Len(t, h.launches, 1, "real serve did not launch through the seam: %q", errb)
	cfg := config(t, h.launches[0].Config)
	assert.Equal(t, "127.0.0.1,100.100.1.2", strings.Join(only(t, cfg, "bind"), ","), "the launch binds what the preview reported")
	assert.Equal(t, "6380", strings.Join(only(t, cfg, "port"), " "), "the launch uses the port the preview reported")
	assert.Equal(t, h.dir, h.launches[0].Dir, "the launch uses the directory the preview reported")
	assert.Contains(t, out, "SERVE START bind=127.0.0.1,100.100.1.2 port=6380", "the real START line reports what the preview did: %q", out)
}

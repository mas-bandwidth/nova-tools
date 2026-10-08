package main

// serve_allatonce_test.go pins serve's one refusal: the password is checked
// with the flags, so a line with a bad flag and an empty password names both
// problems in one run (docs/STANDARD.md section 2, "recovery takes one turn"),
// and a --dry-run still reads no password or environment.

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestServeNamesThePasswordWithTheFlags: a bad --bind and an empty
// NOVA_REDIS_PASSWORD are named in the SAME refusal, so the password is
// checked with the flags and not after them; exit 2 and nothing is launched.
func TestServeNamesThePasswordWithTheFlags(t *testing.T) {
	t.Parallel()
	h := newServeHarness(t, "")
	code, out, errs := h.run("serve", "--bind", "0.0.0.0", "--port", "6379", "--dir", h.dir)
	require.Equal(t, 2, code, "stdout %q stderr %q", out, errs)
	assert.Empty(t, out)
	assert.Contains(t, errs, `--bind "0.0.0.0" binds every interface`, errs)
	assert.Contains(t, errs, PasswordEnv+" is empty", errs)
	assert.Contains(t, errs, "nova-secrets exec", errs)
	assert.Len(t, h.launches, 0, "a refused serve launched %d times", len(h.launches))
}

// TestServeDryRunNeedsNoPassword: --dry-run defers every effect and reads no
// password or environment, so a dry run of a store line with no password still
// prints its plan.
func TestServeDryRunNeedsNoPassword(t *testing.T) {
	t.Parallel()
	h := newServeHarness(t, "")
	h.d.getenv = func(k string) string {
		require.FailNowf(t, "", "a dry run read %s; it reads no password or environment", k)
		return ""
	}
	code, out, errs := h.run("serve", "--dry-run", "--bind", "127.0.0.1", "--port", "6379", "--dir", h.dir)
	require.Equal(t, 0, code, "stdout %q stderr %q", out, errs)
	assert.Contains(t, out, "SERVE OK")
}

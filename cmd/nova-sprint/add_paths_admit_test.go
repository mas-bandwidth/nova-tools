package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// holdPathsAdmit skips a brief only when REPO or BASE is omitted. A named REPO that no
// clone can be made of is unresolved, not omitted: the call keeps the MISSING line and
// refuses, and it does not invent a path miss from a tree it never read.
func TestHoldPathsAdmitSkipsOnlyAnOmittedRepoOrBase(t *testing.T) {
	t.Parallel()
	body := "\nTHE TASK. Fix the empty case.\n"
	paths := "PATHS: cmd/nova-sprint/add.go\n"
	run := func(t *testing.T, brief string) (int, string) {
		t.Helper()
		var stderr bytes.Buffer
		code := (&app{}).holdPathsAdmit("add", &stderr, briefCheck{id: "c1", brief: brief})
		return code, stderr.String()
	}

	t.Run("a named unresolved REPO is MISSING", func(t *testing.T) {
		t.Parallel()
		code, stderr := run(t, "RESULT: c sha=0123456789ab tier: pro\nREPO: not-a-repo\nBASE: sprint/s\n"+paths+body)
		require.Equal(t, 2, code, stderr)
		assert.Contains(t, stderr, "MISSING:")
		assert.Contains(t, stderr, "not-a-repo")
		assert.NotContains(t, stderr, "names nothing")
		assert.Contains(t, stderr, "nova-sprint add REFUSED:")
	})

	t.Run("an omitted REPO is not read", func(t *testing.T) {
		t.Parallel()
		code, stderr := run(t, "RESULT: c sha=0123456789ab tier: pro\nBASE: sprint/s\n"+paths+body)
		assert.Equal(t, 0, code, stderr)
		assert.Empty(t, stderr)
		code, stderr = run(t, "RESULT: c sha=0123456789ab tier: pro\nREPO: none\nBASE: sprint/s\n"+paths+body)
		assert.Equal(t, 0, code, stderr)
		code, stderr = run(t, "RESULT: c sha=0123456789ab tier: pro\nREPO: -\nBASE: sprint/s\n"+paths+body)
		assert.Equal(t, 0, code, stderr)
	})

	t.Run("an omitted BASE is not read", func(t *testing.T) {
		t.Parallel()
		code, stderr := run(t, "RESULT: c sha=0123456789ab tier: pro\nREPO: not-a-repo\n"+paths+body)
		assert.Equal(t, 0, code, stderr)
		assert.Empty(t, stderr)
	})

	t.Run("no PATHS is not read", func(t *testing.T) {
		t.Parallel()
		code, stderr := run(t, "RESULT: c sha=0123456789ab tier: pro\nREPO: not-a-repo\nBASE: sprint/s\n"+body)
		assert.Equal(t, 0, code, stderr)
		assert.False(t, strings.Contains(stderr, "MISSING:"))
	})
}

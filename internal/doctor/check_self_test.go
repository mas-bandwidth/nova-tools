package doctor

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/pkg/buildinfo"
)

// selfRig is a PATH of one directory under t.TempDir() holding the named files, and an
// exec that answers each tool's `version` from a map; nothing real runs.
func selfRig(t *testing.T, versions map[string]string, extra ...string) Env {
	t.Helper()
	root := t.TempDir()
	bin := filepath.Join(root, "bin")
	require.NoError(t, os.MkdirAll(bin, 0o755))
	for n := range versions {
		require.NoError(t, os.WriteFile(filepath.Join(bin, n), []byte("#!/bin/sh\n"), 0o755))
	}
	for _, n := range extra { // not executable: never a tool
		require.NoError(t, os.WriteFile(filepath.Join(bin, n), []byte("x"), 0o644))
	}
	return fakeEnv{env: map[string]string{"PATH": "bin:nowhere"}, root: root,
		exec: func(name string, args ...string) (string, error) {
			require.Equal(t, []string{"version"}, args)
			v, ok := versions[filepath.Base(name)]
			if !ok {
				return "", os.ErrNotExist
			}
			if v == "" {
				return "usage: nope\n", nil
			}
			return buildinfo.Line(filepath.Base(name), v), nil
		}}
}

func TestSelfCheck(t *testing.T) {
	t.Parallel()
	run := func(env Env) Result {
		r := NewRegistry()
		for _, n := range Default.Names() {
			if n == "self" {
				r.Register(Default.checks[n])
			}
		}
		res, _, err := r.Run(context.Background(), env, Options{})
		require.NoError(t, err)
		require.Len(t, res, 1)
		return res[0]
	}
	t.Run("one release is ok", func(t *testing.T) {
		t.Parallel()
		r := run(selfRig(t, map[string]string{"nova-bus": "v1.2.0", "nova-cairn": "v1.2.0"}, "nova-notexec", "other"))
		assert.Equal(t, OK, r.Status, r)
		assert.Equal(t, "2 tools on PATH, all v1.2.0", r.Evidence)
		assert.Empty(t, r.Fix)
	})
	t.Run("a skew is a fail naming the odd one", func(t *testing.T) {
		t.Parallel()
		r := run(selfRig(t, map[string]string{"nova-bus": "v1.2.0", "nova-cairn": "v1.2.0", "nova-card": "v1.1.0"}))
		assert.Equal(t, Fail, r.Status, r)
		assert.Contains(t, r.Evidence, "nova-card=v1.1.0 differ from v1.2.0 (2 tools)")
		assert.NotContains(t, r.Evidence, "nova-bus=")
		assert.Equal(t, "nova-update apply --file <manifest> nova-card --version v1.2.0", r.Fix)
	})
	t.Run("a tool that does not answer is a fail naming it", func(t *testing.T) {
		t.Parallel()
		r := run(selfRig(t, map[string]string{"nova-bus": "v1.2.0", "nova-cairn": ""}))
		assert.Equal(t, Fail, r.Status, r)
		assert.Contains(t, r.Evidence, "nova-cairn did not answer")
		assert.NotEmpty(t, r.Fix)
	})
	t.Run("no tool on PATH is a fail", func(t *testing.T) {
		t.Parallel()
		r := run(fakeEnv{env: map[string]string{"PATH": "bin"}})
		assert.Equal(t, Fail, r.Status, r)
		assert.Contains(t, r.Evidence, "no nova-* tool is on PATH")
	})
	t.Run("the first on PATH is the one that answers", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		for _, d := range []string{"a", "b"} {
			require.NoError(t, os.MkdirAll(filepath.Join(root, d), 0o755))
			require.NoError(t, os.WriteFile(filepath.Join(root, d, "nova-bus"), nil, 0o755))
		}
		var ran []string
		r := run(fakeEnv{env: map[string]string{"PATH": "a:b"}, root: root,
			exec: func(name string, _ ...string) (string, error) {
				ran = append(ran, name)
				return buildinfo.Line("nova-bus", "v1.2.0"), nil
			}})
		assert.Equal(t, OK, r.Status, r)
		assert.Equal(t, []string{"a/nova-bus"}, ran)
	})
}

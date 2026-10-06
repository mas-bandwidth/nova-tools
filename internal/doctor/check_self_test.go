package doctor

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func selfEnv(t *testing.T, versions map[string]string) *fakeEnv {
	t.Helper()
	e := &fakeEnv{root: t.TempDir(), programs: map[string]string{}, broken: map[string]bool{}}
	for tool, v := range versions {
		e.programs[tool] = versionLine(tool, v)
	}
	return e
}

func allAt(v string) map[string]string {
	m := map[string]string{}
	for _, n := range selfTools {
		m[n] = v
	}
	return m
}

func TestSelfCheckReadsEveryToolOnPathAndNamesTheOddOne(t *testing.T) {
	t.Parallel()
	skew := allAt("v1.2.0")
	skew["nova-bus"] = "v1.1.0"
	partial := allAt("v1.2.0")
	delete(partial, "nova-fuse")
	delete(partial, "nova-work")

	cases := []struct {
		name     string
		env      func(t *testing.T) *fakeEnv
		status   Status
		contains []string
	}{
		{"one release is ok", func(t *testing.T) *fakeEnv { return selfEnv(t, allAt("v1.2.0")) }, OK,
			[]string{"all release v1.2.0"}},
		{"a skew fails naming the odd tool", func(t *testing.T) *fakeEnv { return selfEnv(t, skew) }, Fail,
			[]string{"skew", "nova-bus=v1.1.0", "release v1.2.0"}},
		{"a missing tool warns naming it", func(t *testing.T) *fakeEnv { return selfEnv(t, partial) }, Warn,
			[]string{"not on PATH", "nova-fuse", "nova-work"}},
		{"no nova tool fails", func(t *testing.T) *fakeEnv { return selfEnv(t, nil) }, Fail,
			[]string{"no nova tool is on PATH"}},
		{"a tool that prints no version line fails naming it", func(t *testing.T) *fakeEnv {
			e := selfEnv(t, allAt("v1.2.0"))
			e.programs["nova-card"] = "usage: nova-card ...\n"
			return e
		}, Fail, []string{"nova-card"}},
		{"a tool whose version errors fails naming it", func(t *testing.T) *fakeEnv {
			e := selfEnv(t, allAt("v1.2.0"))
			e.broken["nova-table"] = true
			return e
		}, Fail, []string{"nova-table"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := runSelf(context.Background(), tc.env(t))
			assert.Equal(t, tc.status, r.Status, r.Evidence)
			for _, s := range tc.contains {
				assert.Contains(t, r.Evidence, s)
			}
			if tc.status != OK {
				assert.NotEmpty(t, r.Fix)
			}
		})
	}
}

func TestSelfCheckAsksEachToolItsVersionVerbOnly(t *testing.T) {
	t.Parallel()
	e := selfEnv(t, allAt("v1.2.0"))
	runSelf(context.Background(), e)
	require.Len(t, e.ran, len(selfTools))
	for _, r := range e.ran {
		assert.True(t, strings.HasSuffix(r, " version"), r)
	}
}

func TestEveryToolSelfNamesHasACommandDirectory(t *testing.T) {
	t.Parallel()
	for _, n := range selfTools {
		_, err := os.Stat(filepath.Join("..", "..", "cmd", n))
		assert.NoError(t, err, n)
	}
}

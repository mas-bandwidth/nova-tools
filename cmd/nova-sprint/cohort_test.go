package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/sprintcohort"
	"github.com/mas-bandwidth/nova-tools/internal/sprintwire"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCohortWhereSelectsExactPrimariesAndDoesNotMutateTheTwin(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	ta.ok("add --stream run --count 2")
	ta.ok("add --stream other --count 1")
	ta.ok("add --stream run --sentinel stop")
	before := ta.ok("log --json")
	var v sprintcohort.Status
	ta.json("where --stream run", &v)
	assert.Equal(t, 2, v.Primaries)
	assert.Equal(t, []string{"stop"}, v.Excluded)
	assert.Equal(t, 2, v.FirstWork.Pending)
	assert.Equal(t, 2, v.DevLandingUnknown)
	out := ta.ok("where --ids run-2,run-1 --max 1")
	assert.Contains(t, out, "COHORT MORE shown=1 total=2")
	assert.Contains(t, out, "inflight unknown, not a provider invoice")
	after := ta.ok("log --json")
	assert.JSONEq(t, before, after)
	code, _, errb := ta.do("where --ids missing")
	assert.Equal(t, 2, code)
	assert.Contains(t, errb, "missing")
	code, _, errb = ta.do("where --stream run --ids run-1")
	assert.Equal(t, 2, code)
	assert.Contains(t, errb, "exactly one")
}

func TestCohortManifestIsLocalAndSendsOneServerRequest(t *testing.T) {
	t.Parallel()
	file := filepath.Join(t.TempDir(), "cohort.json")
	require.NoError(t, os.WriteFile(file, []byte(`["p","q"]`), 0600))
	a := newApp(func(k string) string {
		if k == ServerEnv {
			return "localhost:9999"
		}
		return ""
	})
	requests := 0
	a.forward = func(_ context.Context, _ string, verbs ...[]string) ([]sprintwire.Result, error) {
		requests++
		require.Len(t, verbs, 1)
		args := strings.Join(verbs[0], " ")
		assert.Contains(t, args, "--ids p,q")
		assert.NotContains(t, args, "--manifest")
		assert.NotContains(t, args, file)
		return []sprintwire.Result{{Stdout: `{"primaries":2}` + "\n"}}, nil
	}
	var out, errb bytes.Buffer
	code := a.run([]string{"where", "--manifest", file, "--json"}, &out, &errb)
	require.Equal(t, 0, code, errb.String())
	assert.Equal(t, 1, requests)
	var got map[string]int
	require.NoError(t, json.Unmarshal(out.Bytes(), &got))
	assert.Equal(t, 2, got["primaries"])
}

func TestCohortManifestRefusesDuplicatesAndWorkerIdentities(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, data string }{{"duplicate", "p\np\n"}, {"work identity", `["p.w1"]`}, {"non string", `[{}]`}} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			file := filepath.Join(t.TempDir(), "ids")
			require.NoError(t, os.WriteFile(file, []byte(tc.data), 0600))
			a := newApp(func(string) string { return "" })
			var out, errb bytes.Buffer
			code := a.run([]string{"where", "--manifest", file}, &out, &errb)
			assert.Equal(t, 2, code)
			assert.Empty(t, out.String())
		})
	}
}

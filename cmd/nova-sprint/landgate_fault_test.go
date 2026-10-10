package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

func TestEveryBenchFaultedDefersAndRaisesJudgment(t *testing.T) {
	t.Parallel()
	r := newLandRig(t)
	r.env = append(r.env, "NOVA_TEST_NO_HOST=1")
	r.a.gitEnv = r.env
	r.live = []string{"m1", "m2", "m3"}
	r.ok("init --members m1,m2,m3")
	r.ok("fleet beat m1 --load 1 --cores 8")
	r.ok("fleet up m1 --width 1")
	r.ok("fleet beat m2 --load 1 --cores 8")
	r.ok("fleet up m2 --width 1")
	r.ok("fleet beat m3 --load 1 --cores 8")
	r.ok("fleet up m3 --width 1")
	r.promotionStream("s1")

	r.commit("go.mod", "module github.com/test/m\n\ngo 1.22\n", "mod")
	r.git(r.worker, "push", "-q", "origin", "HEAD:refs/heads/main")
	r.git(r.worker, "fetch", "-q", "origin")

	briefs := t.TempDir()
	path := filepath.Join(briefs, "a.md")
	require.NoError(t, os.WriteFile(path, []byte(passingBrief("REPO: "+r.remote+"\nBASE: main\n\nWrite a.txt.")), 0o600))
	r.ok("add --stream s1 --one --brief-file " + path)

	r.queued(map[string]string{"a": r.head("a", "main", "a.txt", "a\n")}, "a")

	var asked []string
	b := r.a.landState()
	b.mu.Lock()
	b.gateBench = func(ctx context.Context, host, dir string, runs [][]string, withGit bool) (string, int, error) {
		asked = append(asked, host)
		return "git ls-files: exit status 128", 1, nil
	}
	b.mu.Unlock()

	r.ok("start")
	r.ok("tick")
	r.ok("stop --reason r --until 9999h")

	require.ElementsMatch(t, []string{"m1", "m2", "m3"}, asked, "every bench slot should be asked")

	assert.Contains(t, r.streamState("s1"), "LAND DEFERRED stream=s1 faults=3", "LAND DEFERRED is said")

	var faultJudgments []sprint.Group
	for _, g := range r.inboxGroups() {
		if g.Kind == sprint.Judgment && strings.HasPrefix(g.Type, "every bench faulted") {
			faultJudgments = append(faultJudgments, g)
		}
	}
	require.Len(t, faultJudgments, 1, "one judgment 'every bench faulted: git' is raised")
	assert.Equal(t, "every bench faulted: git", faultJudgments[0].Type)
}

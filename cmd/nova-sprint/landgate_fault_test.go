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
	r.ok("init")

	st, err := r.a.store(common{redis: "mem:0", actor: "tester"})
	require.NoError(t, err)

	hosts := []string{"m1", "m2", "m3"}
	var asked []string
	b := r.a.landState()
	b.mu.Lock()
	b.gateBench = func(ctx context.Context, host, dir string, runs [][]string, withGit bool) (string, int, error) {
		asked = append(asked, host)
		return "git ls-files: exit status 128", 1, nil
	}
	b.mu.Unlock()

	l := &lander{a: r.a, st: st, gateKey: "s1"}
	runs := gateRuns(false, nil)
	why, ran := l.benchGate(context.Background(), hosts, r.clone, runs, false)

	assert.True(t, ran, "gate ran on benches")
	assert.Equal(t, "LAND DEFERRED stream=s1 faults=3", why, "LAND DEFERRED is said")
	require.ElementsMatch(t, []string{"m1", "m2", "m3"}, asked, "every bench slot should be asked")

	l.locks().gateMu.Lock()
	faults := l.locks().benchFaults
	l.locks().gateMu.Unlock()

	require.NotNil(t, faults)
	require.Contains(t, faults, "m1")
	assert.Equal(t, "git", faults["m1"].kind)

	var faultJudgments []sprint.Group
	for _, g := range r.inboxGroups() {
		if g.Kind == sprint.Judgment && strings.HasPrefix(g.Type, "every bench faulted") {
			faultJudgments = append(faultJudgments, g)
		}
	}
	require.Len(t, faultJudgments, 1, "one judgment 'every bench faulted: git' is raised")
	assert.Equal(t, "every bench faulted: git", faultJudgments[0].Type)
}

package main

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/bench"
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

// An ssh that cannot reach the first slot's bench is bench.NoAnswer; the gate must
// classify it as a bench fault, report GATE FAULT kind=ssh, and step to the next
// slot, never returning an empty non-run result that ends the ring.
func TestAnSSHBenchFaultStepsToTheNextSlotAndIsNotRed(t *testing.T) {
	t.Parallel()
	r := newLandRig(t)
	r.ok("init")

	st, err := r.a.store(common{redis: "mem:0", actor: "tester"})
	require.NoError(t, err)

	hosts := []string{"m1", "m2"}
	first := benchRing("s1", hosts)[0]
	var asked []string
	b := r.a.landState()
	b.mu.Lock()
	b.gateBench = func(ctx context.Context, host, dir string, runs [][]string, withGit bool) (string, int, error) {
		asked = append(asked, host)
		if host == first {
			return "ssh: connect to host " + host + " port 22: Connection refused", bench.NoAnswer, nil
		}
		return "", 0, nil
	}
	b.mu.Unlock()

	l := &lander{a: r.a, st: st, gateKey: "s1"}
	runs := gateRuns(false, nil)
	why, ran := l.benchGate(context.Background(), hosts, r.clone, runs, false)

	assert.True(t, ran, "the gate ran on the slot after the fault")
	assert.Empty(t, why, "an ssh bench fault is never a red tree")
	require.Len(t, asked, 2, "the faulted slot is left and the next is asked")
	assert.Equal(t, first, asked[0], "the slot the stream hashes to is asked first")
	assert.NotEqual(t, first, asked[1], "the next slot is the other bench")
}

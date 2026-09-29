//go:build functional

package main

import (
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fleet"
)

func TestFleetChurnFunctional(t *testing.T) {
	t.Parallel()

	addr := startThrowawayRedis(t)
	at := int64(1_800_000_000)
	sampleHulk := fleet.PSSample{
		At: at,
		Procs: []fleet.PSProc{
			{PID: 10, PPID: 1, Start: at - 2, Cmd: "/usr/local/bin/bench-row"},
			{PID: 11, PPID: 1, Start: at - 5, Cmd: "bench-row"},
			{PID: 20, PPID: 1, Start: at - 90000, Cmd: "ci-run --daemon"},
			{PID: 30, PPID: 1, Start: at - 90000, Cmd: "sshd"},
		},
	}
	sampleSpace := fleet.PSSample{
		At: at,
		Procs: []fleet.PSProc{
			{PID: 5, PPID: 1, Start: at - 30, Cmd: "/usr/bin/thing"},
		},
	}

	seed(t, addr, [][]string{
		{"SADD", "benches", "hulk", "space"},
		{"HSET", "bench:hulk:beat", "ps", sampleHulk.Encode(), "at", "1"},
		{"HSET", "bench:space:beat", "ps", sampleSpace.Encode(), "at", "1"},
	})

	code, stdout, stderr := runSprint("fleet", "churn", "--redis", addr, "--seconds", "12")
	if code != 0 {
		t.Fatalf("exit %d stderr %q\n%s", code, stderr, stdout)
	}

	want := strings.Join([]string{
		"hulk young bench-row 2",
		"hulk old pid=20 age=1d comm=ci-run",
		"CHURN hulk young=2 old=1",
		"CHURN space young=0 old=0",
		"FLEET CHURN OK benches=2 churn=2 orphans=1",
		"",
	}, "\n")

	if stdout != want {
		t.Errorf("stdout:\n%s\nwant:\n%s", stdout, want)
	}
}

package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestPlanVerb drives plan apply and plan show through the CLI against a
// throwaway Redis: a good plan applies, show prints DRIFT none, a second apply
// is PLAN-UNCHANGED, and argument mistakes are refused before any store read.
func TestPlanVerb(t *testing.T) {
	for _, args := range [][]string{
		{"plan"},
		{"plan", "wipe", "--sprint", "s"},
		{"plan", "apply", "--sprint", "s"},
		{"plan", "show"},
		{"plan", "show", "--sprint", "s", "--plan", "x.tsv"},
	} {
		if code, out, errOut := runSprint(args...); code != 2 || out != "" || !strings.Contains(errOut, "run: nova-sprint help") {
			t.Fatalf("%v: code=%d out=%q err=%q; want a refusal", args, code, out, errOut)
		}
	}

	addr, client := sprintRedis(t)
	ctx := context.Background()
	client.HSet(ctx, "machine:m1:ceiling", "slots", 32)
	client.SAdd(ctx, "benches", "a")
	client.HSet(ctx, "bench:a:desired", "slots", 2, "machine", "m1", "paused", "0")
	file := filepath.Join(t.TempDir(), "plan.tsv")
	body := "#nova-sprint-plan v1\npolicy\tbackpressure_missing\topen\npolicy\tci_reruns\t0\n" +
		"policy\treaders\t2\npolicy\tabsent_after\t30m\nbench\ta\tm1\t5\n"
	if err := os.WriteFile(file, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	apply := []string{"plan", "apply", "--redis", addr, "--sprint", "cli-2380", "--plan", file}
	if code, out, errOut := runSprint(apply...); code != 0 || !strings.HasPrefix(out, "PLAN-APPLIED cli-2380 rows=1 sha=") {
		t.Fatalf("apply: code=%d out=%q err=%q", code, out, errOut)
	}
	if got := client.HGet(ctx, "bench:a:desired", "slots").Val(); got != "5" {
		t.Fatalf("bench a slots=%s want 5", got)
	}
	if got := client.HGet(ctx, "s:cli-2380:plan", "applied_by").Val(); got != "default" {
		t.Fatalf("applied_by=%q want default", got)
	}
	if code, out, errOut := runSprint("plan", "show", "--redis", addr, "--sprint", "cli-2380"); code != 0 || !strings.HasSuffix(out, "\nDRIFT none\n") {
		t.Fatalf("show: code=%d out=%q err=%q", code, out, errOut)
	}
	if code, out, errOut := runSprint(apply...); code != 0 || !strings.HasPrefix(out, "PLAN-UNCHANGED cli-2380 sha=") {
		t.Fatalf("re-apply: code=%d out=%q err=%q", code, out, errOut)
	}
	if code, out, errOut := runSprint("plan", "show", "--redis", addr, "--sprint", "no-plan"); code != 1 || out != "PLAN no-plan none\n" {
		t.Fatalf("show without a plan: code=%d out=%q err=%q", code, out, errOut)
	}
}

// TestPlanHoldPolicy is the DONE-WHEN of #3798: plan apply accepts fix_to and
// release_reader, ns_sprint_plan stores them in s:<S>:policy, and hold route
// --once on that sprint runs a pass instead of refusing with ErrNoPolicy.
func TestPlanHoldPolicy(t *testing.T) {
	e := newHoldEnv(t, "hold-3798")
	e.friends("rowan", "stella")
	if code, _, errOut := e.run("hold", "route", "--once", "--sprint", e.S); code != 1 || !strings.Contains(errOut, "fix_to and release_reader") || !strings.Contains(errOut, "remedy: nova-sprint plan apply with policy fix_to and policy release_reader") {
		t.Fatalf("route before the plan: code=%d err=%q; want the ErrNoPolicy refusal (exit 1 with the remedy, #3814)", code, errOut)
	}
	file := filepath.Join(t.TempDir(), "plan.tsv")
	body := "#nova-sprint-plan v1\npolicy\tbackpressure_missing\topen\npolicy\tci_reruns\t0\n" +
		"policy\treaders\t1\npolicy\tabsent_after\t30m\npolicy\tfix_to\trowan\npolicy\trelease_reader\tstella\n"
	if err := os.WriteFile(file, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if code, out, errOut := e.run("plan", "apply", "--sprint", e.S, "--plan", file); code != 0 || !strings.HasPrefix(out, "PLAN-APPLIED "+e.S+" rows=0 sha=") {
		t.Fatalf("apply: code=%d out=%q err=%q", code, out, errOut)
	}
	pol := e.c.HMGet(context.Background(), "s:"+e.S+":policy", "fix_to", "release_reader").Val()
	if pol[0] != "rowan" || pol[1] != "stella" {
		t.Fatalf("s:%s:policy fix_to,release_reader = %v; want rowan, stella", e.S, pol)
	}
	if code, out, errOut := e.run("plan", "show", "--sprint", e.S); !strings.Contains(out, "policy fix_to plan=rowan store=rowan\n") ||
		!strings.Contains(out, "policy release_reader plan=stella store=stella\n") || strings.Contains(out, "DRIFT policy") {
		t.Fatalf("show: code=%d out=%q err=%q", code, out, errOut)
	}
	if code, out, errOut := e.run("hold", "route", "--once", "--sprint", e.S); code != 0 {
		t.Fatalf("route after the plan: code=%d out=%q err=%q; want a pass", code, out, errOut)
	}
}

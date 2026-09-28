//go:build functional

package main

import (
	"context"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
	"github.com/redis/go-redis/v9"
)

// shadowLines is a window of lander --shadow output (#3786's line shape)
// against four PR records the hand lander left: one landed (CLOSE), one
// parked, one still open, one with no record at all.
const shadowLines = `SHADOW nova-tools#101 head=abcdef12 verdict=LAND why=ci-green
SHADOW nova-tools#101 head=abcdef12 verdict=WAIT-CI why=ci
SHADOW nova-tools#101 head=00000000 verdict=LAND why=ci-green
SHADOW nova-tools#102 head=12345678 verdict=LAND why=ci-green
SHADOW nova-tools#103 head=feedface verdict=NO-READ why=read
SHADOW nova-tools#104 head=- verdict=NO-RECORD why=record
LANDER shadow repo=nova-tools members=4 land=2 wait-ci=1 no-read=1 hold=0 conflict=0 no-record=1
`

func seedHandLander(t *testing.T, addr string) *redis.Client {
	t.Helper()
	c := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = c.Close() })
	ctx := context.Background()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(c.HSet(ctx, "pr:nova-tools:101", "repo", "nova-tools", "n", "101", "head", "abcdef1234567890", "state", "landed",
		"close", "CLOSE who=rowan: in stream/landing at 1234abcd; landed with nova-tools#900").Err())
	must(c.HSet(ctx, "pr:nova-tools:102", "repo", "nova-tools", "n", "102", "head", "1234567890abcdef", "state", "parked",
		"park", "PARKED nova-tools#102 conflict in stream/landing at 1234abcd").Err())
	must(c.HSet(ctx, "pr:nova-tools:103", "repo", "nova-tools", "n", "103", "head", "feedface00000000", "state", "open").Err())
	return c
}

// TestLandEvalShadowAgreesPerPR is #3800's DONE-WHEN: land eval --shadow reads
// the SHADOW lines and the CLOSE records and prints agree/disagree per PR and
// one receipt, on a throwaway server, writing nothing.
func TestLandEvalShadowAgreesPerPR(t *testing.T) {
	addr := testutil.Start(t)
	c := seedHandLander(t, addr)
	ctx := context.Background()
	before, _ := c.DBSize(ctx).Result()

	landEvalStdin = strings.NewReader(shadowLines)
	t.Cleanup(func() { landEvalStdin = nil })
	code, out, errOut := runSprint("land", "eval", "--shadow", "--redis", addr)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	want := "EVAL nova-tools#101 hand=landed agree=1 disagree=1 stale=1\n" +
		"EVAL nova-tools#102 hand=parked agree=0 disagree=1 stale=0\n" +
		"EVAL nova-tools#103 hand=open agree=1 disagree=0 stale=0\n" +
		"EVAL nova-tools#104 hand=none agree=1 disagree=0 stale=0\n" +
		"EVAL shadow prs=4 lines=6 agree=3 disagree=2 stale=1 skipped=1\n"
	if out != want {
		t.Fatalf("stdout:\n%s\nwant:\n%s", out, want)
	}
	if after, _ := c.DBSize(ctx).Result(); after != before {
		t.Fatalf("land eval --shadow wrote keys: %d -> %d", before, after)
	}
}

func TestLandEvalShadowNoLinesRefuses(t *testing.T) {
	addr := testutil.Start(t)
	landEvalStdin = strings.NewReader("LANDER shadow repo=nova-tools members=0\n")
	t.Cleanup(func() { landEvalStdin = nil })
	code, out, errOut := runSprint("land", "eval", "--shadow", "--redis", addr)
	if code != 2 || out != "" || !strings.Contains(errOut, "no SHADOW lines") || !strings.Contains(errOut, "lander --shadow") {
		t.Fatalf("code=%d out=%q err=%q", code, out, errOut)
	}
}

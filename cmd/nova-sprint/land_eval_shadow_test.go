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

// TestLandEvalShadowSinceMatchesPipedRuns is #3821's DONE-WHEN:
// on a throwaway server, two lander --shadow --record runs then land eval --shadow --since 1h
// print the same per-PR agree/disagree counts as piping both runs' lines into land eval --shadow,
// and lander --shadow without --record still writes nothing.
func TestLandEvalShadowSinceMatchesPipedRuns(t *testing.T) {
	addr := testutil.Start(t)
	c := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = c.Close() })
	ctx := context.Background()

	if err := c.HSet(ctx, "cfg:land", "min_score", "8").Err(); err != nil {
		t.Fatal(err)
	}
	seedShadow(t, c, "nova-tools", map[int][]string{
		101: {"head", "aaaa1111ffff", "state", "landed", "ci", "green", "mergeable", "true",
			"reads", "SCORE who=emma head=aaaa1111 score=9/10",
			"close", "CLOSE who=rowan: in stream/landing at 1234abcd; landed with nova-tools#900"},
		102: {"head", "bbbb2222ffff", "state", "parked", "ci", "green", "mergeable", "false",
			"reads", "SCORE who=emma head=bbbb2222 score=9/10",
			"park", "PARKED nova-tools#102 conflict in stream/landing at 1234abcd"},
		103: {"head", "cccc3333ffff", "state", "open", "ci", "pending", "mergeable", "true",
			"reads", "SCORE who=stella head=cccc3333 score=8/10"},
	})

	// 1. Verify lander --shadow without --record still writes nothing
	beforeKeys, err := c.Keys(ctx, "*").Result()
	if err != nil {
		t.Fatal(err)
	}
	code0, _, err0 := runSprint("lander", "--shadow", "--redis", addr, "--repo", "nova-tools", "101", "102", "103", "104")
	if code0 != 0 {
		t.Fatalf("lander --shadow exit %d: %s", code0, err0)
	}
	afterKeys, err := c.Keys(ctx, "*").Result()
	if err != nil {
		t.Fatal(err)
	}
	if len(afterKeys) != len(beforeKeys) {
		t.Fatalf("lander --shadow without --record wrote keys: before=%d, after=%d", len(beforeKeys), len(afterKeys))
	}

	// 2. Two lander --shadow --record runs
	code1, out1, err1 := runSprint("lander", "--shadow", "--record", "--redis", addr, "--repo", "nova-tools", "101", "102", "103", "104")
	if code1 != 0 {
		t.Fatalf("lander --shadow --record run 1 exit %d: %s", code1, err1)
	}
	code2, out2, err2 := runSprint("lander", "--shadow", "--record", "--redis", addr, "--repo", "nova-tools", "101", "102", "103", "104")
	if code2 != 0 {
		t.Fatalf("lander --shadow --record run 2 exit %d: %s", code2, err2)
	}

	// Verify shadow stream was created and has 8 entries
	streamLen, err := c.XLen(ctx, "land:nova-tools:shadow").Result()
	if err != nil || streamLen != 8 {
		t.Fatalf("stream len %d, err %v, want 8", streamLen, err)
	}

	// 3. land eval --shadow --since 1h (reads from Redis stream)
	codeSince, outSince, errSince := runSprint("land", "eval", "--shadow", "--since", "1h", "--redis", addr)
	if codeSince != 0 {
		t.Fatalf("land eval --shadow --since 1h exit %d: %s", codeSince, errSince)
	}

	// Also verify --repo works with --since
	codeSinceRepo, outSinceRepo, errSinceRepo := runSprint("land", "eval", "--shadow", "--since", "1h", "--repo", "nova-tools", "--redis", addr)
	if codeSinceRepo != 0 {
		t.Fatalf("land eval --shadow --since 1h --repo nova-tools exit %d: %s", codeSinceRepo, errSinceRepo)
	}
	if outSince != outSinceRepo {
		t.Fatalf("outSince with vs without repo mismatch:\nwithout:\n%s\nwith:\n%s", outSince, outSinceRepo)
	}

	// 4. land eval --shadow with both runs piped
	landEvalStdin = strings.NewReader(out1 + out2)
	t.Cleanup(func() { landEvalStdin = nil })
	codePipe, outPipe, errPipe := runSprint("land", "eval", "--shadow", "--redis", addr)
	if codePipe != 0 {
		t.Fatalf("land eval --shadow (piped) exit %d: %s", codePipe, errPipe)
	}

	// Compare per-PR lines
	perPRLines := func(out string) []string {
		var prLines []string
		for _, l := range strings.Split(strings.TrimSpace(out), "\n") {
			if strings.HasPrefix(l, "EVAL ") && !strings.HasPrefix(l, "EVAL shadow ") {
				prLines = append(prLines, l)
			}
		}
		return prLines
	}
	sincePRs := perPRLines(outSince)
	pipePRs := perPRLines(outPipe)
	if len(sincePRs) == 0 {
		t.Fatalf("expected per-PR lines in outSince, got none:\n%s", outSince)
	}
	if strings.Join(sincePRs, "\n") != strings.Join(pipePRs, "\n") {
		t.Fatalf("per-PR lines mismatch:\nsince 1h:\n%s\npiped:\n%s", strings.Join(sincePRs, "\n"), strings.Join(pipePRs, "\n"))
	}
}

func TestLandEvalShadowFlags(t *testing.T) {
	addr := testutil.Start(t)

	// --since without --shadow refuses
	code, _, errOut := runSprint("land", "eval", "--since", "1h", "--redis", addr)
	if code != 2 || !strings.Contains(errOut, "--since is a --shadow flag") {
		t.Fatalf("code=%d errOut=%q", code, errOut)
	}

	// --since with non-positive duration refuses
	code, _, errOut = runSprint("land", "eval", "--shadow", "--since", "-1h", "--redis", addr)
	if code != 2 || !strings.Contains(errOut, "--since: duration must be positive") {
		t.Fatalf("code=%d errOut=%q", code, errOut)
	}

	// --since with invalid duration refuses
	code, _, errOut = runSprint("land", "eval", "--shadow", "--since", "invalid", "--redis", addr)
	if code != 2 || !strings.Contains(errOut, "--since:") {
		t.Fatalf("code=%d errOut=%q", code, errOut)
	}

	// --since with empty redis stream refuses
	code, _, errOut = runSprint("land", "eval", "--shadow", "--since", "1h", "--redis", addr)
	if code != 2 || !strings.Contains(errOut, "no shadow stream found") {
		t.Fatalf("code=%d errOut=%q", code, errOut)
	}

	// lander --record without --shadow refuses
	code, _, errOut = runSprint("lander", "--record", "--redis", addr, "--repo", "nova-tools", "1")
	if code != 2 || !strings.Contains(errOut, "--record is a --shadow flag") {
		t.Fatalf("code=%d errOut=%q", code, errOut)
	}
}

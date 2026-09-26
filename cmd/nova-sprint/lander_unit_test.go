package main

import (
	"bytes"
	"context"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/land"
)

// landerUnit is one member's unit record in the shape land.lua writes:
// s:<S>:u:<unit> with head and the ns_unit_mergeable word fenced on
// mergeable_head, and s:<S>:prunit:<repo>:<n> naming the unit.
type landerUnit struct {
	n                              int
	head, mergeable, mergeableHead string
}

func seedLanderUnits(t *testing.T, c *redis.Client, S, repo string, units []landerUnit) {
	t.Helper()
	ctx := context.Background()
	pipe := c.TxPipeline()
	for _, u := range units {
		unit := "gh/mas-bandwidth/" + repo + "/" + strconv.Itoa(u.n)
		pipe.HSet(ctx, land.UnitKey(S, unit), "repo", repo, "base", "dev", "pr", u.n, "head", u.head,
			"mergeable", u.mergeable, "mergeable_head", u.mergeableHead, "state", "landable")
		pipe.Set(ctx, land.PRUnitKey(S, repo, u.n), unit, 0)
		pipe.SAdd(ctx, land.UnitsSetKey(S), unit)
	}
	if _, err := pipe.Exec(ctx); err != nil {
		t.Fatal(err)
	}
}

// headGate is a green gate that records the members (<n>@<head>) it gated.
type headGate struct {
	mu    sync.Mutex
	gated [][]string
}

func (g *headGate) Run(_ context.Context, b land.Batch, _ int) (land.Verdict, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.gated = append(g.gated, memberArgs(b))
	return land.Verdict{OK: true}, nil
}

// TestLanderLoadsMembersFromUnitRecords (nova-tools#3611): the lander reads
// its members from the one key contract `why` and `land status` read, the
// unit records resolved through s:<S>:prunit, and never the retired
// s:<S>:pr:<repo>:<n> that nothing writes. Each member here also has a
// retired record with a different head and word; the gate sees only the unit
// heads, the word counts only at the unit's head (#13's MERGEABLE was
// observed at an older head, so it is UNKNOWN and dropped), and a member
// known only by a retired record refuses before any gate runs.
func TestLanderLoadsMembersFromUnitRecords(t *testing.T) {
	addr := startThrowawayRedis(t)
	c := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = c.Close() })
	ctx := context.Background()
	const S, repo = "control-3611", "nova-tools"
	seedLanderUnits(t, c, S, repo, []landerUnit{
		{n: 21, head: "aaaa2121", mergeable: "MERGEABLE", mergeableHead: "aaaa2121"},
		{n: 22, head: "bbbb2222", mergeable: "MERGEABLE", mergeableHead: "bbbb2222"},
		{n: 23, head: "cccc2323", mergeable: "MERGEABLE", mergeableHead: "0ld02323"},
	})
	pipe := c.TxPipeline()
	for _, n := range []int{21, 22, 23, 24} {
		pipe.HSet(ctx, "s:"+S+":pr:"+repo+":"+strconv.Itoa(n), "head", "dead"+strconv.Itoa(n), "mergeable", "MERGEABLE")
	}
	if _, err := pipe.Exec(ctx); err != nil {
		t.Fatal(err)
	}

	fakes, gate := &landerFakes{}, &headGate{}
	seams, prevClock := landerSeams, landerClock
	landerSeams = func(landerPrograms) (land.Gate, land.Bisect, land.Lander, land.Filer) {
		return gate, fakes, fakes, fakes
	}
	landerClock = &quietClock{at: time.Date(2026, 9, 25, 9, 0, 0, 0, time.UTC)}
	t.Cleanup(func() { landerSeams, landerClock = seams, prevClock })

	run := func(members ...string) (int, string, string) {
		var out, errOut bytes.Buffer
		args := []string{"--redis", addr, "--sprint", S, "--repo", repo, "--batch", "b-3611",
			"--gate", "g", "--bisect", "b", "--land", "l", "--file", "f"}
		code := runLander(ctx, append(args, members...), &out, &errOut)
		return code, out.String(), errOut.String()
	}

	code, out, errOut := run("21", "22", "23")
	if code != 0 {
		t.Fatalf("lander exit %d, want 0; stdout %q stderr %q", code, out, errOut)
	}
	if !strings.Contains(out, "landed=true") || !strings.Contains(out, "kept=2 dropped=#23:mergeable-unknown") {
		t.Fatalf("stdout %q, want #21 and #22 landed and #23 dropped on a stale mergeable word", out)
	}
	if len(gate.gated) != 1 || strings.Join(gate.gated[0], " ") != "21@aaaa2121 22@bbbb2222" {
		t.Fatalf("gated %v, want [21@aaaa2121 22@bbbb2222]: the unit heads, never the retired records'", gate.gated)
	}

	code, _, errOut = run("21", "24")
	want := "nova-tools#24 has no head: MISSING " + land.PRUnitKey(S, repo, 24)
	if code != 2 || !strings.Contains(errOut, want) {
		t.Fatalf("exit %d stderr %q, want 2 and %q: a retired record is not a member", code, errOut, want)
	}
	if len(gate.gated) != 1 {
		t.Fatalf("gate ran %v on a batch with a member known only by a retired record", gate.gated)
	}
}

// TestWhyReadsUnitRecordsOnly (nova-tools#3611): `why <repo>#<n>` answers
// from the unit record s:<S>:prunit names. A retired s:<S>:pr:<repo>:<n>
// beside it (another head, another word) changes nothing, and a PR known only
// by a retired record is "no unit record", exit 1.
func TestWhyReadsUnitRecordsOnly(t *testing.T) {
	t.Parallel()

	mr := control35(t)
	const deadHead = "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef"
	mr.HSet("s:"+c35Sprint+":pr:nova-tools:3200", "head", deadHead, "mergeable", "CONFLICTING", "state", "landed")
	mr.HSet("s:"+c35Sprint+":pr:nova-tools:3201", "head", deadHead, "mergeable", "MERGEABLE", "state", "reading")

	out := why(t, mr)
	mustLine(t, out, "unit "+c35Unit+" nova-tools#3200")
	mustLine(t, out, "ci OK@4139b79f")
	mustLine(t, out, "reads 1/1 (stella 9 @4139b79f)")
	if strings.Contains(out, "deadbeef") || strings.Contains(out, "CONFLICTING") {
		t.Fatalf("why printed the retired record:\n%s", out)
	}

	code, stdout, stderr := runSprint("why", "nova-tools#3201", "--redis", mr.Addr(), "--sprint", c35Sprint, "--now", strconv.FormatInt(c35Now, 10))
	want := "why nova-tools#3201: no unit record: MISSING s:" + c35Sprint + ":prunit:nova-tools:3201"
	if code != 1 || !strings.Contains(stdout, want) {
		t.Fatalf("exit %d stdout %q stderr %q, want 1 and %q", code, stdout, stderr, want)
	}
}

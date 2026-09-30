//go:build functional

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/sprintfn"
	"github.com/mas-bandwidth/nova-tools/internal/testredis"
	"github.com/redis/go-redis/v9"
)

// storeApp is the new command on a store of its own, with this build's sprint
// profile loaded.
func storeApp(t *testing.T) (*app, func(line string) (int, string)) {
	t.Helper()
	addr := testutil.Start(t)
	c := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { c.Close() })
	if err := fn.LoadTSet(context.Background(), c, fn.TSetSprint); err != nil {
		t.Fatalf("FUNCTION LOAD of the sprint profile: %v", err)
	}
	env := map[string]string{"NOVA_SPRINT_REDIS": addr, "NOVA_SPRINT_ACTOR": "coordinator"}
	a := newApp(func(k string) string { return env[k] })
	t.Cleanup(a.close)
	return a, func(line string) (int, string) {
		var out, errb bytes.Buffer
		code := a.run(split(line), &out, &errb)
		return code, out.String() + errb.String()
	}
}

// TestNewPathAddNeedsOnAStore (the 4806 read, B3): add --needs is admitted on
// a store as on the twin, alone and placed in line: the derive phase's
// commands on wait:<n> are taken by X.plan in the Lua as the twin takes them.
// Before, every add --needs was refused CONFIG with no words.
func TestNewPathAddNeedsOnAStore(t *testing.T) {
	t.Parallel()
	_, run := storeApp(t)
	for _, line := range []string{"init", "add --stream a --count 3", "add --stream b --count 3",
		"add --stream a a-dep --needs b-2", "add --stream b b-dep --needs a-3 --after b-1", "add --stream a a-two --needs b-3,a-1 --after a-2"} {
		if code, out := run(line); code != 0 {
			t.Fatalf("%s on a store: exit %d\n%s", line, code, out)
		}
	}
	// a need that closes a loop through the gates is refused on a store too
	// (M1): a-3 needs b-4, behind b's gate, and b-loop needs a-3 before the gate
	for _, line := range []string{"add --stream b --sentinel b-gate", "add --stream b --count 2", "add --stream a a-4 --needs b-4"} {
		if code, out := run(line); code != 0 {
			t.Fatalf("%s on a store: exit %d\n%s", line, code, out)
		}
	}
	if code, out := run("add --stream b b-loop --needs a-4 --after b-1"); code != exitRefused || !strings.Contains(out, "a-4 needs b-4") || !strings.Contains(out, "b-gate") {
		t.Errorf("the add that closes a loop through the gates on a store: exit %d\n%s", code, out)
	}
}

// TestNewPathDealsOnAStore (the 4806 read, B1): the machine deals through the
// command on a store: the fleet query names the deal's rolling index, which the
// Lua listing reads, and every member up gets its share.
func TestNewPathDealsOnAStore(t *testing.T) {
	t.Parallel()
	_, run := storeApp(t)
	for _, line := range []string{"init", "reader add reader-a reader-b", "fleet up m1 m2 m3", "fleet beat m1 m2 m3", "start", "tick", "tick",
		"add --stream s1 --count 9", "tick", "tick"} {
		if code, out := run(line); code != 0 {
			t.Fatalf("%s on a store: exit %d\n%s", line, code, out)
		}
	}
	for _, m := range []string{"m1", "m2", "m3"} {
		code, out := run("queue --as " + m + " --json")
		var q struct {
			Cards []struct{ ID string } `json:"cards"`
		}
		if code != 0 || json.Unmarshal([]byte(out), &q) != nil || len(q.Cards) != 3 {
			t.Errorf("queue --as %s on a store: exit %d, want 3 cards\n%s", m, code, out)
		}
	}
	if _, out := run("inbox"); strings.Contains(out, "refused") {
		t.Errorf("the inbox on a store:\n%s", out)
	}
}

// TestCommandFCALLOnlyOnTheStore: the command on a store writes through FCALL
// and nothing else: testredis.OnlyFCALL is on the sprint client the command
// builds (sprintfn.Redis.AddHookForTest), and a suite of the verbs, the
// machine's ticks and a clear runs clean (the library is loaded through
// another client; the lifecycle's define and teardown are Layer 1's own, E7).
func TestCommandFCALLOnlyOnTheStore(t *testing.T) {
	t.Parallel()
	a, run := storeApp(t)
	a.sprintClient = func(ctx context.Context, addr string, names sprint.Names) (sprintfn.Client, func() error, error) {
		c, closer, err := a.newPathClient(ctx, addr, names)
		if err != nil {
			return c, closer, err
		}
		r, ok := c.(*sprintfn.Redis)
		if !ok {
			t.Fatalf("the new path's client is %T, not sprintfn.Redis", c)
		}
		r.AddHookForTest(testredis.OnlyFCALL(t))
		return c, closer, nil
	}
	for _, line := range []string{"init", "reader add ra rb", "fleet up m1 m2", "fleet beat m1 m2", "add --stream s1 --count 4",
		"add --stream s1 --sentinel s1-gate", "add --stream s2 s2-a --needs s1-2", "start", "tick", "tick", "tick", "where", "inbox", "log",
		"queue --as m1", "check", "stop", "clear --confirm sprint"} {
		if code, out := run(line); code != 0 {
			t.Fatalf("%s on a store: exit %d\n%s", line, code, out)
		}
	}
}

// TestNewPathNeedMetOnAStore (the drive's stall at 185 of 332): when a need
// lands, R4's needmet makes its waiter ready on a store as on the twin. The
// derive phase's entries are arrays Layer 1 validates as JSON; before, its
// lists were Lua tables of integer keys, which Layer 1 read as objects and
// refused REQUEST, and every waiter of a need stayed waiting for good.
func TestNewPathNeedMetOnAStore(t *testing.T) {
	t.Parallel()
	_, run := storeApp(t)
	ok := func(line string) string {
		t.Helper()
		code, out := run(line)
		if code != 0 {
			t.Fatalf("%s on a store: exit %d\n%s", line, code, out)
		}
		return out
	}
	for _, l := range []string{"init", "reader add ra rb", "fleet up m1", "fleet beat m1", "add --stream a --count 1",
		"add --stream b b-1 --needs a-1", "start", "tick", "tick"} {
		ok(l)
	}
	var q struct {
		Cards []struct {
			ID  string `json:"id"`
			Gen int    `json:"gen"`
		} `json:"cards"`
	}
	if err := json.Unmarshal([]byte(ok("queue --as m1 --json")), &q); err != nil || len(q.Cards) != 1 {
		t.Fatalf("the deal of a-1: %v %+v", err, q)
	}
	word := q.Cards[0].ID + "@" + strconv.Itoa(q.Cards[0].Gen)
	ok("take --as m1 " + word)
	ok("finish --as m1 " + word)
	ok("tick")
	for _, r := range []string{"ra", "rb"} {
		ok("read --as " + r + " --begin a-1.r1." + r)
		ok("read --as " + r + " --ok a-1.r1." + r)
	}
	ok("tick")
	ok("merge --stream a --batch 10")
	ok("tick")
	ok("tick")
	var w struct {
		Tables map[string]map[string]map[string]string `json:"tables"`
	}
	if err := json.Unmarshal([]byte(ok("where --json")), &w); err != nil {
		t.Fatal(err)
	}
	if got := w.Tables["work"]["b"]; got["waiting"] != "0" {
		t.Errorf("b-1 still waits after a-1 landed: %v\n%s", got, ok("inbox"))
	}
	if in := ok("inbox"); strings.Contains(in, "refused") {
		t.Errorf("a step was refused:\n%s", in)
	}
}

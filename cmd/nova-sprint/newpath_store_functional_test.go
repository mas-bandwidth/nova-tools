//go:build functional

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
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

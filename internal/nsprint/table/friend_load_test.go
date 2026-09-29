package table_test

import (
	"context"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/table"
)

// TestFriendLoadComesFromItsOwnBeat (#4233, Glenn 2026-09-26 8:58 AM ET:
// friends may run on any bench): a friend row's load is its own beat's
// load1 (what `nova-sprint friend beat` measures on the machine the
// friend's session runs on), and a friend whose beat carries no load prints
// "-" even while bench:studio:beat has one: the Studio hardcode of
// 2026-09-25 is gone.
func TestFriendLoadComesFromItsOwnBeat(t *testing.T) {
	t.Parallel()
	now := table.SprintFixtureNow()
	ms := func(d time.Duration) string { return strconv.FormatInt(now.Add(d).UnixMilli(), 10) }
	client, _ := consumerStore(t, [][]string{
		{"SADD", "friends", "emma", "rowan"},
		{"SADD", "benches", "studio"},
		{"HSET", "friend:rowan:beat", "at", ms(-time.Second), "host", "laptop", "load1", "0.50", "harness", "friend beat"},
		{"HSET", "friend:emma:beat", "at", ms(-time.Second), "session", "s1"},
		{"HSET", "bench:studio:beat", "load1", "9.99", "at", ms(-time.Second)},
	})
	snap, err := table.NewSprintReader(client, table.SprintConfig{}).Read(context.Background(), now)
	if err != nil {
		t.Fatal(err)
	}
	got := consumerBlock(t, snap.Render(now))
	for _, want := range []string{
		"emma                      |     0 |       0 |     0 |    - | up     | -\n",
		"rowan                     |     0 |       0 |     0 |    - | up     | 0.50\n",
		"studio                    |     0 |       0 |     0 |    - | up     | 9.99\n",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("consumer table lacks %q:\n%s", want, got)
		}
	}
}

// TestFriendLoadOnArbitraryBenchMachine (nova-tools#4233):
// friends may run on any bench machine (not only studio); each friend's load
// comes strictly from friend:<f>:beat's host/load1 and never studio's load.
func TestFriendLoadOnArbitraryBenchMachine(t *testing.T) {
	t.Parallel()
	now := table.SprintFixtureNow()
	ms := func(d time.Duration) string { return strconv.FormatInt(now.Add(d).UnixMilli(), 10) }
	client, _ := consumerStore(t, [][]string{
		{"SADD", "friends", "stella", "johnny"},
		{"SADD", "benches", "hetzner", "studio"},
		// stella running on hetzner
		{"HSET", "friend:stella:beat", "at", ms(-time.Second), "host", "hetzner", "load1", "2.50", "harness", "friend beat"},
		// johnny running on macpro
		{"HSET", "friend:johnny:beat", "at", ms(-time.Second), "host", "macpro", "load1", "0.15", "harness", "friend beat"},
		{"HSET", "bench:studio:beat", "load1", "8.88", "at", ms(-time.Second)},
		{"HSET", "bench:hetzner:beat", "load1", "2.50", "at", ms(-time.Second)},
	})
	snap, err := table.NewSprintReader(client, table.SprintConfig{}).Read(context.Background(), now)
	if err != nil {
		t.Fatal(err)
	}
	got := consumerBlock(t, snap.Render(now))
	for _, want := range []string{
		"johnny                    |     0 |       0 |     0 |    - | up     | 0.15\n",
		"stella                    |     0 |       0 |     0 |    - | up     | 2.50\n",
		"hetzner                   |     0 |       0 |     0 |    - | up     | 2.50\n",
		"studio                    |     0 |       0 |     0 |    - | up     | 8.88\n",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("consumer table lacks %q:\n%s", want, got)
		}
	}
}

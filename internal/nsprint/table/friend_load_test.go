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

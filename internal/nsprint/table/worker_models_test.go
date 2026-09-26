package table_test

import (
	"context"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/table"
)

// TestFriendRowNamesItsModels (seat-keeps-beat, invariant B): an up friend
// with copies in working prints its beat's models beside its name, inside
// the 25-wide worker cell (cut to it), so the row is no wider; a friend
// with none working, a down friend and a bench print the name alone.
func TestFriendRowNamesItsModels(t *testing.T) {
	t.Parallel()
	now := table.SprintFixtureNow()
	ms := func(d time.Duration) string { return strconv.FormatInt(now.Add(d).UnixMilli(), 10) }
	client, _ := consumerStore(t, [][]string{
		{"SADD", "friends", "rowan", "emma", "stella", "johnny"},
		{"SADD", "benches", "hulk"},
		{"HSET", "friend:rowan:beat", "at", ms(-time.Second), "cpu", "12.5", "models", "opus-5.5"},
		{"ZADD", "friend:rowan:cards:working", "1", "console-grammar~1", "2", "verb-read-brief~1"},
		{"HSET", "friend:emma:beat", "at", ms(-time.Second), "models", "opus-5.5"},
		{"HSET", "friend:stella:beat", "at", ms(-2 * time.Minute), "models", "sonnet-5"},
		{"ZADD", "friend:stella:cards:working", "1", "a~1"},
		{"HSET", "friend:johnny:beat", "at", ms(-time.Second), "models", "claude-opus-5-5-20260901,sonnet-5"},
		{"ZADD", "friend:johnny:cards:working", "1", "b~1"},
		{"HSET", "bench:hulk:beat", "at", ms(-time.Second), "load1", "0.40", "models", "x"},
		{"ZADD", "bench:hulk:cards:working", "1", "c~1"},
	})
	snap, err := table.NewSprintReader(client, table.SprintConfig{Friends: []string{"rowan", "emma", "stella", "johnny"}}).Read(context.Background(), now)
	if err != nil {
		t.Fatal(err)
	}
	block := consumerBlock(t, snap.Render(now))
	want := []string{
		"rowan opus-5.5            |     0 |       2 |     0 |    - | up     | 12.5%",
		"emma                      |     0 |       0 |     0 |    - | up     | -",
		"stella                    |     0 |       1 |     0 |    - | down   | -",
		"johnny claude-opus-5-5-20 |     0 |       1 |     0 |    - | up     | -",
		"hulk                      |     0 |       1 |     0 |    - | up     | 0.40",
	}
	rows := strings.Split(block, "\n")[2:7]
	if strings.Join(rows, "\n") != strings.Join(want, "\n") {
		t.Fatalf("rows:\n%s\nwant:\n%s", strings.Join(rows, "\n"), strings.Join(want, "\n"))
	}
	header := strings.Split(block, "\n")[0]
	if !strings.HasPrefix(header, "worker                    | ready |") {
		t.Fatalf("header moved: %q", header)
	}
}

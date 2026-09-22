package friendqueue

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

// TestSeededStreamEntryAppearsAndPriorityTSVDoesNotOverrideIt is the read.
// The dealer seeds q:<friend> (and q:<friend>:front) with task, kind and ref.
// That entry is the queue the table column counts. A hand-written
// PRIORITY.tsv sitting in the working directory, naming a different task and
// the word OVERRIDE, does not replace it and does not join it.
func TestSeededStreamEntryAppearsAndPriorityTSVDoesNotOverrideIt(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	ctx := context.Background()

	front, bulk, err := Keys("Johnny")
	if err != nil {
		t.Fatal(err)
	}
	if front != "q:johnny:front" || bulk != "q:johnny" {
		t.Fatalf("keys = %s then %s; the dealer writes q:<friend>:front and q:<friend>", front, bulk)
	}

	frontID := mustXAdd(t, ctx, rdb, front, map[string]interface{}{
		"task":        "read-9002",
		"kind":        "read",
		"ref":         "mas-bandwidth/nova-tools#9002",
		"est_minutes": "10",
	})
	bulkID := mustXAdd(t, ctx, rdb, bulk, map[string]interface{}{
		"task":        "read-9001",
		"kind":        "read",
		"ref":         "mas-bandwidth/nova-tools#9001",
		"est_minutes": "10",
	})
	// Not a dealt task: no task field. It must not become a queue item.
	mustXAdd(t, ctx, rdb, bulk, map[string]interface{}{
		"note": "PRIORITY",
	})
	// A second copy of the front task, with a different ref, must not
	// replace the earlier entry.
	mustXAdd(t, ctx, rdb, bulk, map[string]interface{}{
		"task": "read-9002",
		"kind": "read",
		"ref":  "OVERRIDE-FROM-LATER-COPY",
	})

	dir := t.TempDir()
	prio := filepath.Join(dir, "PRIORITY.tsv")
	const priorityLine = "7777\tfrom-priority-tsv\tOVERRIDE\tnot-the-seeded-entry\n"
	if err := os.WriteFile(prio, []byte(priorityLine), 0o644); err != nil {
		t.Fatal(err)
	}
	// A reader that still opened PRIORITY.tsv from the working directory
	// would see this file. The queue must not change.
	t.Chdir(dir)

	body, err := os.ReadFile(prio)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Read(ctx, rdb, "Johnny", string(body))
	if err != nil {
		t.Fatal(err)
	}
	if got.Who != "johnny" {
		t.Fatalf("who = %q, want johnny", got.Who)
	}
	if got.N() != 2 {
		t.Fatalf("queue n = %d, want the two seeded tasks; items %+v", got.N(), got.Items)
	}
	want := []Item{
		{ID: frontID, Stream: front, Task: "read-9002", Kind: "read", Ref: "mas-bandwidth/nova-tools#9002", Front: true},
		{ID: bulkID, Stream: bulk, Task: "read-9001", Kind: "read", Ref: "mas-bandwidth/nova-tools#9001", Front: false},
	}
	for i, w := range want {
		if got.Items[i] != w {
			t.Errorf("item %d: got %+v, want %+v", i, got.Items[i], w)
		}
	}
	shown := got.Shown()
	for _, needle := range []string{"task=read-9001", "task=read-9002", "ref=mas-bandwidth/nova-tools#9001", "stream=q:johnny", "n=2"} {
		if !strings.Contains(shown, needle) {
			t.Errorf("shown queue missing %q:\n%s", needle, shown)
		}
	}
	for _, banned := range []string{"7777", "OVERRIDE", "from-priority-tsv", "PRIORITY.tsv", "not-the-seeded-entry"} {
		if strings.Contains(shown, banned) {
			t.Errorf("PRIORITY.tsv line overrode the seeded entry; %q is in the queue:\n%s", banned, shown)
		}
	}
	// The seeded bulk entry is still the bulk entry, not the TSV's task.
	if got.Items[1].Task != "read-9001" || got.Items[1].Ref != "mas-bandwidth/nova-tools#9001" {
		t.Fatalf("seeded entry was replaced: %+v", got.Items[1])
	}
}

func TestAnAbsentStreamIsAnEmptyQueue(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	got, err := Read(context.Background(), rdb, "stella", "7777\tOVERRIDE\n")
	if err != nil {
		t.Fatal(err)
	}
	if got.N() != 0 || got.Who != "stella" {
		t.Fatalf("got %+v, want an empty queue for stella", got)
	}
	if strings.Contains(got.Shown(), "7777") || strings.Contains(got.Shown(), "OVERRIDE") {
		t.Fatalf("an absent stream still showed the PRIORITY.tsv line:\n%s", got.Shown())
	}
}

func TestReadRefusesAFriendNameThatIsNotAQueueKey(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	for _, name := range []string{"", "johnny:front", "two words", "q:johnny"} {
		if _, err := Read(context.Background(), rdb, name, ""); err == nil {
			t.Errorf("name %q was accepted as a queue key", name)
		}
	}
}

func mustXAdd(t *testing.T, ctx context.Context, rdb *redis.Client, stream string, values map[string]interface{}) string {
	t.Helper()
	id, err := rdb.XAdd(ctx, &redis.XAddArgs{Stream: stream, Values: values}).Result()
	if err != nil {
		t.Fatalf("XADD %s %v: %s", stream, values, err)
	}
	return id
}

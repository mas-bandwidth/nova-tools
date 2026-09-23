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
	// The stream entry is history. It counts only while task:<id> says this
	// friend still owns it and state is open.
	mustHSet(t, ctx, rdb, "read-9002", "johnny", stateOpen)
	mustHSet(t, ctx, rdb, "read-9001", "johnny", stateOpen)

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

// TestClosedEntryInStreamHistoryDoesNotCountAsLiveQueued is the state
// filter. One friend's stream history holds a queued task (hash state
// open), a working task, and a closed task. The dealer does not delete on
// close, so the closed entry is still in the stream. It must not count.
// Working is leased, not queued. An open task owned by someone else is
// not this friend's queue. A PRIORITY.tsv line is still not a source.
func TestClosedEntryInStreamHistoryDoesNotCountAsLiveQueued(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	ctx := context.Background()

	front, bulk, err := Keys("johnny")
	if err != nil {
		t.Fatal(err)
	}

	// Front holds an older copy of the closed task. It would sort first if
	// every stream id counted. The hash says closed, so it does not.
	mustXAdd(t, ctx, rdb, front, map[string]interface{}{
		"task": "read-closed",
		"kind": "read",
		"ref":  "mas-bandwidth/nova-tools#1",
	})
	// One bulk stream's history, in placement order: still queued, then
	// leased, then closed, then the closed id placed again, then an open
	// task that belongs to someone else.
	queuedID := mustXAdd(t, ctx, rdb, bulk, map[string]interface{}{
		"task": "read-queued",
		"kind": "read",
		"ref":  "mas-bandwidth/nova-tools#2",
	})
	mustXAdd(t, ctx, rdb, bulk, map[string]interface{}{
		"task": "read-working",
		"kind": "read",
		"ref":  "mas-bandwidth/nova-tools#3",
	})
	mustXAdd(t, ctx, rdb, bulk, map[string]interface{}{
		"task": "read-closed",
		"kind": "read",
		"ref":  "mas-bandwidth/nova-tools#1",
	})
	mustXAdd(t, ctx, rdb, bulk, map[string]interface{}{
		"task": "read-closed",
		"kind": "read",
		"ref":  "STILL-IN-THE-STREAM",
	})
	mustXAdd(t, ctx, rdb, bulk, map[string]interface{}{
		"task": "read-other",
		"kind": "read",
		"ref":  "mas-bandwidth/nova-tools#4",
	})
	mustHSet(t, ctx, rdb, "read-queued", "johnny", stateOpen)
	mustHSet(t, ctx, rdb, "read-working", "johnny", "working")
	mustHSet(t, ctx, rdb, "read-closed", "johnny", "closed")
	mustHSet(t, ctx, rdb, "read-other", "emma", stateOpen)

	dir := t.TempDir()
	prio := filepath.Join(dir, "PRIORITY.tsv")
	const priorityLine = "7777\tfrom-priority-tsv\tOVERRIDE\tnot-the-seeded-entry\n"
	if err := os.WriteFile(prio, []byte(priorityLine), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	body, err := os.ReadFile(prio)
	if err != nil {
		t.Fatal(err)
	}

	got, err := Read(ctx, rdb, "johnny", string(body))
	if err != nil {
		t.Fatal(err)
	}
	if got.N() != 1 {
		t.Fatalf("live queued n = %d, want only the open task; items %+v", got.N(), got.Items)
	}
	want := Item{ID: queuedID, Stream: bulk, Task: "read-queued", Kind: "read", Ref: "mas-bandwidth/nova-tools#2", Front: false}
	if got.Items[0] != want {
		t.Fatalf("queued item: got %+v, want %+v", got.Items[0], want)
	}
	for _, banned := range []string{"read-working", "read-closed", "read-other", "7777", "OVERRIDE", "from-priority-tsv", "STILL-IN-THE-STREAM"} {
		if strings.Contains(got.Shown(), banned) {
			t.Errorf("live queued count includes %q:\n%s", banned, got.Shown())
		}
	}
	n, err := rdb.XLen(ctx, bulk).Result()
	if err != nil {
		t.Fatal(err)
	}
	if n < 3 {
		t.Fatalf("bulk stream len = %d; the closed entry must stay in the stream history", n)
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

func mustHSet(t *testing.T, ctx context.Context, rdb *redis.Client, id, owner, state string) {
	t.Helper()
	if err := rdb.HSet(ctx, taskPrefix+id, fieldOwner, owner, fieldState, state).Err(); err != nil {
		t.Fatalf("HSET %s%s: %s", taskPrefix, id, err)
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

// Package friendqueue reads a friend's queue from the dealer's streams.
//
// nova-tools #2677. The sprint table's friend queue column used to count
// pull requests listed in a hand-written PRIORITY.tsv. nova-pulse sprint
// route/refill deals work onto Redis streams q:<friend> and
// q:<friend>:front, one entry per task with fields task, kind and ref.
// Current owner and state live on the hash task:<id>. The dealer appends
// to the stream and does not delete it on close, so a stream entry is not
// itself the queue. This package reads both. It does not open PRIORITY.tsv,
// it does not call gh, and it does not render the table.
package friendqueue

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/redis/go-redis/v9"
)

const (
	// Prefix is the dealer's stream prefix. The friend's name is the only
	// variable: there is no fixed set of queues.
	Prefix = "q:"
	// FrontSuffix is the priority stream beside the bulk one.
	FrontSuffix = ":front"

	// FieldTask, FieldKind and FieldRef are the fields Place writes.
	FieldTask = "task"
	FieldKind = "kind"
	FieldRef  = "ref"

	// taskPrefix is the dealer's hash key. owner and state on that hash are
	// current; the stream only remembers that the task was placed.
	taskPrefix = "task:"
	fieldOwner = "owner"
	fieldState = "state"
	// stateOpen is the queued state: owned, not leased, not closed.
	// working is leased. closed stays on the stream and is not queued.
	stateOpen = "open"
)

// Item is one dealt task still on the friend's stream. ID is the Redis
// stream id. Stream is q:<friend> or q:<friend>:front.
type Item struct {
	ID     string
	Stream string
	Task   string
	Kind   string
	Ref    string
	Front  bool
}

// Queue is what the table's queue column and the sprint verb count: the
// friend's dealt tasks, priority stream first. N is the column.
type Queue struct {
	Who   string
	Items []Item
}

// N is the queue column: stream entries whose task hash still says this
// friend owns them and state is open. A working or closed entry the stream
// still holds is not in it.
func (q Queue) N() int { return len(q.Items) }

// Shown is the queue as one block of scannable lines. The first line is the
// column; each following line is one seeded entry. A PRIORITY.tsv line is
// not among them.
func (q Queue) Shown() string {
	var b strings.Builder
	fmt.Fprintf(&b, "QUEUE who=%s n=%d\n", oneline.Field(q.Who), q.N())
	for _, it := range q.Items {
		fmt.Fprintf(&b, "QUEUE who=%s task=%s kind=%s ref=%s stream=%s id=%s\n",
			oneline.Field(q.Who), oneline.Field(it.Task), oneline.Field(it.Kind),
			oneline.Field(it.Ref), oneline.Field(it.Stream), oneline.Field(it.ID))
	}
	return b.String()
}

// Keys are the two streams the dealer writes for this friend: the priority
// stream, then the bulk stream q:<friend>.
func Keys(friend string) (front, bulk string, err error) {
	name, err := normalize(friend)
	if err != nil {
		return "", "", err
	}
	return Prefix + name + FrontSuffix, Prefix + name, nil
}

// Read returns the friend's live queued tasks from q:<friend>:front and then
// q:<friend>.
//
// A task id is kept once, the earlier entry: the front stream is read first,
// so a later copy does not replace it. An entry with no task field is not a
// dealt task and is skipped. The kept entry counts only when task:<id>
// currently names this friend as owner and state open. A missing hash, another
// owner, state working, or state closed does not count. A stream that does not
// exist is an empty queue, not an error. priorityTSV is the body of the
// hand-written PRIORITY.tsv the column used to count. It is not parsed: a line
// in it does not add an item and does not replace a stream entry.
//
// The read is two round trips, never 2 + n (#3275): both streams in one
// pipeline, then every task:<id> hash in one second pipeline. At 83 ms a
// round trip, the serial 2 + n reader spent 2.8 minutes on 2,025 entries;
// two trips spend 166 ms whatever n is. A pipeline is not a multi-key step:
// each command stands alone and none depends on another's write, so no Lua
// and no transaction is owed.
func Read(ctx context.Context, rdb redis.Cmdable, friend, priorityTSV string) (Queue, error) {
	// The file is not a source. The argument stays so a caller that still
	// holds the list passes it here, where it loses, instead of counting it.
	_ = priorityTSV
	if rdb == nil {
		return Queue{}, fmt.Errorf("no redis; the queue is read from q:<friend>, refusing to guess")
	}
	name, err := normalize(friend)
	if err != nil {
		return Queue{}, err
	}
	front, bulk, err := Keys(name)
	if err != nil {
		return Queue{}, err
	}
	// Round trip 1: both streams at once. The front stream's entries are
	// folded in first, so a task on both streams keeps its front entry.
	pipe := rdb.Pipeline()
	frontCmd := pipe.XRange(ctx, front, "-", "+")
	bulkCmd := pipe.XRange(ctx, bulk, "-", "+")
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return Queue{}, err
	}
	frontMsgs, err := frontCmd.Result()
	if err != nil && !errors.Is(err, redis.Nil) {
		return Queue{}, fmt.Errorf("read %s: %w", front, err)
	}
	bulkMsgs, err := bulkCmd.Result()
	if err != nil && !errors.Is(err, redis.Nil) {
		return Queue{}, fmt.Errorf("read %s: %w", bulk, err)
	}
	seen := map[string]struct{}{}
	q := Queue{Who: name}
	q.Items = append(q.Items, streamItems(frontMsgs, front, true, seen)...)
	q.Items = append(q.Items, streamItems(bulkMsgs, bulk, false, seen)...)
	if len(q.Items) == 0 {
		return q, nil
	}
	// Round trip 2: every task:<id> hash at once. The stream is placement
	// history; the hash is current, so each kept entry answers against its
	// own hash.
	hashes := make(map[string]*redis.SliceCmd, len(q.Items))
	pipe = rdb.Pipeline()
	for _, it := range q.Items {
		if _, done := hashes[it.Task]; done {
			continue
		}
		hashes[it.Task] = pipe.HMGet(ctx, taskPrefix+it.Task, fieldOwner, fieldState)
	}
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return Queue{}, err
	}
	live := make([]Item, 0, len(q.Items))
	for _, it := range q.Items {
		ok, err := hashIsLiveQueued(name, hashes[it.Task])
		if err != nil {
			return Queue{}, err
		}
		if ok {
			live = append(live, it)
		}
	}
	q.Items = live
	return q, nil
}

// hashIsLiveQueued reads the fetched task:<id> hash: it counts when owner
// names this friend and state is open. A missing hash, another owner, state
// working, or state closed does not count.
func hashIsLiveQueued(friend string, get *redis.SliceCmd) (bool, error) {
	vals, err := get.Result()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return false, nil
		}
		return false, fmt.Errorf("read %s: %w", get.Args()[1], err)
	}
	owner, state := "", ""
	if len(vals) > 0 {
		owner = strings.TrimSpace(asString(vals[0]))
	}
	if len(vals) > 1 {
		state = strings.TrimSpace(asString(vals[1]))
	}
	if owner == "" || state == "" {
		return false, nil
	}
	name, err := normalize(owner)
	if err != nil || name != friend {
		return false, nil
	}
	return state == stateOpen, nil
}

// streamItems folds one stream's entries into queue items, earliest first:
// a task seen on an earlier stream is not replaced by this one, and an entry
// with no task field is not a dealt task.
func streamItems(msgs []redis.XMessage, stream string, front bool, seen map[string]struct{}) []Item {
	var out []Item
	for _, m := range msgs {
		task := strings.TrimSpace(asString(m.Values[FieldTask]))
		if task == "" {
			continue
		}
		if _, ok := seen[task]; ok {
			continue
		}
		seen[task] = struct{}{}
		out = append(out, Item{
			ID:     m.ID,
			Stream: stream,
			Task:   task,
			Kind:   strings.TrimSpace(asString(m.Values[FieldKind])),
			Ref:    strings.TrimSpace(asString(m.Values[FieldRef])),
			Front:  front,
		})
	}
	return out
}

func asString(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case []byte:
		return string(t)
	default:
		return fmt.Sprint(t)
	}
}

func normalize(friend string) (string, error) {
	name := strings.ToLower(strings.TrimSpace(friend))
	if name == "" {
		return "", fmt.Errorf("friend name is empty; the queue is q:<friend>")
	}
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-', r == '_':
		default:
			return "", fmt.Errorf("friend name %q is not a queue key; the queue is q:<friend> and the name is one token of letters, digits, '-' and '_'", friend)
		}
	}
	return name, nil
}

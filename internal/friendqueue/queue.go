// Package friendqueue reads a friend's queue from the dealer's streams.
//
// nova-tools #2677. The sprint table's friend queue column used to count
// pull requests listed in a hand-written PRIORITY.tsv. nova-pulse sprint
// route/refill deals work onto Redis streams q:<friend> and
// q:<friend>:front, one entry per task with fields task, kind and ref.
// This package reads those streams. It does not open PRIORITY.tsv, it
// does not call gh, and it does not render the table.
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

// N is the queue column: how many dealt tasks the streams hold.
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

// Read returns the friend's queue from q:<friend>:front and then q:<friend>.
//
// A task id is kept once, the earlier entry: the front stream is read first,
// so a later copy does not replace it. An entry with no task field is not a
// dealt task and is skipped. A stream that does not exist is an empty queue,
// not an error. priorityTSV is the body of the hand-written PRIORITY.tsv the
// column used to count. It is not parsed: a line in it does not add an item
// and does not replace a stream entry.
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
	q := Queue{Who: name}
	seen := map[string]struct{}{}
	for _, spec := range []struct {
		stream string
		front  bool
	}{
		{front, true},
		{bulk, false},
	} {
		items, err := readStream(ctx, rdb, spec.stream, spec.front, seen)
		if err != nil {
			return Queue{}, err
		}
		q.Items = append(q.Items, items...)
	}
	return q, nil
}

func readStream(ctx context.Context, rdb redis.Cmdable, stream string, front bool, seen map[string]struct{}) ([]Item, error) {
	msgs, err := rdb.XRange(ctx, stream, "-", "+").Result()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return nil, nil
		}
		return nil, fmt.Errorf("read %s: %w", stream, err)
	}
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
	return out, nil
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

package main

// readevent.go is the read verb's one write to the store (nova-tools #2683). The 1 s
// table's done column counts a friend's kind=read events on cards:done today (#2678):
// a typed line becomes one such event when this tool parses it. The lane-branch record
// stays the truth; the stream entry is the projection the table reads, so the table
// makes no per-tick GitHub call.

import (
	"context"
	"strings"

	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/events"
	"github.com/mas-bandwidth/nova-tools/internal/friendread"
)

// emitReadEvent XADDs the one kind=read event of a recorded read: event=read, the
// friend, the verdict in the fold's alphabet, the head the verdict binds to, the pull
// request when the entry is one, and the record's own stamp. One Deliver is one event;
// the id Redis answers is how a redelivery is told from a second read. The stream is
// trimmed the way every writer trims it (MAXLEN ~ events.MaxLen).
func emitReadEvent(ctx context.Context, rdb *redis.Client, who, verdict, head, pr, at string) (string, error) {
	fields := map[string]any{
		"event":   friendread.EventRead,
		"who":     who,
		"verdict": strings.ToUpper(verdict),
		"head":    head,
		"at":      at,
	}
	if pr != "" {
		fields["pr"] = pr
	}
	return rdb.XAdd(ctx, &redis.XAddArgs{
		Stream: friendread.Stream,
		MaxLen: events.MaxLen,
		Approx: true,
		Values: fields,
	}).Result()
}

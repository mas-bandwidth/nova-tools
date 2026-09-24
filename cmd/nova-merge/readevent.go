package main

// readevent.go is the read verb's one write to the store (nova-tools #2683). The 1 s
// table's done column counts a friend's kind=read events on cards:done today (#2678):
// a typed line becomes one such event when this tool parses it. The lane-branch record
// stays the truth; the stream entry is the projection the table reads, so the table
// makes no per-tick GitHub call.
//
// The write is idempotent. The read verb tells its caller to re-run it when the record
// is pushed and the event is not, and every XADD gets a fresh stream id, so a stream id
// cannot tell a retry from a second read. The event carries a stable identity instead,
// derived from the read itself -- the entry, the friend, the head and the verdict -- and
// one script claims that identity and appends the event together: a retry of the same
// read finds the claim and appends nothing, and a failed append leaves no claim behind.

import (
	"context"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/events"
	"github.com/mas-bandwidth/nova-tools/internal/friendread"
)

// readEventClaimTTL is how long a read's identity stays claimed. The fold counts one
// UTC day, so a week outlives every retry that could land in a counted day.
const readEventClaimTTL = 7 * 24 * time.Hour

// readEventKeyPrefix names the claim keys, one per read identity.
const readEventKeyPrefix = "cards:done:read:"

// readEventID is the stable identity of one read: the same entry, friend, head and
// verdict is the same read however many times the verb is run to post it.
func readEventID(entry, who, head, verdict string) string {
	return strings.Join([]string{
		strings.TrimSpace(entry),
		strings.ToLower(strings.TrimSpace(who)),
		strings.ToLower(strings.TrimSpace(head)),
		strings.ToUpper(strings.TrimSpace(verdict)),
	}, "|")
}

// emitReadScript claims KEYS[1] and appends to KEYS[2] in one step. It answers the new
// stream id, or false when the identity was already claimed (a retry of a posted read).
// ARGV: claim ttl ms, maxlen, then field/value pairs.
var emitReadScript = redis.NewScript(`
if redis.call('EXISTS', KEYS[1]) == 1 then
  return false
end
local fields = {}
for i = 3, #ARGV do fields[#fields + 1] = ARGV[i] end
local id = redis.call('XADD', KEYS[2], 'MAXLEN', '~', ARGV[2], '*', unpack(fields))
redis.call('SET', KEYS[1], id, 'PX', ARGV[1])
return id
`)

// emitReadEvent XADDs the one kind=read event of a recorded read: event=read, the
// friend, the verdict in the fold's alphabet, the head the verdict binds to, the pull
// request when the entry is one, the record's own stamp, and read=<identity>. entry
// is the lane's entry id (the pull request number or the branch). It answers the new
// stream id and posted=true, or posted=false when this read's event is already on the
// stream -- a retry is not a second read. The stream is trimmed the way every writer
// trims it (MAXLEN ~ events.MaxLen).
func emitReadEvent(ctx context.Context, rdb *redis.Client, entry, who, verdict, head, pr, at string) (string, bool, error) {
	rid := readEventID(entry, who, head, verdict)
	argv := []any{readEventClaimTTL.Milliseconds(), events.MaxLen,
		"event", friendread.EventRead,
		"who", who,
		"verdict", strings.ToUpper(verdict),
		"head", head,
		"at", at,
		"read", rid,
	}
	if pr != "" {
		argv = append(argv, "pr", pr)
	}
	res, err := emitReadScript.Run(ctx, rdb, []string{readEventKeyPrefix + rid, friendread.Stream}, argv...).Result()
	if err == redis.Nil {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	id, _ := res.(string)
	return id, true, nil
}

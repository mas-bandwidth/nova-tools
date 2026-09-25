package wake

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/ghevent"
	"github.com/redis/go-redis/v9"
)

// EvGithub reads ev:github, the stream the webhook receiver writes one entry
// per carried GitHub delivery onto (internal/ghevent), for `nova-wake watch
// --store` (nova-tools #3876). It only reads: TIP once for a cold cursor, then
// XREAD BLOCK from the cursor, so a watch on a pull request runs no gh and no
// git fetch and returns the moment Redis serves a matching entry.
type EvGithub struct{ rdb *redis.Client }

// EvGithubStream is the stream name, the one ghevent writes.
const EvGithubStream = ghevent.Stream

// Event is one ev:github entry, the fields a watch prints, each trimmed.
type Event struct {
	ID, Repo, Number, Kind, Action, Head, Sender, At string
}

// OpenEvGithub makes the client for addr (host:port, already normalized).
// user and password are set only when password is non-empty: a store with no
// ACL is dialled with no credentials, because an empty AUTH is a failed login.
// No round trip happens here; the first read is the first call.
func OpenEvGithub(addr, user, password string) *EvGithub {
	opts := &redis.Options{Addr: addr, ContextTimeoutEnabled: true}
	if password != "" {
		opts.Username, opts.Password = user, password
	}
	return &EvGithub{rdb: redis.NewClient(opts)}
}

// Close releases the client.
func (e *EvGithub) Close() error { return e.rdb.Close() }

// Tip is the id of the newest entry, or "0-0" when the stream is empty or
// absent: the cursor a cold watch starts from, so history is not news.
func (e *EvGithub) Tip(ctx context.Context) (string, error) {
	msgs, err := e.rdb.XRevRangeN(ctx, ghevent.Stream, "+", "-", 1).Result()
	if err != nil {
		return "", err
	}
	if len(msgs) == 0 {
		return "0-0", nil
	}
	return msgs[0].ID, nil
}

// Read blocks up to block for entries after cursor, at most count of them.
// A block that ends with nothing is (nil, nil), not an error.
func (e *EvGithub) Read(ctx context.Context, cursor string, count int64, block time.Duration) ([]Event, error) {
	res, err := e.rdb.XRead(ctx, &redis.XReadArgs{
		Streams: []string{ghevent.Stream, cursor}, Count: count, Block: block,
	}).Result()
	if errors.Is(err, redis.Nil) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []Event
	for _, s := range res {
		for _, m := range s.Messages {
			f := func(name string) string {
				v, _ := m.Values[name].(string)
				return strings.TrimSpace(v)
			}
			out = append(out, Event{ID: m.ID, Repo: f("repo"), Number: f("number"), Kind: f("kind"),
				Action: f("action"), Head: f("head"), Sender: f("sender"), At: f("at")})
		}
	}
	return out, nil
}

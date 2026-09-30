package ghevent

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/redisconn"
	"github.com/redis/go-redis/v9"
)

// Reader reads ev:github, the stream Publish writes one entry onto per carried
// GitHub delivery. It only reads: Tip once for a cold cursor, then XREAD BLOCK
// from the cursor. The connection is opened through redisconn, the one way a
// nova tool opens its Redis connection.
type Reader struct{ conn *redisconn.Conn }

// Event is one ev:github entry, the fields a reader prints, each trimmed.
type Event struct {
	ID, Repo, Number, Kind, Action, Head, Sender, At string
}

// OpenReader opens the connection to the store o names (redisconn.Open; a nil
// getenv reads no environment) and returns the reader on it.
func OpenReader(ctx context.Context, o redisconn.Options, getenv func(string) string) (*Reader, error) {
	conn, err := redisconn.Open(ctx, o, getenv)
	if err != nil {
		return nil, err
	}
	return &Reader{conn: conn}, nil
}

// Close releases the connection.
func (r *Reader) Close() error { return r.conn.Close() }

// Tip is the id of the newest entry, or "0-0" when the stream is empty or
// absent: the cursor a cold reader starts from, so history is not news.
func (r *Reader) Tip(ctx context.Context) (string, error) {
	msgs, err := r.conn.Client().XRevRangeN(ctx, Stream, "+", "-", 1).Result()
	if err != nil {
		return "", r.conn.Explain(err)
	}
	if len(msgs) == 0 {
		return "0-0", nil
	}
	return msgs[0].ID, nil
}

// Read blocks up to block for entries after cursor, at most count of them.
// A block that ends with nothing is (nil, nil), not an error.
func (r *Reader) Read(ctx context.Context, cursor string, count int64, block time.Duration) ([]Event, error) {
	res, err := r.conn.Client().XRead(ctx, &redis.XReadArgs{
		Streams: []string{Stream, cursor}, Count: count, Block: block,
	}).Result()
	if errors.Is(err, redis.Nil) {
		return nil, nil
	}
	if err != nil {
		return nil, r.conn.Explain(err)
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

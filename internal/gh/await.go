package gh

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/ghevent"
	"github.com/redis/go-redis/v9"
)

// Events over polling (#4343 BUILD 2): anything that waits on GitHub state
// blocks on ev:github, the stream the webhook ingest (#2657) appends every
// delivery to, and never reads that state from GitHub. Await is one XREAD
// BLOCK from a cursor; a caller that waits for a head's checks re-reads
// the Redis record after each wake.

// Match decides whether one ev:github entry is the one waited for.
type Match func(fields map[string]any) bool

// Tip is the stream's last id, or 0-0 for an empty stream: the cursor a
// wait starts from so nothing already there is missed by a $ read.
func Tip(ctx context.Context, rdb redis.Cmdable) (string, error) {
	msgs, err := rdb.XRevRangeN(ctx, ghevent.Stream, "+", "-", 1).Result()
	if err != nil && !errors.Is(err, redis.Nil) {
		return "", fmt.Errorf("XREVRANGE %s: %w", ghevent.Stream, err)
	}
	if len(msgs) == 0 {
		return "0-0", nil
	}
	return msgs[0].ID, nil
}

// Await reads ev:github after tip until an entry matches or block passes
// (block <= 0 reads what is there and does not block): a batch of entries
// that match nothing keeps the wait blocking for the time left. It returns
// the cursor to continue from and whether an entry matched; entries after
// the match are left for the next wait.
func Await(ctx context.Context, rdb redis.Cmdable, tip string, block time.Duration, match Match) (string, bool, error) {
	if tip == "" {
		tip = "0-0"
	}
	deadline := time.Now().Add(block)
	for {
		left := time.Duration(-1) // go-redis: no BLOCK
		if block > 0 {
			if left = time.Until(deadline); left <= 0 {
				return tip, false, nil
			}
		}
		res, err := rdb.XRead(ctx, &redis.XReadArgs{Streams: []string{ghevent.Stream, tip}, Count: 100, Block: left}).Result()
		if err != nil {
			if errors.Is(err, redis.Nil) {
				return tip, false, nil
			}
			return tip, false, fmt.Errorf("XREAD %s: %w", ghevent.Stream, err)
		}
		n := 0
		for _, s := range res {
			for _, m := range s.Messages {
				n++
				tip = m.ID
				if match != nil && match(m.Values) {
					return tip, true, nil
				}
			}
		}
		if block <= 0 || n == 0 {
			return tip, false, nil
		}
	}
}

// HeadEvent matches a check_run, check_suite, workflow_run or pull_request
// entry for head: the deliveries that move a head's check state.
func HeadEvent(head string) Match {
	return func(f map[string]any) bool {
		if str(f["head"]) != head {
			return false
		}
		switch str(f["kind"]) {
		case "check_run", "check_suite", "workflow_run", "pull_request", "merge_group":
			return true
		}
		return false
	}
}

// PREvent matches a pull_request entry for repo#n (closed, merged, ...).
func PREvent(repo, n string) Match {
	return func(f map[string]any) bool {
		return str(f["kind"]) == "pull_request" && str(f["repo"]) == repo && str(f["number"]) == n
	}
}

func str(v any) string {
	s, _ := v.(string)
	return s
}

package land

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/redis/go-redis/v9"
)

// RedKey is land:<repo>:<base>:red, the dev-red hold (nova-tools #3629):
// the reconciler's dev-red duty sets it while the base's own CI at its tip
// is red and deletes it on green. A lander that would merge a stream into
// the base reads it first and refuses while it is set; the fix task the
// duty pushed is named in the record.
//
//	land:<repo>:<base>:red  hash  sha (the red tip), check (the failing
//	                              check or test), task (the fix task id
//	                              pushed once), to (its queue), at (ms)
func RedKey(repo, base string) string {
	return fmt.Sprintf("land:%s:%s:red", repo, base)
}

// Red is one dev-red record.
type Red struct {
	SHA   string
	Check string
	Task  string
	To    string
	At    string
}

// Line is the `dev-red status` row: RED <check> <sha>.
func (r Red) Line() string {
	return strings.TrimRight(fmt.Sprintf("RED %s %s task=%s", r.Check, r.SHA, r.Task), " ")
}

// ReadRed reads the dev-red record; ok is false when the base is not held.
func ReadRed(ctx context.Context, c redis.Cmdable, repo, base string) (Red, bool, error) {
	m, err := c.HGetAll(ctx, RedKey(repo, base)).Result()
	if errors.Is(err, redis.Nil) || (err == nil && len(m) == 0) {
		return Red{}, false, nil
	}
	if err != nil {
		return Red{}, false, fmt.Errorf("read %s: %w", RedKey(repo, base), err)
	}
	return Red{SHA: m["sha"], Check: m["check"], Task: m["task"], To: m["to"], At: m["at"]}, true, nil
}

// RedBlocked is the lander's gate: the reason a merge into repo/base must
// wait, or "" when the base is green. One HGETALL.
func RedBlocked(ctx context.Context, c redis.Cmdable, repo, base string) (string, error) {
	r, ok, err := ReadRed(ctx, c, repo, base)
	if err != nil || !ok {
		return "", err
	}
	return fmt.Sprintf("dev-red: %s red at %s on %s/%s (fix task %s); merges wait until %s is green", r.Check, short(r.SHA), repo, base, r.Task, base), nil
}

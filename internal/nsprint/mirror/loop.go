package mirror

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/redis/go-redis/v9"
)

// Pass is one refresh and its receipt: the pass, one TIME, one HSET. With all
// set it prints every repo's line (--once); otherwise only a line whose tip
// moved since last, or a refusal (the loop: silence while nothing changes).
// It reports whether every repo was OK.
func Pass(ctx context.Context, c Config, rdb redis.Cmdable, last map[string]string, all bool, out io.Writer) (bool, error) {
	results := Refresh(ctx, c)
	at, err := Now(ctx, rdb)
	if err != nil {
		return false, err
	}
	if err := Record(ctx, rdb, c.Bench, at, results); err != nil {
		return false, err
	}
	ok := true
	for _, r := range results {
		if !r.OK {
			ok = false
		}
		changed := r.OK && last != nil && last[r.Repo] != r.Tip
		if all || !r.OK || changed || r.Repaired {
			fmt.Fprintln(out, r.Line(c.Bench))
		}
		if r.OK && last != nil {
			last[r.Repo] = r.Tip
		}
	}
	return ok, nil
}

// Loop holds lease:mirror:<b> and runs one Pass per tick until ctx ends. A
// second copy on the same bench exits 2 with REFUSED <b> - reason=lease. The
// lease lives three ticks, so a dead loop frees it within that.
func Loop(ctx context.Context, c Config, rdb redis.Cmdable, ticks <-chan time.Time, every time.Duration, session string, out, errOut io.Writer) int {
	ttl := 3 * every
	if ttl < 30*time.Second {
		ttl = 30 * time.Second
	}
	held, holder, err := Lease(ctx, rdb, c.Bench, session, ttl)
	if err != nil {
		fmt.Fprintf(errOut, "REFUSED %s - reason=redis err=%s\n", c.Bench, oneLine(err.Error()))
		return 2
	}
	if !held {
		fmt.Fprintf(errOut, "REFUSED %s - reason=lease err=held by %s\n", c.Bench, holder)
		return 2
	}
	last := map[string]string{}
	pass := func() int {
		held, holder, err := Lease(ctx, rdb, c.Bench, session, ttl)
		switch {
		case err != nil:
			fmt.Fprintf(errOut, "REFUSED %s - reason=redis err=%s\n", c.Bench, oneLine(err.Error()))
			return -1
		case !held:
			fmt.Fprintf(errOut, "REFUSED %s - reason=lease err=held by %s\n", c.Bench, holder)
			return 2
		}
		if _, err := Pass(ctx, c, rdb, last, false, out); err != nil {
			fmt.Fprintf(errOut, "REFUSED %s - reason=redis err=%s\n", c.Bench, oneLine(err.Error()))
		}
		return -1
	}
	if code := pass(); code >= 0 {
		return code
	}
	for {
		select {
		case <-ctx.Done():
			return 0
		case _, ok := <-ticks:
			if !ok {
				return 0
			}
			if code := pass(); code >= 0 {
				return code
			}
		}
	}
}

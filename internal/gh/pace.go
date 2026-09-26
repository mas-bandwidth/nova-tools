package gh

import (
	"context"
	"fmt"
	"strconv"
	"time"
)

// WriterKey is the writer's pace: one field per unix second, the writes
// made in it; the field two seconds back is deleted on every write, so the
// hash never grows and needs no TTL.
const WriterKey = "gh:writer"

// pace takes one write slot in the current second, across every process
// sharing the store (or in this process when there is no store), sleeping
// to the next second while the second is full. A write is never refused
// for pace; it waits.
func (c *Client) pace(ctx context.Context) error {
	rate := c.Rate
	if rate <= 0 {
		rate = DefaultRate
	}
	for {
		now := c.now()
		sec := now.Unix()
		n, err := c.take(ctx, sec)
		if err != nil {
			return err
		}
		if n <= int64(rate) {
			return nil
		}
		wait := time.Unix(sec+1, 0).Sub(now)
		if wait <= 0 {
			wait = time.Millisecond
		}
		if err := c.sleep(ctx, wait); err != nil {
			return err
		}
	}
}

// take counts this write in its second and returns the count.
func (c *Client) take(ctx context.Context, sec int64) (int64, error) {
	if c.Redis == nil {
		c.mu.Lock()
		defer c.mu.Unlock()
		if c.local == nil {
			c.local = map[int64]int{}
		}
		delete(c.local, sec-2)
		delete(c.local, sec-1)
		c.local[sec]++
		return int64(c.local[sec]), nil
	}
	p := c.Redis.Pipeline()
	incr := p.HIncrBy(ctx, WriterKey, strconv.FormatInt(sec, 10), 1)
	p.HDel(ctx, WriterKey, strconv.FormatInt(sec-2, 10))
	if _, err := p.Exec(ctx); err != nil {
		return 0, fmt.Errorf("gh writer pace: %w", err)
	}
	return incr.Val(), nil
}

package gh

import (
	"context"
	"fmt"
	"time"
)

// WriterKey is the writer's pace: one field per unix second (the store's
// TIME, so benches with different clocks share one second), the writes
// made in it. Every field older than the previous second is deleted in the
// same call, so the hash holds at most two seconds whatever the idle gap
// between writes, and needs no TTL.
const WriterKey = "gh:writer"

// paceScript takes one write slot in the store's current second: HINCRBY
// the second, prune every field before the previous second, and answer
// {count, seconds, microseconds}.
const paceScript = `local t = redis.call('TIME')
local sec = tonumber(t[1])
local n = redis.call('HINCRBY', KEYS[1], tostring(sec), 1)
for _, f in ipairs(redis.call('HKEYS', KEYS[1])) do
  if tonumber(f) < sec - 1 then redis.call('HDEL', KEYS[1], f) end
end
return {n, sec, tonumber(t[2])}`

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
		n, wait, err := c.take(ctx)
		if err != nil {
			return err
		}
		if n <= int64(rate) {
			return nil
		}
		if wait <= 0 {
			wait = time.Millisecond
		}
		if err := c.sleep(ctx, wait); err != nil {
			return err
		}
	}
}

// take counts this write in its second and returns the count and the time
// left in that second.
func (c *Client) take(ctx context.Context) (int64, time.Duration, error) {
	if c.Redis == nil {
		now := c.now()
		sec := now.Unix()
		c.mu.Lock()
		defer c.mu.Unlock()
		if c.local == nil {
			c.local = map[int64]int{}
		}
		for s := range c.local {
			if s < sec-1 {
				delete(c.local, s)
			}
		}
		c.local[sec]++
		return int64(c.local[sec]), time.Unix(sec+1, 0).Sub(now), nil
	}
	v, err := c.Redis.Eval(ctx, paceScript, []string{WriterKey}).Int64Slice()
	if err != nil || len(v) != 3 {
		return 0, 0, fmt.Errorf("gh writer pace: %v", err)
	}
	return v[0], time.Second - time.Duration(v[2])*time.Microsecond, nil
}

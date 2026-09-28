package fenced

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/prkey"
)

// List is `land list`, read only: one line per offered stream PR, oldest
// first within each queue, `<repo>#<n> <created_at> <stream> <state>
// <skip_reason or ->`. Three pipelined round trips: the queue index, the
// queues, the PR records. repo and base filter when set.
//
// Each PR is read from its unit record pr:<name>:<n> (prkey.KeyText, the
// record `pr record` writes): the lander's land_* fields, written by
// internal/nsprint/fn/lua/land_take.lua (nova-tools #4079), and nothing else.
func List(ctx context.Context, c redis.Cmdable, sprint, repo, base string) ([]string, error) {
	rows, err := c.SMembers(ctx, "s:"+sprint+":land:queues").Result()
	if err != nil {
		return nil, err
	}
	sort.Strings(rows)
	var keep []string
	for _, row := range rows {
		i := strings.LastIndex(row, ":")
		if i < 0 {
			continue
		}
		if (repo == "" || row[:i] == repo) && (base == "" || row[i+1:] == base) {
			keep = append(keep, row)
		}
	}
	pipe := c.Pipeline()
	qs := make([]*redis.StringSliceCmd, len(keep))
	for i, row := range keep {
		qs[i] = pipe.ZRange(ctx, "s:"+sprint+":land:queue:"+row, 0, -1)
	}
	if len(keep) > 0 {
		if _, err := pipe.Exec(ctx); err != nil {
			return nil, err
		}
	}
	type item struct{ repo, n string }
	var items []item
	pipe = c.Pipeline()
	var hs []*redis.SliceCmd
	for i, row := range keep {
		r := row[:strings.LastIndex(row, ":")]
		for _, n := range qs[i].Val() {
			items = append(items, item{r, n})
			hs = append(hs, pipe.HMGet(ctx, prkey.KeyText(r, n), listFields...))
		}
	}
	if len(items) > 0 {
		if _, err := pipe.Exec(ctx); err != nil {
			return nil, err
		}
	}
	lines := make([]string, 0, len(items))
	for i, it := range items {
		v := hs[i].Val()
		f := make([]string, 4)
		for j := range f {
			if j < len(v) && v[j] != nil {
				f[j] = fmt.Sprint(v[j])
			}
			if f[j] == "" {
				f[j] = "-"
			}
		}
		lines = append(lines, fmt.Sprintf("%s#%s %s %s %s %s", it.repo, it.n, f[0], f[1], f[2], f[3]))
	}
	return lines, nil
}

// listFields are the unit record's fields `land list` prints, in its order:
// created_at, stream (the offer's slug), state and skip_reason as the
// lander keeps them.
var listFields = []string{"land_created_at", "land_stream", "land_state", "land_skip_reason"}

// StreamsLanded is `land status`'s streams line: x is the size of
// s:<S>:land:landed and y is x plus every queue's size (one pipeline after
// the index read). any is false when the sprint has no stream PR offered or
// landed, so the line is printed only for a sprint that uses the lander.
func StreamsLanded(ctx context.Context, c redis.Cmdable, sprint string) (x, y int64, any bool, err error) {
	rows, err := c.SMembers(ctx, "s:"+sprint+":land:queues").Result()
	if err != nil {
		return 0, 0, false, err
	}
	pipe := c.Pipeline()
	landed := pipe.ZCard(ctx, "s:"+sprint+":land:landed")
	qs := make([]*redis.IntCmd, len(rows))
	for i, row := range rows {
		qs[i] = pipe.ZCard(ctx, "s:"+sprint+":land:queue:"+row)
	}
	if _, err := pipe.Exec(ctx); err != nil {
		return 0, 0, false, err
	}
	x = landed.Val()
	y = x
	for _, q := range qs {
		y += q.Val()
	}
	return x, y, len(rows) > 0 || x > 0, nil
}

package ci

import (
	"context"
	"errors"
	"sort"

	"github.com/redis/go-redis/v9"
)

// NoMirrorKey is ci:nomirror:<bench> (#3724): the repos whose mirror the
// bench lacks. ns_ci_release with nomirror adds a repo, a claim naming the
// repo in its mirrors removes it (ci_run.lua); the table's host row and card
// fsck print it (#3804).
func NoMirrorKey(bench string) string { return "ci:nomirror:" + bench }

// BenchRegistry is the set every registered bench is a member of.
const BenchRegistry = "benches"

// NoMirror reads ci:nomirror:<bench> for every registered bench: SMEMBERS
// benches, then one pipeline of SMEMBERS. The map holds only the benches
// whose set is non-empty, each list sorted. No SCAN: a bench outside the
// registry is not read.
func NoMirror(ctx context.Context, client redis.UniversalClient) (map[string][]string, error) {
	benches, err := client.SMembers(ctx, BenchRegistry).Result()
	if err != nil {
		return nil, err
	}
	sort.Strings(benches)
	out := map[string][]string{}
	if len(benches) == 0 {
		return out, nil
	}
	pipe := client.Pipeline()
	cmds := make([]*redis.StringSliceCmd, len(benches))
	for i, b := range benches {
		cmds[i] = pipe.SMembers(ctx, NoMirrorKey(b))
	}
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return nil, err
	}
	for i, b := range benches {
		repos := cmds[i].Val()
		if len(repos) == 0 {
			continue
		}
		sort.Strings(repos)
		out[b] = repos
	}
	return out, nil
}

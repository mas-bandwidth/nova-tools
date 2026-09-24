// Package decide handles mind routing decisions across the ladder.
//
// friends.go reads friend presence keys (friend:<name>:down) from the fleet
// store so that route never sends work to a friend who is down (#3397).
package decide

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

// FriendRungs returns the minds in the registry that are friend rungs:
// minds asked by bus, excluding the all-friends broadcast and Glenn.
func (r *Registry) FriendRungs() []Mind {
	if r == nil {
		return nil
	}
	var out []Mind
	for _, m := range r.Minds {
		if m.Ask == AskBus && m.Name != "all-friends" && m.Name != "glenn" && m.Lineage != "friends" && m.Lineage != "glenn" {
			out = append(out, m)
		}
	}
	return out
}

// ReadDownFriends checks Redis for friend:<name>:down for every friend rung in
// the registry in one pipeline. It returns the set of down friend names (e.g.
// "emma" -> true) and the sorted list of exclusion tokens (e.g. ["emma:down"]).
func ReadDownFriends(ctx context.Context, client redis.Cmdable, reg *Registry) (map[string]bool, []string, error) {
	if client == nil || reg == nil {
		return nil, nil, nil
	}
	friends := reg.FriendRungs()
	if len(friends) == 0 {
		return nil, nil, nil
	}

	pipe := client.Pipeline()
	keys := make([]string, len(friends))
	existsCmds := make([]*redis.IntCmd, len(friends))
	for i, m := range friends {
		keys[i] = "friend:" + m.Name + ":down"
		existsCmds[i] = pipe.Exists(ctx, keys[i])
	}
	mgetCmd := pipe.MGet(ctx, keys...)

	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return nil, nil, fmt.Errorf("decide: read down friends: %w", err)
	}

	downSet := map[string]bool{}
	var downList []string
	vals, _ := mgetCmd.Result()
	for i, cmd := range existsCmds {
		isDown := cmd.Val() > 0
		if !isDown && i < len(vals) && vals[i] != nil {
			isDown = true
		}
		if isDown {
			name := friends[i].Name
			downSet[name] = true
			downList = append(downList, name+":down")
		}
	}
	sort.Strings(downList)
	return downSet, downList, nil
}

// DownFriends connects to Redis at addr and reads friend:<name>:down for every
// friend rung in one pipeline.
func DownFriends(ctx context.Context, addr, user, password string, reg *Registry) (map[string]bool, []string, error) {
	if strings.TrimSpace(addr) == "" {
		return nil, nil, nil
	}
	rdb := redis.NewClient(&redis.Options{
		Addr:     addr,
		Username: user,
		Password: password,
	})
	defer rdb.Close()
	timeoutCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return ReadDownFriends(timeoutCtx, rdb, reg)
}

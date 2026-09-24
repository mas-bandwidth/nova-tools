package task

// counts, owners and fill (#3206 rev 5 PR A): friend-queue's read verbs on
// the one store. Ready is ZCARD s:<S>:open:<f>; a waiting task (an unmet
// DEPENDS-ON, ruling nova-tools#3516) is never on that queue, so it is never
// counted ready and never filled.

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/redis/go-redis/v9"
)

// Counts is one friend's line: friend-queue's `<working> <ready> <done>` plus
// the waiting count the table shows beside them.
type Counts struct {
	Working int64
	Ready   int64
	Done    int64
	Waiting int64
}

// String is the bash order, `<working> <ready> <done>`, so callers that read
// three words keep working.
func (c Counts) String() string {
	return fmt.Sprintf("%d %d %d", c.Working, c.Ready, c.Done)
}

// CountsFor reads one friend's counts in one pipeline: ZCARD starting and
// living, ZCARD open:<f>, SCARD done:<f> and ZCARD friend:<f>:waiting.
func CountsFor(ctx context.Context, st *store.Store, sprint, as string) (Counts, error) {
	if err := need("counts", "sprint", sprint, "as", as); err != nil {
		return Counts{}, err
	}
	pipe := st.Client().Pipeline()
	starting := pipe.ZCard(ctx, "friend:"+as+":starting")
	living := pipe.ZCard(ctx, "friend:"+as+":living")
	ready := pipe.ZCard(ctx, "s:"+sprint+":open:"+as)
	done := pipe.SCard(ctx, "s:"+sprint+":done:"+as)
	waiting := pipe.ZCard(ctx, "friend:"+as+":waiting")
	if _, err := pipe.Exec(ctx); err != nil && err != redis.Nil {
		return Counts{}, fmt.Errorf("task counts %s: %w", as, err)
	}
	return Counts{
		Working: starting.Val() + living.Val(),
		Ready:   ready.Val(),
		Done:    done.Val(),
		Waiting: waiting.Val(),
	}, nil
}

// Owner is one live task with its owner (the lease holder, else the queue it
// is on) and state.
type Owner struct {
	ID    string
	Owner string
	State string
}

// liveStates are the index sets owners reads.
var liveStates = []string{"open", "claimed", "working", "waiting"}

// Owners lists the live tasks whose id starts with prefix, sorted by id, in
// two pipelines: the live index sets, then each hash's owner, dest and state.
// A stale index member (its hash says another state) is skipped.
func Owners(ctx context.Context, st *store.Store, sprint, prefix string) ([]Owner, error) {
	if err := need("owners", "sprint", sprint); err != nil {
		return nil, err
	}
	client := st.Client()
	pipe := client.Pipeline()
	sets := make([]*redis.StringSliceCmd, len(liveStates))
	for i, state := range liveStates {
		sets[i] = pipe.SMembers(ctx, "s:"+sprint+":idx:task:"+state)
	}
	if _, err := pipe.Exec(ctx); err != nil && err != redis.Nil {
		return nil, fmt.Errorf("task owners: %w", err)
	}
	seen := map[string]bool{}
	var ids []string
	for _, cmd := range sets {
		for _, id := range cmd.Val() {
			if strings.HasPrefix(id, prefix) && !seen[id] {
				seen[id] = true
				ids = append(ids, id)
			}
		}
	}
	sort.Strings(ids)
	if len(ids) == 0 {
		return []Owner{}, nil
	}
	pipe = client.Pipeline()
	rows := make([]*redis.SliceCmd, len(ids))
	for i, id := range ids {
		rows[i] = pipe.HMGet(ctx, "s:"+sprint+":task:"+id, "owner", "dest", "state")
	}
	if _, err := pipe.Exec(ctx); err != nil && err != redis.Nil {
		return nil, fmt.Errorf("task owners: %w", err)
	}
	live := map[string]bool{"open": true, "claimed": true, "working": true, "waiting": true}
	out := make([]Owner, 0, len(ids))
	for i, id := range ids {
		v := rows[i].Val()
		owner, dest, state := str(v, 0), str(v, 1), str(v, 2)
		if !live[state] {
			continue
		}
		if owner == "" {
			owner = dest
		}
		out = append(out, Owner{ID: id, Owner: owner, State: state})
	}
	return out, nil
}

func str(v []any, i int) string {
	if i < len(v) && v[i] != nil {
		return fmt.Sprint(v[i])
	}
	return ""
}

// Fill leases up to max of as's ready tasks at once (0 = every free slot):
// take --n min(free, max). A waiting task is not on the queue, so it is never
// filled.
func Fill(ctx context.Context, st *store.Store, sprint, as string, max int, actor, idem string) ([]Claim, error) {
	if max < 0 {
		return nil, fmt.Errorf("task fill: --max must be nonnegative")
	}
	return TakeAvailable(ctx, st, as, sprint, "", max, actor, idem)
}

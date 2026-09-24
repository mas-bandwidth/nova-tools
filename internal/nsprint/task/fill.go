package task

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/redis/go-redis/v9"
)

// FunctionFill is the Redis Function name for ns_width_fill.
const FunctionFill = "ns_width_fill"

// FillTask represents one task claimed during a fill.
type FillTask struct {
	ID    string
	Kind  string
	Ref   string
	Token string
}

// FillResult is the outcome of task.Fill.
type FillResult struct {
	Tasks   []FillTask
	Friend  string
	N       int
	Deficit int
}

// Fill claims min(deficit, eligible, max) tasks for friend as via ns_width_fill.
func Fill(ctx context.Context, st *store.Store, as, sprint string, max int, actor, idem string) (FillResult, error) {
	if st == nil {
		return FillResult{}, fmt.Errorf("task fill: nil store")
	}
	if as == "" {
		return FillResult{}, fmt.Errorf("task fill: as is required")
	}
	if max < 0 {
		return FillResult{}, fmt.Errorf("task fill: max must be a positive integer")
	}
	client := st.Client()
	if client.Exists(ctx, "friend:"+as+":desired").Val() == 0 {
		return FillResult{}, fmt.Errorf("task fill: friend %s has no desired slots", as)
	}
	slotsStr, err := client.HGet(ctx, "friend:"+as+":desired", "slots").Result()
	if err != nil && !errors.Is(err, redis.Nil) {
		return FillResult{}, fmt.Errorf("task fill: read slots: %w", err)
	}

	// One random part per possible claim. ns_width_fill claims at most
	// min(deficit, max) and fails closed when the parts run out, and
	// deficit <= slots, so min(slots, max) parts never cap a fill below what
	// the Lua would claim (an unbounded fill of 65 claims 65). A slots value
	// the Lua reads as 0 gets no parts; a slots raise racing this read only
	// makes the fill claim fewer, never mint a token without a part.
	numParts, _ := strconv.Atoi(slotsStr)
	if numParts < 0 {
		numParts = 0
	}
	if max > 0 && max < numParts {
		numParts = max
	}
	randoms := make([]any, numParts)
	for i := 0; i < numParts; i++ {
		tok, err := RandomToken()
		if err != nil {
			return FillResult{}, fmt.Errorf("task fill: random token: %w", err)
		}
		randoms[i] = tok
	}

	args := []any{as, sprint, strconv.Itoa(max), actor, idem}
	args = append(args, randoms...)

	reply, err := client.FCall(ctx, FunctionFill, nil, args...).Result()
	if err != nil {
		return FillResult{}, fmt.Errorf("task fill: %w", err)
	}

	vals, ok := reply.([]any)
	if !ok || len(vals) < 3 {
		return FillResult{}, fmt.Errorf("task fill: unexpected reply %T", reply)
	}
	if fmt.Sprint(vals[0]) != "OK" {
		return FillResult{}, fmt.Errorf("task fill: status %v", vals[0])
	}
	n, _ := strconv.Atoi(fmt.Sprint(vals[1]))
	deficit, _ := strconv.Atoi(fmt.Sprint(vals[2]))

	var tasks []FillTask
	for i := 3; i+3 < len(vals); i += 4 {
		tasks = append(tasks, FillTask{
			ID:    fmt.Sprint(vals[i]),
			Kind:  fmt.Sprint(vals[i+1]),
			Ref:   fmt.Sprint(vals[i+2]),
			Token: fmt.Sprint(vals[i+3]),
		})
	}

	return FillResult{
		Tasks:   tasks,
		Friend:  as,
		N:       n,
		Deficit: deficit,
	}, nil
}

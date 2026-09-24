package task

import (
	"context"
	"fmt"
	"strconv"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
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

	numTokens := 64
	if max > numTokens {
		numTokens = max
	}
	randoms := make([]any, numTokens)
	for i := 0; i < numTokens; i++ {
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

package life

import (
	"context"
	"fmt"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
)

const FunctionRedistributeFloor = "ns_redistribute_floor"

type FloorEvent struct{ What, Task, To, Detail string }

type FloorResult struct {
	Moved  int
	Events []FloorEvent
}

func RedistributeFloor(ctx context.Context, st *store.Store, sprint string, floor, maxMove int, actor, idem string) (FloorResult, error) {
	if st == nil || sprint == "" || floor < 0 || maxMove < 0 {
		return FloorResult{}, fmt.Errorf("redistribute floor: store, sprint and non-negative limits are required")
	}
	reply, err := st.Client().FCall(ctx, FunctionRedistributeFloor, nil, sprint, floor, maxMove, actor, idem).Result()
	if err != nil {
		return FloorResult{}, fmt.Errorf("redistribute floor: %w", err)
	}
	values, ok := reply.([]any)
	if !ok || len(values) != 3 || fmt.Sprint(values[0]) != "OK" {
		return FloorResult{}, fmt.Errorf("redistribute floor: unexpected reply %v", reply)
	}
	var result FloorResult
	if _, err := fmt.Sscan(fmt.Sprint(values[1]), &result.Moved); err != nil {
		return result, err
	}
	events, _ := values[2].([]any)
	if len(events)%4 != 0 {
		return result, fmt.Errorf("redistribute floor: malformed events")
	}
	for i := 0; i < len(events); i += 4 {
		result.Events = append(result.Events, FloorEvent{fmt.Sprint(events[i]), fmt.Sprint(events[i+1]), fmt.Sprint(events[i+2]), fmt.Sprint(events[i+3])})
	}
	return result, nil
}

package task

import (
	"context"
	"fmt"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
)

// FunctionList is the read-only FCALL behind ListStore (nova-tools #3263).
const FunctionList = "ns_task_list"

// ListRequest selects one friend's tasks. A state filter is optional; without
// one, only open, claimed, working and waiting tasks are returned.
type ListRequest struct {
	Sprint string
	As     string
	State  string
}

type Row struct {
	Sprint string
	ID     string
	State  string
}

func ListStore(ctx context.Context, st *store.Store, req ListRequest) ([]Row, error) {
	if st == nil {
		return nil, fmt.Errorf("task list: nil store")
	}
	if req.As == "" {
		return nil, fmt.Errorf("task list: as is required")
	}
	reply, err := st.Client().FCallRO(ctx, FunctionList, nil, req.As, req.Sprint, req.State).Result()
	if err != nil {
		return nil, fmt.Errorf("task list: %w", err)
	}
	rows, ok := reply.([]any)
	if !ok || len(rows)%3 != 0 {
		return nil, fmt.Errorf("task list: unexpected reply %T", reply)
	}
	out := make([]Row, 0, len(rows)/3)
	for i := 0; i < len(rows); i += 3 {
		sprint, _ := rows[i].(string)
		id, _ := rows[i+1].(string)
		state, _ := rows[i+2].(string)
		if sprint == "" || id == "" {
			continue
		}
		out = append(out, Row{Sprint: sprint, ID: id, State: state})
	}
	return out, nil
}

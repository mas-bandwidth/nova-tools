package task

import (
	"context"
	"fmt"
	"sort"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
)

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

// Candidate retains whether an id came from this friend's assigned open queue.
// State indexes are only candidates: the hash is the source of current state.
type Candidate struct {
	ID       string
	Assigned bool
}

type Detail struct {
	State string
	Owner string
}

type TaskReader interface {
	Sprints(context.Context) ([]string, error)
	Candidates(context.Context, string, string) ([]Candidate, error)
	Details(context.Context, string, []string) (map[string]Detail, error)
}

type RedisReader struct{ Store *store.Store }

func (r RedisReader) Sprints(ctx context.Context) ([]string, error) {
	if r.Store == nil {
		return nil, fmt.Errorf("task list: nil store")
	}
	return r.Store.Client().ZRange(ctx, "sprint:order", 0, -1).Result()
}

// Candidates reads the friend's open queue, completed tasks, and active-state
// indexes. Claimed/working indexes contain tasks for every friend; List checks
// each task's owner before showing it. A stale index entry never establishes
// the task's state or owner.
func (r RedisReader) Candidates(ctx context.Context, sprint, as string) ([]Candidate, error) {
	if r.Store == nil {
		return nil, fmt.Errorf("task list: nil store")
	}
	client := r.Store.Client()
	queue, err := client.ZRange(ctx, "s:"+sprint+":open:"+as, 0, -1).Result()
	if err != nil {
		return nil, fmt.Errorf("task list: open queue: %w", err)
	}
	seen := make(map[string]Candidate)
	for _, id := range queue {
		if id != "" {
			seen[id] = Candidate{ID: id, Assigned: true}
		}
	}
	for _, key := range []string{
		"s:" + sprint + ":idx:task:claimed", "s:" + sprint + ":idx:task:working",
		"s:" + sprint + ":idx:task:" + StateWaiting, "s:" + sprint + ":done:" + as,
	} {
		ids, err := client.SMembers(ctx, key).Result()
		if err != nil {
			return nil, fmt.Errorf("task list: index %s: %w", key, err)
		}
		for _, id := range ids {
			if id != "" {
				if _, ok := seen[id]; !ok {
					seen[id] = Candidate{ID: id}
				}
			}
		}
	}
	out := make([]Candidate, 0, len(seen))
	for _, c := range seen {
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func (r RedisReader) Details(ctx context.Context, sprint string, ids []string) (map[string]Detail, error) {
	if r.Store == nil {
		return nil, fmt.Errorf("task list: nil store")
	}
	out := make(map[string]Detail, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	reads := make([]store.HashRead, len(ids))
	for i, id := range ids {
		reads[i] = store.HashRead{Key: "task:" + id, Fields: []string{"state", "owner"}}
	}
	values, err := r.Store.PipelineHMGet(ctx, reads)
	if err != nil {
		return nil, fmt.Errorf("task list: details: %w", err)
	}
	for i, row := range values {
		if len(row) < 2 {
			continue
		}
		state, _ := row[0].(string)
		owner, _ := row[1].(string)
		if state != "" {
			out[ids[i]] = Detail{State: state, Owner: owner}
		}
	}
	return out, nil
}

// live includes waiting (#3090): a waiting task is still the owner's, it only
// holds no child.
func live(state string) bool {
	return state == "open" || state == "claimed" || state == "working" || state == StateWaiting
}

// List deduplicates ids within each sprint and counts a task only by its
// current hash state. An open task must be in this friend's assigned queue;
// claimed, working, and closed tasks must name this friend as owner.
func List(ctx context.Context, r TaskReader, req ListRequest) ([]Row, error) {
	if r == nil {
		return nil, fmt.Errorf("task list: nil reader")
	}
	if req.As == "" {
		return nil, fmt.Errorf("task list: as is required")
	}
	sprints := []string{req.Sprint}
	if req.Sprint == "" {
		var err error
		sprints, err = r.Sprints(ctx)
		if err != nil {
			return nil, fmt.Errorf("task list: sprint order: %w", err)
		}
	}
	rows := make([]Row, 0)
	for _, sprint := range sprints {
		if sprint == "" {
			continue
		}
		candidates, err := r.Candidates(ctx, sprint, req.As)
		if err != nil {
			return nil, err
		}
		merged := make(map[string]Candidate, len(candidates))
		for _, c := range candidates {
			if c.ID == "" {
				continue
			}
			old := merged[c.ID]
			merged[c.ID] = Candidate{ID: c.ID, Assigned: old.Assigned || c.Assigned}
		}
		ids := make([]string, 0, len(merged))
		for id := range merged {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		details, err := r.Details(ctx, sprint, ids)
		if err != nil {
			return nil, err
		}
		for _, id := range ids {
			c := merged[id]
			d, ok := details[c.ID]
			if !ok {
				continue
			}
			if req.State != "" && d.State != req.State {
				continue
			}
			if req.State == "" && !live(d.State) {
				continue
			}
			if d.State == "open" {
				if !c.Assigned {
					continue
				}
			} else if d.Owner != req.As {
				continue
			}
			rows = append(rows, Row{Sprint: sprint, ID: c.ID, State: d.State})
		}
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Sprint != rows[j].Sprint {
			return rows[i].Sprint < rows[j].Sprint
		}
		return rows[i].ID < rows[j].ID
	})
	return rows, nil
}

func ListStore(ctx context.Context, st *store.Store, req ListRequest) ([]Row, error) {
	return List(ctx, RedisReader{Store: st}, req)
}

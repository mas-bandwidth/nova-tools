package sprint

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// FakeStore is the in-memory store the tests run against. It is STRICT LIKE THE REAL TOOL
// (AGENTS.md): it refuses the names RedisStore refuses, it refuses a task added to a sprint
// that was never opened, and it returns tasks in the same order. A lenient fake here would
// ship the verb broken on the one store that matters.
type FakeStore struct {
	mu      sync.Mutex
	sprints map[string]Sprint
	tasks   map[string]Task
	sets    map[string][]string
	queues  map[string][]Task
	present map[string]bool
}

// NewFakeStore returns an empty store.
func NewFakeStore() *FakeStore {
	return &FakeStore{
		sprints: map[string]Sprint{},
		tasks:   map[string]Task{},
		sets:    map[string][]string{},
		queues:  map[string][]Task{},
		present: map[string]bool{},
	}
}

// SetPresent is the test's heartbeat: it stands in for `friend:<name>` and `bench:<name>`.
func (f *FakeStore) SetPresent(name string, up bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.present[strings.ToLower(name)] = up
}

// Queue is what a consumer's stream holds, for a test that asserts the refill.
func (f *FakeStore) Queue(name string) []Task {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Task(nil), f.queues[name]...)
}

// PutSprint records the bounded set.
func (f *FakeStore) PutSprint(ctx context.Context, s Sprint) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := ValidateName("sprint", s.Name); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sprints[s.Name] = s
	return nil
}

// GetSprint reads one back.
func (f *FakeStore) GetSprint(ctx context.Context, name string) (Sprint, error) {
	if err := ctx.Err(); err != nil {
		return Sprint{}, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	s, ok := f.sprints[name]
	if !ok {
		return Sprint{}, fmt.Errorf("no sprint named %q is open; run: nova-pulse sprint open %s --goal <one sentence>", name, name)
	}
	return s, nil
}

// Sprints is every sprint the store holds, oldest first.
func (f *FakeStore) Sprints(ctx context.Context) ([]Sprint, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []Sprint
	for _, s := range f.sprints {
		out = append(out, s)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if !out[i].OpenedAt.Equal(out[j].OpenedAt) {
			return out[i].OpenedAt.Before(out[j].OpenedAt)
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

// PutTask writes one task hash.
func (f *FakeStore) PutTask(ctx context.Context, t Task) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := ValidateName("task", t.ID); err != nil {
		return err
	}
	if err := ValidateKind(t.Kind); err != nil {
		return err
	}
	if t.State == "" {
		t.State = StateOpen
	}
	if err := ValidateState(t.State); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.tasks[t.ID] = t
	return nil
}

// GetTask reads one back.
func (f *FakeStore) GetTask(ctx context.Context, id string) (Task, error) {
	if err := ctx.Err(); err != nil {
		return Task{}, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	t, ok := f.tasks[id]
	if !ok {
		return Task{}, fmt.Errorf("no task %q in the store", id)
	}
	return t, nil
}

// AddTask puts a task id in a sprint's set.
func (f *FakeStore) AddTask(ctx context.Context, sprintName, taskID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.sprints[sprintName]; !ok {
		return fmt.Errorf("no sprint named %q is open; open it before adding to it", sprintName)
	}
	for _, id := range f.sets[sprintName] {
		if id == taskID {
			return nil
		}
	}
	f.sets[sprintName] = append(f.sets[sprintName], taskID)
	return nil
}

// Tasks is the sprint's whole set, in id order.
func (f *FakeStore) Tasks(ctx context.Context, sprintName string) ([]Task, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.sprints[sprintName]; !ok {
		return nil, fmt.Errorf("no sprint named %q is open", sprintName)
	}
	ids := append([]string(nil), f.sets[sprintName]...)
	sort.Strings(ids)
	var out []Task
	for _, id := range ids {
		if t, ok := f.tasks[id]; ok {
			out = append(out, t)
		}
	}
	return out, nil
}

// Presence is the measured heartbeat of every consumer.
func (f *FakeStore) Presence(ctx context.Context) (map[string]bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	out := map[string]bool{}
	for k, v := range f.present {
		out[k] = v
	}
	return out, nil
}

// QueueDepths is how deep each named consumer's stream is.
func (f *FakeStore) QueueDepths(ctx context.Context, consumers []string) (map[string]int, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	out := map[string]int{}
	for _, c := range consumers {
		out[c] = len(f.queues["q:"+c]) + len(f.queues["q:"+c+":front"])
	}
	return out, nil
}

// Place appends a task to a consumer's stream.
func (f *FakeStore) Place(ctx context.Context, queue string, t Task) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if strings.TrimSpace(queue) == "" {
		return "", fmt.Errorf("the queue name is required; it is q:<consumer>")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.queues[queue] = append(f.queues[queue], t)
	return fmt.Sprintf("%d-%d", time.Now().UnixMilli(), len(f.queues[queue])), nil
}

// Close is the Store contract; the fake holds nothing to release.
func (f *FakeStore) Close() error { return nil }

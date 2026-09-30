package config

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// Store is the permanent store: Postgres in production (PG), Mem in the unit
// tests. Every write is one transaction that changes the row AND appends its
// history row (kind, name, op, before, after, actor, at); there is no write
// without a record (docs/SPEC-CONFIG.md, "History").
type Store interface {
	// Get reads one row; found is false when there is none.
	Get(ctx context.Context, kind, name string) (row Row, found bool, err error)
	// List reads every row of a kind, by name.
	List(ctx context.Context, kind string) ([]Row, error)
	// Insert adds a row. A row of that name is ErrExists; a ref field naming
	// no row of its kind is ErrNoRef. It returns the history id.
	Insert(ctx context.Context, kind string, row Row, actor string) (int64, error)
	// Update changes the named fields of a row (ErrNotFound when there is
	// none) and returns the row after and the history id.
	Update(ctx context.Context, kind, name string, changes map[string]string, actor string) (Row, int64, error)
	// Delete removes a row (ErrNotFound when there is none; ErrReferenced
	// when a row of another kind names it) and returns the history id.
	Delete(ctx context.Context, kind, name string, actor string) (int64, error)
	// History reads the change rows of one row, oldest first.
	History(ctx context.Context, kind, name string) ([]Change, error)
	// MachinesAndFleet reads every machine row and the fleet row from one
	// snapshot: one transaction on Postgres, one lock hold on Mem.
	MachinesAndFleet(ctx context.Context) (machines []Row, fleet Row, err error)
	// Rev is the kind's revision: the greatest history id of the kind, 0
	// when it has none. apply stamps it into Redis.
	Rev(ctx context.Context, kind string) (int64, error)
	// Counts is the row count per kind that has many (a singleton kind is
	// always one row and is left out).
	Counts(ctx context.Context) (map[string]int, error)
}

// Change is one history row.
type Change struct {
	ID     int64
	Kind   string
	Name   string
	Op     string // OpAdd, OpSet or OpRemove
	Before map[string]string
	After  map[string]string
	Actor  string
	At     string // RFC 3339 UTC
}

// The three operations a history row records.
const (
	OpAdd    = "add"
	OpSet    = "set"
	OpRemove = "remove"
)

// The refusals a store returns; the CLI turns each into one line with its
// remedy.
var (
	ErrExists     = errors.New("exists")
	ErrNotFound   = errors.New("not found")
	ErrNoRef      = errors.New("names no row")
	ErrReferenced = errors.New("is named by")
)

// RefusedError is a store refusal with the words for the line: Err is one of
// the sentinels above, Detail says which row or field.
type RefusedError struct {
	Err    error
	Detail string
}

func (e *RefusedError) Error() string { return e.Detail }
func (e *RefusedError) Unwrap() error { return e.Err }

// Refused reports whether err is a store refusal (exit 1), as opposed to a
// store that could not answer (exit 2).
func Refused(err error) bool {
	var r *RefusedError
	return errors.As(err, &r)
}

// CheckRefs is the cross-row validation every store runs before a write:
// a ref field must name a row of its kind (an optional one may be empty).
func CheckRefs(ctx context.Context, st Store, k *Kind, row Row) error {
	for _, f := range k.Fields {
		if f.Type != TypeRef || (row.Fields[f.Name] == "" && !f.Required) {
			continue
		}
		if _, found, err := st.Get(ctx, f.Ref, row.Fields[f.Name]); err != nil {
			return err
		} else if !found {
			return &RefusedError{Err: ErrNoRef, Detail: fmt.Sprintf("--%s %s names no %s row", f.Name, row.Fields[f.Name], f.Ref)}
		}
	}
	return nil
}

func checkRefs(ctx context.Context, st Store, k *Kind, row Row) error {
	return CheckRefs(ctx, st, k, row)
}

// CheckReferenced refuses removing a row that a ref field of another kind
// names.
func CheckReferenced(ctx context.Context, st Store, kind, name string) error {
	for _, other := range Kinds {
		for _, f := range other.Fields {
			if f.Type != TypeRef || f.Ref != kind {
				continue
			}
			rows, err := st.List(ctx, other.Name)
			if err != nil {
				return err
			}
			var names []string
			for _, r := range rows {
				if r.Fields[f.Name] == name {
					names = append(names, r.Name)
				}
			}
			if len(names) > 0 && other.Singleton {
				return &RefusedError{Err: ErrReferenced, Detail: fmt.Sprintf("%s %s is the --%s of the %s", kind, name, f.Name, other.Name)}
			}
			if len(names) > 0 {
				return &RefusedError{Err: ErrReferenced, Detail: fmt.Sprintf("%s %s is the --%s of %s %s", kind, name, f.Name, other.Name, strings.Join(names, ","))}
			}
		}
	}
	return nil
}

func checkReferenced(ctx context.Context, st Store, kind, name string) error {
	return CheckReferenced(ctx, st, kind, name)
}

// Mem is the in-memory Store the unit tests use. It is strict like PG: the
// same refusals, the same history, the same revision.
type Mem struct {
	mu      sync.Mutex
	rows    map[string]map[string]Row // kind -> name -> row
	history []Change
	Now     func() time.Time
}

// NewMem returns an empty store: no rows of any kind but the singleton
// kinds' one row each, as a migrated Postgres has.
func NewMem() *Mem {
	m := &Mem{rows: map[string]map[string]Row{}, Now: func() time.Time { return time.Unix(1700000000, 0).UTC() }}
	for _, k := range Kinds {
		if !k.Singleton {
			continue
		}
		row := Row{Name: k.Name, Fields: map[string]string{}, CreatedAt: m.stamp(), UpdatedAt: m.stamp()}
		for _, f := range k.Fields {
			row.Fields[f.Name] = ""
		}
		m.rows[k.Name] = map[string]Row{k.Name: row}
	}
	return m
}

func (m *Mem) stamp() string { return m.Now().UTC().Format(time.RFC3339) }

func (m *Mem) record(kind, name, op string, before, after map[string]string, actor string) int64 {
	c := Change{ID: int64(len(m.history) + 1), Kind: kind, Name: name, Op: op, Before: before, After: after, Actor: actor, At: m.stamp()}
	m.history = append(m.history, c)
	return c.ID
}

func (m *Mem) Get(_ context.Context, kind, name string) (Row, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.rows[kind][name]
	if !ok {
		return Row{}, false, nil
	}
	return r.Clone(), true, nil
}

func (m *Mem) List(_ context.Context, kind string) ([]Row, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Row
	for _, r := range m.rows[kind] {
		out = append(out, r.Clone())
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func (m *Mem) MachinesAndFleet(_ context.Context) ([]Row, Row, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var machines []Row
	for _, r := range m.rows[KindMachine] {
		machines = append(machines, r.Clone())
	}
	sort.Slice(machines, func(i, j int) bool { return machines[i].Name < machines[j].Name })
	var fleet Row
	if r, ok := m.rows[KindFleet][KindFleet]; ok {
		fleet = r.Clone()
	}
	return machines, fleet, nil
}

func (m *Mem) Insert(ctx context.Context, kind string, row Row, actor string) (int64, error) {
	k, ok := Lookup(kind)
	if !ok {
		return 0, fmt.Errorf("unknown kind %q", kind)
	}
	if err := checkRefs(ctx, m, k, row); err != nil {
		return 0, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.rows[kind][row.Name]; exists {
		return 0, &RefusedError{Err: ErrExists, Detail: fmt.Sprintf("%s %s exists", kind, row.Name)}
	}
	if m.rows[kind] == nil {
		m.rows[kind] = map[string]Row{}
	}
	r := row.Clone()
	r.CreatedAt, r.UpdatedAt = m.stamp(), m.stamp()
	m.rows[kind][row.Name] = r
	return m.record(kind, row.Name, OpAdd, nil, r.Clone().Fields, actor), nil
}

func (m *Mem) Update(ctx context.Context, kind, name string, changes map[string]string, actor string) (Row, int64, error) {
	k, ok := Lookup(kind)
	if !ok {
		return Row{}, 0, fmt.Errorf("unknown kind %q", kind)
	}
	m.mu.Lock()
	cur, exists := m.rows[kind][name]
	m.mu.Unlock()
	if !exists {
		return Row{}, 0, &RefusedError{Err: ErrNotFound, Detail: fmt.Sprintf("%s %s not found", kind, name)}
	}
	next := cur.Clone()
	for f, v := range changes {
		next.Fields[f] = v
	}
	if err := checkRefs(ctx, m, k, next); err != nil {
		return Row{}, 0, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	next.UpdatedAt = m.stamp()
	m.rows[kind][name] = next
	return next.Clone(), m.record(kind, name, OpSet, cur.Clone().Fields, next.Clone().Fields, actor), nil
}

func (m *Mem) Delete(ctx context.Context, kind, name string, actor string) (int64, error) {
	m.mu.Lock()
	cur, exists := m.rows[kind][name]
	m.mu.Unlock()
	if !exists {
		return 0, &RefusedError{Err: ErrNotFound, Detail: fmt.Sprintf("%s %s not found", kind, name)}
	}
	if err := checkReferenced(ctx, m, kind, name); err != nil {
		return 0, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.rows[kind], name)
	return m.record(kind, name, OpRemove, cur.Clone().Fields, nil, actor), nil
}

func (m *Mem) History(_ context.Context, kind, name string) ([]Change, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Change
	for _, c := range m.history {
		if c.Kind == kind && c.Name == name {
			out = append(out, c)
		}
	}
	return out, nil
}

func (m *Mem) Rev(_ context.Context, kind string) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var rev int64
	for _, c := range m.history {
		if c.Kind == kind && c.ID > rev {
			rev = c.ID
		}
	}
	return rev, nil
}

func (m *Mem) Counts(_ context.Context) (map[string]int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := map[string]int{}
	for _, k := range Kinds {
		if !k.Singleton {
			out[k.Name] = len(m.rows[k.Name])
		}
	}
	return out, nil
}

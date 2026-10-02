package config

import (
	"context"
	"errors"
	"fmt"
	"slices"
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
	// Rev is the kind's revision: the greatest history id of the kind, 0
	// when it has none. apply stamps it into Redis.
	Rev(ctx context.Context, kind string) (int64, error)
	// Counts is the row count per kind that has many (a singleton kind is
	// always one row and is left out).
	Counts(ctx context.Context) (map[string]int, error)
	// Ownership reads from the catalog who owns schema config and each of
	// its tables, and the role connected: migrate's preflight (MigrateGaps).
	// A store with no roles (the file) answers the zero Ownership.
	Ownership(ctx context.Context) (Ownership, error)
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
	ErrInvalid    = errors.New("invalid")
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

// checkRefs is the validation every store runs before an add or a set: the
// kind's Check on the row the write would leave (ErrInvalid), then the
// cross-row rule that a ref field must name a row of its kind (an optional
// one may be empty).
func checkRefs(ctx context.Context, st Store, k *Kind, row Row) error {
	if k.Check != nil {
		if err := k.Check(row); err != nil {
			return &RefusedError{Err: ErrInvalid, Detail: err.Error()}
		}
	}
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
	if k.Name == KindTier {
		return checkTierRoutes(ctx, st, row)
	}
	return nil
}

// checkTierRoutes refuses a tier's array that names a route that is not a row,
// is disabled, or serves another tier: the deal would only skip it.
func checkTierRoutes(ctx context.Context, st Store, row Row) error {
	for _, name := range strings.Split(row.Fields["routes"], ",") {
		if name == "" {
			continue
		}
		r, found, err := st.Get(ctx, KindRoute, name)
		switch {
		case err != nil:
			return err
		case !found:
			return &RefusedError{Err: ErrNoRef, Detail: fmt.Sprintf("--routes %s names no route row", name)}
		case r.Fields["enabled"] != "true":
			return &RefusedError{Err: ErrInvalid, Detail: fmt.Sprintf("--routes %s names a disabled route; enable it first (route set %s --enabled true)", name, name)}
		case r.Fields["tier"] != row.Name:
			return &RefusedError{Err: ErrInvalid, Detail: fmt.Sprintf("--routes %s names a route of tier %s, not %s", name, r.Fields["tier"], row.Name)}
		}
	}
	return nil
}

// checkReferenced refuses removing a row that a ref field of another kind
// names, and a row the kind's migration made (Kind.Seed: the tiers, which the
// deal reads).
func checkReferenced(ctx context.Context, st Store, kind, name string) error {
	if k, ok := Lookup(kind); ok && hasWord(strings.Join(k.Seed, ","), name) {
		return &RefusedError{Err: ErrInvalid, Detail: fmt.Sprintf("%s %s is made by migrate and the deal reads it; it is never removed: set its fields instead (%s set %s --<field> <value>)", kind, name, kind, name)}
	}
	for _, other := range Kinds {
		for _, f := range other.Fields {
			if (f.Type != TypeRef && f.Type != TypeSeq) || f.Ref != kind {
				continue
			}
			rows, err := st.List(ctx, other.Name)
			if err != nil {
				return err
			}
			var names []string
			for _, r := range rows {
				// a ref names one row; a seq (a tier's routes) is a comma list of them
				if r.Fields[f.Name] == name || (f.Type == TypeSeq && slices.Contains(strings.Split(r.Fields[f.Name], ","), name)) {
					names = append(names, r.Name)
				}
			}
			if len(names) > 0 && f.Type == TypeSeq {
				return &RefusedError{Err: ErrReferenced, Detail: fmt.Sprintf("%s %s is in the --%s of %s %s; set it out of the list first (%s set %s --%s <the rest>)", kind, name, f.Name, other.Name, strings.Join(names, ","), other.Name, names[0], f.Name)}
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

// PlanWrite is the history row a write would add, worked out from the store as it
// stands and written nowhere: the dry run of Insert (OpAdd, row), Update
// (OpSet, row.Name and changes) and Delete (OpRemove, row.Name), with the
// refusals both stores make before they write (a name taken or missing, the
// kind's Check, a ref naming no row, a row another names or the migration
// made), from the same checks. The change has no id and no instant: nothing
// was recorded.
func PlanWrite(ctx context.Context, st Store, op, kind string, row Row, changes map[string]string) (Change, error) {
	k, ok := Lookup(kind)
	if !ok {
		return Change{}, fmt.Errorf("unknown kind %q", kind)
	}
	cur, found, err := st.Get(ctx, kind, row.Name)
	if err != nil {
		return Change{}, err
	}
	c := Change{Kind: kind, Name: row.Name, Op: op}
	missing := &RefusedError{Err: ErrNotFound, Detail: fmt.Sprintf("%s %s not found", kind, row.Name)}
	switch op {
	case OpAdd:
		if err := checkRefs(ctx, st, k, row); err != nil {
			return Change{}, err
		}
		if found {
			return Change{}, &RefusedError{Err: ErrExists, Detail: fmt.Sprintf("%s %s exists", kind, row.Name)}
		}
		c.After = row.Clone().Fields
	case OpSet:
		if !found {
			return Change{}, missing
		}
		next := cur.Clone()
		for f, v := range changes {
			next.Fields[f] = v
		}
		if err := checkRefs(ctx, st, k, next); err != nil {
			return Change{}, err
		}
		c.Before, c.After = cur.Fields, next.Fields
	case OpRemove:
		if !found {
			return Change{}, missing
		}
		if err := checkReferenced(ctx, st, kind, row.Name); err != nil {
			return Change{}, err
		}
		c.Before = cur.Fields
	default:
		return Change{}, fmt.Errorf("plan: unknown op %q", op)
	}
	return c, nil
}

// Mem is the in-memory Store the unit tests use. It is strict like PG: the
// same refusals, the same history, the same revision.
type Mem struct {
	mu      sync.Mutex
	rows    map[string]map[string]Row // kind -> name -> row
	history []Change
	Now     func() time.Time
	// Catalog is what Ownership answers; a test sets the owners it needs.
	Catalog Ownership
}

// memRole is the role a Mem is connected as: it owns schema config and every
// table in it until a test sets Catalog.
const memRole = "nova_config"

// NewMem returns an empty store: no rows of any kind but the singleton
// kinds' one row each, as a migrated Postgres has.
func NewMem() *Mem {
	m := &Mem{rows: map[string]map[string]Row{}, Now: func() time.Time { return time.Unix(1700000000, 0).UTC() },
		Catalog: Ownership{Role: memRole, SchemaOwner: memRole, Create: true, Tables: catalogTables(memRole)}}
	for _, k := range Kinds {
		names := k.Seed
		if k.Singleton {
			names = []string{k.Name}
		}
		for _, name := range names {
			row := Row{Name: name, Fields: map[string]string{}, CreatedAt: m.stamp(), UpdatedAt: m.stamp()}
			for _, f := range k.Fields {
				row.Fields[f.Name] = f.Default
			}
			if m.rows[k.Name] == nil {
				m.rows[k.Name] = map[string]Row{}
			}
			m.rows[k.Name][name] = row
		}
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

func (m *Mem) Ownership(context.Context) (Ownership, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	o := m.Catalog
	o.Tables = map[string]string{}
	for t, owner := range m.Catalog.Tables {
		o.Tables[t] = owner
	}
	return o, nil
}

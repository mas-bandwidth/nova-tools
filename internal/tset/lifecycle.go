package tset

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
)

// Layer 1's lifecycle: define a namespace and tear it down, each through one
// checked library function (the L1 contract amendment, lifecycle,
// 2026-09-30; lua/table_set_lifecycle.lua). Nothing here writes a key
// directly: the Redis store calls ns_tset_define and ns_tset_teardown, and the
// Mem twin applies the same rules to its own state.

// The lifecycle's bounds (the amendment, sections 2 and 3).
const (
	MaxColumns              = 32       // columns of one table (L1 1.2)
	MaxLifecycleRequestSize = 64 << 10 // one encoded define or teardown request
	TeardownBatchKeys       = 1000     // keys one teardown call deletes at most
	TeardownScansPerCall    = 16       // SCAN calls one teardown call makes at most
	MaxTeardownCalls        = 100000   // calls one Teardown makes before it reports a stall
	LifecycleReceiptsMax    = 1024     // the receipt stream's length bound
)

// MaxNamespaceName is the longest <name> of a namespace {<name>}:
// (ValidNamespace).
const MaxNamespaceName = 32

// ValidNamespace says s is a namespace the lifecycle admits: exactly
// {<name>}: with <name> [a-z][a-z0-9-]{0,31} (the amendment, section 1). The
// closing brace ends every namespace, so none is a prefix of another, nor of a
// key outside the grammar; define, teardown, the twin and this client refuse
// any other CONFIG (lua/table_set_lifecycle.lua namespace_ok is the same rule).
func ValidNamespace(s string) bool {
	n := len(s) - 3
	if n < 1 || n > MaxNamespaceName || s[0] != '{' || s[len(s)-2:] != "}:" {
		return false
	}
	for i := 1; i <= n; i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z':
		case i > 1 && ((c >= '0' && c <= '9') || c == '-'):
		default:
			return false
		}
	}
	return true
}

// ClockRunning reads the upper design's clock field stopped_since_ms (v2.1
// 1.2): "" while RUNNING, the time the current STOPPED span began otherwise,
// so "0" is a stop at time 0, not a run. An absent field (nil) reads as "".
// Teardown's RUNNING (lua/table_set_lifecycle.lua clock_running) and the
// sprint's verbs read the clock by this one rule.
func ClockRunning(stoppedSinceMS *string) bool {
	return stoppedSinceMS == nil || *stoppedSinceMS == ""
}

// ColumnKindSet is the one column kind define admits: a set column, the kind
// the trusted fixture initializer writes (L1 1.2).
const ColumnKindSet = "set"

// ColumnSpec is one column of a table and its kind.
type ColumnSpec struct {
	Name string `json:"name"`
	Kind string `json:"kind"`
}

// TableSpec is one table of a namespace: its name and its columns in order.
type TableSpec struct {
	Name    string       `json:"t"`
	Columns []ColumnSpec `json:"columns"`
}

// DefineSpec is a namespace as define makes it: the tables in catalog order, the
// view's name, and the build of the library the caller expects on the store.
type DefineSpec struct {
	Space  string      `json:"space"`
	Build  string      `json:"build"`
	View   string      `json:"view"`
	Tables []TableSpec `json:"tables"`
}

// DefineReply is a define that applied: the namespace at epoch 0 with its tables
// and view.
type DefineReply struct {
	Status string   `json:"status"`
	Space  string   `json:"space"`
	View   string   `json:"view"`
	Tables []string `json:"tables"`
	Epoch  Decimal  `json:"epoch"`
	TimeMS Decimal  `json:"time_ms"`
}

// TeardownReply is a teardown that finished: how many calls it took and how
// many keys the store deleted. The twin holds no keys: its Deleted is 0.
type TeardownReply struct {
	Calls   int
	Deleted int64
	Done    bool
}

// teardownCallReply is one ns_tset_teardown call's answer.
type teardownCallReply struct {
	Status  string  `json:"status"`
	Deleted int64   `json:"deleted"`
	Done    bool    `json:"done"`
	TimeMS  Decimal `json:"time_ms"`
}

type teardownRequest struct {
	Space   string `json:"space"`
	Confirm string `json:"confirm"`
}

// LifecycleReceipt is one entry of a namespace's receipt stream,
// <namespace>sprint:lifecycle: a define, or a teardown call that deleted keys or
// finished.
type LifecycleReceipt struct {
	Fn      string
	View    string
	Tables  []string
	Build   string
	Deleted int64
	Done    bool
}

// Lifecycle is Layer 1's production lifecycle, on a store and on the twin.
type Lifecycle interface {
	Define(context.Context, DefineSpec) (DefineReply, error)
	Teardown(ctx context.Context, space, confirm string) (TeardownReply, error)
}

var (
	_ Lifecycle = (*RedisStore)(nil)
	_ Lifecycle = (*Mem)(nil)
)

// checkDefine is define's shape, the same checks in the same order as
// ns_tset_define's before BUILD: the namespace's CONFIG, then REQUEST,
// LIMIT, CONFIG.
func checkDefine(spec DefineSpec) error {
	if !ValidNamespace(spec.Space) {
		return NewRefusal("CONFIG", RefusalDetail{})
	}
	if !validMemColumn(spec.View) || len(spec.Tables) == 0 {
		return NewRefusal("REQUEST", RefusalDetail{})
	}
	if len(spec.Tables) > MaxTables {
		return NewRefusal("LIMIT", RefusalDetail{Budget: "tables", Actual: int64p(int64(len(spec.Tables))), Limit: int64p(MaxTables)})
	}
	seen := make(map[string]bool, len(spec.Tables))
	for i, t := range spec.Tables {
		index := i
		if !validMemColumn(t.Name) || seen[t.Name] || len(t.Columns) == 0 {
			return NewRefusal("REQUEST", RefusalDetail{EntryIndex: &index})
		}
		if len(t.Columns) > MaxColumns {
			return NewRefusal("LIMIT", RefusalDetail{EntryIndex: &index, Table: t.Name, Budget: "columns",
				Actual: int64p(int64(len(t.Columns))), Limit: int64p(MaxColumns)})
		}
		cols := make(map[string]bool, len(t.Columns))
		for _, c := range t.Columns {
			if !validMemColumn(c.Name) || cols[c.Name] {
				return NewRefusal("REQUEST", RefusalDetail{EntryIndex: &index, Table: t.Name})
			}
			if c.Kind != ColumnKindSet {
				return NewRefusal("CONFIG", RefusalDetail{EntryIndex: &index, Table: t.Name})
			}
			cols[c.Name] = true
		}
		seen[t.Name] = true
	}
	return nil
}

func int64p(v int64) *int64 { return &v }

// EncodeDefine is define's request on the wire, checked first.
func EncodeDefine(spec DefineSpec) ([]byte, error) {
	if err := checkDefine(spec); err != nil {
		return nil, err
	}
	b, err := json.Marshal(spec)
	if err != nil {
		return nil, NewRefusal("REQUEST", RefusalDetail{})
	}
	if len(b) > MaxLifecycleRequestSize {
		return nil, NewRefusal("LIMIT", RefusalDetail{Budget: "request_bytes", Actual: int64p(int64(len(b))), Limit: int64p(MaxLifecycleRequestSize)})
	}
	return b, nil
}

func encodeTeardown(space, confirm string) ([]byte, error) {
	if !ValidNamespace(space) {
		return nil, NewRefusal("CONFIG", RefusalDetail{})
	}
	b, err := json.Marshal(teardownRequest{Space: space, Confirm: confirm})
	if err != nil {
		return nil, NewRefusal("REQUEST", RefusalDetail{})
	}
	if len(b) > MaxLifecycleRequestSize {
		return nil, NewRefusal("LIMIT", RefusalDetail{Budget: "request_bytes", Actual: int64p(int64(len(b))), Limit: int64p(MaxLifecycleRequestSize)})
	}
	return b, nil
}

// decodeLifecycle decodes an ok answer into out, or returns the refusal.
func decodeLifecycle(data []byte, out any) error {
	var head struct {
		Status string `json:"status"`
	}
	if json.Unmarshal(data, &head) != nil {
		return errors.New("tset: malformed lifecycle response")
	}
	if head.Status == "refused" {
		var r Refusal
		if json.Unmarshal(data, &r) != nil || r.Code == "" {
			return errors.New("tset: malformed lifecycle refusal")
		}
		return &r
	}
	if head.Status != "ok" {
		return errors.New("tset: unknown lifecycle response status")
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("tset: malformed lifecycle response: %w", err)
	}
	return nil
}

// Define calls ns_tset_define once. A lost reply is an unknown outcome; the
// same Define again answers EXISTS when the first applied.
func (r *RedisStore) Define(ctx context.Context, spec DefineSpec) (DefineReply, error) {
	raw, err := EncodeDefine(spec)
	if err != nil {
		return DefineReply{}, err
	}
	if err := r.preflight(); err != nil {
		return DefineReply{}, err
	}
	if err := ctx.Err(); err != nil {
		return DefineReply{}, err
	}
	text, err := r.client.FCall(ctx, "ns_tset_define", nil, Version, string(raw)).Text()
	if err != nil {
		return DefineReply{}, classifyWriteError(err, raw)
	}
	var reply DefineReply
	if err := decodeLifecycle([]byte(text), &reply); err != nil {
		var refusal *Refusal
		if errors.As(err, &refusal) {
			return DefineReply{}, refusal
		}
		return DefineReply{}, &OutcomeUnknownError{RawRequest: append([]byte(nil), raw...), Cause: err}
	}
	return reply, nil
}

// Teardown calls ns_tset_teardown until a call answers done, each call one
// bounded atomic batch. A refusal or an error stops it with the calls and
// keys so far; Teardown again goes on from where the store stands, and
// answers NOSPACE when the teardown already finished.
func (r *RedisStore) Teardown(ctx context.Context, space, confirm string) (TeardownReply, error) {
	raw, err := encodeTeardown(space, confirm)
	if err != nil {
		return TeardownReply{}, err
	}
	if err := r.preflight(); err != nil {
		return TeardownReply{}, err
	}
	var out TeardownReply
	for out.Calls < MaxTeardownCalls {
		if err := ctx.Err(); err != nil {
			return out, err
		}
		text, err := r.client.FCall(ctx, "ns_tset_teardown", nil, Version, string(raw)).Text()
		if err != nil {
			return out, classifyWriteError(err, raw)
		}
		out.Calls++
		var call teardownCallReply
		if err := decodeLifecycle([]byte(text), &call); err != nil {
			var refusal *Refusal
			if errors.As(err, &refusal) {
				return out, refusal
			}
			return out, &OutcomeUnknownError{RawRequest: append([]byte(nil), raw...), Cause: err}
		}
		if call.Deleted < 0 || call.Deleted > TeardownBatchKeys {
			return out, &OutcomeUnknownError{RawRequest: append([]byte(nil), raw...), Cause: errors.New("tset: teardown deleted count out of bounds")}
		}
		out.Deleted += call.Deleted
		if call.Done {
			out.Done = true
			return out, nil
		}
	}
	return out, fmt.Errorf("tset: teardown of %q did not finish in %d calls", space, MaxTeardownCalls)
}

// SetBuild pins the build the twin stands for: Define refuses BUILD for any
// other. Unpinned, the twin refuses only an empty build, as a store does.
func (m *Mem) SetBuild(build string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.build = build
}

// SetRunning is the twin's stand-in for the upper design's clock (1.2),
// which the twin does not hold: Teardown refuses RUNNING while it is set.
func (m *Mem) SetRunning(space string, running bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.running == nil {
		m.running = make(map[string]bool)
	}
	if running {
		m.running[space] = true
	} else {
		delete(m.running, space)
	}
}

// View is the namespace's view name, "" when the namespace has none.
func (m *Mem) View(space string) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if s := m.spaces[space]; s != nil {
		return s.view
	}
	return ""
}

// LifecycleReceipts are the namespace's lifecycle receipts, oldest first.
func (m *Mem) LifecycleReceipts(space string) []LifecycleReceipt {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]LifecycleReceipt, len(m.lifecycle[space]))
	copy(out, m.lifecycle[space])
	return out
}

func (m *Mem) receipt(space string, r LifecycleReceipt) {
	if m.lifecycle == nil {
		m.lifecycle = make(map[string][]LifecycleReceipt)
	}
	list := append(m.lifecycle[space], r)
	if len(list) > LifecycleReceiptsMax {
		list = list[len(list)-LifecycleReceiptsMax:]
	}
	m.lifecycle[space] = list
}

// Define is ns_tset_define on the twin: the same refusals in the same order,
// and the same definitions, epoch 0 and view.
func (m *Mem) Define(ctx context.Context, spec DefineSpec) (DefineReply, error) {
	if err := ctx.Err(); err != nil {
		return DefineReply{}, err
	}
	if err := checkDefine(spec); err != nil {
		return DefineReply{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if spec.Build == "" || (m.build != "" && spec.Build != m.build) {
		return DefineReply{}, memRefusal("BUILD", RefusalDetail{})
	}
	// EXISTS over any state of the namespace, as the store refuses any key
	// under it but the receipt stream: the twin's fixture calls are its only
	// other writers.
	if m.spaces[spec.Space] != nil {
		return DefineReply{}, memRefusal("EXISTS", RefusalDetail{})
	}
	s := m.space(spec.Space)
	names := make([]string, 0, len(spec.Tables))
	for _, t := range spec.Tables {
		cols := make([]string, len(t.Columns))
		for i, c := range t.Columns {
			cols[i] = c.Name
		}
		def := TableDefinition{Columns: cols, MemberPrefix: spec.Space + "member:" + t.Name + ":",
			EpochKey: spec.Space + "sprint:epoch", EpochField: "n"}
		if err := m.defineLocked(s, spec.Space, t.Name, def); err != nil {
			return DefineReply{}, err
		}
		names = append(names, t.Name)
	}
	s.view = spec.View
	s.version++
	now := Decimal(strconv.FormatInt(m.now().UnixMilli(), 10))
	m.receipt(spec.Space, LifecycleReceipt{Fn: "define", View: spec.View, Tables: append([]string(nil), names...), Build: spec.Build})
	return DefineReply{Status: "ok", Space: spec.Space, View: spec.View, Tables: names, Epoch: "0", TimeMS: now}, nil
}

// Teardown is ns_tset_teardown on the twin: the same refusals in the same
// order; the whole namespace goes in one call, since the twin has no keys to
// batch.
func (m *Mem) Teardown(ctx context.Context, space, confirm string) (TeardownReply, error) {
	if err := ctx.Err(); err != nil {
		return TeardownReply{}, err
	}
	if _, err := encodeTeardown(space, confirm); err != nil {
		return TeardownReply{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.spaces[space]
	if s == nil || s.view == "" {
		return TeardownReply{}, memRefusal("NOSPACE", RefusalDetail{})
	}
	if s.view != confirm {
		return TeardownReply{}, memRefusal("CONFIRM", RefusalDetail{})
	}
	if m.running[space] {
		return TeardownReply{}, memRefusal("RUNNING", RefusalDetail{})
	}
	delete(m.spaces, space)
	m.receipt(space, LifecycleReceipt{Fn: "teardown", View: confirm, Done: true})
	return TeardownReply{Calls: 1, Done: true}, nil
}

// Package table parses and renders the nova-sprint table from the single
// consistent snapshot returned by the ns_snapshot Redis Function. One call,
// one server instant: section 6 of #2756.
package table

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// Bounds of the snapshot (6.3). A registry larger than its bound makes the
// function return `snapshot: bound exceeded`.
const (
	MaxSprints = 4
	MaxBenches = 64
	MaxFriends = 16
)

// Row is one bench or friend. Benches and friends are rows of one shape
// (6.4): name | up | desired | starting | living | stale | leased | queue |
// done | why. Width is derived from starting+living and never stored (6.5).
type Row struct {
	Name     string
	Up       bool
	Desired  int64
	Missing  bool
	Starting int64
	Living   int64
	Stale    int64
	Leased   int64
	Queue    int64
	Done     int64
	Why      string
}

// Width includes stale leases until the reconciler releases them; it is
// derived, never read from a sidecar key (6.5).
func (r Row) Width() int64 { return r.Starting + r.Living + r.Stale }

// Pipeline is one sprint's counts (6.4).
type Pipeline struct {
	Sprint       string
	Cards        map[string]int64
	Pool         int64
	Waiting      int64
	Backpressure int64
	Orphan       int64
	Reconcile    int64
}

// Proc is one process line: reconciler, harvest worker or consumer.
type Proc struct {
	Name string
	Up   bool
	Age  int64
	Why  string
}

// Snapshot is one consistent instant.
type Snapshot struct {
	Time      int64
	Benches   []Row
	Friends   []Row
	Pipelines []Pipeline
	Procs     []Proc
	Errors    []string
}

const (
	rowFields      = 11
	pipelineFields = 14
	procFields     = 4
)

// Parse decodes the flat tagged list returned by ns_snapshot.
func Parse(raw []any) (*Snapshot, error) {
	snap := &Snapshot{}
	i := 0
	for i < len(raw) {
		tag, err := token(raw, i)
		if err != nil {
			return nil, err
		}
		i++
		switch tag {
		case "time":
			v, err := token(raw, i)
			if err != nil {
				return nil, err
			}
			i++
			n, err := strconv.ParseInt(v, 10, 64)
			if err != nil {
				return nil, fmt.Errorf("snapshot: bad time %q", v)
			}
			snap.Time = n
		case "error":
			v, err := token(raw, i)
			if err != nil {
				return nil, err
			}
			i++
			snap.Errors = append(snap.Errors, v)
		case "bench", "friend":
			row, err := parseRow(raw, i)
			if err != nil {
				return nil, err
			}
			i += rowFields
			if tag == "bench" {
				snap.Benches = append(snap.Benches, row)
			} else {
				snap.Friends = append(snap.Friends, row)
			}
		case "pipeline":
			p, err := parsePipeline(raw, i)
			if err != nil {
				return nil, err
			}
			i += pipelineFields
			snap.Pipelines = append(snap.Pipelines, p)
		case "proc":
			p, err := parseProc(raw, i)
			if err != nil {
				return nil, err
			}
			i += procFields
			snap.Procs = append(snap.Procs, p)
		default:
			return nil, fmt.Errorf("snapshot: unknown record %q", tag)
		}
	}
	sort.Slice(snap.Benches, func(a, b int) bool { return snap.Benches[a].Name < snap.Benches[b].Name })
	sort.Slice(snap.Friends, func(a, b int) bool { return snap.Friends[a].Name < snap.Friends[b].Name })
	sort.Slice(snap.Procs, func(a, b int) bool { return snap.Procs[a].Name < snap.Procs[b].Name })
	return snap, nil
}

func parseRow(raw []any, i int) (Row, error) {
	var r Row
	vals := make([]string, rowFields)
	for k := 0; k < rowFields; k++ {
		v, err := token(raw, i+k)
		if err != nil {
			return r, err
		}
		vals[k] = v
	}
	r.Name = vals[0]
	r.Up = vals[1] == "1"
	r.Missing = vals[3] == "1"
	nums := []*int64{&r.Desired, &r.Starting, &r.Living, &r.Stale, &r.Leased, &r.Queue, &r.Done}
	for k, field := range []int{2, 4, 5, 6, 7, 8, 9} {
		n, err := count(vals[field])
		if err != nil {
			return Row{}, fmt.Errorf("snapshot row %s field %d: %w", r.Name, field, err)
		}
		*nums[k] = n
	}
	r.Why = vals[10]
	return r, nil
}

var pipelineState = []string{
	"queued", "dealt", "running", "ended",
	"harvested", "review-ready", "land-ready", "landed",
}

func parsePipeline(raw []any, i int) (Pipeline, error) {
	vals := make([]string, pipelineFields)
	for k := 0; k < pipelineFields; k++ {
		v, err := token(raw, i+k)
		if err != nil {
			return Pipeline{}, err
		}
		vals[k] = v
	}
	p := Pipeline{Sprint: vals[0], Cards: map[string]int64{}}
	for k, state := range pipelineState {
		n, err := count(vals[1+k])
		if err != nil {
			return Pipeline{}, fmt.Errorf("snapshot pipeline %s %s: %w", p.Sprint, state, err)
		}
		p.Cards[state] = n
	}
	nums := []*int64{&p.Pool, &p.Waiting, &p.Backpressure, &p.Orphan, &p.Reconcile}
	for k, field := range []int{9, 10, 11, 12, 13} {
		n, err := count(vals[field])
		if err != nil {
			return Pipeline{}, fmt.Errorf("snapshot pipeline %s field %d: %w", p.Sprint, field, err)
		}
		*nums[k] = n
	}
	return p, nil
}

func parseProc(raw []any, i int) (Proc, error) {
	vals := make([]string, procFields)
	for k := 0; k < procFields; k++ {
		v, err := token(raw, i+k)
		if err != nil {
			return Proc{}, err
		}
		vals[k] = v
	}
	age, err := strconv.ParseInt(vals[2], 10, 64)
	if err != nil {
		return Proc{}, fmt.Errorf("snapshot process %s age %q: %w", vals[0], vals[2], err)
	}
	return Proc{Name: vals[0], Up: vals[1] == "1", Age: age, Why: vals[3]}, nil
}

func count(s string) (int64, error) {
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("bad count %q", s)
	}
	return n, nil
}

func token(raw []any, i int) (string, error) {
	if i < 0 || i >= len(raw) {
		return "", fmt.Errorf("snapshot: short reply at %d of %d", i, len(raw))
	}
	switch v := raw[i].(type) {
	case string:
		return v, nil
	case []byte:
		return string(v), nil
	case int64:
		return strconv.FormatInt(v, 10), nil
	case nil:
		return "", nil
	default:
		return fmt.Sprint(v), nil
	}
}

// Render prints the table. It is deterministic: registries are sorted, so the
// same snapshot always prints byte for byte the same table.
func (s *Snapshot) Render() string {
	var b strings.Builder
	b.WriteString("name | up | desired | starting | living | stale | leased | queue | done | why\n")
	for _, r := range s.Benches {
		b.WriteString(rowLine("bench", r))
		b.WriteByte('\n')
	}
	for _, r := range s.Friends {
		b.WriteString(rowLine("friend", r))
		b.WriteByte('\n')
	}
	for _, p := range s.Pipelines {
		b.WriteString(pipelineLine(p))
		b.WriteByte('\n')
	}
	for _, p := range s.Procs {
		b.WriteString(procLine(p))
		b.WriteByte('\n')
	}
	for _, e := range s.Errors {
		b.WriteString("RED " + e)
		b.WriteByte('\n')
	}
	return b.String()
}

func rowLine(kind string, r Row) string {
	up := "down"
	if r.Up {
		up = "up"
	}
	desired := strconv.FormatInt(r.Desired, 10)
	if r.Missing {
		desired = "missing"
	}
	cells := []string{
		kind + ":" + r.Name, up, desired,
		strconv.FormatInt(r.Starting, 10),
		strconv.FormatInt(r.Living, 10),
		strconv.FormatInt(r.Stale, 10),
		strconv.FormatInt(r.Leased, 10),
		strconv.FormatInt(r.Queue, 10),
		strconv.FormatInt(r.Done, 10),
		r.Why,
	}
	return strings.Join(cells, " | ")
}

func pipelineLine(p Pipeline) string {
	parts := []string{"pipeline", p.Sprint}
	for _, state := range pipelineState {
		parts = append(parts, state+"="+strconv.FormatInt(p.Cards[state], 10))
	}
	parts = append(parts,
		"pool="+strconv.FormatInt(p.Pool, 10),
		"waiting="+strconv.FormatInt(p.Waiting, 10),
		"backpressure="+strconv.FormatInt(p.Backpressure, 10),
		"orphan-effect="+strconv.FormatInt(p.Orphan, 10),
		"reconcile-required="+strconv.FormatInt(p.Reconcile, 10),
	)
	return strings.Join(parts, " ")
}

func procLine(p Proc) string {
	state := "down"
	if p.Up {
		state = "up"
	}
	return fmt.Sprintf("proc %s %s age=%ds", p.Name, state, p.Age)
}

package main

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

const (
	protocolVersion     = 3
	defaultReservePort  = "7680"
	defaultKeepaliveMs  = 5_000
	defaultConnDeadMs   = 15_000
	defaultReconnectMs  = 15_000
	defaultCIFailOpenMs = 3_000
	defaultClaimTTLMs   = 4 * 60 * 60 * 1_000
)

var (
	identRe   = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)
	prioRanks = map[string]int{"low": 0, "normal": 1, "high": 2, "read": 3, "critical": 4, "blocker": 5}
)

func priorityRank(p string) int {
	if r, ok := prioRanks[p]; ok {
		return r
	}
	return prioRanks["normal"]
}

type needItem struct {
	name   string
	amount int
	hasNum bool
}

func parseNeedItems(raw string) ([]needItem, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	seen := map[string]bool{}
	var items []needItem
	for _, part := range strings.Split(raw, ",") {
		f := strings.Fields(part)
		if len(f) == 0 || len(f) > 2 || !identRe.MatchString(f[0]) || seen[f[0]] {
			return nil, fmt.Errorf("malformed or duplicate need %q", strings.TrimSpace(part))
		}
		seen[f[0]] = true
		if len(f) == 1 {
			items = append(items, needItem{name: f[0]})
			continue
		}
		n, err := strconv.Atoi(f[1])
		if err != nil || n < 1 {
			return nil, fmt.Errorf("%s wants a whole number >= 1, not %q", f[0], f[1])
		}
		items = append(items, needItem{name: f[0], amount: n, hasNum: true})
	}
	return items, nil
}

// NeedsSpec decodes either the canonical string form ("cpu 8, memory 16, linux")
// or a resource map ({"cpu":8,"memory":16}).
type NeedsSpec struct {
	Raw string
	Map map[string]int
}

func (n *NeedsSpec) UnmarshalJSON(b []byte) error {
	var s string
	if json.Unmarshal(b, &s) == nil {
		n.Raw, n.Map = s, nil
		return nil
	}
	var m map[string]int
	if err := json.Unmarshal(b, &m); err != nil {
		return err
	}
	n.Map, n.Raw = m, ""
	return nil
}

func (n NeedsSpec) MarshalJSON() ([]byte, error) {
	if n.Map != nil {
		return json.Marshal(n.Map)
	}
	return json.Marshal(n.Raw)
}

func (n NeedsSpec) items() ([]needItem, error) {
	if n.Map != nil {
		var out []needItem
		for _, k := range slices.Sorted(maps.Keys(n.Map)) {
			v := n.Map[k]
			if !identRe.MatchString(k) || v < 1 {
				return nil, fmt.Errorf("%s wants a whole number >= 1, not %d", k, v)
			}
			out = append(out, needItem{name: k, amount: v, hasNum: true})
		}
		return out, nil
	}
	return parseNeedItems(n.Raw)
}

// ReconnectLease is an in-flight lease re-presented by a live client during the
// server's 15s restart reconnect window (docs/RESOURCES.md §8.1).
type ReconnectLease struct {
	LeaseID     string         `json:"lease_id"`
	Host        string         `json:"host"`
	Needs       map[string]int `json:"needs"`
	Exclusive   bool           `json:"exclusive"`
	ParentLease string         `json:"parent_lease"`
	Holder      string         `json:"holder"`
	Source      string         `json:"source"`
	Consumer    string         `json:"consumer"`
	Label       string         `json:"label"`
	Priority    string         `json:"priority"`
	Why         string         `json:"why"`
	Cores       int            `json:"cores"`
	CPUs        []int          `json:"cpus"`
	PID         int            `json:"pid"`
	Ancestors   []int          `json:"ancestors,omitempty"`
}

// ClientFrame is one newline-delimited JSON message from peer to server (§8.1).
type ClientFrame struct {
	Op          string           `json:"op"`
	Version     int              `json:"version,omitempty"`
	Password    string           `json:"password,omitempty"`
	Client      string           `json:"client,omitempty"`
	ReqID       string           `json:"req_id,omitempty"`
	Mode        string           `json:"mode,omitempty"`
	Host        string           `json:"host,omitempty"`
	Needs       NeedsSpec        `json:"needs,omitempty"`
	Exclusive   bool             `json:"exclusive,omitempty"`
	Nowait      bool             `json:"nowait,omitempty"`
	WaitMs      int64            `json:"wait_ms,omitempty"`
	TTLMs       int64            `json:"ttl_ms,omitempty"`
	ParentLease string           `json:"parent_lease,omitempty"`
	Holder      string           `json:"holder,omitempty"`
	Source      string           `json:"source,omitempty"`
	Consumer    string           `json:"consumer,omitempty"`
	Label       string           `json:"label,omitempty"`
	Priority    string           `json:"priority,omitempty"`
	Why         string           `json:"why,omitempty"`
	PID         int              `json:"pid,omitempty"`
	Ancestors   []int            `json:"ancestors,omitempty"`
	UsableCPUs  []int            `json:"usable_cpus,omitempty"`
	SMTGroups   [][]int          `json:"smt_groups,omitempty"`
	LeaseID     string           `json:"lease_id,omitempty"`
	Leases      []ReconnectLease `json:"leases,omitempty"`
	TS          int64            `json:"ts,omitempty"`
}

type LeaseView struct {
	LeaseID     string         `json:"lease_id"`
	Sublease    bool           `json:"sublease"`
	ParentLease string         `json:"parent_lease,omitempty"`
	Host        string         `json:"host"`
	Needs       map[string]int `json:"needs"`
	Exclusive   bool           `json:"exclusive,omitempty"`
	Holder      string         `json:"holder"`
	Source      string         `json:"source"`
	Consumer    string         `json:"consumer,omitempty"`
	Label       string         `json:"label"`
	Priority    string         `json:"priority"`
	Paused      bool           `json:"paused"`
	Cores       int            `json:"cores"`
	CPUs        []int          `json:"cpus,omitempty"`
	PID         int            `json:"pid,omitempty"`
	Since       string         `json:"since"`
	Why         string         `json:"why,omitempty"`
}

type ClaimView struct {
	LeaseID   string         `json:"lease_id"`
	Host      string         `json:"host"`
	Needs     map[string]int `json:"needs"`
	Exclusive bool           `json:"exclusive,omitempty"`
	Source    string         `json:"source"`
	Since     string         `json:"since"`
	ExpiresAt string         `json:"expires_at"`
	Why       string         `json:"why,omitempty"`
}

type WaiterView struct {
	ReqID       string         `json:"req_id"`
	Position    int            `json:"position"`
	Mode        string         `json:"mode"`
	Host        string         `json:"host,omitempty"`
	Needs       map[string]int `json:"needs"`
	Tags        []string       `json:"tags,omitempty"`
	Exclusive   bool           `json:"exclusive,omitempty"`
	ParentLease string         `json:"parent_lease,omitempty"`
	Holder      string         `json:"holder"`
	Source      string         `json:"source"`
	Consumer    string         `json:"consumer,omitempty"`
	Label       string         `json:"label"`
	Priority    string         `json:"priority"`
	Since       string         `json:"since"`
	Why         string         `json:"why,omitempty"`
}

type SnapshotMachine struct {
	Host        string         `json:"host"`
	Address     string         `json:"address,omitempty"`
	User        string         `json:"user,omitempty"`
	Online      bool           `json:"online"`
	Tags        []string       `json:"tags"`
	UsableCores int            `json:"usable_cores"`
	Resources   map[string]int `json:"resources"`
	Free        map[string]int `json:"free"`
	Leases      []LeaseView    `json:"leases"`
	Claims      []ClaimView    `json:"claims"`
	Queue       []WaiterView   `json:"queue"`
}

// ServerFrame is one newline-delimited JSON message from server to peer (§8.2).
type ServerFrame struct {
	Op             string            `json:"op"`
	ReqID          string            `json:"req_id,omitempty"`
	LeaseID        string            `json:"lease_id,omitempty"`
	Sublease       bool              `json:"sublease,omitempty"`
	Host           string            `json:"host,omitempty"`
	Address        string            `json:"address,omitempty"`
	User           string            `json:"user,omitempty"`
	Needs          map[string]int    `json:"needs,omitempty"`
	Cores          int               `json:"cores,omitempty"`
	CPUs           []int             `json:"cpus,omitempty"`
	Env            map[string]string `json:"env,omitempty"`
	ExpiresAt      string            `json:"expires_at,omitempty"`
	Position       int               `json:"position,omitempty"`
	State          string            `json:"state,omitempty"`
	Reason         string            `json:"reason,omitempty"`
	Blockers       []string          `json:"blockers,omitempty"`
	Code           string            `json:"code,omitempty"`
	Message        string            `json:"message,omitempty"`
	Cycle          []string          `json:"cycle,omitempty"`
	ReconnectUntil string            `json:"reconnect_until"`
	Machines       []SnapshotMachine `json:"machines,omitempty"`
	Queue          []WaiterView      `json:"queue,omitempty"`
	Type           string            `json:"type,omitempty"`
	Summary        string            `json:"summary,omitempty"`
	At             string            `json:"at,omitempty"`
	TS             int64             `json:"ts,omitempty"`
}

// MarshalServerFrame encodes ServerFrame without emitting reconnect_until on
// non-snapshot frames, while keeping reconnect_until:"" present on snapshot (§8.2).
func MarshalServerFrame(f ServerFrame) ([]byte, error) {
	if f.Op == "snapshot" {
		type snapOut struct {
			Op             string            `json:"op"`
			ReconnectUntil string            `json:"reconnect_until"`
			Machines       []SnapshotMachine `json:"machines"`
			Queue          []WaiterView      `json:"queue,omitempty"`
		}
		ms := f.Machines
		if ms == nil {
			ms = []SnapshotMachine{}
		}
		return json.Marshal(snapOut{Op: "snapshot", ReconnectUntil: f.ReconnectUntil, Machines: ms, Queue: f.Queue})
	}
	if f.Op == "granted" {
		type grantOut struct {
			Op        string            `json:"op"`
			ReqID     string            `json:"req_id"`
			LeaseID   string            `json:"lease_id"`
			Sublease  bool              `json:"sublease"`
			Host      string            `json:"host"`
			Address   string            `json:"address,omitempty"`
			User      string            `json:"user,omitempty"`
			Needs     map[string]int    `json:"needs"`
			Cores     int               `json:"cores"`
			CPUs      []int             `json:"cpus,omitempty"`
			Env       map[string]string `json:"env"`
			ExpiresAt string            `json:"expires_at"`
		}
		return json.Marshal(grantOut{
			Op: "granted", ReqID: f.ReqID, LeaseID: f.LeaseID, Sublease: f.Sublease,
			Host: f.Host, Address: f.Address, User: f.User, Needs: f.Needs,
			Cores: f.Cores, CPUs: f.CPUs, Env: f.Env, ExpiresAt: f.ExpiresAt,
		})
	}
	type genericOut struct {
		Op       string   `json:"op"`
		ReqID    string   `json:"req_id,omitempty"`
		LeaseID  string   `json:"lease_id,omitempty"`
		Host     string   `json:"host,omitempty"`
		Position int      `json:"position,omitempty"`
		State    string   `json:"state,omitempty"`
		Reason   string   `json:"reason,omitempty"`
		Blockers []string `json:"blockers,omitempty"`
		Code     string   `json:"code,omitempty"`
		Message  string   `json:"message,omitempty"`
		Cycle    []string `json:"cycle,omitempty"`
		Type     string   `json:"type,omitempty"`
		Summary  string   `json:"summary,omitempty"`
		At       string   `json:"at,omitempty"`
		TS       int64    `json:"ts,omitempty"`
	}
	return json.Marshal(genericOut{
		Op: f.Op, ReqID: f.ReqID, LeaseID: f.LeaseID, Host: f.Host, Position: f.Position,
		State: f.State, Reason: f.Reason, Blockers: f.Blockers, Code: f.Code, Message: f.Message,
		Cycle: f.Cycle, Type: f.Type, Summary: f.Summary, At: f.At, TS: f.TS,
	})
}

func buildLeaseEnv(leaseID, host string, cores int) map[string]string {
	cs := strconv.Itoa(max(1, cores))
	return map[string]string{
		"NOVA_RESERVE_LEASE":         leaseID,
		"NOVA_RESERVE_HOST":          host,
		"NOVA_RESERVE_CPUS":          cs,
		"GOMAXPROCS":                 cs,
		"CARGO_BUILD_JOBS":           cs,
		"RAYON_NUM_THREADS":          cs,
		"CMAKE_BUILD_PARALLEL_LEVEL": cs,
		"MAKEFLAGS":                  "-j" + cs,
	}
}

func mergeProcessEnv(baseFn func() []string, extra map[string]string) []string {
	var base []string
	if baseFn != nil {
		base = baseFn()
	}
	out := make([]string, 0, len(base)+len(extra))
	for _, kv := range base {
		if k, _, _ := strings.Cut(kv, "="); extra[k] == "" {
			out = append(out, kv)
		}
	}
	for _, k := range slices.Sorted(maps.Keys(extra)) {
		out = append(out, k+"="+extra[k])
	}
	return out
}

type capRecord struct {
	Tags        []string       `json:"tags"`
	UsableCores int            `json:"usable_cores"`
	Resources   map[string]int `json:"resources"`
	SetByPID    int            `json:"set_by_pid,omitempty"`
	SetAt       string         `json:"set_at,omitempty"`
}

type corrupt string

func (c corrupt) Error() string { return string(c) }

func capPath(dir string) string { return filepath.Join(dir, "capacity.json") }

func effectiveOS(goos string) string {
	if goos == "darwin" {
		return "mac"
	}
	return goos
}

func effectiveTags(custom []string, goos, goarch string) []string {
	set := map[string]bool{}
	for _, t := range custom {
		if t = strings.TrimSpace(t); t != "" {
			set[t] = true
		}
	}
	if osTag := effectiveOS(goos); osTag != "" {
		set[osTag] = true
	}
	if goarch != "" {
		set[goarch] = true
	}
	if out := slices.Sorted(maps.Keys(set)); out != nil {
		return out
	}
	return []string{}
}

func readCapacity(dir string) (capRecord, bool, error) {
	p := capPath(dir)
	line, ok, err := readRecord(p)
	if !ok || err != nil {
		return capRecord{}, false, err
	}
	var c capRecord
	if err := json.Unmarshal(line, &c); err != nil {
		return capRecord{}, false, corrupt(fmt.Sprintf("%s holds invalid JSON (%v)", p, err))
	}
	if c.UsableCores < 1 || c.Resources["cpu"] < 1 || c.Resources["memory"] < 1 {
		return capRecord{}, false, corrupt(fmt.Sprintf("%s is missing positive cpu, memory, or usable_cores", p))
	}
	for k, v := range c.Resources {
		if !identRe.MatchString(k) || v < 1 || slices.Contains(c.Tags, k) {
			return capRecord{}, false, corrupt(fmt.Sprintf("%s has invalid or colliding resource %s=%d", p, k, v))
		}
	}
	if c.Tags == nil {
		c.Tags = []string{}
	}
	return c, true, nil
}

func writeCapacity(dir string, c capRecord, pid int) error {
	if c.Tags == nil {
		c.Tags = []string{}
	}
	slices.Sort(c.Tags)
	b, err := json.Marshal(c)
	if err != nil {
		return err
	}
	if err := writeRecord(capPath(dir), b, pid); err != nil {
		if odd := (*exitError)(nil); errors.As(err, &odd) {
			return err
		}
		return refuse(2, "%v; run: sudo rm -f %s", cmp.Or(errors.Unwrap(err), err), shellQuote(capPath(dir)))
	}
	return nil
}

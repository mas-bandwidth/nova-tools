// Package preflight holds the nova-sprint preflight checks (#2756 section 7).
// Each check prints one line, GREEN or RED with its number, and any RED
// exits 1. This file is the verb's frame (#2947 rev 3): the one Line type,
// Run, the snapshot reader and the windows. store.go holds the store checks
// over the snapshot; fleet.go holds #2948's fleet checks.
//
// Preflight reads Redis only, never a file, and writes nothing. Every age is
// Redis TIME minus the value's own at (2.1 rule 3). The windows come from
// s:<S>:policy, which sprint open (#2939) fills from the embedded
// preflight.conf; a missing window is RED on 7.9 and never defaults.
package preflight

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"net"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/redis/go-redis/v9"
)

// ACLRules is the fleet Redis user preflight runs as (#2947 rev 3): read
// every key, INFO, FUNCTION LIST and TIME, and nothing dangerous. Two changes
// from rev 3's text, both found against redis-server: -@dangerous comes
// before the single commands because INFO is itself in @dangerous (rev 3's
// order, -@dangerous last, takes INFO away again and 7.1 could never read
// AOF), and +ping is added because store.Open, the one auth path (#3320),
// PINGs and PING is not in @read.
const ACLRules = "+@read -@dangerous +info +function|list +time +ping ~*"

// Conf is the embedded preflight.conf: the windows sprint open copies into
// s:<S>:policy.
//
//go:embed preflight.conf
var Conf string

// Defaults parses Conf into field -> value.
func Defaults() map[string]string {
	out := map[string]string{}
	for _, line := range strings.Split(Conf, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if k, v, ok := strings.Cut(line, "="); ok {
			out[strings.TrimSpace(k)] = strings.TrimSpace(v)
		}
	}
	return out
}

// PolicyFields are the fields 7.9 requires on s:<S>:policy: the brakes and
// the four windows.
var PolicyFields = []string{"backpressure_missing", "debt_cap", "share",
	"start_window_s", "beat_stale_s", "reconciler_pass_s", "interim_window_s"}

// Exchanges is how many pipelined round trips Run makes, at any fleet size:
// the registries, then every member's keys, then what the members name (the
// queued cards, the ready tasks, the desired machines' ceilings and cap:log
// since the sprint opened). The third exists because the preflight user has
// no scripting to join those server-side.
const Exchanges = 3

// ReconcilerTTL and ReconcilerPass are sprint plan's own 7.7 thresholds
// (internal/nsprint/sprint/plan.go reconcilerLine, read in the viewer seat
// without the policy). Preflight itself takes reconciler_pass_s from
// s:<S>:policy and never these.
const (
	ReconcilerTTL  = 6 * time.Second
	ReconcilerPass = 20 * time.Second
)

// Line is one preflight check's result.
type Line struct {
	N    string // the #2756 section 7 number, for example "7.3"
	Name string
	Red  bool
	Why  string
}

func (l Line) String() string {
	state := "GREEN"
	if l.Red {
		state = "RED"
	}
	return fmt.Sprintf("%s %s %s: %s", state, l.N, l.Name, l.Why)
}

// ExitCode is 1 when any line is RED, else 0.
func ExitCode(lines []Line) int {
	for _, l := range lines {
		if l.Red {
			return 1
		}
	}
	return 0
}

// Options names the sprint. Empty checks every open sprint in `sprints`.
type Options struct {
	Sprint string
}

// StoreNames is the store lines in section order.
var StoreNames = []struct{ N, Name string }{
	{"7.1", "redis"}, {"7.2", "state-files"}, {"7.3", "leases-vs-beats"}, {"7.7", "reconciler"},
	{"7.9", "policy"}, {"7.10", "receipts"}, {"7.11", "supply"}, {"7.13", "guards"},
	{"7.15", "machine-ceiling"}, {"7.27", "ttl"}, {"7.28", "under-load"},
}

// Run reads the snapshot and returns the store lines in section order.
func Run(ctx context.Context, c *redis.Client, o Options) []Line {
	s := readSnapshot(ctx, c, o.Sprint)
	if s.down != nil {
		return Unreachable(s.down)
	}
	return s.checks()
}

// Unreachable is every store line RED when Redis cannot be reached: 7.1 says
// unreachable, every other line says it could not read.
func Unreachable(err error) []Line {
	lines := make([]Line, len(StoreNames))
	for i, n := range StoreNames {
		why := "not read: redis unreachable"
		if n.N == "7.1" {
			why = "unreachable: " + firstLine(err.Error())
		}
		lines[i] = Line{N: n.N, Name: n.Name, Red: true, Why: why}
	}
	return lines
}

// IsUnreachable reports a connection that never reached Redis (refused,
// timed out, no route), as distinct from a refusal by Redis itself (AUTH, ACL).
func IsUnreachable(err error) bool {
	var op *net.OpError
	return errors.As(err, &op)
}

// consumer is one registered bench or friend and the keys it owns.
type consumer struct {
	kind, name string
	desired    map[string]string
	beat       map[string]string
	roles      map[string]string
	row        map[string]string // bench:<b>, the host table row
	starting   []redis.Z
	living     []redis.Z
	slotsKey   bool // friend:<f>:slots, rev 0's interim width key
}

func (k consumer) key(suffix string) string { return k.kind + ":" + k.name + suffix }

// sprintState is one checked sprint's keys.
type sprintState struct {
	name   string
	meta   map[string]string // s:<S>
	policy map[string]string
	pool   []string
	ready  []string
	log    []redis.XMessage // newest first
	idx    map[string][]string
	cards  map[string]map[string]string // queued and pooled card records by label
	tasks  map[string]map[string]string // ready task records by id
}

func (s *sprintState) open() bool { return s.meta["status"] == "open" }

// snapshot is everything the store checks read, taken in Exchanges round
// trips.
type snapshot struct {
	down error  // a connection error: Redis was never reached
	seat string // the ACL user the snapshot was read as

	now       time.Time
	info      map[string]string
	infoErr   error
	libs      []redis.Library
	libErr    error
	lease     map[string]string
	proc      map[string]string
	acl       map[string]string
	consumers []*consumer
	sprints   []*sprintState
	ceilings  map[string]string // machine -> slots ("" when absent)
	capLog    []redis.XMessage
	pttl      map[string]time.Duration
	readErr   []string // a read that failed (other than INFO and FUNCTION LIST)
}

// reader queues reads on one pipeline and remembers each key's PTTL read.
type reader struct {
	ctx  context.Context
	pipe redis.Pipeliner
	ttl  map[string]*redis.DurationCmd
}

func (r *reader) touch(key string) {
	if _, ok := r.ttl[key]; !ok {
		r.ttl[key] = r.pipe.PTTL(r.ctx, key)
	}
}

func (r *reader) hash(key string) *redis.MapStringStringCmd {
	r.touch(key)
	return r.pipe.HGetAll(r.ctx, key)
}

func (r *reader) exec(s *snapshot) error {
	_, err := r.pipe.Exec(r.ctx)
	for k, cmd := range r.ttl {
		if cmd.Err() == nil {
			s.pttl[k] = cmd.Val()
		}
	}
	if err != nil && !errors.Is(err, redis.Nil) && IsUnreachable(err) {
		return err
	}
	return nil
}

func (s *snapshot) failed(what string, err error) {
	if err != nil && !errors.Is(err, redis.Nil) {
		s.readErr = append(s.readErr, what+": "+firstLine(err.Error()))
	}
}

func readSnapshot(ctx context.Context, c *redis.Client, sprint string) *snapshot {
	s := &snapshot{seat: seatOf(c), pttl: map[string]time.Duration{}, ceilings: map[string]string{}}

	// Exchange 1: the clock, the server, the registries and the singletons.
	r := &reader{ctx: ctx, pipe: c.Pipeline(), ttl: map[string]*redis.DurationCmd{}}
	clock := r.pipe.Time(ctx)
	info := r.pipe.Info(ctx, "server", "persistence")
	libs := r.pipe.FunctionList(ctx, redis.FunctionListQuery{LibraryNamePattern: fn.Library, WithCode: true})
	sets := map[string]*redis.StringSliceCmd{}
	for _, k := range []string{"sprints", "benches", "friends"} {
		r.touch(k)
		sets[k] = r.pipe.SMembers(ctx, k)
	}
	lease, proc, acl := r.hash("lease:reconciler"), r.hash("proc:reconciler"), r.hash("proc:acl-test")
	if err := r.exec(s); err != nil {
		s.down = err
		return s
	}
	if err := clock.Err(); err != nil {
		if IsUnreachable(err) {
			s.down = err
			return s
		}
		s.failed("TIME", err)
	}
	s.now = clock.Val()
	s.infoErr, s.libErr = info.Err(), libs.Err()
	if s.infoErr == nil {
		s.info = infoFields(info.Val())
	}
	s.libs = libs.Val()
	for k, cmd := range sets {
		s.failed(k, cmd.Err())
	}
	for k, cmd := range map[string]*redis.MapStringStringCmd{"lease:reconciler": lease, "proc:reconciler": proc, "proc:acl-test": acl} {
		s.failed(k, cmd.Err())
	}
	s.lease, s.proc, s.acl = lease.Val(), proc.Val(), acl.Val()

	names := sets["sprints"].Val()
	if sprint != "" {
		names = []string{sprint}
	}
	sort.Strings(names)
	for _, reg := range []struct{ kind, set string }{{"bench", "benches"}, {"friend", "friends"}} {
		members := sets[reg.set].Val()
		sort.Strings(members)
		for _, m := range members {
			s.consumers = append(s.consumers, &consumer{kind: reg.kind, name: m})
		}
	}

	// Exchange 2: every member's keys.
	r = &reader{ctx: ctx, pipe: c.Pipeline(), ttl: map[string]*redis.DurationCmd{}}
	type consumerReads struct {
		desired, beat, roles, row *redis.MapStringStringCmd
		starting, living          *redis.ZSliceCmd
		slots                     *redis.IntCmd
	}
	cr := make([]consumerReads, len(s.consumers))
	for i, k := range s.consumers {
		cr[i].desired = r.hash(k.key(":desired"))
		cr[i].beat = r.hash(k.key(":beat"))
		r.touch(k.key(":starting"))
		cr[i].starting = r.pipe.ZRangeWithScores(ctx, k.key(":starting"), 0, -1)
		r.touch(k.key(":living"))
		cr[i].living = r.pipe.ZRangeWithScores(ctx, k.key(":living"), 0, -1)
		if k.kind == "friend" {
			cr[i].roles = r.hash(k.key(":roles"))
			r.touch(k.key(":slots"))
			cr[i].slots = r.pipe.Exists(ctx, k.key(":slots"))
		} else {
			cr[i].row = r.hash(k.key(""))
		}
	}
	type sprintReads struct {
		meta, policy *redis.MapStringStringCmd
		pool, ready  *redis.StringSliceCmd
		log          *redis.XMessageSliceCmd
		idx          map[string]*redis.StringSliceCmd
	}
	sr := make([]sprintReads, len(names))
	for i, name := range names {
		p := "s:" + name
		sr[i].meta = r.hash(p)
		sr[i].policy = r.hash(p + ":policy")
		r.touch(p + ":pool")
		sr[i].pool = r.pipe.ZRange(ctx, p+":pool", 0, -1)
		r.touch(p + ":ready")
		sr[i].ready = r.pipe.ZRange(ctx, p+":ready", 0, -1)
		r.touch(p + ":log")
		sr[i].log = r.pipe.XRevRange(ctx, p+":log", "+", "-")
		sr[i].idx = map[string]*redis.StringSliceCmd{}
		for _, kind := range fn.IndexKinds {
			for _, state := range fn.IndexStates[kind] {
				key := p + ":idx:" + kind + ":" + state
				r.touch(key)
				sr[i].idx[kind+":"+state] = r.pipe.SMembers(ctx, key)
			}
		}
	}
	if err := r.exec(s); err != nil {
		s.down = err
		return s
	}
	for i, k := range s.consumers {
		k.desired, k.beat = cr[i].desired.Val(), cr[i].beat.Val()
		k.starting, k.living = cr[i].starting.Val(), cr[i].living.Val()
		for _, cmd := range []redis.Cmder{cr[i].desired, cr[i].beat, cr[i].starting, cr[i].living} {
			s.failed(k.key(""), cmd.Err())
		}
		if cr[i].roles != nil {
			k.roles = cr[i].roles.Val()
			k.slotsKey = cr[i].slots.Val() > 0
		}
		if cr[i].row != nil {
			k.row = cr[i].row.Val()
		}
	}
	for i, name := range names {
		st := &sprintState{name: name, meta: sr[i].meta.Val(), policy: sr[i].policy.Val(),
			pool: sr[i].pool.Val(), ready: sr[i].ready.Val(), log: sr[i].log.Val(),
			idx: map[string][]string{}, cards: map[string]map[string]string{}, tasks: map[string]map[string]string{}}
		for _, cmd := range []redis.Cmder{sr[i].meta, sr[i].policy, sr[i].pool, sr[i].ready, sr[i].log} {
			s.failed("s:"+name, cmd.Err())
		}
		for k, cmd := range sr[i].idx {
			s.failed("s:"+name+":idx:"+k, cmd.Err())
			v := cmd.Val()
			sort.Strings(v)
			st.idx[k] = v
		}
		s.sprints = append(s.sprints, st)
	}

	// Exchange 3: what the members name.
	r = &reader{ctx: ctx, pipe: c.Pipeline(), ttl: map[string]*redis.DurationCmd{}}
	cards := make([]map[string]*redis.MapStringStringCmd, len(s.sprints))
	tasks := make([]map[string]*redis.MapStringStringCmd, len(s.sprints))
	var since time.Time
	for i, st := range s.sprints {
		cards[i], tasks[i] = map[string]*redis.MapStringStringCmd{}, map[string]*redis.MapStringStringCmd{}
		for _, label := range append(append([]string{}, st.idx["card:queued"]...), st.pool...) {
			if _, ok := cards[i][label]; !ok {
				cards[i][label] = r.hash("s:" + st.name + ":card:" + label)
			}
		}
		for _, id := range st.ready {
			tasks[i][id] = r.hash("s:" + st.name + ":task:" + id)
		}
		if at, ok := parseStamp(st.meta["opened_at"]); ok && st.open() && (since.IsZero() || at.Before(since)) {
			since = at
		}
	}
	machines := map[string]*redis.StringCmd{}
	for _, k := range s.consumers {
		if m := k.desired["machine"]; m != "" && machines[m] == nil {
			key := "machine:" + m + ":ceiling"
			r.touch(key)
			machines[m] = r.pipe.HGet(ctx, key, "slots")
		}
	}
	var capLog *redis.XMessageSliceCmd
	if !since.IsZero() {
		r.touch("cap:log")
		capLog = r.pipe.XRange(ctx, "cap:log", strconv.FormatInt(since.UnixMilli(), 10), "+")
	}
	if err := r.exec(s); err != nil {
		s.down = err
		return s
	}
	for i, st := range s.sprints {
		for label, cmd := range cards[i] {
			s.failed("s:"+st.name+":card:"+label, cmd.Err())
			st.cards[label] = cmd.Val()
		}
		for id, cmd := range tasks[i] {
			s.failed("s:"+st.name+":task:"+id, cmd.Err())
			st.tasks[id] = cmd.Val()
		}
	}
	for m, cmd := range machines {
		s.failed("machine:"+m+":ceiling", cmd.Err())
		s.ceilings[m] = cmd.Val()
	}
	if capLog != nil {
		s.failed("cap:log", capLog.Err())
		s.capLog = capLog.Val()
	}
	sort.Strings(s.readErr)
	return s
}

func library(libs []redis.Library, name string) (redis.Library, bool) {
	for _, l := range libs {
		if l.Name == name {
			return l, true
		}
	}
	return redis.Library{}, false
}

func infoFields(info string) map[string]string {
	out := map[string]string{}
	for _, line := range strings.Split(info, "\n") {
		if k, v, ok := strings.Cut(strings.TrimSpace(line), ":"); ok {
			out[k] = v
		}
	}
	return out
}

// window is a policy field in seconds over the checked sprints: the smallest
// value any of them sets, so the strictest sprint governs. ok is false when
// no checked sprint sets it (7.9 is RED then; the caller never defaults).
func (s *snapshot) window(field string) (time.Duration, bool) {
	var best time.Duration
	found := false
	for _, st := range s.sprints {
		v, err := strconv.Atoi(strings.TrimSpace(st.policy[field]))
		if err != nil || v <= 0 {
			continue
		}
		if d := time.Duration(v) * time.Second; !found || d < best {
			best, found = d, true
		}
	}
	return best, found
}

// stamp reads a Redis time as seconds, or milliseconds when it is that large.
func stamp(v float64) time.Time {
	if v > 1e12 {
		return time.UnixMilli(int64(v))
	}
	sec := int64(v)
	return time.Unix(sec, int64((v-float64(sec))*1e9))
}

// parseStamp reads seconds, milliseconds or RFC 3339.
func parseStamp(s string) (time.Time, bool) {
	s = strings.TrimSpace(s)
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, true
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil || v <= 0 {
		return time.Time{}, false
	}
	return stamp(v), true
}

func wholeSecs(d time.Duration) string {
	return fmt.Sprintf("%ds", int64(d.Round(time.Second)/time.Second))
}

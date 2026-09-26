package friend

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/ws"
	"github.com/redis/go-redis/v9"
)

// Policy is the ladder's sweeps per rung (spec 5.5). The interim idle_ticks is
// 3; underfull is 60 s, which is 6 sweeps at the 10 s sweep.
type Policy struct {
	IdleTicks      int
	UnderfullTicks int
}

// DefaultPolicy is the interim policy of spec 5.5.
var DefaultPolicy = Policy{IdleTicks: 3, UnderfullTicks: 6}

// Repairer checks and repairs a unit wake path (bootstrap or kickstart of THAT
// unit, never a group restart; #3048). The ladder calls it once, on the sweep
// that reaches rung 2 for a friend whose wake path is a unit.
type Repairer interface {
	Repair(ctx context.Context, friend string, wp WakePath) (string, error)
}

// Ladder is the reconciler's friend sweep. It holds configuration only: the
// rung, its tick count and the last applied call live in friend:<f>:state, so
// a new Ladder after a restart continues where the old one stopped.
type Ladder struct {
	Store  *store.Store
	Policy Policy
	// Repair is optional; nil leaves a unit wake path to #3048's check.
	Repair Repairer
	Actor  string
}

// SweepResult is one sweep: the ladder steps that did something.
type SweepResult struct {
	Idem  string
	Steps []Step
	// Life is each ApplyLife that wrote (#3153).
	Life []ApplyResult
}

type reading struct {
	friend string
	up     bool
	paused bool
	slots  int
	leased int
	open   int
	// life: the life classifier applies to this friend (#3153): its wake
	// mode is declared (a firing receipt proved the deliver and turn
	// producers), or its stored state is the classifier's own.
	life bool
}

// Sweep first applies the life classifier (ApplyLife, #3153) to every friend
// it covers, then observes every registered UP friend once, applies the
// ladder through ns_friend_state, then runs the redistribute tick, which
// moves the work of every out-of-credits, away, down, wake-missed, or
// idle-at-rung-3 friend in one call. The classifier covers a friend whose
// wake mode is declared or whose state it already owns: until the turn
// hooks that write turn-start are installed on a friend's bench, its beats
// alone would read OFFLINE-MODEL and block its work.
func (l *Ladder) Sweep(ctx context.Context) (SweepResult, error) {
	if l == nil || l.Store == nil {
		return SweepResult{}, fmt.Errorf("friend sweep: nil store")
	}
	policy := l.Policy
	if policy.IdleTicks < 1 {
		policy.IdleTicks = DefaultPolicy.IdleTicks
	}
	if policy.UnderfullTicks < 1 {
		policy.UnderfullTicks = DefaultPolicy.UnderfullTicks
	}
	idem, err := sweepIdem()
	if err != nil {
		return SweepResult{}, err
	}
	readings, err := read(ctx, l.Store)
	if err != nil {
		return SweepResult{}, err
	}
	res := SweepResult{Idem: idem}
	now := time.Now()
	for _, r := range readings {
		if !r.life {
			continue
		}
		applied, err := ApplyLife(ctx, l.Store, r.friend, now, nil)
		if err != nil {
			return res, err
		}
		if applied.Status == "OK" {
			res.Life = append(res.Life, applied)
		}
	}
	for _, r := range readings {
		if !r.up {
			continue // presence: the redistribute tick marks down
		}
		obs := Observation{Friend: r.friend, State: StateUp, Open: r.open, Living: r.leased, Ticks: policy.IdleTicks}
		switch {
		case r.paused || r.open == 0:
		case r.leased == 0:
			obs.State = StateIdle
		case r.leased < r.slots:
			obs.State, obs.Ticks = StateUnderfull, policy.UnderfullTicks
		}
		step, err := Observe(ctx, l.Store, obs, l.Actor, idem+":"+r.friend)
		if err != nil {
			return res, err
		}
		if step.Action == "wake-unit" && l.Repair != nil {
			wp, err := ReadWakePath(ctx, l.Store, r.friend)
			if err != nil {
				return res, err
			}
			line, err := l.Repair.Repair(ctx, r.friend, wp)
			if err != nil {
				line = "down: " + err.Error()
			}
			step.Repair = line
		}
		if step.Status == "OK" && (step.State != StateUp || step.Action != "") {
			res.Steps = append(res.Steps, step)
		}
	}
	return res, nil
}

func sweepIdem() (string, error) {
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("friend sweep: idem: %w", err)
	}
	return "sweep-" + hex.EncodeToString(b[:]), nil
}

// openSprints lists open and paused sprints, as the Lua ticks do.
func openSprints(ctx context.Context, client *redis.Client) ([]string, error) {
	names, err := client.SMembers(ctx, "sprints").Result()
	if err != nil {
		return nil, fmt.Errorf("friend: sprints: %w", err)
	}
	sort.Strings(names)
	pipe := client.Pipeline()
	cmds := make([]*redis.StringCmd, len(names))
	for i, s := range names {
		cmds[i] = pipe.HGet(ctx, "s:"+s, "status")
	}
	if _, err := pipe.Exec(ctx); err != nil && err != redis.Nil {
		return nil, fmt.Errorf("friend: sprint status: %w", err)
	}
	var out []string
	for i, s := range names {
		if status := cmds[i].Val(); status == "open" || status == "paused" {
			out = append(out, s)
		}
	}
	return out, nil
}

// read takes every friend's counts in one pipelined round trip.
func read(ctx context.Context, st *store.Store) ([]reading, error) {
	client := st.Client()
	friends, err := client.SMembers(ctx, "friends").Result()
	if err != nil {
		return nil, fmt.Errorf("friend: registry: %w", err)
	}
	sort.Strings(friends)
	sprints, err := openSprints(ctx, client)
	if err != nil {
		return nil, err
	}
	type cmds struct {
		beat     *redis.IntCmd
		desired  *redis.SliceCmd
		working  *redis.IntCmd
		wakemode *redis.IntCmd
		idem     *redis.SliceCmd
		open     []*redis.IntCmd
	}
	epoch, err := ws.Epoch(ctx, client)
	if err != nil {
		return nil, err
	}
	pipe := client.Pipeline()
	all := make([]cmds, len(friends))
	for i, f := range friends {
		c := cmds{
			beat:    pipe.Exists(ctx, "friend:"+f+":beat"),
			desired: pipe.HMGet(ctx, "friend:"+f+":desired", "slots", "paused"),
			working: pipe.ZCard(ctx, ws.ConsumerKeyAt(epoch, "friend:"+f, "working")),

			wakemode: pipe.Exists(ctx, WakeModeKey(f)),
			idem:     pipe.HMGet(ctx, StateKey(f), "idem"),
		}
		for _, s := range sprints {
			c.open = append(c.open, pipe.ZCard(ctx, "s:"+s+":open:"+f))
		}
		all[i] = c
	}
	if len(friends) > 0 {
		if _, err := pipe.Exec(ctx); err != nil {
			return nil, fmt.Errorf("friend: read: %w", err)
		}
	}
	out := make([]reading, len(friends))
	for i, f := range friends {
		c := all[i]
		r := reading{friend: f, up: c.beat.Val() == 1}
		vals := c.desired.Val()
		if len(vals) == 2 {
			if s, ok := vals[0].(string); ok {
				r.slots, _ = strconv.Atoi(s)
			}
			r.paused = vals[1] == "1"
		}
		r.leased = int(c.working.Val())
		idemVal := ""
		if v := c.idem.Val(); len(v) == 1 {
			idemVal, _ = v[0].(string)
		}
		r.life = c.wakemode.Val() == 1 || strings.HasPrefix(idemVal, "life:"+f+":")
		for _, o := range c.open {
			r.open += int(o.Val())
		}
		out[i] = r
	}
	return out, nil
}

// ReadWakePath reads f's declared wake path; Kind is "" when none is declared.
func ReadWakePath(ctx context.Context, st *store.Store, f string) (WakePath, error) {
	vals, err := st.Client().HMGet(ctx, WakePathKey(f), "kind", "unit", "host", "notify").Result()
	if err != nil {
		return WakePath{}, fmt.Errorf("friend wake path %s: %w", f, err)
	}
	s := func(v any) string { x, _ := v.(string); return x }
	return WakePath{Kind: s(vals[0]), Unit: s(vals[1]), Host: s(vals[2]), Notify: s(vals[3])}, nil
}

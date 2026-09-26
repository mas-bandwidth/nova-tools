package life

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/taskcard"
	"github.com/redis/go-redis/v9"
)

// A detached friend's beat is evidence of observed copy owners, not of the
// daemon itself. Each working copy binds an immutable process identity to its
// lease token. Only a fresh independent observation of that owner renews it;
// the store rechecks membership, token, identity and freshness atomically.
// Unknown/dead copies remain visible and lapse normally. Friend presence is
// refreshed only if this pass actually renews at least one live owner.

// FriendBeatRequest is one friend beat.
type FriendBeatRequest struct {
	Friend string
	Host   string
	// Load1, NCPU and CPU are the machine's measurements (Load1Now,
	// runtime.NumCPU, CPUBusyNow); an empty one is not written.
	Load1 string
	NCPU  int
	CPU   string
	// At is the beat's clock; zero means now.
	At time.Time
	// Process and ObserverHost are observation seams. Production uses the
	// kernel probe and the actual local hostname, independently of Host's
	// presentation label.
	Process      func(int) ProcessSample
	ObserverHost string
}

// FriendBeatResult is what one beat wrote.
type FriendBeatResult struct {
	Friend string
	AtMS   int64
	// Working is how many copies' leases the beat renewed, LeaseUntil the
	// lease they now hold (ms; 0 with none).
	Working    int
	LeaseUntil int64
	// Models names observed live owners; presence is untouched with none.
	Models string
	// Dead and Unknown name copies that were not renewed. Changed contains
	// only new states, so a loop can report a lapse once instead of each tick.
	Dead, Unknown, Changed []string
	// Skipped names legacy friend-queue tasks, which task beat renews.
	Skipped []string
}

// FriendBeatHarness is the harness field a friend beat writes, naming its
// producer beside a hello loop's harness.
const FriendBeatHarness = "friend beat"

// FriendBeat observes each local owner and renews only current live copies.
// It ignores legacy friend-queue tasks and preserves another producer's
// presence when none of this daemon's owners can be observed alive.
func FriendBeat(ctx context.Context, st *store.Store, req FriendBeatRequest) (FriendBeatResult, error) {
	friend := strings.ToLower(strings.TrimSpace(req.Friend))
	host := strings.TrimSpace(req.Host)
	if st == nil || st.Client() == nil || friend == "" || host == "" || strings.ContainsAny(friend+host, " \t\r\n:") {
		return FriendBeatResult{}, fmt.Errorf("friend beat: store, friend and host are required")
	}
	at := req.At
	if at.IsZero() {
		at = time.Now()
	}
	ms := at.UnixMilli()
	fields := []any{"host", host, "at", strconv.FormatInt(ms, 10), "harness", FriendBeatHarness}
	if v := strings.TrimSpace(req.Load1); v != "" {
		fields = append(fields, "load1", v)
	}
	if req.NCPU > 0 {
		fields = append(fields, "ncpu", strconv.Itoa(req.NCPU))
	}
	if v := strings.TrimSpace(req.CPU); v != "" {
		fields = append(fields, "cpu", v)
	}
	beat := "friend:" + friend + ":beat"
	c := st.Client()
	as := taskcard.Consumer{Kind: "friend", Name: friend}
	ids, err := c.ZRange(ctx, as.Key("working"), 0, -1).Result()
	if err != nil {
		return FriendBeatResult{}, err
	}
	probe := req.Process
	if probe == nil {
		probe = ProbeProcess
	}
	observer := req.ObserverHost
	if observer == "" {
		observer, err = os.Hostname()
		if err != nil {
			return FriendBeatResult{}, err
		}
	}
	rep := FriendBeatResult{Friend: friend, AtMS: ms}
	// Read only renewal metadata, never the potentially large card prompt.
	p := c.Pipeline()
	records := make(map[string]*redis.SliceCmd, len(ids))
	for _, id := range ids {
		if !taskcard.IsCopy(id) {
			rep.Skipped = append(rep.Skipped, id)
			continue
		}
		records[id] = p.HMGet(ctx, taskcard.Key(id), "consumer", "where", "token", "owner_host", "owner_pid", "owner_start", "owner_token", "model")
	}
	// Sample server time before any OS observation, so delayed work fails closed.
	stamp := p.Time(ctx)
	if _, err := p.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return FriendBeatResult{}, err
	}
	checks := []taskcard.OwnerCheck{}
	modelsByID := map[string]string{}
	samples := map[int]ProcessSample{}
	cachedProbe := func(pid int) ProcessSample {
		if v, ok := samples[pid]; ok {
			return v
		}
		v := probe(pid)
		samples[pid] = v
		return v
	}
	for _, id := range ids {
		rec := records[id]
		if rec == nil {
			continue
		}
		values := rec.Val()
		field := func(i int) string {
			if v, ok := values[i].(string); ok {
				return v
			}
			return ""
		}
		if field(0) != as.String() || field(1) != "working" {
			continue
		}
		token := field(2)
		if token == "" {
			rep.Unknown = append(rep.Unknown, id)
			continue
		}
		pid, _ := strconv.Atoi(field(4))
		owner := taskcard.ProcessOwner{Host: field(3), PID: pid, Start: field(5)}
		if field(6) != token {
			owner = taskcard.ProcessOwner{}
		}
		state := OwnerState(owner, observer, cachedProbe)
		checks = append(checks, taskcard.OwnerCheck{ID: id, Token: token,
			Observation: taskcard.OwnerObservation{Owner: owner, State: state, At: stamp.Val()}})
		modelsByID[id] = field(7)
	}
	outcomes, err := taskcard.ObserveOwners(ctx, c, as, checks)
	if err != nil {
		return FriendBeatResult{}, err
	}
	models := []string{}
	seen := map[string]bool{}
	for i, out := range outcomes {
		id := checks[i].ID
		if out.Err != nil {
			if _, refused := taskcard.IsRefused(out.Err); !refused {
				return FriendBeatResult{}, out.Err
			}
			rep.Unknown = append(rep.Unknown, id)
			continue
		}
		got := out.Receipt
		if got.Changed {
			rep.Changed = append(rep.Changed, id+":"+got.State)
		}
		switch got.State {
		case "live":
			rep.Working++
			rep.LeaseUntil = max(rep.LeaseUntil, got.LeaseUntil)
			if m := modelsByID[id]; m != "" && !seen[m] {
				seen[m] = true
				models = append(models, m)
			}
		case "dead":
			rep.Dead = append(rep.Dead, id)
		default:
			rep.Unknown = append(rep.Unknown, id)
		}
	}
	rep.Models = strings.Join(models, ",")
	// A maintenance daemon cannot manufacture friend presence from stale
	// working records. Other presence producers (hello/session) own their own
	// observations; don't delete them when this loop has nothing live.
	if rep.Working > 0 {
		fields = append(fields, "models", rep.Models)
		p := c.Pipeline()
		p.HSet(ctx, beat, fields...)
		p.Persist(ctx, beat)
		if _, err := p.Exec(ctx); err != nil {
			return FriendBeatResult{}, err
		}
	}
	return rep, nil
}

// FnFriendModels writes the beat's models field from the named working
// copies' models (fn/lua/friend_beatloop.lua): each distinct model once, in
// the set's order, comma joined; the field is removed when none names one.
const FnFriendModels = "ns_friend_models"

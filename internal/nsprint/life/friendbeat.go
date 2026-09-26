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
	// Models is the models field the beat wrote ("" when it removed it).
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
	models := []string{}
	seen := map[string]bool{}
	for _, id := range ids {
		if !taskcard.IsCopy(id) {
			rep.Skipped = append(rep.Skipped, id)
			continue
		} // friend-queue tasks use task beat
		p := c.Pipeline()
		rec := p.HGetAll(ctx, taskcard.Key(id))
		stamp := p.Time(ctx)
		if _, err := p.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
			return FriendBeatResult{}, err
		}
		r := rec.Val()
		if r["consumer"] != as.String() || r["where"] != "working" {
			continue
		}
		pid, _ := strconv.Atoi(r["owner_pid"])
		owner := taskcard.ProcessOwner{Host: r["owner_host"], PID: pid, Start: r["owner_start"]}
		if r["owner_token"] != r["token"] {
			owner = taskcard.ProcessOwner{}
		}
		state := OwnerState(owner, observer, probe)
		if r["token"] == "" {
			rep.Unknown = append(rep.Unknown, id)
			continue
		}
		got, err := taskcard.ObserveOwner(ctx, c, as, id, r["token"], taskcard.OwnerObservation{Owner: owner, State: state, At: stamp.Val()})
		if err != nil {
			if _, refused := taskcard.IsRefused(err); refused {
				rep.Unknown = append(rep.Unknown, id)
				continue
			}
			return FriendBeatResult{}, err
		}
		if got.Changed {
			rep.Changed = append(rep.Changed, id+":"+state)
		}
		switch state {
		case "live":
			rep.Working++
			if got.LeaseUntil > rep.LeaseUntil {
				rep.LeaseUntil = got.LeaseUntil
			}
			if m := r["model"]; m != "" && !seen[m] {
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

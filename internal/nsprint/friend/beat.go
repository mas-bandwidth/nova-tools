// Package friend: heartbeat, hand copies, lease extensions, and coordinator reassignments.
// Issue #4356 Item G:
// 1. friend beat --as friend:<name> [--daemon] [--interval <d>]:
//   - Beats every working copy assigned to friend:<name> or owned by that friend so hand copies never lapse.
//   - Supports --daemon (or loop with interval default ~10s) until canceled, and single-shot beat when run once.
//   - Updates friend:<name>:beat timestamp (at, host, harness, models) in Redis and beats each working copy's lease
//     (card:<id>:lease / hset copy:<id> beat <now> / taskcard.ObserveOwners).
//
// 2. Seat auto-beating integration:
//   - When --seat <friend> is active, coordinator working cards by hand never lapse.
//   - Auto-redeal / reassign for lapsed copies whose process is alive: prints one REVIEW line:
//     REASSIGN card=<id> friend=<f> reason=lapsed-child-alive.
package friend

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/life"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/taskcard"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/ws"
	"github.com/redis/go-redis/v9"
)

// BeatDefaultInterval is the default period between beat ticks.
const BeatDefaultInterval = 10 * time.Second

// BeatRequest holds options for friend beat.
type BeatRequest struct {
	Friend       string
	Host         string
	Harness      string
	Actor        string
	Seat         string
	Interval     time.Duration
	Daemon       bool
	ProbeProcess func(int) life.ProcessSample
	Now          func() time.Time
}

// ReassignedCard represents a card that was automatically redealt or reassigned
// because its lease lapsed while its child process was still alive.
type ReassignedCard struct {
	Card   string
	Friend string
	Reason string
}

// Line formats the REVIEW REASSIGN notice.
func (r ReassignedCard) Line() string {
	return fmt.Sprintf("REASSIGN card=%s friend=%s reason=%s", r.Card, r.Friend, r.Reason)
}

// BeatResult represents the outcome of one friend beat pass.
type BeatResult struct {
	Friend     string
	AtMS       int64
	Working    int
	LeaseUntil int64
	Models     string
	Reassigned []ReassignedCard
}

// Line formats the standard CLI output line for friend beat.
func (r *BeatResult) Line() string {
	return fmt.Sprintf("BEAT friend=%s working=%d lease_until=%d at=%d", r.Friend, r.Working, r.LeaseUntil, r.AtMS)
}

// Beat performs one heartbeat pass for friend f.
// It beats every working copy assigned to friend:<name> so hand copies never lapse,
// auto-redeals/reassigns lapsed copies whose process is alive, and updates friend:<name>:beat.
func Beat(ctx context.Context, c redis.Cmdable, req BeatRequest) (*BeatResult, error) {
	if c == nil {
		return nil, errors.New("friend beat: redis client is required")
	}

	f := strings.TrimSpace(req.Friend)
	if f == "" {
		f = strings.TrimSpace(req.Seat)
	}
	if f == "" {
		f = strings.TrimSpace(req.Actor)
	}
	f = strings.ToLower(f)
	f = strings.TrimPrefix(f, "friend:")
	if f == "" {
		return nil, errors.New("friend beat: friend is required")
	}

	host := strings.TrimSpace(req.Host)
	if host == "" {
		var err error
		host, err = os.Hostname()
		if err != nil {
			host = "localhost"
		}
	}
	harness := strings.TrimSpace(req.Harness)
	if harness == "" {
		harness = "friend beat"
	}
	probe := req.ProbeProcess
	if probe == nil {
		probe = life.ProbeProcess
	}
	nowFn := req.Now
	if nowFn == nil {
		nowFn = time.Now
	}
	now := nowFn()
	nowMS := now.UnixMilli()
	actor := strings.TrimSpace(req.Actor)
	if actor == "" {
		actor = f
	}

	as := taskcard.Consumer{Kind: "friend", Name: f}
	epoch, _ := ws.Epoch(ctx, c)
	workingKey := ws.ConsumerKeyAt(epoch, as.String(), "working")

	ids, _ := ws.IDs(ws.CellRange(ctx, c, as.String(), "working"))
	if len(ids) == 0 {
		ids, _ = c.ZRange(ctx, workingKey, 0, -1).Result()
	}

	var reassigned []ReassignedCard
	reassignedSet := make(map[string]bool)

	// A. Check copies currently in working set for lapsed leases with alive process
	for _, id := range ids {
		rec, err := c.HMGet(ctx, taskcard.Key(id), "primary", "lease_until", "owner_host", "owner_pid", "owner_start", "token").Result()
		if err != nil || len(rec) < 5 {
			continue
		}
		primaryID, _ := rec[0].(string)
		if primaryID == "" {
			if taskcard.IsCopy(id) {
				primaryID = taskcard.PrimaryOf(id)
			} else {
				primaryID = id
			}
		}
		leaseUntil, _ := strconv.ParseInt(fmt.Sprint(rec[1]), 10, 64)
		pid, _ := strconv.Atoi(fmt.Sprint(rec[3]))
		start := fmt.Sprint(rec[4])

		if leaseUntil > 0 && leaseUntil <= nowMS {
			if pid > 0 {
				sample := probe(pid)
				if sample.Err == nil && !sample.Absent && (start == "" || sample.Start == "" || sample.Start == start) {
					// Child process is alive! Auto-redeal / reassign
					_, _ = taskcard.ExpireCopies(ctx, c, actor, as)
					rev, revErr := taskcard.Review(ctx, c, primaryID, "reassign:friend:"+f, "lapsed-child-alive", actor)
					if revErr == nil {
						if !reassignedSet[primaryID] {
							reassignedSet[primaryID] = true
							reassigned = append(reassigned, ReassignedCard{
								Card:   primaryID,
								Friend: f,
								Reason: "lapsed-child-alive",
							})
						}
						if rev.Copy != "" {
							whoHarness := strings.ReplaceAll(harness, " ", "-")
							w, wErr := taskcard.WorkAs(ctx, c, as, actor, 1, false, taskcard.Who{Harness: whoHarness}, rev.Copy)
							if wErr == nil && len(w.Tokens) > 0 {
								oHost := fmt.Sprint(rec[2])
								_ = taskcard.BindOwner(ctx, c, as, rev.Copy, w.Tokens[0], taskcard.ProcessOwner{
									Host:  oHost,
									PID:   pid,
									Start: start,
								})
							}
						}
					}
				}
			}
		}
	}

	// B. Check primaries in review that lapsed for this friend
	streams, _ := c.SMembers(ctx, "ws:names").Result()
	for _, s := range streams {
		revKey := ws.KeyAt(epoch, s, "review")
		cardIDs, _ := c.ZRange(ctx, revKey, 0, -1).Result()
		for _, cid := range cardIDs {
			if reassignedSet[cid] {
				continue
			}
			h, err := c.HMGet(ctx, taskcard.Key(cid), "where", "review_shape", "review_consumer", "review_copy").Result()
			if err != nil || len(h) < 4 {
				continue
			}
			where, _ := h[0].(string)
			cons, _ := h[2].(string)
			lastCopy, _ := h[3].(string)
			if where == "review" && (cons == as.String() || cons == f) {
				if lastCopy != "" {
					cpRec, _ := c.HMGet(ctx, taskcard.Key(lastCopy), "owner_pid", "owner_start", "owner_host").Result()
					if len(cpRec) >= 3 {
						pid, _ := strconv.Atoi(fmt.Sprint(cpRec[0]))
						start := fmt.Sprint(cpRec[1])
						oHost := fmt.Sprint(cpRec[2])
						if pid > 0 {
							sample := probe(pid)
							if sample.Err == nil && !sample.Absent && (start == "" || sample.Start == "" || sample.Start == start) {
								rev, revErr := taskcard.Review(ctx, c, cid, "reassign:friend:"+f, "lapsed-child-alive", actor)
								if revErr == nil {
									if !reassignedSet[cid] {
										reassignedSet[cid] = true
										reassigned = append(reassigned, ReassignedCard{
											Card:   cid,
											Friend: f,
											Reason: "lapsed-child-alive",
										})
									}
									if rev.Copy != "" {
										whoHarness := strings.ReplaceAll(harness, " ", "-")
										w, wErr := taskcard.WorkAs(ctx, c, as, actor, 1, false, taskcard.Who{Harness: whoHarness}, rev.Copy)
										if wErr == nil && len(w.Tokens) > 0 {
											_ = taskcard.BindOwner(ctx, c, as, rev.Copy, w.Tokens[0], taskcard.ProcessOwner{
												Host:  oHost,
												PID:   pid,
												Start: start,
											})
										}
									}
								}
							}
						}
					}
				}
			}
		}
	}

	// Refresh working copies after possible reassignments / expirations
	ids, _ = ws.IDs(ws.CellRange(ctx, c, as.String(), "working"))
	if len(ids) == 0 {
		ids, _ = c.ZRange(ctx, workingKey, 0, -1).Result()
	}

	leaseDuration := 180 * time.Second
	newLeaseUntil := nowMS + 180000

	var models []string
	modelSeen := make(map[string]bool)

	// Obtain self sample in case we need to bind hand copies
	selfSample := probe(os.Getpid())
	selfStart := selfSample.Start
	if selfStart == "" || selfStart == "-" {
		selfStart = "coordinator"
	}

	for _, id := range ids {
		rec, _ := c.HMGet(ctx, taskcard.Key(id), "model", "token", "owner_pid", "owner_start", "owner_host", "owner_token").Result()
		if len(rec) >= 6 {
			m, _ := rec[0].(string)
			if m != "" && !modelSeen[m] {
				modelSeen[m] = true
				models = append(models, m)
			}
			token, _ := rec[1].(string)
			pid, _ := strconv.Atoi(fmt.Sprint(rec[2]))
			start := fmt.Sprint(rec[3])
			oHost := fmt.Sprint(rec[4])
			oToken := fmt.Sprint(rec[5])

			// 1. Direct auxiliary Redis updates for copy ledger and card lease
			c.HSet(ctx, "copy:"+id,
				"beat", strconv.FormatInt(nowMS, 10),
				"lease_until", strconv.FormatInt(newLeaseUntil, 10),
			)
			c.Set(ctx, "card:"+id+":lease", strconv.FormatInt(newLeaseUntil, 10), leaseDuration)

			// 2. Extend lease on task:<id> via ns_cm_owner
			if token != "" {
				if pid > 0 && oToken == token {
					// Known owner process
					sample := probe(pid)
					if sample.Err == nil && !sample.Absent && (start == "" || sample.Start == "" || sample.Start == start) {
						receipt, _ := taskcard.ObserveOwner(ctx, c, as, id, token, taskcard.OwnerObservation{
							Owner: taskcard.ProcessOwner{Host: oHost, PID: pid, Start: start},
							State: "live",
							At:    now,
						})
						if receipt.LeaseUntil > 0 {
							newLeaseUntil = receipt.LeaseUntil
						}
					}
				} else {
					// Hand copy or unbound copy: bind current seat process so it can be renewed via ns_cm_owner
					owner := taskcard.ProcessOwner{Host: host, PID: os.Getpid(), Start: selfStart}
					if bindErr := taskcard.BindOwner(ctx, c, as, id, token, owner); bindErr == nil {
						receipt, _ := taskcard.ObserveOwner(ctx, c, as, id, token, taskcard.OwnerObservation{
							Owner: owner,
							State: "live",
							At:    now,
						})
						if receipt.LeaseUntil > 0 {
							newLeaseUntil = receipt.LeaseUntil
						}
					}
				}
			}
		}
	}

	modelsStr := strings.Join(models, ",")
	beatKey := "friend:" + f + ":beat"
	fields := []any{
		"host", host,
		"at", strconv.FormatInt(nowMS, 10),
		"harness", harness,
		"models", modelsStr,
	}
	c.HSet(ctx, beatKey, fields...)
	c.Persist(ctx, beatKey)

	res := &BeatResult{
		Friend:     f,
		AtMS:       nowMS,
		Working:    len(ids),
		LeaseUntil: 0,
		Models:     modelsStr,
		Reassigned: reassigned,
	}
	if len(ids) > 0 {
		res.LeaseUntil = newLeaseUntil
	}
	return res, nil
}

// BeatLoop runs the friend beat repeatedly at req.Interval (default 10s) until ctx is canceled.
func BeatLoop(ctx context.Context, c redis.Cmdable, req BeatRequest, onBeat func(*BeatResult)) error {
	interval := req.Interval
	if interval <= 0 {
		interval = BeatDefaultInterval
	}
	res, err := Beat(ctx, c, req)
	if err != nil {
		return err
	}
	if onBeat != nil {
		onBeat(res)
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			res, err := Beat(ctx, c, req)
			if err != nil {
				continue
			}
			if onBeat != nil {
				onBeat(res)
			}
		}
	}
}

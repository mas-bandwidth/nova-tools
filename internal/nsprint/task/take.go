package task

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/redis/go-redis/v9"
)

// TakeAvailable claims assigned work in sprint order, with front items first
// within each sprint. A zero limit means all currently free friend slots.
// Every individual claim still happens in the guarded Redis Function.
func TakeAvailable(ctx context.Context, st *store.Store, as, sprint, id string, limit int, actor, idem string) ([]Claim, error) {
	if st == nil || as == "" || limit < 0 {
		return nil, fmt.Errorf("task take: store, as and nonnegative n are required")
	}
	client := st.Client()
	desired, err := client.HGet(ctx, "friend:"+as+":desired", "slots").Int()
	if err != nil {
		return nil, fmt.Errorf("task take: friend %s has no desired slots: %w", as, err)
	}
	starting, err := client.ZCard(ctx, "friend:"+as+":starting").Result()
	if err != nil {
		return nil, err
	}
	living, err := client.ZCard(ctx, "friend:"+as+":living").Result()
	if err != nil {
		return nil, err
	}
	free := desired - int(starting+living)
	if free <= 0 {
		return []Claim{}, nil
	}
	if limit == 0 || limit > free {
		limit = free
	}
	sprints := []string{sprint}
	if sprint == "" {
		sprints, err = client.ZRange(ctx, "sprint:order", 0, -1).Result()
		if err != nil {
			return nil, fmt.Errorf("task take: sprint order: %w", err)
		}
	}
	claims := make([]Claim, 0, limit)
	for _, name := range sprints {
		if name == "" {
			continue
		}
		ids := []string{id}
		if id == "" {
			ids, err = client.ZRange(ctx, "s:"+name+":open:"+as, 0, -1).Result()
			if err != nil {
				return nil, fmt.Errorf("task take: queue %s/%s: %w", name, as, err)
			}
		}
		for _, taskID := range ids {
			claim, ok, err := Take(ctx, st, TakeRequest{Sprint: name, ID: taskID, As: as, Actor: actor, Idem: idem})
			if err != nil {
				return claims, err
			}
			if ok {
				claims = append(claims, claim)
				if len(claims) == limit {
					return claims, nil
				}
			}
		}
	}
	return claims, nil
}

// TakeRequest is one task take (spec 4.2). As is the consumer/friend name.
type TakeRequest struct {
	Sprint string
	ID     string
	As     string
	Actor  string
	Idem   string
}

// Claim is one successful claim. Token is the attempt's fence: every later
// call for that attempt must present it or refuse with exit 3 FENCED
// (spec 2.1 rule 8).
type Claim struct {
	Sprint  string
	ID      string
	Attempt int
	Token   string
}

// RandomToken returns the 128 random bits of a fence token as 32 lowercase
// hex characters. Redis Lua has no secure random, so Go supplies this half
// and the function composes <attempt>.<random> atomically (spec 2.1 rule 8).
func RandomToken() (string, error) {
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", fmt.Errorf("task token: random: %w", err)
	}
	return hex.EncodeToString(buf[:]), nil
}

// Take claims one task for a friend. The guard, the attempt increment, the
// token, the index move and the one receipt are a single atomic Redis
// Function call, so two concurrent takes of one task yield exactly one owner
// and one receipt (#2756 control 4). ok is false when the task is not open or
// not in this friend's queue.
func Take(ctx context.Context, st *store.Store, req TakeRequest) (Claim, bool, error) {
	if st == nil {
		return Claim{}, false, fmt.Errorf("task take: nil store")
	}
	if req.Sprint == "" || req.ID == "" || req.As == "" {
		return Claim{}, false, fmt.Errorf("task take: sprint, id and as are required")
	}
	var values []any
	var status string
	for tries := 0; tries < 3; tries++ {
		previous, err := st.Client().HGet(ctx, "s:"+req.Sprint+":task:"+req.ID, "attempt").Int()
		if err == redis.Nil {
			previous = 0
		} else if err != nil {
			return Claim{}, false, fmt.Errorf("task take %s: read attempt: %w", req.ID, err)
		}
		attempt := previous + 1
		random, err := RandomToken()
		if err != nil {
			return Claim{}, false, err
		}
		token := fmt.Sprintf("%d.%s", attempt, random)
		sum := sha256.Sum256([]byte(token))
		reply, err := st.Client().FCall(ctx, FunctionTake, nil,
			req.Sprint, req.ID, req.As, attempt, token, hex.EncodeToString(sum[:])[:12], req.Actor, req.Idem).Result()
		if err != nil {
			return Claim{}, false, fmt.Errorf("task take %s: %w", req.ID, err)
		}
		var ok bool
		values, ok = reply.([]any)
		if !ok || len(values) == 0 {
			return Claim{}, false, fmt.Errorf("task take %s: unexpected function reply %T", req.ID, reply)
		}
		status, _ = values[0].(string)
		if status != "RETRY" {
			break
		}
	}
	switch status {
	case "NONE", "NOTFOUND":
		return Claim{}, false, nil
	case "DOWN", "FULL":
		return Claim{}, false, fmt.Errorf("task take %s: friend %s is %s", req.ID, req.As, status)
	case TakeClaimed:
	case "RETRY":
		return Claim{}, false, fmt.Errorf("task take %s: attempt moved during three claims", req.ID)
	default:
		return Claim{}, false, fmt.Errorf("task take %s: unexpected status %q", req.ID, status)
	}
	if len(values) < 5 {
		return Claim{}, false, fmt.Errorf("task take %s: short claim reply", req.ID)
	}
	attempt, err := strconv.Atoi(fmt.Sprint(values[3]))
	if err != nil {
		return Claim{}, false, fmt.Errorf("task take %s: attempt %v: %w", req.ID, values[3], err)
	}
	return Claim{
		Sprint:  fmt.Sprint(values[1]),
		ID:      fmt.Sprint(values[2]),
		Attempt: attempt,
		Token:   fmt.Sprint(values[4]),
	}, true, nil
}

// TakeStatus words returned by ns_task_take.
const (
	TakeClaimed = "CLAIMED"
)

// DoneRequest is one task done (spec 4.2). A review task's Evidence must name
// a verdict, a score and a head equal to the task head.
type DoneRequest struct {
	Sprint   string
	ID       string
	Token    string
	Evidence string
	Verdict  string
	Score    string
	Head     string
	Actor    string
	Idem     string
	// Cost, when set, is appended to Evidence as its cost clause (#3105).
	// A read done without one is unmetered in the fold, never $0.
	Cost *Cost
}

// DoneStatus is the outcome of one done.
type DoneStatus string

const (
	// DoneClosed is the transition claimed/working -> closed.
	DoneClosed DoneStatus = "DONE"
	// DoneRepeat is a repeated identical done of a closed task (exit 0).
	DoneRepeat DoneStatus = "CLOSED"
	// DoneFenced is a missing or mismatched token (exit 3).
	DoneFenced DoneStatus = "FENCED"
	// DoneConflict is different evidence on a terminal task (exit 4).
	DoneConflict DoneStatus = "CONFLICT"
	// DoneNoEvidence refuses a done with no evidence.
	DoneNoEvidence DoneStatus = "NOEVIDENCE"
	// DoneInvalid refuses a review done without verdict, score or head.
	DoneInvalid DoneStatus = "INVALID"
)

// ExitCode maps a done outcome to its CLI exit code (spec 4.2, 2.1 rule 8).
func (s DoneStatus) ExitCode() int {
	switch s {
	case DoneFenced:
		return 3
	case DoneConflict:
		return 4
	case DoneNoEvidence, DoneInvalid:
		return 2
	default:
		return 0
	}
}

// Done closes a claimed or working task. The token check, the evidence check,
// the disposition write, the index move and the one receipt are atomic.
func Done(ctx context.Context, st *store.Store, req DoneRequest) (DoneStatus, error) {
	if st == nil {
		return "", fmt.Errorf("task done: nil store")
	}
	if req.Sprint == "" || req.ID == "" {
		return "", fmt.Errorf("task done: sprint and id are required")
	}
	if _, err := ParseEvidence(req.Evidence); err != nil {
		return DoneInvalid, nil
	}
	if req.Cost != nil {
		req.Evidence = WithCost(req.Evidence, *req.Cost)
	}
	if _, _, err := ParseCost(req.Evidence); err != nil {
		return DoneInvalid, fmt.Errorf("task done %s: evidence cost: %w", req.ID, err)
	}
	reply, err := st.Client().FCall(ctx, FunctionDone, nil,
		req.Sprint, req.ID, req.Token, req.Evidence, req.Verdict, req.Score,
		req.Head, req.Actor, req.Idem).Result()
	if err != nil {
		return "", fmt.Errorf("task done %s: %w", req.ID, err)
	}
	status, err := firstString(reply)
	if err != nil {
		return "", fmt.Errorf("task done %s: %w", req.ID, err)
	}
	switch DoneStatus(status) {
	case DoneClosed, DoneRepeat:
		_ = ProcessDoneEvidenceAndFollowUps(ctx, st, req.Sprint, req.ID, req.Evidence, DoneStatus(status))
		return DoneStatus(status), nil
	case DoneFenced, DoneConflict, DoneNoEvidence, DoneInvalid:
		return DoneStatus(status), nil
	default:
		return "", fmt.Errorf("task done %s: unexpected status %q", req.ID, status)
	}
}

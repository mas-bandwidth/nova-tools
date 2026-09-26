package task

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/deal"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/ws"
	"github.com/redis/go-redis/v9"
)

// TakeAvailable claims assigned work in sprint order, with front items first
// within each sprint. A zero limit means all currently free friend slots.
// Every individual claim still happens in the guarded Redis Function. actor is
// recorded on each take receipt as given (the CLI passes the initiator); as is
// the receipt's `for`.
//
// It costs two round trips however many sprints and claims (#3261; it was
// 4 + S + 2k): one pipeline reads the `friends` membership of as, its desired
// slots, its working set (#3998) and ns_task_take_view (every open sprint's queue
// for as and the task fields its rank needs); Go ranks in memory
// (deal.RankSnapshot); one ns_task_take_n call claims down the ranked list
// until the free slots fill. A non-member returns ErrNotFriend before any
// claim (#2929 rev 6). Only a candidate whose attempt moved between the two
// round trips (RETRY, a race) costs a further single take.
func TakeAvailable(ctx context.Context, st *store.Store, as, sprint, id string, limit int, actor, idem string) ([]Claim, error) {
	if st == nil || as == "" || limit < 0 {
		return nil, fmt.Errorf("task take: store, as and nonnegative n are required")
	}
	client := st.Client()
	pipe := client.Pipeline()
	member := pipe.SIsMember(ctx, "friends", as)
	desiredCmd := pipe.HGet(ctx, "friend:"+as+":desired", "slots")
	// the working set under the current epoch, counted in this one
	// pipeline (nova-tools#4238)
	workingCmd := ws.CellCard(ctx, pipe, "friend:"+as, "working")

	viewCmd := pipe.FCallRO(ctx, FunctionTakeView, nil, as, sprint, id)
	if _, err := pipe.Exec(ctx); err != nil && err != redis.Nil {
		return nil, fmt.Errorf("task take: read friend %s: %w", as, err)
	}
	if !member.Val() {
		return nil, ErrNotFriend
	}
	desired, err := desiredCmd.Int()
	if err != nil {
		return nil, fmt.Errorf("task take: friend %s has no desired slots: %w", as, err)
	}
	free := desired - int(ws.CardVal(workingCmd))

	if free <= 0 {
		return []Claim{}, nil
	}
	if limit == 0 || limit > free {
		limit = free
	}
	candidates, err := takeCandidates(viewCmd.Val(), as, id)
	if err != nil {
		return nil, err
	}
	if len(candidates) == 0 {
		return []Claim{}, nil
	}
	strict := "0"
	if id != "" {
		strict = "1"
	}
	args := []any{as, strconv.Itoa(limit), strict, actor, idem}
	for _, c := range candidates {
		random, err := RandomToken()
		if err != nil {
			return nil, err
		}
		token := fmt.Sprintf("%d.%s", c.attempt, random)
		sum := sha256.Sum256([]byte(token))
		args = append(args, c.sprint, c.id, c.attempt, token, hex.EncodeToString(sum[:])[:12])
	}
	reply, err := client.FCall(ctx, FunctionTakeN, nil, args...).Result()
	if err != nil {
		return nil, fmt.Errorf("task take: %w", err)
	}
	entries, ok := reply.([]any)
	if !ok {
		return nil, fmt.Errorf("task take: unexpected batch reply %T", reply)
	}
	claims := make([]Claim, 0, limit)
	var retry []TakeRequest
	for _, entry := range entries {
		values, ok := entry.([]any)
		if !ok || len(values) == 0 {
			return claims, fmt.Errorf("task take: unexpected batch entry %T", entry)
		}
		status := fmt.Sprint(values[0])
		if status == TakeClaimed {
			claim, err := parseClaim(values)
			if err != nil {
				return claims, err
			}
			claims = append(claims, claim)
			continue
		}
		if len(values) < 4 {
			return claims, fmt.Errorf("task take: short batch entry %v", values)
		}
		name, taskID, detail := fmt.Sprint(values[1]), fmt.Sprint(values[2]), fmt.Sprint(values[3])
		switch status {
		case TakeBlocked:
			if id != "" {
				return claims, &BlockedError{Sprint: name, ID: taskID, Needs: strings.Fields(detail)}
			}
			// Without --id a task with unmet needs is passed over.
		case "RETRY":
			retry = append(retry, TakeRequest{Sprint: name, ID: taskID, As: as, Actor: actor, Idem: idem})
		case "DOWN", "FULL":
			return claims, fmt.Errorf("task take %s: friend %s is %s", taskID, as, status)
		default:
			return claims, fmt.Errorf("task take %s: unexpected status %q", taskID, status)
		}
	}
	for _, req := range retry {
		if len(claims) == limit {
			break
		}
		claim, ok, err := Take(ctx, st, req)
		var blocked *BlockedError
		if id == "" && errors.As(err, &blocked) {
			continue
		}
		if err != nil {
			return claims, err
		}
		if ok {
			claims = append(claims, claim)
		}
	}
	return claims, nil
}

// takeCandidate is one ranked task with the attempt its fence token names.
type takeCandidate struct {
	sprint, id string
	attempt    int
}

// takeCandidates turns the ns_task_take_view reply into the take order: the
// sprints as the view listed them (sprint:order), and within each sprint the
// rank order of deal.RankSnapshot, or the one id when id is set.
func takeCandidates(view any, as, id string) ([]takeCandidate, error) {
	sprints, ok := view.([]any)
	if !ok {
		return nil, fmt.Errorf("task take: unexpected view reply %T", view)
	}
	var out []takeCandidate
	for _, raw := range sprints {
		parts, ok := raw.([]any)
		if !ok || len(parts) != 3 {
			return nil, fmt.Errorf("task take: unexpected view sprint %v", raw)
		}
		name := fmt.Sprint(parts[0])
		open, _ := parts[1].([]any)
		flat, _ := parts[2].([]any)
		const width = 8 // id + title est priority pushed_at owner state attempt
		if len(open)%2 != 0 || len(flat)%width != 0 {
			return nil, fmt.Errorf("task take: view of %s has %d open and %d field values", name, len(open), len(flat))
		}
		fields := make(map[string][]any, len(flat)/width)
		attempts := make(map[string]int, len(flat)/width)
		for i := 0; i < len(flat); i += width {
			taskID := fmt.Sprint(flat[i])
			fields[taskID] = flat[i+1 : i+7]
			attempt, _ := strconv.Atoi(fmt.Sprint(flat[i+7]))
			attempts[taskID] = attempt
		}
		if id != "" {
			out = append(out, takeCandidate{sprint: name, id: id, attempt: attempts[id] + 1})
			continue
		}
		scores := make(map[string]float64, len(open)/2)
		owners := make(map[string]string, len(open)/2)
		for i := 0; i < len(open); i += 2 {
			score, err := strconv.ParseFloat(fmt.Sprint(open[i+1]), 64)
			if err != nil {
				return nil, fmt.Errorf("task take: %s open score %v: %w", name, open[i+1], err)
			}
			taskID := fmt.Sprint(open[i])
			scores[taskID], owners[taskID] = score, as
		}
		ranks := deal.RankSnapshot(deal.RankInput{Sprint: name, As: as, OpenScores: scores, OpenOwners: owners, Fields: fields}, io.Discard)
		for _, r := range ranks {
			out = append(out, takeCandidate{sprint: name, id: r.ID, attempt: attempts[r.ID] + 1})
		}
	}
	return out, nil
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
	// Kind, Ref and Title come from the claim reply itself, so the CLI's
	// TASK line costs no extra round trip (#2929).
	Kind  string
	Ref   string
	Title string
}

// DenyTake records a take the CLI refused because --as is not the initiator
// (#2929 rev 6): one pipeline reads the initiator's `friends` membership (and
// the default sprint when sprint is empty), then ns_task_take_denied writes
// exactly one receipt `kind=task take denied actor=<initiator> for=<as>
// reason=as-not-initiator`. The task is untouched. A non-member initiator
// returns ErrNotFriend with no receipt. It returns the sprint it wrote to.
func DenyTake(ctx context.Context, st *store.Store, initiator, as, sprint, id, idem string) (string, error) {
	if st == nil || initiator == "" || as == "" {
		return "", fmt.Errorf("task take denied: store, initiator and as are required")
	}
	sprint, err := seatRead(ctx, st.Client(), initiator, sprint)
	if err != nil {
		return "", err
	}
	if _, err := st.Client().FCall(ctx, FunctionTakeDenied, nil, sprint, id, initiator, as, idem).Result(); err != nil {
		return "", fmt.Errorf("task take denied %s: %w", id, err)
	}
	return sprint, nil
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
		previous, err := st.Client().HGet(ctx, "task:"+req.ID, "attempt").Int()
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
	case TakeBlocked:
		needs := ""
		if len(values) > 1 {
			needs = fmt.Sprint(values[1])
		}
		return Claim{}, false, &BlockedError{Sprint: req.Sprint, ID: req.ID, Needs: strings.Fields(needs)}
	case "DOWN", "FULL":
		return Claim{}, false, fmt.Errorf("task take %s: friend %s is %s", req.ID, req.As, status)
	case TakeClaimed:
	case "RETRY":
		return Claim{}, false, fmt.Errorf("task take %s: attempt moved during three claims", req.ID)
	default:
		return Claim{}, false, fmt.Errorf("task take %s: unexpected status %q", req.ID, status)
	}
	claim, err := parseClaim(values)
	if err != nil {
		return Claim{}, false, err
	}
	return claim, true, nil
}

// parseClaim reads one CLAIMED reply of ns_task_take: status, sprint, id,
// attempt, token, then kind, ref and title.
func parseClaim(values []any) (Claim, error) {
	if len(values) < 5 {
		return Claim{}, fmt.Errorf("task take: short claim reply %v", values)
	}
	attempt, err := strconv.Atoi(fmt.Sprint(values[3]))
	if err != nil {
		return Claim{}, fmt.Errorf("task take %v: attempt %v: %w", values[2], values[3], err)
	}
	claim := Claim{
		Sprint:  fmt.Sprint(values[1]),
		ID:      fmt.Sprint(values[2]),
		Attempt: attempt,
		Token:   fmt.Sprint(values[4]),
	}
	if len(values) >= 8 {
		claim.Kind, claim.Ref, claim.Title = fmt.Sprint(values[5]), fmt.Sprint(values[6]), fmt.Sprint(values[7])
	}
	return claim, nil
}

// TakeStatus words returned by ns_task_take.
const (
	TakeClaimed = "CLAIMED"
	// TakeBlocked is a task whose needs are not all closed (#2939).
	TakeBlocked = "BLOCKED"
)

// BlockedError is a take refused because the task needs ids that are not
// closed yet (#2939). `task take --id` prints it as BLOCKED needs <ids> and
// exits 7; a take without --id passes over the task.
type BlockedError struct {
	Sprint string
	ID     string
	Needs  []string
}

func (e *BlockedError) Error() string {
	return fmt.Sprintf("task take %s/%s: blocked, needs %s", e.Sprint, e.ID, strings.Join(e.Needs, " "))
}

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
	// Typed, when set, is the review's typed DISPOSITION line as the shared
	// parser (internal/nsprint/disposition) read it from the posted comment
	// (task done --body-file, nova-tools #3092 rev 7). Its head, verdict and
	// score are the close's; ns_task_done records it through
	// ns_ingest_disposition's body in the same atomic call as the close.
	Typed *TypedLine
	// As closes without a token when As owns the claimed or working lease
	// (#3206 PR A, the friend-queue `done --as` shape).
	As string
}

// TypedLine is one parsed typed line carried by a DoneRequest. The parser
// lives in internal/nsprint/disposition, which imports this package, so the
// caller converts its Line into this shape.
type TypedLine struct {
	Type        string // DISPOSITION (a REPAIR refuses a review close)
	Who         string
	Head        string // 40 hex
	Verdict     string // APPROVE | HOLD
	Score       string // 1-10
	Kind        string // explicit kind=, or empty
	KindDerived string // the classifier's kind for a HOLD with no kind=
	Scope       string
	Reason      string
	URL         string // the comment as posted
	CommentID   string
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
	// DoneRefused refuses a typed close: the line is not a DISPOSITION, its
	// who is not the task owner, or ns_ingest_disposition refused it (the
	// reason is DoneOutcome.Why). Nothing is written (exit 2).
	DoneRefused DoneStatus = "REFUSED"
)

// DoneOutcome is one done with its typed record: Why on REFUSED, Record the
// ingest reply after RECORD on a typed close.
type DoneOutcome struct {
	Status DoneStatus
	Why    string
	Record []string
}

// ExitCode maps a done outcome to its CLI exit code (spec 4.2, 2.1 rule 8).
func (s DoneStatus) ExitCode() int {
	switch s {
	case DoneFenced:
		return 3
	case DoneConflict:
		return 4
	case DoneNoEvidence, DoneInvalid, DoneRefused:
		return 2
	default:
		return 0
	}
}

// Done closes a claimed or working task. The token check, the evidence check,
// the disposition write, the index move and the one receipt are atomic.
func Done(ctx context.Context, st *store.Store, req DoneRequest) (DoneStatus, error) {
	out, err := DoneTyped(ctx, st, req)
	return out.Status, err
}

// DoneTyped is Done with the typed record: a request with Typed set closes a
// review only when its line is recorded in the same call.
func DoneTyped(ctx context.Context, st *store.Store, req DoneRequest) (DoneOutcome, error) {
	if st == nil {
		return DoneOutcome{}, fmt.Errorf("task done: nil store")
	}
	if req.Sprint == "" || req.ID == "" {
		return DoneOutcome{}, fmt.Errorf("task done: sprint and id are required")
	}
	if req.Cost != nil {
		req.Evidence = WithCost(req.Evidence, *req.Cost)
	}
	if _, _, err := ParseCost(req.Evidence); err != nil {
		return DoneOutcome{}, fmt.Errorf("task done %s: evidence cost: %w", req.ID, err)
	}
	var ty TypedLine
	if req.Typed != nil {
		if req.Verdict != "" || req.Score != "" || req.Head != "" {
			return DoneOutcome{}, fmt.Errorf("task done %s: a typed line carries verdict, score and head; the flags are not also given", req.ID)
		}
		ty = *req.Typed
		if ty.Type == "" {
			return DoneOutcome{}, fmt.Errorf("task done %s: typed line without a type", req.ID)
		}
		req.Verdict, req.Score, req.Head = ty.Verdict, ty.Score, ty.Head
	}
	reply, err := st.Client().FCall(ctx, FunctionDone, nil,
		req.Sprint, req.ID, req.Token, req.Evidence, req.Verdict, req.Score,
		req.Head, req.Actor, req.Idem, req.As,
		ty.Type, ty.Who, ty.URL, ty.CommentID, ty.Kind, ty.KindDerived, ty.Scope, ty.Reason).Slice()
	if err != nil {
		return DoneOutcome{}, fmt.Errorf("task done %s: %w", req.ID, err)
	}
	if len(reply) == 0 {
		return DoneOutcome{}, fmt.Errorf("task done %s: empty reply", req.ID)
	}
	words := make([]string, len(reply))
	for i, v := range reply {
		words[i] = fmt.Sprint(v)
	}
	switch st := DoneStatus(words[0]); st {
	case DoneClosed:
		out := DoneOutcome{Status: st}
		if len(words) > 2 && words[1] == "RECORD" {
			out.Record = words[2:]
		}
		return out, nil
	case DoneRefused:
		return DoneOutcome{Status: st, Why: strings.Join(words[1:], " ")}, nil
	case DoneRepeat, DoneFenced, DoneConflict, DoneNoEvidence, DoneInvalid:
		return DoneOutcome{Status: st}, nil
	default:
		return DoneOutcome{}, fmt.Errorf("task done %s: unexpected status %q", req.ID, words[0])
	}
}

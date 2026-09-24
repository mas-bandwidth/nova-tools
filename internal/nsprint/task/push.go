// Package task implements the nova-sprint task transitions that are one Redis
// Function call each: push is create-only (#2756 3.1, control 3) and take
// claims with an attempt and a fenced token (#2756 4.2, control 4). Neither
// verb writes state with separate HSET and XADD calls (spec 2.1 rule 2).
package task

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
)

// Function names registered by internal/nsprint/fn/lua/task_claim.lua. The
// library is `nova_sprint` (fn.Library); the loader prepends its header.
const (
	FunctionPush = "ns_task_push"
	FunctionTake = "ns_task_take"
	FunctionDone = "ns_task_done"
)

var (
	sprintNameRx = regexp.MustCompile(`^[a-z0-9-]{1,40}$`)
	estRx        = regexp.MustCompile(`^[1-9][0-9]{0,4}$`)
	titleEstRx   = regexp.MustCompile(`(?i)\best:\s*(\S+)\s*mins?\b`)
)

// IsValidEst reports whether s is a valid task estimate: a whole number of
// minutes 1..10080 (at most one week), matching ^[1-9][0-9]{0,4}$ as text.
func IsValidEst(s string) bool {
	if !estRx.MatchString(s) {
		return false
	}
	n, err := strconv.Atoi(s)
	return err == nil && n >= 1 && n <= 10080
}

// ParseTitleEst extracts an estimate from a task title in the form `est: <v> min`.
// If found and in-domain, it returns the estimate string.
// If found but outside the domain 1..10080, it writes a warning to errOut and returns "".
// If not found, it returns "".
func ParseTitleEst(title string, errOut io.Writer) string {
	matches := titleEstRx.FindStringSubmatch(title)
	if len(matches) < 2 {
		return ""
	}
	v := matches[1]
	if IsValidEst(v) {
		return v
	}
	if errOut != nil {
		fmt.Fprintf(errOut, "WARN est: title value %s outside 1..10080, stored empty\n", v)
	}
	return ""
}

// Kind is a task kind (spec 4.2: read, review, harvest, fix, work).
type Kind string

const (
	KindRead    Kind = "read"
	KindReview  Kind = "review"
	KindHarvest Kind = "harvest"
	KindFix     Kind = "fix"
	KindWork    Kind = "work"
)

// Effects is a task's effect class (spec 4.2: none, idempotent, external).
type Effects string

const (
	EffectsNone       Effects = "none"
	EffectsIdempotent Effects = "idempotent"
	EffectsExternal   Effects = "external"
)

// PushRequest is one create-only task push (spec 4.2). PayloadSHA is the
// create-only identity of the payload; when empty it is derived from the
// request, so an identical re-push is recognized as the same task.
type PushRequest struct {
	Sprint     string
	ID         string
	Kind       Kind
	Title      string
	Effects    Effects
	Repo       string
	PR         int
	Head       string
	Ref        string
	To         string
	Front      bool
	Priority   int
	PayloadSHA string
	Actor      string
	Idem       string
	Est        string
	ErrOut     io.Writer
}

// PushStatus is the outcome of one push.
type PushStatus string

const (
	// PushCreated wrote the task hash, its queue place and one receipt.
	PushCreated PushStatus = "CREATED"
	// PushExists is the identical payload of a live task; nothing was written.
	PushExists PushStatus = "EXISTS"
	// PushClosed is the identical payload of a terminal task; nothing was
	// written and the task never reopens (spec 3.1 row 1, rowan-tools #145).
	PushClosed PushStatus = "CLOSED"
	// PushConflict is a different payload for an existing id (exit 4).
	PushConflict PushStatus = "CONFLICT"
	// PushInvalid is a review task without a repo, PR and head.
	PushInvalid PushStatus = "INVALID"
	// PushOverlap is a build task whose PATHS intersect a live build task's
	// PATHS outside a DEPENDS-ON chain; nothing was written (#3067, exit 5).
	PushOverlap PushStatus = "OVERLAP"
)

// PushResult is a push outcome with the colliding task when it is OVERLAP.
type PushResult struct {
	Status  PushStatus
	Overlap *Overlap
}

// ExitCode maps a push outcome to its CLI exit code (spec 4.2).
func (s PushStatus) ExitCode() int {
	if s == PushConflict {
		return 4
	}
	if s == PushInvalid {
		return 2
	}
	if s == PushOverlap {
		return 5
	}
	return 0
}

// PayloadSHA is the create-only identity of a push payload. The writer column
// is the same for every caller, so a re-push with the same fields compares
// equal and a changed field does not (spec 2.1 rule 4, rule 7).
func PayloadSHA(req PushRequest) string {
	parts := []string{
		string(req.Kind), req.Repo, req.Ref, strconv.Itoa(req.PR),
		req.Head, req.Title, string(req.Effects), req.To,
		strconv.FormatBool(req.Front), strconv.Itoa(req.Priority),
		req.Est,
	}
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x1f")))
	return hex.EncodeToString(sum[:])
}

// ReviewID is the fixed identity of a review task, read-<repo>-<pr>-<head12>-
// <friend> (spec 2.1 rule 7). A new head is a new identity, never a reopened
// task.
func ReviewID(repo string, pr int, head, friend string) string {
	short := head
	if len(short) > 12 {
		short = short[:12]
	}
	return fmt.Sprintf("read-%s-%d-%s-%s", repo, pr, short, friend)
}

// Push issues one create-only push. The guard, the task hash, the queue move
// and the single receipt are one atomic Redis Function call.
func Push(ctx context.Context, st *store.Store, req PushRequest) (PushStatus, error) {
	res, err := PushChecked(ctx, st, req)
	return res.Status, err
}

// PushChecked is Push with the build-task path lint (#3067) reported: a build
// task whose PATHS intersect a live build task's PATHS outside a DEPENDS-ON
// chain returns OVERLAP naming the other task, and nothing is written.
func PushChecked(ctx context.Context, st *store.Store, req PushRequest) (PushResult, error) {
	if st == nil {
		return PushResult{}, fmt.Errorf("task push: nil store")
	}
	if req.Sprint == "" || req.ID == "" {
		return PushResult{}, fmt.Errorf("task push: sprint and id are required")
	}
	if !sprintNameRx.MatchString(req.Sprint) {
		return PushResult{}, fmt.Errorf("task push: sprint must match [a-z0-9-]{1,40}")
	}
	if req.Kind == "" {
		req.Kind = KindWork
	}
	switch req.Kind {
	case KindRead, KindReview, KindHarvest, KindFix, KindWork:
	default:
		return PushResult{}, fmt.Errorf("task push: invalid kind %q", req.Kind)
	}
	if req.Effects == "" {
		req.Effects = EffectsNone
	}
	switch req.Effects {
	case EffectsNone, EffectsIdempotent, EffectsExternal:
	default:
		return PushResult{}, fmt.Errorf("task push: invalid effects %q", req.Effects)
	}
	errOut := req.ErrOut
	if errOut == nil {
		errOut = os.Stderr
	}
	if req.Est != "" {
		if !IsValidEst(req.Est) {
			fmt.Fprintf(errOut, "INVALID est=%s: whole minutes 1..10080\n", req.Est)
			return PushResult{Status: PushInvalid}, nil
		}
	} else {
		req.Est = ParseTitleEst(req.Title, errOut)
	}
	if req.Front && req.Priority == 0 {
		req.Priority = 1
	}
	if req.PayloadSHA == "" {
		req.PayloadSHA = PayloadSHA(req)
	}
	overlap, err := lintPaths(ctx, st, req)
	if err != nil {
		return PushResult{}, err
	}
	if overlap != nil {
		return PushResult{Status: PushOverlap, Overlap: overlap}, nil
	}
	front := "0"
	if req.Front {
		front = "1"
	}
	reply, err := st.Client().FCall(ctx, FunctionPush, nil,
		req.Sprint, req.ID, string(req.Kind), req.Title, string(req.Effects),
		req.Repo, strconv.Itoa(req.PR), req.Head, req.Ref, req.To, front,
		strconv.Itoa(req.Priority), req.PayloadSHA, req.Actor, req.Idem, req.Est).Result()
	if err != nil {
		return PushResult{}, fmt.Errorf("task push %s: %w", req.ID, err)
	}
	status, err := firstString(reply)
	if err != nil {
		return PushResult{}, fmt.Errorf("task push %s: %w", req.ID, err)
	}
	switch PushStatus(status) {
	case PushCreated, PushExists, PushClosed, PushConflict, PushInvalid:
		return PushResult{Status: PushStatus(status)}, nil
	default:
		return PushResult{}, fmt.Errorf("task push %s: unexpected status %q", req.ID, status)
	}
}

// firstString reads the status word the function returns first in its reply.
func firstString(reply any) (string, error) {
	values, ok := reply.([]any)
	if !ok || len(values) == 0 {
		return "", fmt.Errorf("unexpected function reply %T", reply)
	}
	status, ok := values[0].(string)
	if !ok {
		return "", fmt.Errorf("unexpected function status %T", values[0])
	}
	return status, nil
}

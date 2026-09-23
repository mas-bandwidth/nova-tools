// Package consume places ok-to-friend review tasks for one verified head
// (nova-tools #3093, spec 10.12, control 52). The stream consumer that feeds
// this placement is #2933; this package is the waiting-ci half.
package consume

import (
	"context"
	"fmt"
	"strconv"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/task"
)

// Function names registered by internal/nsprint/fn/lua/task.lua.
const (
	FunctionReviewPlace = "ns_review_place"
	FunctionCIEnd       = "ns_ci_end"
	FunctionHeadChange  = "ns_review_head_change"
)

// ReadTitleSuffix is the DONE-WHEN every review task carries.
const ReadTitleSuffix = "DONE-WHEN: typed DISPOSITION at head with score; 8+ lands"

// HeadChangedPkg is the 10.5 pkg of the one fix task a head change cuts.
// A FAIL uses the failing package instead, so each cause has its own dedup key.
const HeadChangedPkg = "head-changed"

// PlaceRequest is one ok-to-friend review placement at an exact head.
type PlaceRequest struct {
	Sprint   string
	Repo     string
	PR       int
	Head     string
	Ref      string
	Readers  []string
	Priority int
	Actor    string
}

// EndRequest is one ns_ci_end. Verdict is OK or FAIL. Pkg is the failing
// package on FAIL and is ignored on OK. Author receives the one fix task.
type EndRequest struct {
	Sprint  string
	Repo    string
	PR      int
	Head    string
	Verdict string
	Pkg     string
	Author  string
	Actor   string
}

// HeadChangeRequest cancels reviews waiting on OldHead and cuts one fix task
// for Author. It does not open reviews at NewHead.
type HeadChangeRequest struct {
	Sprint  string
	Repo    string
	PR      int
	OldHead string
	NewHead string
	Author  string
	Actor   string
}

// PlaceReviews creates one review task per reader at the exact head. When
// ci:<repo>:<head> is not OK the tasks are in waiting-ci and in no open
// queue. When it is OK they are opened to the reader and the read clock
// starts. mode is "waiting-ci" or "open".
func PlaceReviews(ctx context.Context, st *store.Store, req PlaceRequest) (string, error) {
	if st == nil {
		return "", fmt.Errorf("ok-to-friend: nil store")
	}
	if req.Sprint == "" || req.Repo == "" || req.PR <= 0 || req.Head == "" || len(req.Readers) == 0 {
		return "", fmt.Errorf("ok-to-friend: sprint, repo, pr, head and readers are required")
	}
	actor := req.Actor
	if actor == "" {
		actor = "ok-to-friend"
	}
	args := []any{req.Sprint, req.Repo, strconv.Itoa(req.PR), req.Head, req.Ref, actor, strconv.Itoa(len(req.Readers))}
	for _, reader := range req.Readers {
		if reader == "" {
			return "", fmt.Errorf("ok-to-friend: empty reader")
		}
		push := reviewPush(req, reader)
		args = append(args, push.ID, reader, push.Title, strconv.Itoa(push.Priority), task.PayloadSHA(push))
	}
	status, vals, err := call(ctx, st, FunctionReviewPlace, args...)
	if err != nil {
		return "", err
	}
	if status != "PLACED" || len(vals) < 2 {
		return "", fmt.Errorf("ok-to-friend: place %s#%d at %s: %s", req.Repo, req.PR, req.Head, status)
	}
	mode, _ := vals[1].(string)
	if mode != "waiting-ci" && mode != "open" {
		return "", fmt.Errorf("ok-to-friend: place %s#%d at %s: mode %q", req.Repo, req.PR, req.Head, mode)
	}
	return mode, nil
}

// CIEnd is one ns_ci_end call. Writing OK moves this head's waiting reviews
// to their readers in that call. FAIL cancels them and cuts one fix task.
func CIEnd(ctx context.Context, st *store.Store, req EndRequest) error {
	if st == nil {
		return fmt.Errorf("ns_ci_end: nil store")
	}
	if req.Sprint == "" || req.Repo == "" || req.PR <= 0 || req.Head == "" {
		return fmt.Errorf("ns_ci_end: sprint, repo, pr and head are required")
	}
	if req.Verdict != "OK" && req.Verdict != "FAIL" {
		return fmt.Errorf("ns_ci_end: verdict must be OK or FAIL")
	}
	actor := req.Actor
	if actor == "" {
		actor = "ns_ci_end"
	}
	status, _, err := call(ctx, st, FunctionCIEnd,
		req.Sprint, req.Repo, strconv.Itoa(req.PR), req.Head, req.Verdict, req.Pkg, req.Author, actor)
	if err != nil {
		return err
	}
	if status != req.Verdict {
		return fmt.Errorf("ns_ci_end %s at %s: %s", req.Repo, req.Head, status)
	}
	return nil
}

// HeadChange cancels reviews waiting on the old head and cuts one fix task.
// A repeat with nothing left waiting is a no-op.
func HeadChange(ctx context.Context, st *store.Store, req HeadChangeRequest) error {
	if st == nil {
		return fmt.Errorf("head change: nil store")
	}
	if req.Sprint == "" || req.Repo == "" || req.PR <= 0 || req.OldHead == "" || req.NewHead == "" {
		return fmt.Errorf("head change: sprint, repo, pr, old head and new head are required")
	}
	actor := req.Actor
	if actor == "" {
		actor = "pr-to-read"
	}
	status, _, err := call(ctx, st, FunctionHeadChange,
		req.Sprint, req.Repo, strconv.Itoa(req.PR), req.OldHead, req.NewHead, req.Author, actor)
	if err != nil {
		return err
	}
	if status != "CANCELLED" && status != "NONE" {
		return fmt.Errorf("head change %s#%d: %s", req.Repo, req.PR, status)
	}
	return nil
}

func reviewPush(req PlaceRequest, reader string) task.PushRequest {
	short := req.Head
	if len(short) > 12 {
		short = short[:12]
	}
	return task.PushRequest{
		Sprint:   req.Sprint,
		ID:       task.ReviewID(req.Repo, req.PR, req.Head, reader),
		Kind:     task.KindReview,
		Title:    fmt.Sprintf("read %s#%d at %s | %s", req.Repo, req.PR, short, ReadTitleSuffix),
		Effects:  task.EffectsNone,
		Repo:     req.Repo,
		PR:       req.PR,
		Head:     req.Head,
		Ref:      req.Ref,
		To:       reader,
		Front:    true,
		Priority: req.Priority,
	}
}

func call(ctx context.Context, st *store.Store, function string, args ...any) (string, []any, error) {
	reply, err := st.Client().FCall(ctx, function, nil, args...).Result()
	if err != nil {
		return "", nil, fmt.Errorf("%s: %w", function, err)
	}
	vals, ok := reply.([]any)
	if !ok || len(vals) == 0 {
		return "", nil, fmt.Errorf("%s: unexpected function reply %T", function, reply)
	}
	status, ok := vals[0].(string)
	if !ok {
		return "", nil, fmt.Errorf("%s: unexpected function status %T", function, vals[0])
	}
	return status, vals, nil
}

package reconcile

import (
	"context"
	"errors"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/card"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
)

// Evidence is what the bench's batch session and REST found for one attempt
// identity. Branch, PushedSHA, PR and LivePID are effects: any one of them
// proves only that the child touched the world, never that its tests passed.
// Absent is proven absence: no job dir, no live process by command identity,
// no branch and no PR. The caller gathers it; this package never infers it.
type Evidence struct {
	Branch    string
	PushedSHA string
	PR        string
	LivePID   string
	Absent    bool
}

// Effect reports whether any external effect was found.
func (e Evidence) Effect() bool {
	return e.Branch != "" || e.PushedSHA != "" || e.PR != "" || e.LivePID != ""
}

func (e Evidence) String() string {
	var parts []string
	for _, kv := range [][2]string{{"branch", e.Branch}, {"pushed", e.PushedSHA}, {"pr", e.PR}, {"pid", e.LivePID}} {
		if kv[1] != "" {
			parts = append(parts, kv[0]+"="+kv[1])
		}
	}
	return strings.Join(parts, ",")
}

// RequiredRequest resolves one reconcile-required (or orphan-effect) card.
// ResultsDir is the attempt's own directory
// <root>/<sprint>/<label>/<base sha8>/<bench>/<attempt>.
type RequiredRequest struct {
	Sprint     string
	Label      string
	Fence      string
	ResultsDir string
	Evidence   Evidence
}

// ErrEvidence means the caller claimed both an effect and proven absence.
var ErrEvidence = errors.New("evidence names an effect and proven absence")

// ResolveRequired applies 3.2 in order:
//
//  1. an END RECORD for this identity in ResultsDir ends the card with the
//     record's outcome (ENDED): the only thing that ends a card;
//  2. no record and an effect (branch, PR, live process): ORPHAN
//     (orphan-effect, an unresolved item <label>:orphan-effect:<attempt>),
//     never ended(DONE), no harvest and no review task (control 25);
//  3. no record and proven absence: QUEUED under a new attempt (reason lost);
//  4. otherwise NOTHING: the card stays reconcile-required.
//
// An orphan-effect card is re-read for its record every call (harvest
// --orphans) and otherwise left alone until a recut.
func ResolveRequired(ctx context.Context, st *store.Store, req RequiredRequest) (Result, error) {
	const verb = "card required"
	if req.Evidence.Effect() && req.Evidence.Absent {
		return Result{}, ErrEvidence
	}
	if strings.TrimSpace(req.ResultsDir) != "" {
		ended, err := card.Resolve(ctx, st, card.ResolveRequest{Sprint: req.Sprint, Label: req.Label, ResultsDir: req.ResultsDir})
		if err != nil {
			return Result{}, err
		}
		if ended.Resolved {
			return Result{Code: 0, Verb: verb, ID: req.Label, Status: "ENDED", Attempt: ended.Attempt, Receipt: ended.Receipt}, nil
		}
		if ended.Code != 0 {
			return Result{Code: ended.Code, Verb: verb, ID: req.Label, Status: ended.Reason, Attempt: ended.Attempt}, nil
		}
	}
	state, err := st.Client().HGet(ctx, card.CardKey(req.Sprint, req.Label), "state").Result()
	if err != nil {
		return Result{}, err
	}
	if state != "reconcile-required" {
		return Result{Code: 0, Verb: verb, ID: req.Label, Status: "NOTHING"}, nil
	}
	switch {
	case req.Evidence.Effect():
		return call(ctx, st, verb, req.Label, "ns_card_required", req.Sprint, req.Label, req.Fence, "effect", req.Evidence.String())
	case req.Evidence.Absent:
		return call(ctx, st, verb, req.Label, "ns_card_required", req.Sprint, req.Label, req.Fence, "absent", "")
	}
	return Result{Code: 0, Verb: verb, ID: req.Label, Status: "NOTHING"}, nil
}

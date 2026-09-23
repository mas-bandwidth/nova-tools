// Package ci is the nova-sprint CI verbs (#2756 section 10, nova-tools
// #2936): a CI pass is a script card, and ci:<repo>:<sha> is the one verdict
// record every reader consults.
//
// Every state change is one call into the nova_sprint function library
// (internal/nsprint/fn/lua/ci.lua): cut, end, rerun and dispose each write
// the card, the record and one receipt atomically. Show, LandReady and
// Status only read.
package ci

import (
	"context"
	"fmt"
	"regexp"
	"strconv"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
)

// Function names registered by internal/nsprint/fn/lua/ci.lua.
const (
	FunctionCut     = "ns_ci_cut"
	FunctionEnd     = "ns_ci_end"
	FunctionRerun   = "ns_ci_rerun"
	FunctionDispose = "ns_ci_dispose"
)

// Verdicts on the record (10.3). Missing is not a stored value: a missing key
// or a missing verdict field reads Missing, never success and never failure.
const (
	Pending = "PENDING"
	OK      = "OK"
	Fail    = "FAIL"
	Flaky   = "FLAKY"
	Missing = "MISSING"
)

// Exit codes shared by the verbs.
const (
	ExitOK       = 0
	ExitRefused  = 2
	ExitFenced   = 3
	ExitConflict = 4
	ExitMissing  = 5
)

var (
	sprintRx = regexp.MustCompile(`^[a-z0-9-]{1,40}$`)
	shaRx    = regexp.MustCompile(`^[0-9a-f]{40}$`)
	repoRx   = regexp.MustCompile(`^[A-Za-z0-9._-]{1,100}$`)
)

// Label is the one ci card label per PR head (10.2 item 1).
func Label(pr int, head string) string {
	return fmt.Sprintf("ci-%d-%s", pr, head[:8])
}

// RecordKey is the global verdict key for one head (2.2, 10.3).
func RecordKey(repo, head string) string { return "ci:" + repo + ":" + head }

// CutRequest cuts the ci card for one PR head. Head and Base are full shas;
// the caller has verified by REST that Head is the PR's head (4.8).
type CutRequest struct {
	Sprint string
	Repo   string
	PR     int
	Head   string
	Base   string
	Leg    string // toolchain leg a bench must carry; default "go"
	Paths  string // the work card's PATHS, a hint unioned in (10.2 item 2)
	Actor  string
	Idem   string
}

// Result is one function reply: the status word and its exit code.
type Result struct {
	Status  string
	Attempt int
	Detail  string
}

// ExitCode maps a status word to the verb's exit code.
func (r Result) ExitCode() int {
	switch r.Status {
	case "CREATED", "EXISTS", "ENDED", "RERUN", "APPROVE", "HOLD":
		return ExitOK
	case "FENCED":
		return ExitFenced
	case "CONFLICT":
		return ExitConflict
	case "NOTFOUND", "MISSING":
		return ExitMissing
	default:
		return ExitRefused
	}
}

func (r Result) String() string {
	s := r.Status
	if r.Attempt > 0 {
		s += " attempt=" + strconv.Itoa(r.Attempt)
	}
	if r.Detail != "" {
		s += " " + r.Detail
	}
	return s
}

func validate(sprint, repo, head string) error {
	if !sprintRx.MatchString(sprint) {
		return fmt.Errorf("sprint %q is not [a-z0-9-]{1,40}", sprint)
	}
	if !repoRx.MatchString(repo) {
		return fmt.Errorf("repo %q is not a repository name", repo)
	}
	if !shaRx.MatchString(head) {
		return fmt.Errorf("sha %q is not a full 40-hex sha", head)
	}
	return nil
}

// Cut cuts ci-<pr>-<sha8> into the sprint's pool at the front tier and sets
// the record PENDING in the same call.
func Cut(ctx context.Context, st *store.Store, req CutRequest) (Result, error) {
	if err := validate(req.Sprint, req.Repo, req.Head); err != nil {
		return Result{}, err
	}
	if !shaRx.MatchString(req.Base) {
		return Result{}, fmt.Errorf("base %q is not a full 40-hex sha", req.Base)
	}
	if req.PR <= 0 {
		return Result{}, fmt.Errorf("pr must be positive")
	}
	if req.Leg == "" {
		req.Leg = "go"
	}
	label := Label(req.PR, req.Head)
	if req.Idem == "" {
		req.Idem = "ci-cut:" + req.Sprint + "/" + label
	}
	return call(ctx, st, FunctionCut, req.Sprint, label, req.Repo, strconv.Itoa(req.PR),
		req.Head, req.Base, req.Leg, req.Paths, req.Actor, req.Idem)
}

// EndRecord is what the ci card's wrapper writes at its end (10.2 item 3,
// 10.5 item 5). Identity is <S>/<label>/<base8>/<bench>/<attempt>.
type EndRecord struct {
	Sprint   string
	Label    string
	Token    string
	Identity string
	Outcome  string // DONE, FAILED, BLOCKED
	Reason   string // done; crash, timeout, idle-killed; env
	Verdict  string // OK or FAIL on DONE; empty otherwise
	Pkg      string
	Test     string
	WallS    int
	Log      string
	Tree     string
	Actor    string
}

// End ends the ci card and writes the verdict in the same call. A token that
// is not the attempt's is FENCED (exit 3); a record for another attempt
// resolves NOTHING.
func End(ctx context.Context, st *store.Store, rec EndRecord) (Result, error) {
	if !sprintRx.MatchString(rec.Sprint) || rec.Label == "" {
		return Result{}, fmt.Errorf("end needs a sprint and a label")
	}
	return call(ctx, st, FunctionEnd, rec.Sprint, rec.Label, rec.Token, rec.Identity,
		rec.Outcome, rec.Reason, rec.Verdict, rec.Pkg, rec.Test, strconv.Itoa(rec.WallS),
		rec.Log, rec.Tree, rec.Actor)
}

// Rerun cuts the one rerun of a FAIL or MISSING head on another healthy
// bench. SPENT (exit 2) when the budget is used; BLOCKED (exit 2) when no
// other healthy bench carries the leg.
func Rerun(ctx context.Context, st *store.Store, sprint, label, actor, reason string) (Result, error) {
	if !sprintRx.MatchString(sprint) || label == "" {
		return Result{}, fmt.Errorf("rerun needs a sprint and a label")
	}
	if reason == "" {
		return Result{}, fmt.Errorf("rerun needs a reason")
	}
	return call(ctx, st, FunctionRerun, sprint, label, actor, reason, "ci-rerun:"+sprint+"/"+label)
}

// Dispose is the typed disposition on a FLAKY head: APPROVE or HOLD.
func Dispose(ctx context.Context, st *store.Store, sprint, repo, head, disposition, friend, url string) (Result, error) {
	if err := validate(sprint, repo, head); err != nil {
		return Result{}, err
	}
	if disposition != "APPROVE" && disposition != "HOLD" {
		return Result{}, fmt.Errorf("disposition must be APPROVE or HOLD")
	}
	if friend == "" || url == "" {
		return Result{}, fmt.Errorf("a typed disposition names the friend and its url")
	}
	return call(ctx, st, FunctionDispose, sprint, repo, head, disposition, friend, url)
}

func call(ctx context.Context, st *store.Store, fn string, args ...string) (Result, error) {
	argv := make([]any, len(args))
	for i, a := range args {
		argv[i] = a
	}
	raw, err := st.Client().FCall(ctx, fn, nil, argv...).Result()
	if err != nil {
		return Result{}, fmt.Errorf("%s: %w", fn, err)
	}
	parts, ok := raw.([]any)
	if !ok || len(parts) == 0 {
		return Result{}, fmt.Errorf("%s: reply %T", fn, raw)
	}
	r := Result{Status: fmt.Sprint(parts[0])}
	if len(parts) > 1 {
		if n, err := strconv.Atoi(fmt.Sprint(parts[1])); err == nil {
			r.Attempt = n
		} else {
			r.Detail = fmt.Sprint(parts[1])
		}
	}
	if len(parts) > 2 {
		r.Detail = fmt.Sprint(parts[2])
	}
	return r, nil
}

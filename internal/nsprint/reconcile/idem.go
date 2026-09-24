// Package reconcile holds the nova-sprint reconciler's duties (#2756 section 5).
//
// This file: idempotency keys for external effects (5.4: a PR open is keyed
// pr:<repo>:<branch> in s:<S>:idem and is never made twice, from Redis alone:
// the forge is only ever POSTed to, never read; #2930 rev 5) and the one-call
// assignment of a ready task to a consumer (control 9). reclaim.go expires
// cards; orphan.go resolves reconcile-required. Every state change is one
// nova_sprint function call, fenced by the reconciler lease token.
package reconcile

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
)

// Result is one reconciler call. Code is the process exit: 0 applied or
// nothing to do, 1 usage, 2 a guard refused, 3 fenced, 4 conflict, 5 not
// found. Status names what happened (NOTHING, QUEUED, LAUNCHED, REQUIRED,
// ORPHAN, ENDED, OK, ...). Receipt is the log entry id of the transition, or
// the stored one when the call was a repeat.
type Result struct {
	Code    int
	Verb    string
	ID      string
	Status  string
	Attempt int
	Receipt string
}

func (r Result) Line() string {
	return fmt.Sprintf("%s %s %s attempt=%d receipt=%s code=%d", r.Status, r.Verb, r.ID, r.Attempt, r.Receipt, r.Code)
}

func call(ctx context.Context, st *store.Store, verb, id, name string, args ...any) (Result, error) {
	if st == nil || st.Client() == nil {
		return Result{Code: 1, Verb: verb, ID: id, Status: "USAGE"}, nil
	}
	if err := fn.Load(ctx, st.Client()); err != nil {
		return Result{}, err
	}
	return callNoLoad(ctx, st, verb, id, name, args...)
}

// callNoLoad is call without the library load: a server without the
// nova_sprint library answers "Function not found", which is the error.
// The reply is code|status|attempt|receipt; the receipt field is last and
// may itself hold a stored value (a URL), so it is split at most 4 ways.
func callNoLoad(ctx context.Context, st *store.Store, verb, id, name string, args ...any) (Result, error) {
	if st == nil || st.Client() == nil {
		return Result{Code: 1, Verb: verb, ID: id, Status: "USAGE"}, nil
	}
	raw, err := st.Client().FCall(ctx, name, nil, args...).Text()
	if err != nil {
		return Result{}, err
	}
	parts := strings.SplitN(raw, "|", 4)
	if len(parts) != 4 {
		return Result{}, fmt.Errorf("%s reply %q", name, raw)
	}
	code, err := strconv.Atoi(parts[0])
	if err != nil {
		return Result{}, fmt.Errorf("%s reply %q", name, raw)
	}
	attempt := 0
	if parts[2] != "" {
		if attempt, err = strconv.Atoi(parts[2]); err != nil {
			return Result{}, fmt.Errorf("%s reply %q", name, raw)
		}
	}
	return Result{Code: code, Verb: verb, ID: id, Status: parts[1], Attempt: attempt, Receipt: parts[3]}, nil
}

// AssignRequest routes one ready task to one consumer (a friend, or
// harvest:<bench>). Fence is the reconciler lease token.
type AssignRequest struct {
	Sprint   string
	ID       string
	Consumer string
	Fence    string
}

// Assign moves a ready task to open:<consumer> with its owner, receipt and
// idem key route:<id>:<attempt> in one function call. Two concurrent refills
// of one task give one assignment and one receipt; a repeat by the winner
// returns the stored receipt (Code 0), a loser gets Code 4 CONFLICT.
func Assign(ctx context.Context, st *store.Store, req AssignRequest) (Result, error) {
	return call(ctx, st, "task assign", req.ID, "ns_task_assign", req.Sprint, req.ID, req.Consumer, req.Fence)
}

// PRHost is the forge side of a PR open: one Open, one request. There is no
// lookup (#2930 rev 5: GitHub is a git remote only; a crash between the open
// and its record is resolved from Redis, never by reading the forge).
// RESTPRHost is the GitHub REST version.
type PRHost interface {
	Open(ctx context.Context, repo, branch, base, title, body string) (url string, err error)
}

// ErrForgeHasPR is a 422 that does not prove no PR was opened: the "already
// exists" conflict, an empty or unreadable body, and every shape outside the
// validation allowlist. The open is ambiguous (no evidence is not negative
// evidence).
var ErrForgeHasPR = errors.New("forge 422: a PR may exist on the head")

// ErrForgeRejected is a 422 whose every error is on the validation allowlist
// (base or head invalid on PullRequest, or "No commits between "): the forge
// opened nothing. Msg is the first 256 bytes of the error messages.
type ErrForgeRejected struct{ Msg string }

func (e *ErrForgeRejected) Error() string { return "forge 422 rejected: " + e.Msg }

// openDeadline bounds one Open. open_ms (s:<S>:policy, default 60000) is
// twice this, so the expire sweep never flips a live open to ambiguous.
const openDeadline = 30 * time.Second

// PRRequest is one idempotent PR open for a pushed branch.
type PRRequest struct {
	Sprint string
	Repo   string // owner/name
	Branch string
	Base   string
	Title  string
	Body   string
	Who    string // the harvest worker instance, recorded while the open is pending
	Fence  string // the reconciler lease token; a stale one writes no idem key
}

// PRResult is one EnsurePR answer. Code 0: URL is the recorded PR (Status OK),
// or the one this call opened (Status OPENED, Opened true). Code 4: no URL;
// Status IN-FLIGHT (a pending open, Value is pending:<who>:<at_ms>),
// AMBIGUOUS (terminal for the machine until a friend's `idem resolve`, Value
// is ambiguous:<who>:<at_ms>) or REJECTED (the forge refused the request, Msg
// says why, the key is cleared). Receipt is the transition's log entry.
type PRResult struct {
	Code    int
	Status  string
	URL     string
	Opened  bool
	Receipt string
	Value   string
	Msg     string
}

// PRKey is the idem key of a PR open (5.4).
func PRKey(repo, branch string) string { return "pr:" + repo + ":" + branch }

// PendingIndexKey is the zset of pending idem keys scored by their begin
// time (Redis TIME ms), which the expire sweep reads past open_ms.
func PendingIndexKey(sprint string) string { return "s:" + sprint + ":idx:idem:pending" }

// UnresolvedField is the s:<S>:unresolved field an ambiguous key raises:
// pr-ambiguous:<repo>:<branch> for a PR key.
func UnresolvedField(key string) string {
	if rest, ok := strings.CutPrefix(key, "pr:"); ok {
		return "pr-ambiguous:" + rest
	}
	return "ambiguous:" + key
}

// EnsurePR opens at most one PR for repo:branch, whatever crashed before,
// and never reads the forge (control 8). ns_idem_begin writes the key
// pending:<who>:<at_ms> before the forge call. The forge is asked only when
// this call began the key:
//   - a URL already recorded returns it (Code 0 OK);
//   - a pending key returns Code 4 IN-FLIGHT, an ambiguous one Code 4 AMBIGUOUS;
//   - on BEGUN, one Open (30 s deadline): success records the URL
//     (ns_idem_commit, Code 0 OPENED); ErrForgeHasPR flips the key to
//     ambiguous at once (ns_idem_ambiguous, Code 4 AMBIGUOUS); ErrForgeRejected
//     clears it with one rejected-open receipt (ns_idem_rejected, Code 4
//     REJECTED, never retried here); any other error or a lost reply returns
//     the error and leaves the key pending for the expire sweep.
//
// Every idem call carries req.Fence: a stale or missing reconciler token is
// an error naming FENCED, before the idem hash is read or written and before
// the forge is asked anything.
func EnsurePR(ctx context.Context, st *store.Store, host PRHost, req PRRequest) (PRResult, error) {
	if host == nil || req.Repo == "" || req.Branch == "" || req.Who == "" {
		return PRResult{}, errors.New("ensure pr: host, repo, branch and who are required")
	}
	key := PRKey(req.Repo, req.Branch)
	begun, err := call(ctx, st, "pr open", key, "ns_idem_begin", req.Sprint, key, req.Who, req.Fence)
	if err != nil {
		return PRResult{}, err
	}
	if begun.Code != 0 {
		return PRResult{}, fmt.Errorf("ensure pr %s: %s", key, begun.Line())
	}
	stored := begun.Receipt
	if begun.Status == "EXISTS" {
		switch {
		case strings.HasPrefix(stored, "pending:"):
			return PRResult{Code: 4, Status: "IN-FLIGHT", Value: stored}, nil
		case strings.HasPrefix(stored, "ambiguous:"):
			return PRResult{Code: 4, Status: "AMBIGUOUS", Value: stored}, nil
		default:
			return PRResult{Code: 0, Status: "OK", URL: stored}, nil
		}
	}
	octx, cancel := context.WithTimeout(ctx, openDeadline)
	url, openErr := host.Open(octx, req.Repo, req.Branch, req.Base, req.Title, req.Body)
	cancel()
	var rejected *ErrForgeRejected
	switch {
	case openErr == nil:
		done, err := call(ctx, st, "pr open", key, "ns_idem_commit", req.Sprint, key, url, req.Who, "pr-opened", req.Fence)
		if err != nil {
			return PRResult{}, err
		}
		if done.Code != 0 {
			return PRResult{}, fmt.Errorf("ensure pr %s: record %s: %s", key, url, done.Line())
		}
		return PRResult{Code: 0, Status: "OPENED", URL: url, Opened: true, Receipt: done.Receipt}, nil
	case errors.Is(openErr, ErrForgeHasPR):
		amb, err := MarkAmbiguous(ctx, st, req.Sprint, key, stored, req.Fence)
		if err != nil {
			return PRResult{}, err
		}
		if amb.Code != 0 {
			return PRResult{}, fmt.Errorf("ensure pr %s: ambiguous after %v: %s", key, openErr, amb.Line())
		}
		return PRResult{Code: 4, Status: "AMBIGUOUS", Receipt: amb.Receipt, Msg: openErr.Error()}, nil
	case errors.As(openErr, &rejected):
		rej, err := call(ctx, st, "pr open", key, "ns_idem_rejected", req.Sprint, key, stored, "422", rejected.Msg, req.Fence)
		if err != nil {
			return PRResult{}, err
		}
		if rej.Code != 0 {
			return PRResult{}, fmt.Errorf("ensure pr %s: rejected %q: %s", key, rejected.Msg, rej.Line())
		}
		return PRResult{Code: 4, Status: "REJECTED", Receipt: rej.Receipt, Msg: rejected.Msg}, nil
	default:
		return PRResult{}, fmt.Errorf("ensure pr %s: open (the key stays pending): %w", key, openErr)
	}
}

// MarkAmbiguous flips a pending idem key to ambiguous:<who>:<at_ms> with one
// receipt, one pr-ambiguous item in s:<S>:unresolved and its pending-index
// member removed (ns_idem_ambiguous). was, when not empty, must equal the
// stored pending value. Callers are the reconciler lease holder: EnsurePR on
// ErrForgeHasPR and the expire sweep for keys pending past open_ms. A replay
// on an ambiguous key returns the first receipt; any other value is Code 2.
func MarkAmbiguous(ctx context.Context, st *store.Store, sprint, key, was, fence string) (Result, error) {
	return call(ctx, st, "idem ambiguous", key, "ns_idem_ambiguous", sprint, key, fence, was)
}

// IdemResolveRequest is a friend's typed resolution of an ambiguous key
// (`nova-sprint idem resolve`). Was is the ambiguous value the friend read;
// exactly one of URL and None is set.
type IdemResolveRequest struct {
	Sprint string
	Key    string
	Was    string
	URL    string
	None   bool
	Who    string
}

// ResolveResult is one ns_idem_resolve answer: Code 0 RESOLVED with the
// receipt, or Code 2 STATE with the stored value ("" when absent) and nothing
// written.
type ResolveResult struct {
	Code    int
	Status  string
	Receipt string
	Value   string
}

// ResolveIdem runs ns_idem_resolve, the one writer of the ambiguous-to-URL or
// ambiguous-to-absent transition. It holds no reconciler token: its fence is
// compare-and-set, writing only when the stored value is ambiguous:* and equal
// to req.Was byte for byte. It does not load the function library: a server
// without it is an error (the verb's exit 2), never a silent load by a friend.
func ResolveIdem(ctx context.Context, st *store.Store, req IdemResolveRequest) (ResolveResult, error) {
	if req.Sprint == "" || req.Key == "" || req.Who == "" {
		return ResolveResult{}, errors.New("idem resolve: sprint, key and who are required")
	}
	if req.Was == "" {
		return ResolveResult{}, errors.New("idem resolve: --was <ambiguous:who:at_ms> is required (the value its STATE line printed)")
	}
	if (req.URL == "") == !req.None {
		return ResolveResult{}, errors.New("idem resolve: want exactly one of --url <u> and --none")
	}
	mode, url := "url", req.URL
	if req.None {
		mode, url = "none", ""
	}
	r, err := callNoLoad(ctx, st, "idem resolve", req.Key, "ns_idem_resolve", req.Sprint, req.Key, req.Was, mode, url, req.Who)
	if err != nil {
		return ResolveResult{}, err
	}
	switch r.Status {
	case "RESOLVED":
		return ResolveResult{Code: r.Code, Status: r.Status, Receipt: r.Receipt}, nil
	case "STATE":
		return ResolveResult{Code: r.Code, Status: r.Status, Value: r.Receipt}, nil
	default:
		return ResolveResult{}, fmt.Errorf("idem resolve %s: %s", req.Key, r.Line())
	}
}

// RESTPRHost talks to the GitHub REST API (BaseURL defaults to
// https://api.github.com). It never uses GraphQL, and it only ever POSTs.
type RESTPRHost struct {
	BaseURL string
	Token   string
	HTTP    *http.Client
}

// Open makes exactly one request, POST /repos/<repo>/pulls, and never a
// second one. A 422 is classified by its body (classify422); any other
// non-2xx is an error that leaves the key pending.
func (h RESTPRHost) Open(ctx context.Context, repo, branch, base, title, body string) (string, error) {
	base0 := h.BaseURL
	if base0 == "" {
		base0 = "https://api.github.com"
	}
	payload, err := json.Marshal(map[string]string{"head": branch, "base": base, "title": title, "body": body})
	if err != nil {
		return "", err
	}
	path := "/repos/" + repo + "/pulls"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(base0, "/")+path, bytes.NewReader(payload))
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Content-Type", "application/json")
	if h.Token != "" {
		req.Header.Set("Authorization", "Bearer "+h.Token)
	}
	client := h.HTTP
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if resp.StatusCode == http.StatusUnprocessableEntity {
		if err != nil {
			raw = nil // a body cut short is unreadable: ambiguous
		}
		return "", fmt.Errorf("POST %s: 422: %w", path, classify422(raw))
	}
	if err != nil {
		return "", err
	}
	if resp.StatusCode/100 != 2 {
		return "", fmt.Errorf("POST %s: %d %s", path, resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	var pr struct {
		HTMLURL string `json:"html_url"`
	}
	if err := json.Unmarshal(raw, &pr); err != nil {
		return "", fmt.Errorf("POST %s: reply: %w", path, err)
	}
	if pr.HTMLURL == "" {
		return "", errors.New("pr open: no html_url in the reply")
	}
	return pr.HTMLURL, nil
}

// classify422 is the one place a 422 from POST /pulls is read. Conflict (any
// message starting "A pull request already exists") is ErrForgeHasPR.
// ErrForgeRejected only when errors[] is non-empty and every entry is on the
// allowlist: PullRequest base invalid, PullRequest head invalid, or a message
// starting "No commits between ". Everything else, including an empty or
// unparseable body, {} and any unknown or mixed shape, is ErrForgeHasPR: none
// of them proves that no PR was created.
func classify422(body []byte) error {
	var reply struct {
		Message string `json:"message"`
		Errors  []struct {
			Resource string `json:"resource"`
			Field    string `json:"field"`
			Code     string `json:"code"`
			Message  string `json:"message"`
		} `json:"errors"`
	}
	if len(bytes.TrimSpace(body)) == 0 || json.Unmarshal(body, &reply) != nil || len(reply.Errors) == 0 {
		return ErrForgeHasPR
	}
	if strings.HasPrefix(strings.TrimSpace(reply.Message), "A pull request already exists") {
		return ErrForgeHasPR
	}
	for _, e := range reply.Errors {
		if strings.HasPrefix(strings.TrimSpace(e.Message), "A pull request already exists") {
			return ErrForgeHasPR
		}
	}
	msgs := make([]string, 0, len(reply.Errors))
	for _, e := range reply.Errors {
		invalidRef := e.Resource == "PullRequest" && e.Code == "invalid" && (e.Field == "base" || e.Field == "head")
		trimmedMsg := strings.TrimSpace(e.Message)
		if !invalidRef && !strings.HasPrefix(trimmedMsg, "No commits between ") {
			return ErrForgeHasPR
		}
		m := trimmedMsg
		if m == "" {
			m = e.Resource + " " + e.Field + " " + e.Code
		}
		msgs = append(msgs, m)
	}
	msg := strings.Join(msgs, "; ")
	if len(msg) > 256 {
		msg = strings.ToValidUTF8(msg[:256], "")
	}
	return &ErrForgeRejected{Msg: msg}
}

package disposition

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/task"
)

// Function names registered by internal/nsprint/fn/lua/hold.lua.
const (
	FunctionIngest  = "ns_ingest_disposition"
	FunctionRoute   = "ns_hold_route"
	FunctionRelease = "ns_hold_release"
	// Group is the consumer group of the hold router on s:<S>:hold:events.
	Group = "hold-route"
	// LeaseKey is the router's single-writer lease.
	LeaseKey = "lease:hold-route"
	// DefaultReclaimIdle is XAUTOCLAIM's min-idle in production (5 s).
	DefaultReclaimIdle = 5000
)

// EventsKey is the hold event stream.
func EventsKey(sprint string) string { return "s:" + sprint + ":hold:events" }

// PolicyKey carries fix_to and release_reader.
func PolicyKey(sprint string) string { return "s:" + sprint + ":policy" }

// ParkKey is the parked-action hash.
func ParkKey(sprint string) string { return "s:" + sprint + ":holdpark" }

// OwnerKey is the per-unit owner hash.
func OwnerKey(sprint, unit string) string { return "s:" + sprint + ":holdowner:" + unit }

// NoteKey is the per-unit note hash.
func NoteKey(sprint, unit string) string { return "s:" + sprint + ":note:" + unit }

// RepairKey is the per-unit repair hash.
func RepairKey(sprint, unit string) string { return "s:" + sprint + ":repair:" + unit }

// IngestRequest is one comment to ingest; identity comes from context, never
// from the line.
type IngestRequest struct {
	Sprint string
	Repo   string // owner/repo or repo
	PR     int
	URL    string
	Body   string
	Actor  string
}

// IngestResult is the outcome line and its exit code.
type IngestResult struct {
	Outcome Outcome
	Why     string
	Reply   []string
}

// Exit is 2 for REFUSED, else 0.
func (r IngestResult) Exit() int {
	if r.Outcome == Refused {
		return 2
	}
	return 0
}

// String is the one line the verb prints.
func (r IngestResult) String() string {
	parts := []string{string(r.Outcome)}
	if r.Why != "" {
		parts = append(parts, r.Why)
	}
	parts = append(parts, r.Reply...)
	return strings.Join(parts, " ")
}

var commentID = regexp.MustCompile(`(\d+)$`)

// CommentID is the trailing number of a comment URL, or "".
func CommentID(url string) string {
	if m := commentID.FindStringSubmatch(url); m != nil {
		return m[1]
	}
	return ""
}

// Ingest parses the body and makes at most one record with one
// ns_ingest_disposition call.
func Ingest(ctx context.Context, c *redis.Client, req IngestRequest) (IngestResult, error) {
	res := Parse(req.Body)
	if res.Outcome != Record {
		return IngestResult{Outcome: res.Outcome, Why: res.Why}, nil
	}
	ln := res.Line
	derived := ""
	if ln.Verdict == "HOLD" && ln.Kind == "" {
		derived = Classify(ln.Reason, req.PR)
	}
	cid := CommentID(req.URL)
	score := ""
	if ln.Score > 0 {
		score = strconv.Itoa(ln.Score)
	}
	reply, err := c.FCall(ctx, FunctionIngest, nil,
		req.Sprint, ShortRepo(req.Repo), strconv.Itoa(req.PR), req.URL, cid,
		string(ln.Type), ln.Who, ln.Head, ln.Verdict, score,
		ln.Kind, derived, ln.Scope, ln.Reason).StringSlice()
	if err != nil {
		return IngestResult{}, fmt.Errorf("%s: %w", FunctionIngest, err)
	}
	if len(reply) == 0 {
		return IngestResult{}, fmt.Errorf("%s: empty reply", FunctionIngest)
	}
	switch reply[0] {
	case "REFUSED":
		return IngestResult{Outcome: Refused, Why: strings.Join(reply[1:], " ")}, nil
	case "RECORD":
		return IngestResult{Outcome: Record, Reply: reply[1:]}, nil
	}
	return IngestResult{}, fmt.Errorf("%s: unexpected reply %v", FunctionIngest, reply)
}

// Release is `hold release`: one ns_hold_release call.
func Release(ctx context.Context, c *redis.Client, sprint, repo string, pr int, holder, as, head, evidence string) ([]string, error) {
	reply, err := c.FCall(ctx, FunctionRelease, nil,
		sprint, ShortRepo(repo), strconv.Itoa(pr), holder, as, strings.ToLower(head), evidence).StringSlice()
	if err != nil {
		return nil, fmt.Errorf("%s: %w", FunctionRelease, err)
	}
	return reply, nil
}

// RouteConfig is one router tick's parameters.
type RouteConfig struct {
	Sprint      string
	Consumer    string
	ReclaimIdle int64 // ms, XAUTOCLAIM min-idle
	Actor       string
}

// ErrNoPolicy is returned when s:<S>:policy lacks fix_to or release_reader.
var ErrNoPolicy = errors.New("s:<S>:policy needs fix_to and release_reader")

// Park is one parked action (s:<S>:holdpark value).
type Park struct {
	ID         string `json:"id"`
	Kind       string `json:"kind"`
	Role       string `json:"role"`
	Holder     string `json:"holder"`
	ReleaseFor string `json:"release_for"`
	Reason     string `json:"reason"`
	ParkedAt   int64  `json:"parked_at"`
	Unit       string `json:"unit"`
	Repo       string `json:"repo"`
	PR         string `json:"pr"`
	Head       string `json:"head"`
	Owner      string `json:"owner"`
	Action     string `json:"action"`
	HoldHead   string `json:"hold_head"`
}

type candidate struct {
	to, id, kind, title, releaseFor string
}

type action struct {
	mode, ref, suffix string
	last              bool
	unit, repo, pr    string
	head, owner       string
	act, holder       string
	holdHead, role    string
	cands             []candidate
}

// RouteOnce is one router tick: one pipelined read of the events, the
// policy, the parks and the registry, a second pipelined read of the units
// the events name, then one ns_hold_route call per action. It returns the
// result lines.
func RouteOnce(ctx context.Context, c *redis.Client, cfg RouteConfig) ([]string, error) {
	ev := EventsKey(cfg.Sprint)
	if err := c.XGroupCreateMkStream(ctx, ev, Group, "0").Err(); err != nil && !strings.Contains(err.Error(), "BUSYGROUP") {
		return nil, fmt.Errorf("create group: %w", err)
	}
	pipe := c.Pipeline()
	own := pipe.XReadGroup(ctx, &redis.XReadGroupArgs{Group: Group, Consumer: cfg.Consumer, Streams: []string{ev, "0"}, Count: 100, Block: -1})
	fresh := pipe.XReadGroup(ctx, &redis.XReadGroupArgs{Group: Group, Consumer: cfg.Consumer, Streams: []string{ev, ">"}, Count: 100, Block: -1})
	claimed := pipe.XAutoClaim(ctx, &redis.XAutoClaimArgs{Stream: ev, Group: Group, Consumer: cfg.Consumer,
		MinIdle: time.Duration(cfg.ReclaimIdle) * time.Millisecond, Start: "0-0", Count: 100})
	pol := pipe.HMGet(ctx, PolicyKey(cfg.Sprint), "fix_to", "release_reader")
	parks := pipe.HGetAll(ctx, ParkKey(cfg.Sprint))
	friends := pipe.SMembers(ctx, "friends")
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return nil, fmt.Errorf("route read: %w", err)
	}
	fixTo, reader := str(pol.Val(), 0), str(pol.Val(), 1)
	if fixTo == "" || reader == "" {
		return nil, ErrNoPolicy
	}
	var msgs []redis.XMessage
	seen := map[string]bool{}
	add := func(ms []redis.XMessage) {
		for _, m := range ms {
			if !seen[m.ID] && len(m.Values) > 0 {
				seen[m.ID] = true
				msgs = append(msgs, m)
			}
		}
	}
	for _, s := range own.Val() {
		add(s.Messages)
	}
	cms, _, _ := claimed.Result()
	add(cms)
	for _, s := range fresh.Val() {
		add(s.Messages)
	}

	// Second read: per-unit mergeable and the open holds a REPAIR re-reads.
	names := friends.Val()
	sort.Strings(names)
	pipe = c.Pipeline()
	merg := map[string]*redis.StringCmd{}
	holds := map[string]map[string]*redis.MapStringStringCmd{}
	for _, m := range msgs {
		unit := val(m, "unit")
		if _, ok := merg[unit]; !ok {
			merg[unit] = pipe.HGet(ctx, "s:"+cfg.Sprint+":u:"+unit, "mergeable")
		}
		if val(m, "type") == "repair" && holds[unit] == nil {
			holds[unit] = map[string]*redis.MapStringStringCmd{}
			for _, f := range names {
				holds[unit][f] = pipe.HGetAll(ctx, "s:"+cfg.Sprint+":hold:"+unit+":"+f)
			}
		}
	}
	if len(msgs) > 0 {
		if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
			return nil, fmt.Errorf("route unit read: %w", err)
		}
	}

	var acts []action
	for _, m := range msgs {
		acts = append(acts, eventActions(m, fixTo, reader, merg, holds, names)...)
	}
	fields := make([]string, 0, len(parks.Val()))
	for f := range parks.Val() {
		fields = append(fields, f)
	}
	sort.Strings(fields)
	for _, f := range fields {
		var p Park
		if err := json.Unmarshal([]byte(parks.Val()[f]), &p); err != nil {
			continue
		}
		acts = append(acts, parkAction(f, p, fixTo, reader))
	}

	var out []string
	for _, a := range acts {
		line, retry, err := call(ctx, c, cfg, a, fixTo, reader)
		if err != nil {
			return out, err
		}
		out = append(out, line)
		if retry {
			break
		}
	}
	return out, nil
}

func str(v []any, i int) string {
	if i < len(v) && v[i] != nil {
		return fmt.Sprint(v[i])
	}
	return ""
}

func val(m redis.XMessage, k string) string {
	if v, ok := m.Values[k]; ok {
		return fmt.Sprint(v)
	}
	return ""
}

func sha8(h string) string {
	if len(h) > 8 {
		return h[:8]
	}
	return h
}

// FixID is the fix task of a hold: fix-<n>-hold-<holder>-<sha8>.
func FixID(pr, holder, head string) string {
	return fmt.Sprintf("fix-%s-hold-%s-%s", pr, holder, sha8(head))
}

// UpdateID is the update-branch task of a CONFLICTING note.
func UpdateID(pr, head string) string { return fmt.Sprintf("update-%s-%s", pr, sha8(head)) }

func reviewID(repo, pr, head, friend string) string {
	n, _ := strconv.Atoi(pr)
	return task.ReviewID(repo, n, head, friend)
}

func readTitle(repo, pr, head, releaseFor string) string {
	t := fmt.Sprintf("read %s#%s at %s", repo, pr, sha8(head))
	if releaseFor != "" {
		t += " release_for=" + releaseFor
	}
	return t
}

func eventActions(m redis.XMessage, fixTo, reader string, merg map[string]*redis.StringCmd,
	holds map[string]map[string]*redis.MapStringStringCmd, names []string) []action {
	unit, repo, pr, head, who := val(m, "unit"), val(m, "repo"), val(m, "pr"), val(m, "head"), val(m, "who")
	base := action{mode: "event", ref: m.ID, last: true, unit: unit, repo: repo, pr: pr, head: head}
	switch val(m, "type") {
	case "hold":
		a := base
		a.act, a.holder, a.holdHead, a.role = "fix", who, head, "fix_to"
		a.owner = "h:" + who + ":" + head
		a.cands = []candidate{{to: fixTo, id: FixID(pr, who, head), kind: "fix",
			title: fmt.Sprintf("fix %s#%s hold by %s at %s", repo, pr, who, sha8(head))}}
		return []action{a}
	case "note":
		a := base
		a.owner = val(m, "note")
		a.holder = who
		switch val(m, "kind") {
		case "ci", "self":
			if cmd := merg[unit]; cmd != nil && cmd.Val() == "CONFLICTING" {
				a.act, a.role = "update", "fix_to"
				a.cands = []candidate{{to: fixTo, id: UpdateID(pr, head), kind: "fix",
					title: fmt.Sprintf("update %s#%s branch (CONFLICTING) at %s", repo, pr, sha8(head))}}
			} else {
				a.act, a.role = "read", "release_reader"
				a.cands = []candidate{{to: reader, id: reviewID(repo, pr, head, reader), kind: "read",
					title: readTitle(repo, pr, head, "")}}
			}
		default:
			a.act = "none"
		}
		return []action{a}
	case "repair":
		var acts []action
		for _, f := range names {
			cmd := holds[unit][f]
			if cmd == nil {
				continue
			}
			h := cmd.Val()
			if len(h) == 0 || h["released_by"] != "" || h["head"] == head {
				continue
			}
			a := base
			a.last, a.suffix = false, ":"+f
			a.act, a.holder, a.holdHead, a.role = "reread", f, h["head"], "holder"
			a.owner = "r" + head + ":" + f
			id := reviewID(repo, pr, head, f)
			a.cands = []candidate{
				{to: f, id: id, kind: "read", title: readTitle(repo, pr, head, "")},
				{to: reader, id: id, kind: "read", title: readTitle(repo, pr, head, f), releaseFor: f},
			}
			acts = append(acts, a)
		}
		if len(acts) == 0 {
			a := base
			a.act = "none"
			return []action{a}
		}
		acts[len(acts)-1].last = true
		return acts
	}
	a := base
	a.act = "none"
	return []action{a}
}

func parkAction(field string, p Park, fixTo, reader string) action {
	a := action{mode: "unpark", ref: field, unit: p.Unit, repo: p.Repo, pr: p.PR, head: p.Head,
		owner: p.Owner, act: p.Action, holder: p.Holder, holdHead: p.HoldHead, role: p.Role}
	switch p.Role {
	case "fix_to":
		title := fmt.Sprintf("fix %s#%s hold by %s at %s", p.Repo, p.PR, p.Holder, sha8(p.Head))
		if p.Action == "update" {
			title = fmt.Sprintf("update %s#%s branch (CONFLICTING) at %s", p.Repo, p.PR, sha8(p.Head))
		}
		a.cands = []candidate{{to: fixTo, id: p.ID, kind: p.Kind, title: title}}
	case "holder":
		id := reviewID(p.Repo, p.PR, p.Head, p.Holder)
		a.cands = []candidate{
			{to: p.Holder, id: id, kind: "read", title: readTitle(p.Repo, p.PR, p.Head, "")},
			{to: reader, id: id, kind: "read", title: readTitle(p.Repo, p.PR, p.Head, p.Holder), releaseFor: p.Holder},
		}
	default:
		a.cands = []candidate{{to: reader, id: reviewID(p.Repo, p.PR, p.Head, reader), kind: "read",
			title: readTitle(p.Repo, p.PR, p.Head, "")}}
	}
	return a
}

func call(ctx context.Context, c *redis.Client, cfg RouteConfig, a action, fixTo, reader string) (string, bool, error) {
	last := "0"
	if a.last {
		last = "1"
	}
	args := []any{cfg.Sprint, a.mode, a.ref, a.suffix, last, a.unit, a.repo, a.pr, a.head, a.owner,
		a.act, a.holder, a.holdHead, fixTo, reader, a.role, cfg.Actor, strconv.Itoa(len(a.cands))}
	n, _ := strconv.Atoi(a.pr)
	for _, cd := range a.cands {
		sha := task.PayloadSHA(task.PushRequest{
			Sprint: cfg.Sprint, ID: cd.id, Kind: task.Kind(cd.kind), Title: cd.title,
			Effects: task.EffectsNone, Repo: a.repo, Ref: a.repo + "#" + a.pr, PR: n,
			Head: a.head, To: cd.to, Front: true, Priority: 1,
		})
		args = append(args, cd.to, cd.id, cd.kind, cd.title, cd.releaseFor, sha)
	}
	reply, err := c.FCall(ctx, FunctionRoute, nil, args...).StringSlice()
	if err != nil {
		return "", false, fmt.Errorf("%s %s %s: %w", FunctionRoute, a.mode, a.ref, err)
	}
	line := fmt.Sprintf("HOLDROUTE %s %s%s %s", a.mode, a.ref, a.suffix, strings.Join(reply, " "))
	return line, len(reply) > 0 && reply[0] == "RETRY", nil
}

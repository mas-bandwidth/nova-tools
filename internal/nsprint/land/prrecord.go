package land

// prrecord.go: the PR record pr:<name>:<n> follows GitHub (card
// pr-record-follows-github). Measured 2026-09-26 13:29 EDT: the record of
// #4377 held 537c3e22 while GitHub's head was ee0191c4, #4388 held c812f3b3
// against fc91885b, #4399 had no record, and #4371 read ci=pending while its
// head's runner receipt said green. Nothing wrote the record from GitHub:
// ConsumeInbound moved only a sprint unit's head (and only with a unit and a
// mirror), the runner receipt wrote only ci:<repo>:<sha>:gh, and ev:github
// carried no pull_request delivery at all (the receiver's funnel is off).
//
// RecordPRHead is the one writer of what GitHub said about a PR's head. A
// claim is a pull_request delivery (opened, synchronize, reopened, closed:
// source delivery) or a runner receipt of a pull_request run (source runner,
// the event source the fleet has while the funnel is off). It creates the
// record when absent, moves head and state, keeps the head index
// pr:<name>:head:<sha> -> n, and stamps the claim (gh_head, gh_ev, gh_src,
// gh_at) so a reader can see whether the head it is about to read is the
// head GitHub last named (StaleHead).
//
// Order: a delivery applies only when its ev:github id is after the last
// applied claim's (a redelivery or an older entry is KEPT); a runner claim
// applies only when its run id is above the last runner claim's, and never
// for a cancelled run (a newer push cancels the older run of a PR, whose
// always() ci-ok still reports: that receipt must not rewind the head). A
// non-cancelled older run finishing after a newer push's delivery can still
// rewind for one run; the next claim moves it back. No Lua: the bench seat
// that writes the runner receipt may HSET/SADD/SREM on pr:*, not EVAL.

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/prkey"
	"github.com/redis/go-redis/v9"
)

// Claim sources.
const (
	ClaimDelivery = "delivery" // a pull_request delivery on ev:github
	ClaimRunner   = "runner"   // the ci-ok job's receipt of a pull_request run
)

var fullSHARx = regexp.MustCompile(`^[0-9a-f]{40}$`)

// PRClaim is one statement of GitHub's head for a PR.
type PRClaim struct {
	Repo   string // owner/name or name
	N      int
	Head   string // the 40-hex head GitHub named
	Action string // opened, synchronize, reopened, closed; a runner claim is "run"
	Merged bool   // closed only: the PR merged
	Branch string // the head branch, "" when not carried
	Base   string // the base branch, "" when not carried
	Source string // ClaimDelivery or ClaimRunner
	EvID   string // the ev:github entry id of the claim
	RunID  string // runner only: github.run_id (decimal)
	At     int64  // the claim's time, unix ms; 0 means now
}

// PRHeadResult is what RecordPRHead did.
type PRHeadResult struct {
	Key     string
	Outcome string // created, moved, same, kept
	Why     string // kept: why the claim did not apply
	Head    string // the record's head after
	Prev    string // moved: the head before
	State   string // the record's state after
	Stream  string
	Task    string
}

// Line is the one receipt line.
func (r PRHeadResult) Line() string {
	d := func(s string) string {
		if s == "" {
			return "-"
		}
		return s
	}
	line := fmt.Sprintf("PR HEAD %s outcome=%s head=%s prev=%s state=%s stream=%s task=%s",
		r.Key, r.Outcome, d(short8(r.Head)), d(short8(r.Prev)), d(r.State), d(r.Stream), d(r.Task))
	if r.Why != "" {
		line += " why=" + strings.Join(strings.Fields(r.Why), "_")
	}
	return line
}

func short8(s string) string {
	if len(s) > 8 {
		return s[:8]
	}
	return s
}

// prRecFields are the record fields a claim is planned from.
var prRecFields = []string{"head", "state", "stream", "task", "branch", "gh_ev", "gh_run_id"}

// cardOf is the card a branch names, as far as the store shows it.
type cardOf struct {
	ID, Stream string
}

// prHeadPlan is what one claim writes. Pure: planPRHead decides it from the
// record and the claim, so the rules are tested without a store.
type prHeadPlan struct {
	res     PRHeadResult
	set     []any  // HSET fields on the record
	addIdx  string // head to SADD n under
	remIdx  string // old head to SREM n from
	writes  bool
	created bool
}

// planPRHead is the rule. rec is the record's prRecFields ("" absent), card
// the branch's card (zero when none), nowMS the clock.
func planPRHead(key string, rec map[string]string, c PRClaim, card cardOf, nowMS int64) prHeadPlan {
	p := prHeadPlan{res: PRHeadResult{Key: key, Head: rec["head"], State: rec["state"], Stream: rec["stream"], Task: rec["task"]}}
	kept := func(why string) prHeadPlan {
		p.res.Outcome, p.res.Why = "kept", why
		return p
	}
	exists := rec["head"] != ""
	switch c.Source {
	case ClaimDelivery:
		if c.EvID != "" && rec["gh_ev"] != "" && !evAfter(c.EvID, rec["gh_ev"]) {
			return kept("ev " + c.EvID + " is not after " + rec["gh_ev"])
		}
	case ClaimRunner:
		if rec["gh_run_id"] != "" && !decimalAfter(c.RunID, rec["gh_run_id"]) {
			return kept("run " + c.RunID + " is not after " + rec["gh_run_id"])
		}
		if exists && rec["state"] != "" && rec["state"] != "open" {
			return kept("the record is " + rec["state"] + "; a run does not reopen it")
		}
	}
	if exists && rec["state"] == "merged" && rec["head"] != c.Head {
		return kept("the record is merged at " + short8(rec["head"]) + "; its head is final")
	}
	at := c.At
	if at <= 0 {
		at = nowMS
	}
	now := strconv.FormatInt(nowMS, 10)
	p.writes = true
	p.set = append(p.set, "gh_head", c.Head, "gh_src", c.Source, "gh_at", strconv.FormatInt(at, 10), "gh_action", c.Action)
	if c.EvID != "" {
		p.set = append(p.set, "gh_ev", c.EvID)
	}
	if c.Source == ClaimRunner && c.RunID != "" {
		p.set = append(p.set, "gh_run_id", c.RunID)
	}

	state := rec["state"]
	switch {
	case c.Source == ClaimDelivery && c.Action == "closed" && c.Merged:
		state = "merged"
	case c.Source == ClaimDelivery && c.Action == "closed":
		if state != "merged" {
			state = "closed"
		}
	case c.Source == ClaimDelivery:
		if state != "merged" {
			state = "open"
		}
	case state == "":
		state = "open"
	}

	if !exists {
		stream := "-"
		if card.Stream != "" {
			stream = card.Stream
		}
		full := c.Repo
		if f, err := prkey.Full(c.Repo); err == nil {
			full = f
		}
		p.created = true
		p.set = append(p.set, "repo", full, "n", strconv.Itoa(c.N), "head", c.Head, "head_at", now, "head_src", c.Source,
			"state", state, "ci", "pending", "mergeable", "", "created_at", now, "updated_at", now, "stream", stream)
		if c.Branch != "" {
			p.set = append(p.set, "branch", c.Branch)
		}
		if c.Base != "" {
			p.set = append(p.set, "base", c.Base)
		}
		if card.ID != "" {
			p.set = append(p.set, "task", card.ID)
		}
		p.addIdx = c.Head
		p.res.Outcome, p.res.Head, p.res.State, p.res.Stream, p.res.Task = "created", c.Head, state, stream, card.ID
		return p
	}

	if state != rec["state"] {
		p.set = append(p.set, "state", state, "updated_at", now)
	}
	if rec["task"] == "" && card.ID != "" {
		p.set = append(p.set, "task", card.ID)
		p.res.Task = card.ID
	}
	if rec["branch"] == "" && c.Branch != "" {
		p.set = append(p.set, "branch", c.Branch)
	}
	p.res.State = state
	if rec["head"] == c.Head {
		// The head GitHub named is the record's; the index is healed for a
		// record written before it.
		p.addIdx = rec["head"]
		p.res.Outcome = "same"
		return p
	}
	p.set = append(p.set, "head", c.Head, "head_prev", rec["head"], "head_at", now, "head_src", c.Source,
		"ci", "pending", "ci_sha", "", "mergeable", "", "updated_at", now)
	p.addIdx, p.remIdx = c.Head, rec["head"]
	p.res.Outcome, p.res.Prev, p.res.Head = "moved", rec["head"], c.Head
	return p
}

// Validate refuses a claim that cannot be written.
func (c PRClaim) Validate() error {
	if _, _, err := prkey.Split(c.Repo); err != nil {
		return fmt.Errorf("pr head: %w", err)
	}
	if c.N <= 0 {
		return fmt.Errorf("pr head: pull request number %d", c.N)
	}
	if !fullSHARx.MatchString(c.Head) {
		return fmt.Errorf("pr head: head %q is not a 40-hex sha", c.Head)
	}
	switch c.Source {
	case ClaimDelivery:
		switch c.Action {
		case "opened", "synchronize", "reopened", "closed":
		default:
			return fmt.Errorf("pr head: a delivery's action is opened, synchronize, reopened or closed, not %q", c.Action)
		}
	case ClaimRunner:
		if !decimalAfter(c.RunID, "0") {
			return fmt.Errorf("pr head: a runner claim needs its run id, got %q", c.RunID)
		}
	default:
		return fmt.Errorf("pr head: source %q is not delivery or runner", c.Source)
	}
	return nil
}

// RecordPRHead writes one claim: one pipeline to read the record, one read
// of the branch's card when the record is new or names no task, one pipeline
// to write. now nil is time.Now.
func RecordPRHead(ctx context.Context, c redis.Cmdable, cl PRClaim, now func() time.Time) (PRHeadResult, error) {
	if c == nil {
		return PRHeadResult{}, errors.New("pr head: no redis client")
	}
	cl.Head = strings.ToLower(strings.TrimSpace(cl.Head))
	if err := cl.Validate(); err != nil {
		return PRHeadResult{}, err
	}
	if now == nil {
		now = time.Now
	}
	key := prkey.Key(cl.Repo, cl.N)
	vals, err := c.HMGet(ctx, key, prRecFields...).Result()
	if err != nil {
		return PRHeadResult{}, fmt.Errorf("pr head: read %s: %w", key, err)
	}
	rec := map[string]string{}
	for i, f := range prRecFields {
		if s, ok := vals[i].(string); ok {
			rec[f] = s
		}
	}
	var card cardOf
	if rec["task"] == "" {
		branch := cl.Branch
		if branch == "" {
			branch = rec["branch"]
		}
		if card, err = branchCard(ctx, c, cl.Repo, cl.N, branch); err != nil {
			return PRHeadResult{}, err
		}
	}
	p := planPRHead(key, rec, cl, card, now().UnixMilli())
	if !p.writes {
		return p.res, nil
	}
	n := strconv.Itoa(cl.N)
	pipe := c.Pipeline()
	pipe.HSet(ctx, key, p.set...)
	if p.remIdx != "" && fullSHARx.MatchString(p.remIdx) {
		pipe.SRem(ctx, prkey.HeadKey(cl.Repo, p.remIdx), n)
	}
	if p.addIdx != "" && fullSHARx.MatchString(p.addIdx) {
		pipe.SAdd(ctx, prkey.HeadKey(cl.Repo, p.addIdx), n)
	}
	if _, err := pipe.Exec(ctx); err != nil {
		return p.res, fmt.Errorf("pr head: write %s: %w", key, err)
	}
	return p.res, nil
}

// branchCard is the card a branch names: nova/<S>/<label>-a<n> (a bench
// copy's branch) names label; any other <prefix>/<slug> names slug. It is
// the card only when task:<id> exists and names this PR's repo and no other
// PR.
func branchCard(ctx context.Context, c redis.Cmdable, repo string, n int, branch string) (cardOf, error) {
	id := BranchCardID(branch)
	if id == "" {
		return cardOf{}, nil
	}
	vals, err := c.HMGet(ctx, "task:"+id, "stream", "repo", "pr").Result()
	if err != nil {
		return cardOf{}, fmt.Errorf("pr head: read task:%s: %w", id, err)
	}
	str := func(i int) string { s, _ := vals[i].(string); return s }
	stream, trepo, tpr := str(0), str(1), str(2)
	if stream == "" && trepo == "" && tpr == "" {
		return cardOf{}, nil
	}
	if trepo != "" && prkey.Name(trepo) != prkey.Name(repo) {
		return cardOf{}, nil
	}
	if tpr != "" && tpr != "0" && tpr != strconv.Itoa(n) {
		return cardOf{}, nil
	}
	return cardOf{ID: id, Stream: stream}, nil
}

var copyBranchRx = regexp.MustCompile(`^nova/[^/]+/(.+)-a[0-9]+$`)

// BranchCardID is the card id a head branch spells, "" when none.
func BranchCardID(branch string) string {
	branch = strings.TrimPrefix(strings.TrimSpace(branch), "refs/heads/")
	if m := copyBranchRx.FindStringSubmatch(branch); m != nil {
		return m[1]
	}
	i := strings.LastIndex(branch, "/")
	if i < 0 || i == len(branch)-1 {
		return ""
	}
	id := branch[i+1:]
	if strings.ContainsAny(id, " :*?") {
		return ""
	}
	return id
}

// evAfter: stream id a is after b (ms-seq).
func evAfter(a, b string) bool {
	am, as := splitEv(a)
	bm, bs := splitEv(b)
	if am != bm {
		return am > bm
	}
	return as > bs
}

func splitEv(id string) (uint64, uint64) {
	ms, seq, _ := strings.Cut(id, "-")
	m, _ := strconv.ParseUint(ms, 10, 64)
	s, _ := strconv.ParseUint(seq, 10, 64)
	return m, s
}

// decimalAfter: decimal a is above decimal b; a non-decimal a never is.
func decimalAfter(a, b string) bool {
	x, err := strconv.ParseUint(strings.TrimSpace(a), 10, 64)
	if err != nil {
		return false
	}
	y, err := strconv.ParseUint(strings.TrimSpace(b), 10, 64)
	if err != nil {
		return true
	}
	return x > y
}

// StaleError is a record whose head is not the head GitHub last named.
type StaleError struct {
	Key, Head, GitHub, Src, Ev string
}

func (e *StaleError) Error() string {
	d := func(s string) string {
		if s == "" {
			return "-"
		}
		return s
	}
	return fmt.Sprintf("STALE %s head=%s github=%s src=%s ev=%s", e.Key, d(short8(e.Head)), d(short8(e.GitHub)), d(e.Src), d(e.Ev))
}

// StaleHead is nil when the record's head is the head GitHub last named
// (gh_head, written by every claim), or when no claim is recorded (no
// evidence is not negative evidence). Otherwise the typed STALE error.
func StaleHead(key string, rec map[string]string) error {
	gh, head := rec["gh_head"], rec["head"]
	if gh == "" || head == "" || gh == head || rec["state"] == "merged" {
		return nil
	}
	return &StaleError{Key: key, Head: head, GitHub: gh, Src: rec["gh_src"], Ev: rec["gh_ev"]}
}

// FoldCI writes a final GitHub word (green or red) at sha onto every open
// record whose head is sha: the members of the head index, plus extra (the
// PR the receipt names, for a record the index never saw). One pipeline to
// read, one to write; it returns the PR numbers written. why is ci_why.
func FoldCI(ctx context.Context, c redis.Cmdable, repo, sha, word, why string, extra []string, now func() time.Time) ([]string, error) {
	if word != "green" && word != "red" {
		return nil, nil
	}
	sha = strings.ToLower(strings.TrimSpace(sha))
	if !fullSHARx.MatchString(sha) {
		return nil, fmt.Errorf("ci fold: sha %q is not a 40-hex sha", sha)
	}
	members, err := c.SMembers(ctx, prkey.HeadKey(repo, sha)).Result()
	if err != nil && !errors.Is(err, redis.Nil) {
		return nil, fmt.Errorf("ci fold: read %s: %w", prkey.HeadKey(repo, sha), err)
	}
	seen := map[string]bool{}
	var ns []string
	for _, n := range append(members, extra...) {
		if v, err := strconv.Atoi(n); err == nil && v > 0 && !seen[n] {
			seen[n] = true
			ns = append(ns, n)
		}
	}
	if len(ns) == 0 {
		return nil, nil
	}
	pipe := c.Pipeline()
	cmds := make([]*redis.SliceCmd, len(ns))
	for i, n := range ns {
		cmds[i] = pipe.HMGet(ctx, prkey.KeyText(repo, n), "head", "state")
	}
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return nil, fmt.Errorf("ci fold: read records: %w", err)
	}
	if now == nil {
		now = time.Now
	}
	at := strconv.FormatInt(now().UnixMilli(), 10)
	var wrote []string
	pipe = c.Pipeline()
	for i, n := range ns {
		v := cmds[i].Val()
		head, _ := v[0].(string)
		state, _ := v[1].(string)
		if head != sha || state != "open" {
			continue
		}
		pipe.HSet(ctx, prkey.KeyText(repo, n), "ci", word, "ci_sha", sha, "ci_at", at, "ci_why", why)
		wrote = append(wrote, n)
	}
	if len(wrote) == 0 {
		return nil, nil
	}
	if _, err := pipe.Exec(ctx); err != nil {
		return nil, fmt.Errorf("ci fold: write: %w", err)
	}
	return wrote, nil
}

// RecordPRMerged marks an existing record merged at mergeSHA (land pr, after
// GitHub answered the merge). It writes nothing when there is no record.
func RecordPRMerged(ctx context.Context, c redis.Cmdable, repo string, n int, head, mergeSHA string, now func() time.Time) (bool, error) {
	key := prkey.Key(repo, n)
	have, err := c.HGet(ctx, key, "head").Result()
	if err != nil && !errors.Is(err, redis.Nil) {
		return false, fmt.Errorf("pr merged: read %s: %w", key, err)
	}
	if have == "" {
		return false, nil
	}
	if now == nil {
		now = time.Now
	}
	at := strconv.FormatInt(now().UnixMilli(), 10)
	set := []any{"state", "merged", "merge_sha", mergeSHA, "merged_at", at, "updated_at", at}
	if head != "" {
		set = append(set, "merged_head", head)
	}
	if err := c.HSet(ctx, key, set...).Err(); err != nil {
		return false, fmt.Errorf("pr merged: write %s: %w", key, err)
	}
	return true, nil
}

// ClaimOf reads an ev:github pull_request entry as a claim of the PR's head:
// opened, synchronize, reopened or closed, with a PR number and a 40-hex
// head. ok is false for any other entry. Both ev:github consumers call it
// (webhook's ci-github group and ConsumeInbound's land group); the order
// rule makes the second write of one entry a KEPT no-op.
func ClaimOf(m redis.XMessage) (PRClaim, bool) {
	v := func(k string) string { s, _ := m.Values[k].(string); return strings.TrimSpace(s) }
	if v("kind") != "pull_request" {
		return PRClaim{}, false
	}
	switch v("action") {
	case "opened", "synchronize", "reopened", "closed":
	default:
		return PRClaim{}, false
	}
	n, err := strconv.Atoi(v("number"))
	head := strings.ToLower(v("head"))
	if err != nil || n <= 0 || !fullSHARx.MatchString(head) || v("repo") == "" {
		return PRClaim{}, false
	}
	c := PRClaim{Repo: v("repo"), N: n, Head: head, Action: v("action"), Merged: v("merged") == "true",
		Branch: v("branch"), Base: v("base"), Source: ClaimDelivery, EvID: m.ID}
	if t, err := time.Parse(time.RFC3339, v("at")); err == nil {
		c.At = t.UnixMilli()
	}
	return c, true
}

// NoteHead is the claim of a writer that itself pushed to or read GitHub and
// set the record's head (harvest's push, `pr record`'s import): gh_head
// follows head, so StaleHead does not refuse the head that writer knows, and
// the head index moves from prev to head. src names the writer. A head that
// is not a full sha is not indexed and not claimed.
func NoteHead(ctx context.Context, c redis.Cmdable, repo string, n int, prev, head, src string, now func() time.Time) error {
	head = strings.ToLower(strings.TrimSpace(head))
	if c == nil || n <= 0 || !fullSHARx.MatchString(head) {
		return nil
	}
	if now == nil {
		now = time.Now
	}
	num := strconv.Itoa(n)
	pipe := c.Pipeline()
	pipe.HSet(ctx, prkey.Key(repo, n), "gh_head", head, "gh_src", src, "gh_at", strconv.FormatInt(now().UnixMilli(), 10))
	if prev != head && fullSHARx.MatchString(prev) {
		pipe.SRem(ctx, prkey.HeadKey(repo, prev), num)
	}
	pipe.SAdd(ctx, prkey.HeadKey(repo, head), num)
	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("pr head: note %s: %w", prkey.Key(repo, n), err)
	}
	return nil
}

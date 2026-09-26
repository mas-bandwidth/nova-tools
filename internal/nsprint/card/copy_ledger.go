package card

// copy_ledger.go is the bench harness's side of a consumer copy (#3998;
// rowan-new specs/table-moves.md): a bench enrolled as a consumer runs COPIES
// of stream primaries (task:<primary>~<n>), dealt to bench:<b>:cards:ready by
// the reconciler's card-deal duty. The copy's life on the bench is three
// calls of the table moves, each one FCALL of 02_card_move.lua and nothing
// else:
//
//	session start  card work --as bench:<b> --fill   (OpenCopySession: ready -> working,
//	                                                  one call, a token per copy)
//	while it runs  card beat --as bench:<b> --ids    (CopyLedger.Launched and Beat)
//	at its end     card end --id <copy>              (CopyLedger.End, unless the card
//	                                                  already ended itself)
//
// A work copy's end is the boundary step too (#4227): on a DONE harness with
// a commit, End pushes the copy's branch and opens the PR through
// harvestcopy (the bench's push credential, GH_PUSH_TOKEN, reaching
// git only through askpass), writes the PR record and ends the copy
// `--ok --pr <owner/name>#<n> --head <sha>` itself, so the model never pushes
// and never runs card end.
//
// The wrapper (RunWrapper) runs a copy like a sprint card through CopyLedger:
// the copy's card file is RenderCopy of its record, its sprint is CopySprint,
// its label CopyCardLabel and its attempt the copy's number, so the job and
// results directories, the branch and the identity keep their one shape. The
// old bench deal pass does not deal to an enrolled bench (deal.Bench.Enrolled),
// so a bench has one writer of its sets either way.

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/card/harvestcopy"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/prkey"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/taskcard"
	"github.com/redis/go-redis/v9"
)

// CopySprint is the sprint part of a copy's identity, job and results paths.
const CopySprint = "copies"

var copyIDRE = regexp.MustCompile(`^(\S+)~([1-9][0-9]*)$`)

// CopyNumber is n of a copy id <primary>~<n>.
func CopyNumber(id string) (int, error) {
	m := copyIDRE.FindStringSubmatch(id)
	if m == nil {
		return 0, fmt.Errorf("%q is not a copy id <primary>~<n>", id)
	}
	return strconv.Atoi(m[2])
}

var cardLabelBad = regexp.MustCompile(`[^a-z0-9-]+`)

// CopyCardLabel is the copy's card label in the wrapper's [a-z0-9-] form:
// <primary>-c<n>, lower-cased, every other run of characters '-'.
func CopyCardLabel(id string) string {
	l := cardLabelBad.ReplaceAllString(strings.ToLower(strings.Replace(id, "~", "-c", 1)), "-")
	l = strings.Trim(l, "-")
	if len(l) > 80 {
		l = strings.Trim(l[len(l)-80:], "-")
	}
	return l
}

// CopyIdentity is the copy's attempt identity on bench.
func CopyIdentity(id, baseSHA, bench string) (Identity, error) {
	n, err := CopyNumber(id)
	if err != nil {
		return Identity{}, err
	}
	base := "00000000"
	if len(baseSHA) >= 8 && baseRE.MatchString(baseSHA[:8]) {
		base = baseSHA[:8]
	}
	return Identity{Sprint: CopySprint, Label: CopyCardLabel(id), BaseSHA: base, Bench: bench, Attempt: n}, nil
}

// CopyLedger is the wrapper's ledger for one copy on one bench. Token is the
// copy's token from card work; it is handed only to card beat and card end.
type CopyLedger struct {
	Client redis.Cmdable
	Copy   string
	Bench  string
	Token  string
	By     string
	// PushToken is the bench's push credential (harvestcopy.TokenEnv in the
	// nova-card process); empty, a work copy's DONE ends fail reason
	// no-token. It is handed to Harvest only, never to Redis or a receipt.
	PushToken string
	// Askpass is the program git asks for the credential (nova-card itself);
	// "" is this executable.
	Askpass string
	// Harvest is the boundary step (#4227); nil is harvestcopy.Harvest. A
	// test injects one that reaches no forge.
	Harvest func(context.Context, harvestcopy.Request) (harvestcopy.Result, error)
	// Now is the clock the PR record is stamped by; nil is time.Now.
	Now func() time.Time
}

var _ WrapperLedger = (*CopyLedger)(nil)

func (l *CopyLedger) consumer() taskcard.Consumer {
	return taskcard.Consumer{Kind: "bench", Name: l.Bench}
}

func (l *CopyLedger) record(ctx context.Context) (map[string]string, error) {
	if l.Client == nil {
		return nil, errors.New("no store")
	}
	return l.Client.HGetAll(ctx, "task:"+l.Copy).Result()
}

// Card reads the copy's record (one HGETALL) and cfg:card wall_max_min: the
// copy is "dealt" to this bench when it is in bench:<b>:cards:working under
// this token.
func (l *CopyLedger) Card(ctx context.Context) (WrapperCard, error) {
	rec, err := l.record(ctx)
	if err != nil {
		return WrapperCard{}, err
	}
	if len(rec) == 0 {
		return WrapperCard{}, nil
	}
	c := WrapperCard{State: rec["where"], Kind: copyKind(CopyCardFrom(l.Copy, rec)), EstMin: positiveFloat(rec["est"])}
	if kind, name, ok := strings.Cut(rec["consumer"], ":"); ok && kind == "bench" {
		c.Bench = name
	}
	switch {
	case rec["where"] == "working" && rec["token"] != "" && rec["token"] == l.Token:
		c.State = "dealt"
	case rec["where"] == "working":
		c.State = "working under another token"
	}
	id, err := CopyIdentity(l.Copy, rec["base_sha"], c.Bench)
	if err == nil {
		c.Attempt, c.Identity = id.Attempt, id.String()
	}
	if v, err := l.Client.HGet(ctx, CardConfigKey, "wall_max_min").Result(); err == nil {
		c.WallMaxMin = positiveFloat(v)
	}
	return c, nil
}

// Claim is the copy's token: card work handed it to exactly one session, so
// the claim is that the copy is still working under it on this bench.
func (l *CopyLedger) Claim(ctx context.Context, _ string) (int, error) {
	c, err := l.Card(ctx)
	if err != nil {
		return WrapperExitRedis, err
	}
	switch {
	case c.State == "":
		return 5, nil
	case c.State != "dealt":
		return 3, nil
	case c.Bench != l.Bench:
		return 2, nil
	}
	return 0, nil
}

// Launched is the start acknowledgement: the copy's first card beat.
func (l *CopyLedger) Launched(ctx context.Context, _, _ string, _ time.Duration) (int, error) {
	return l.Beat(ctx)
}

// Beat is card beat --as bench:<b> --ids <copy>; a copy no longer working
// here (revoked, expired) is fenced.
func (l *CopyLedger) Beat(ctx context.Context) (int, error) {
	if _, err := taskcard.BeatCopies(ctx, l.Client, l.consumer(), l.Copy); err != nil {
		var r *taskcard.Refused
		if errors.As(err, &r) {
			return WrapperExitFenced, nil
		}
		return WrapperExitRedis, err
	}
	return 0, nil
}

// End is card end --id <copy>, the copy's end as the wrapper writes it. A
// copy that ended itself (a read copy's card ends with its score, a fix
// copy's with its PR head) is already ok|fail and End writes nothing.
//
// A WORK copy never runs card end (its card says so, RenderCopy): a DONE
// harness with a commit is the boundary step (#4227). End hands the commit
// to Harvest (harvestcopy: push the branch to the primary's repository,
// never force; open the PR against the primary's BASE, or find it open),
// writes the PR record pr:<name>:<n> at that head (what TM.finish's ok with
// a PR reads) and ends the copy ok with the PR and the head, which moves the
// primary working -> review and cuts its read copies in that one call. A
// harvest that fails ends the copy fail with the typed reason and the error
// text as the why: no-token (GH_PUSH_TOKEN empty in the wrapper's
// environment), push-refused (the branch moved, no commit, git refused) or
// pr-refused (REST refused); the commit and the branch stay on the record so
// a review can see the work.
//
// Otherwise the wrapper fails the copy: a FAILED harness with the reason and
// the evidence, a DONE read or fix copy that never ended itself with "done
// without card end", a DONE work copy with nothing committed with "done
// without a commit". The result fields go on the record either way: the
// model's two RESULT lines, the commit and the branch.
func (l *CopyLedger) End(ctx context.Context, end WrapperEnd) (int, error) {
	rec, err := l.record(ctx)
	if err != nil {
		return WrapperExitRedis, err
	}
	if w := rec["where"]; w == "ok" || w == "fail" {
		return 0, nil
	}
	committed := end.PushedSHA != "" && end.PushedSHA != NoCommit
	fields := []string{}
	if committed {
		fields = append(fields, "commit", end.PushedSHA)
	}
	branch := ""
	if n, err := CopyNumber(l.Copy); err == nil {
		branch = WrapperBranch(CopySprint, CopyCardLabel(l.Copy), n)
		fields = append(fields, "branch", branch)
	}
	l1, l2 := resultLines(end.ResultsDir)
	if l1 != "" {
		fields = append(fields, "line1", l1)
	}
	if l2 != "" {
		fields = append(fields, "line2", l2)
	}
	c := CopyCardFrom(l.Copy, rec)
	if end.Outcome == "DONE" && committed && c.Leg != "read" && c.Leg != "fix" && branch != "" {
		return l.harvest(ctx, end, c, branch, fields)
	}
	why := end.Reason
	if end.Outcome == "FAILED" && end.Reason == "crash" && end.Exit >= 0 {
		// The harness's exit is the one number a crash leaves (#4234: a
		// copy's record said "crash" and nothing else while the wrapper
		// line held exit=2).
		why = fmt.Sprintf("crash: harness exit %d", end.Exit)
	}
	if end.Outcome == "DONE" {
		why = "done without card end"
		if c.Leg != "read" && c.Leg != "fix" {
			why = "done without a commit"
		}
	}
	if end.Why != "" {
		why += ": " + end.Why
	}
	return l.endCopy(ctx, taskcard.EndRequest{IDs: []string{l.Copy}, Why: why, Fields: fields})
}

// harvest is the work copy's boundary step (End's comment): push, PR, the
// PR record, then the ok end; any refusal is the fail end with its reason.
func (l *CopyLedger) harvest(ctx context.Context, end WrapperEnd, c CopyCard, branch string, fields []string) (int, error) {
	fail := func(reason, text string) (int, error) {
		return l.endCopy(ctx, taskcard.EndRequest{IDs: []string{l.Copy}, Why: reason + ": " + oneLine(text), Fields: fields})
	}
	if strings.TrimSpace(l.PushToken) == "" {
		return fail("no-token", harvestcopy.TokenEnv+" is empty in the wrapper's environment; the bench cannot push "+branch+" or open its PR")
	}
	if end.RepoDir == "" {
		return fail("push-refused", "the commit step's checkout is unknown; nothing to push "+end.PushedSHA+" from")
	}
	h := l.Harvest
	if h == nil {
		h = harvestcopy.Harvest
	}
	res, err := h(ctx, harvestcopy.Request{
		RepoDir: end.RepoDir, SHA: end.PushedSHA, Branch: branch, Repo: c.Repo, Base: c.Base,
		Title: c.Title, Stream: c.Stream, Origin: c.Origin, DoneWhen: c.DoneWhen,
		Token: l.PushToken, Askpass: l.Askpass,
	})
	if err != nil {
		return fail(harvestcopy.Reason(err), err.Error())
	}
	if err := l.recordPR(ctx, res, c); err != nil {
		return WrapperExitRedis, err
	}
	return l.endCopy(ctx, taskcard.EndRequest{IDs: []string{l.Copy}, OK: true, Repo: res.Repo, PR: strconv.Itoa(res.PR),
		Head: res.Head, Fields: fields})
}

// recordPR writes the PR record pr:<name>:<n> the copy's ok end is checked
// against (TM.prok: the record's head is the end's --head and its base the
// copy's), the way `nova-sprint pr record` does (land_stream.lua's record
// op) but in plain commands, since the bench's Redis user may FCALL only the
// card moves: a new record starts open, ci pending, and asks for CI at the
// head (ci:<name>:<head>) when cfg:ci:<name> names checks.
func (l *CopyLedger) recordPR(ctx context.Context, res harvestcopy.Result, c CopyCard) error {
	name := prkey.Name(res.Repo)
	key := prkey.Key(res.Repo, res.PR)
	ciKey := "ci:" + name + ":" + res.Head
	pipe := l.Client.Pipeline()
	head := pipe.HGet(ctx, key, "head")
	checks := pipe.HGet(ctx, "cfg:ci:"+name, "checks")
	ciThere := pipe.Exists(ctx, ciKey)
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return fmt.Errorf("pr record %s: %w", key, err)
	}
	now := l.Now
	if now == nil {
		now = time.Now
	}
	at := strconv.FormatInt(now().UnixMilli(), 10)
	pipe = l.Client.Pipeline()
	if head.Val() == "" {
		pipe.HSet(ctx, key, "repo", res.Repo, "n", strconv.Itoa(res.PR), "state", "open", "ci", "pending",
			"mergeable", "", "created_at", at)
		pipe.HSetNX(ctx, key, "reads", "")
	} else if head.Val() != res.Head {
		pipe.HSet(ctx, key, "ci", "pending", "mergeable", "")
	}
	set := []any{"head", res.Head, "base", c.Base, "branch", res.Branch, "kind", "member", "updated_at", at}
	for k, v := range map[string]string{"base_sha": c.BaseSHA, "stream": c.Stream, "task": c.Primary} {
		if v != "" {
			set = append(set, k, v)
		}
	}
	pipe.HSet(ctx, key, set...)
	if checks.Val() != "" && ciThere.Val() == 0 {
		pipe.HSet(ctx, ciKey, "repo", name, "sha", res.Head, "pr", strconv.Itoa(res.PR), "url", res.URL,
			"checks", checks.Val(), "requested_at", at, "ci", "pending", "attempt", "0", "bench", "",
			"token", "", "lease_until", "0", "claimed_at", "0", "ended_at", "0")
	}
	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("pr record %s: %w", key, err)
	}
	return nil
}

// endCopy is the one card end call of a copy, under the copy's token.
func (l *CopyLedger) endCopy(ctx context.Context, r taskcard.EndRequest) (int, error) {
	r.Token, r.By = l.Token, l.byName()
	_, err := taskcard.End(ctx, l.Client, r)
	if err != nil {
		var r *taskcard.Refused
		if errors.As(err, &r) {
			if strings.HasPrefix(r.Why, "FENCED") {
				return WrapperExitFenced, nil
			}
			return WrapperExitCouldNot, nil
		}
		return WrapperExitRedis, err
	}
	return 0, nil
}

func (l *CopyLedger) byName() string {
	if l.By != "" {
		return l.By
	}
	return "nova-card@" + l.Bench
}

// resultLines are RESULT.md's first two lines under dir, or "".
func resultLines(dir string) (string, string) {
	if dir == "" {
		return "", ""
	}
	f, err := os.Open(filepath.Join(dir, "RESULT.md"))
	if err != nil {
		return "", ""
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	var lines []string
	for len(lines) < 2 && sc.Scan() {
		lines = append(lines, strings.TrimSpace(sc.Text()))
	}
	for len(lines) < 2 {
		lines = append(lines, "")
	}
	return lines[0], lines[1]
}

// CopyLaunch is one copy a session started: its id and token.
type CopyLaunch struct{ Copy, Token string }

// CopySession is what one session start did.
type CopySession struct {
	Enrolled bool
	Launched []CopyLaunch
	// GivenBack are the copies whose launch failed, given back to their
	// primaries in one card cancel.
	GivenBack []string
	Free      int
}

// Line is the session's receipt.
func (s CopySession) Line(bench string) string {
	if !s.Enrolled {
		return "SESSION bench:" + bench + " not enrolled (nova-sprint card consumers --add bench:" + bench + ")"
	}
	return fmt.Sprintf("SESSION bench:%s worked=%d launched=%d given_back=%d free=%d", bench,
		len(s.Launched)+len(s.GivenBack), len(s.Launched), len(s.GivenBack), s.Free)
}

// OpenCopySession is the bench harness's session start (#3998): when
// bench:<b> is enrolled (a member of consumers), card work --as bench:<b>
// --fill (one call), then launch for each copy; a copy whose launch fails is
// given back to its primary in one card cancel. A bench that is not enrolled
// takes nothing: the old deal pass still deals to it.
func OpenCopySession(ctx context.Context, c redis.Cmdable, bench, by string, launch func(CopyLaunch) error) (CopySession, error) {
	var s CopySession
	on, err := c.SIsMember(ctx, taskcard.ConsumersKey, "bench:"+bench).Result()
	if err != nil {
		return s, fmt.Errorf("session bench:%s: %w", bench, err)
	}
	if !on {
		return s, nil
	}
	s.Enrolled = true
	w, err := taskcard.Work(ctx, c, taskcard.Consumer{Kind: "bench", Name: bench}, by, 0, true)
	if err != nil {
		return s, fmt.Errorf("session bench:%s: card work: %w", bench, err)
	}
	s.Free = w.Free
	var why []string
	for i, id := range w.IDs {
		l := CopyLaunch{Copy: id, Token: w.Tokens[i]}
		if err := launch(l); err != nil {
			s.GivenBack = append(s.GivenBack, id)
			why = append(why, id+": "+err.Error())
			continue
		}
		s.Launched = append(s.Launched, l)
	}
	if len(s.GivenBack) > 0 {
		if _, err := taskcard.CancelCards(ctx, c, by, "launch: "+strings.Join(why, "; "), s.GivenBack...); err != nil {
			return s, fmt.Errorf("session bench:%s: give back: %w", bench, err)
		}
	}
	return s, nil
}

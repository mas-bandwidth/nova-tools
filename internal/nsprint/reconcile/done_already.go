package reconcile

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

// The done-already leg (nova-tools#3919), beside harvest -> merging (#3793):
// a card whose model ended it `ABSTAIN done-already <sha>` says its work
// already landed. ns_card_end queued the label on s:<S>:done-already. Each
// pass, for every queued card: the sha must be on the card's base in this
// host's mirror (git merge-base --is-ancestor, never GitHub); then the card's
// origin issue is closed by REST with the evidence comment, and
// ns_card_done_already writes the verdict on the record, takes the label off
// the queue and lands every sprint task naming the issue, in one fenced call.
// A sha the mirror does not know yet, a mirror that is not there, and a forge
// error are retried; a sha that is known and not on the base, or a card with
// no origin issue, is refused on the record. Each card gets one try per
// DoneAlreadyEvery (SET NX PX), so the forge sees at most one close per card
// per interval.

// DoneAlreadyKey is the queue ns_card_end writes: labels scored by ended_at.
func DoneAlreadyKey(sprint string) string { return "s:" + sprint + ":done-already" }

// DoneAlreadyTryKey is one card's try token (SET NX PX DoneAlreadyEvery).
func DoneAlreadyTryKey(sprint, label string) string {
	return "s:" + sprint + ":done-already:try:" + label
}

// DefaultDoneAlreadyEvery is the default interval between tries of one card.
const DefaultDoneAlreadyEvery = 60 * time.Second

// doneAlreadyBatch is the most queued cards one pass takes per sprint.
const doneAlreadyBatch = 32

// IssueCloser closes one issue with one comment by REST. repo is owner/name.
type IssueCloser interface {
	CloseIssue(ctx context.Context, repo string, number int, comment string) error
}

// DoneAlready is the duty.
type DoneAlready struct {
	Client *redis.Client
	Forge  IssueCloser
	// Mirror is the bare mirror of a repo (owner/name or name); "" is none.
	Mirror func(repo string) string
	Every  time.Duration
	// Git bounds each git call; zero is 10 s.
	Git time.Duration

	known []string // the sprint index as the last pass read it
}

// DoneAlreadyOutcome is one card's result in one pass.
type DoneAlreadyOutcome struct {
	Sprint, Label string
	// Action is CLOSED (issue closed, record written), REFUSED (record says
	// why), WAIT (retried on a later pass) or BUDGET (tried this interval).
	Action string
	Why    string
	Moved  int // tasks moved to landed
}

// Line is the receipt line.
func (o DoneAlreadyOutcome) Line() string {
	s := fmt.Sprintf("DONE-ALREADY %s/%s %s", o.Sprint, o.Label, o.Action)
	if o.Action == "CLOSED" {
		s += " landed=" + strconv.Itoa(o.Moved)
	}
	if o.Why != "" {
		s += " why=" + strconv.Quote(o.Why)
	}
	return s
}

var doneAlreadyRE = regexp.MustCompile(`^ABSTAIN done-already ([0-9a-fA-F]{7,40})\b`)

// DoneAlreadySHA is the sha a line 2 `ABSTAIN done-already <sha>` names, or "".
func DoneAlreadySHA(line2 string) string {
	m := doneAlreadyRE.FindStringSubmatch(line2)
	if m == nil {
		return ""
	}
	return strings.ToLower(m[1])
}

var issueURLRE = regexp.MustCompile(`^https?://[^/\s]+/([A-Za-z0-9_.-]+)/([A-Za-z0-9_.-]+)/issues/([1-9][0-9]*)$`)
var issueRefRE = regexp.MustCompile(`^([A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+)#([1-9][0-9]*)$`)

// OriginIssue is the owner/name and number of the issue a card's origin
// names: an issue URL (https://<forge>/<owner>/<name>/issues/<n>, as
// GitHub's) or owner/name#n. ok is false for anything else.
func OriginIssue(origin string) (repo string, n int, ok bool) {
	origin = strings.TrimSpace(origin)
	if m := issueURLRE.FindStringSubmatch(origin); m != nil {
		n, _ = strconv.Atoi(m[3])
		return m[1] + "/" + m[2], n, true
	}
	if m := issueRefRE.FindStringSubmatch(origin); m != nil {
		n, _ = strconv.Atoi(m[2])
		return m[1], n, true
	}
	return "", 0, false
}

type doneAlreadyCard struct {
	label                            string
	line2, origin, repo, base, bench string
}

// Pass runs the leg over every open sprint with the fence token.
func (d *DoneAlready) Pass(ctx context.Context, token string) ([]DoneAlreadyOutcome, error) {
	queues, err := d.queued(ctx)
	if err != nil {
		return nil, fmt.Errorf("done-already: %w", err)
	}
	var out []DoneAlreadyOutcome
	var errs []string
	for _, q := range queues {
		for _, c := range q.cards {
			o, err := d.one(ctx, token, q.sprint, c)
			if errors.Is(err, ErrFenced) {
				return out, err
			}
			if err != nil {
				errs = append(errs, err.Error())
				continue
			}
			out = append(out, o)
		}
	}
	if len(errs) > 0 {
		return out, fmt.Errorf("done-already: %s", strings.Join(errs, "; "))
	}
	return out, nil
}

// doneAlreadyQueue is one open sprint's queued cards.
type doneAlreadyQueue struct {
	sprint string
	cards  []doneAlreadyCard
}

// doneAlreadyRow is one sprint's status and queue head, read together.
type doneAlreadyRow struct {
	status *redis.StringCmd
	queue  *redis.StringSliceCmd
}

// queued reads every open sprint's queue, in sprint name order, and each
// queued card's fields. An idle pass is ONE round trip (#3831): the sprint
// index and each sprint's status and queue, for the sprints the last pass
// found in the index, in one pipeline; a sprint new to the index costs one
// more round trip, once, and queued cards one more for their fields.
func (d *DoneAlready) queued(ctx context.Context) ([]doneAlreadyQueue, error) {
	rows := map[string]doneAlreadyRow{}
	read := func(pipe redis.Pipeliner, names []string) {
		for _, s := range names {
			rows[s] = doneAlreadyRow{
				status: pipe.HGet(ctx, "s:"+s, "status"),
				queue:  pipe.ZRange(ctx, DoneAlreadyKey(s), 0, doneAlreadyBatch-1),
			}
		}
	}
	pipe := d.Client.Pipeline()
	members := pipe.SMembers(ctx, "sprints")
	read(pipe, d.known)
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return nil, err
	}
	names := members.Val()
	sort.Strings(names)
	var fresh []string
	for _, s := range names {
		if _, ok := rows[s]; !ok {
			fresh = append(fresh, s)
		}
	}
	if len(fresh) > 0 {
		pipe := d.Client.Pipeline()
		read(pipe, fresh)
		if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
			return nil, err
		}
	}
	d.known = names
	var queues []doneAlreadyQueue
	var cmds [][]*redis.SliceCmd
	pipe = d.Client.Pipeline()
	for _, s := range names {
		r := rows[s]
		labels := r.queue.Val()
		if r.status.Val() != "open" || len(labels) == 0 {
			continue
		}
		q := doneAlreadyQueue{sprint: s, cards: make([]doneAlreadyCard, len(labels))}
		cs := make([]*redis.SliceCmd, len(labels))
		for i, l := range labels {
			q.cards[i].label = l
			cs[i] = pipe.HMGet(ctx, "s:"+s+":card:"+l, "result_line2", "origin", "repo", "base", "bench")
		}
		queues = append(queues, q)
		cmds = append(cmds, cs)
	}
	if len(queues) == 0 {
		return nil, nil
	}
	if _, err := pipe.Exec(ctx); err != nil {
		return nil, err
	}
	for qi, q := range queues {
		for i := range q.cards {
			v := cmds[qi][i].Val()
			str := func(j int) string { x, _ := v[j].(string); return x }
			q.cards[i].line2, q.cards[i].origin, q.cards[i].repo, q.cards[i].base, q.cards[i].bench = str(0), str(1), str(2), str(3), str(4)
		}
	}
	return queues, nil
}

func (d *DoneAlready) one(ctx context.Context, token, s string, c doneAlreadyCard) (DoneAlreadyOutcome, error) {
	o := DoneAlreadyOutcome{Sprint: s, Label: c.label}
	sha := DoneAlreadySHA(c.line2)
	if sha == "" {
		return d.record(ctx, token, s, c, "refused", "", "line 2 names no sha: "+c.line2, o)
	}
	repo, n, ok := OriginIssue(c.origin)
	if !ok {
		return d.record(ctx, token, s, c, "refused", sha, "the card has no origin issue to close (origin "+strconv.Quote(c.origin)+")", o)
	}
	every := d.Every
	if every <= 0 {
		every = DefaultDoneAlreadyEvery
	}
	got, err := d.Client.SetNX(ctx, DoneAlreadyTryKey(s, c.label), sha, every).Result()
	if err != nil {
		return o, fmt.Errorf("done-already %s/%s: try: %w", s, c.label, err)
	}
	if !got {
		o.Action = "BUDGET"
		return o, nil
	}
	base := c.base
	if base == "" {
		base = "dev"
	}
	mirrorRepo := c.repo
	if mirrorRepo == "" {
		mirrorRepo = repo
	}
	on, why := d.onBase(ctx, mirrorRepo, sha, base)
	switch on {
	case "no":
		return d.record(ctx, token, s, c, "refused", sha, why, o)
	case "wait":
		o.Action, o.Why = "WAIT", why
		return o, nil
	}
	evidence := fmt.Sprintf("DONE-ALREADY: %s on %s (card %s, bench %s)", sha, base, c.label, c.bench)
	if d.Forge == nil {
		o.Action, o.Why = "WAIT", "no forge"
		return o, nil
	}
	// A deposed instance must not reach the forge: the fence is read first,
	// and ns_card_done_already checks it again before it writes.
	if held, err := d.Client.HGet(ctx, "lease:reconciler", "token").Result(); err != nil || held != token {
		return o, fmt.Errorf("done-already %s/%s: %w", s, c.label, ErrFenced)
	}
	if err := d.Forge.CloseIssue(ctx, repo, n, evidence); err != nil {
		o.Action, o.Why = "WAIT", "close "+repo+"#"+strconv.Itoa(n)+": "+err.Error()
		return o, nil
	}
	return d.record(ctx, token, s, c, "closed", sha, evidence, o)
}

// record is the one fenced write: ns_card_done_already.
func (d *DoneAlready) record(ctx context.Context, token, s string, c doneAlreadyCard, verdict, sha, evidence string, o DoneAlreadyOutcome) (DoneAlreadyOutcome, error) {
	reply, err := d.Client.FCall(ctx, "ns_card_done_already", nil, token, s, c.label, verdict, sha, c.base, evidence).StringSlice()
	if err != nil {
		return o, fmt.Errorf("done-already %s/%s: %w", s, c.label, err)
	}
	switch {
	case len(reply) > 0 && reply[0] == "FENCED":
		return o, fmt.Errorf("done-already %s/%s: %w", s, c.label, ErrFenced)
	case len(reply) > 0 && reply[0] == "NOTHING":
		o.Action = "NOTHING"
		return o, nil
	case len(reply) < 5 || reply[0] != "OK":
		return o, fmt.Errorf("done-already %s/%s: reply %q", s, c.label, reply)
	}
	o.Action, o.Why = strings.ToUpper(verdict), evidence
	o.Moved, _ = strconv.Atoi(reply[2])
	return o, nil
}

// onBase is yes, no (the sha is in the mirror and not on base) or wait (no
// mirror, or the mirror does not know the sha or the base yet), with why.
func (d *DoneAlready) onBase(ctx context.Context, repo, sha, base string) (string, string) {
	mirror := ""
	if d.Mirror != nil {
		mirror = d.Mirror(repo)
	}
	if mirror == "" {
		return "wait", "no mirror of " + repo + " on this host"
	}
	if _, err := os.Stat(filepath.Join(mirror, "objects")); err != nil {
		return "wait", "no mirror of " + repo + " at " + mirror
	}
	limit := d.Git
	if limit <= 0 {
		limit = 10 * time.Second
	}
	gctx, cancel := context.WithTimeout(ctx, limit)
	defer cancel()
	git := func(args ...string) error {
		cmd := exec.CommandContext(gctx, "git", append([]string{"--git-dir", mirror}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
		return cmd.Run()
	}
	ref := "refs/heads/" + base
	if git("cat-file", "-e", sha+"^{commit}") != nil {
		return "wait", sha + " is not in the mirror of " + repo + " yet"
	}
	if git("rev-parse", "--verify", "-q", ref) != nil {
		return "wait", "the mirror of " + repo + " has no " + base
	}
	err := git("merge-base", "--is-ancestor", sha, ref)
	var exit *exec.ExitError
	switch {
	case err == nil:
		return "yes", ""
	case errors.As(err, &exit) && exit.ExitCode() == 1:
		return "no", sha + " is not on " + base + " in the mirror of " + repo
	default:
		return "wait", "git merge-base: " + err.Error()
	}
}

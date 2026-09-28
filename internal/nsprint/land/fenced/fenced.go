// Package fenced is the stream-PR lander of nova-tools #2942 rev 6: nova-sprint
// land run takes offered stream PRs (stream/<slug> -> <base>) from Redis
// oldest first, checks four facts at each head H (ci:<repo>:<H> OK, a clean
// merge with the base tip T, no open typed HOLD at H, a body first line that
// is not HOLD/BLOCKED), and lands each as one atomic git push of the base
// fast-forward plus a child fence commit under a lease on refs/nova-land/<base>.
// A PR is landed only when the mirror's base has a first-parent commit whose
// second parent is H. It makes no REST or GraphQL call: its only GitHub
// traffic is one fetch per pass and one push per arm or landing.
//
// One writer per (repo, base): the Redis lease s:<S>:land:writer:<repo>:<base>
// carries a gen from land:gen, and the receiver's fence ref carries the same
// gen, so once a successor arms, a stale writer's push is refused whole by the
// remote. The Redis functions are in internal/nsprint/fn/lua/land_take.lua.
package fenced

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/safepath"
	"github.com/redis/go-redis/v9"
)

// PushBound is the push's deadline, and the lease a worker must still hold
// (PTTL) before it claims and pushes (lease 120 s, renewed every pass).
const PushBound = 45 * time.Second

// Hooks are seams for the controls: each is called, when set, at its point in
// a pass, so a test can pause a worker there.
type Hooks struct {
	BeforeArm   func(gen int64)
	BeforeClaim func(n string)
	AfterClaim  func(n string)
}

// Config is one worker's row and seams.
type Config struct {
	Client   *redis.Client
	Sprint   string
	Repo     string // owner/name
	Base     string
	Remote   string
	Mirror   string // a bare repository; never written
	Runner   string // <host>:<pid>
	Max      int
	DryRun   bool
	Git      string // default "git"
	TmpRoot  string // default os.TempDir()
	Identity Identity
	Env      []string // extra git env (GIT_ASKPASS and its token variable)
	Hooks    Hooks
}

// FatalError is a pass that could not run and wrote nothing: exit 2.
type FatalError struct{ Msg string }

func (e *FatalError) Error() string { return e.Msg }

// Candidate is one offered PR as a pass saw it.
type Candidate struct {
	N, Body, Stream, State, Head, Reason string
}

// Result is one pass.
type Result struct {
	Run                              string
	Gen                              int64
	Offered, Skipped, Pushed, Landed int
	Fenced                           bool
	Writer, WriterGen                string
	PushRefused                      string // a refusal that is not the fence or base-moved
	Candidates                       []Candidate
}

// Line is the pass's receipt line.
func (r Result) Line(repo, base string) string {
	if r.Writer != "" {
		return fmt.Sprintf("LAND repo=%s base=%s writer=%s gen=%s", repo, base, r.Writer, r.WriterGen)
	}
	run := r.Run
	if run == "" {
		run = "-"
	}
	f := 0
	if r.Fenced {
		f = 1
	}
	return fmt.Sprintf("LAND run=%s repo=%s base=%s gen=%d offered=%d skipped=%d pushed=%d landed=%d fenced=%d",
		run, repo, base, r.Gen, r.Offered, r.Skipped, r.Pushed, r.Landed, f)
}

// Worker lands one row. It remembers the gen it armed and the fence it last
// pushed, so a worker looping every second arms once per gen.
type Worker struct {
	cfg      Config
	armedGen int64
	fence    string
}

// New returns a worker for cfg.
func New(cfg Config) *Worker {
	if cfg.Git == "" {
		cfg.Git = "git"
	}
	if cfg.Max <= 0 {
		cfg.Max = 8
	}
	if cfg.Identity.Name == "" {
		cfg.Identity = Identity{Name: "nova-sprint land", Email: "nova-land@invalid"}
	}
	return &Worker{cfg: cfg}
}

// Renew is ns_land_renew at gen: the lease's PTTL in ms, or fenced.
func (w *Worker) Renew(ctx context.Context, gen int64) (pttl int64, fenced bool, err error) {
	res, err := w.cfg.Client.FCall(ctx, "ns_land_renew", nil, w.cfg.Sprint, w.cfg.Repo, w.cfg.Base,
		w.cfg.Runner, strconv.FormatInt(gen, 10)).Slice()
	if err != nil {
		return 0, false, err
	}
	if fmt.Sprint(res[0]) != "OK" {
		return 0, true, nil
	}
	v, _ := res[1].(int64)
	return v, false, nil
}

// Release is ns_land_release at gen; fenced when the lease is not this
// worker's at gen (nothing is written then).
func (w *Worker) Release(ctx context.Context, gen int64) (fenced bool, err error) {
	res, err := w.cfg.Client.FCall(ctx, "ns_land_release", nil, w.cfg.Sprint, w.cfg.Repo, w.cfg.Base,
		w.cfg.Runner, strconv.FormatInt(gen, 10)).Slice()
	if err != nil {
		return false, err
	}
	return fmt.Sprint(res[0]) != "OK", nil
}

// Mark is ns_land_mark; it returns the function's status word (OK, FENCED).
func Mark(ctx context.Context, c redis.Cmdable, sprint, repo, base, n string, gen int64, run, kind, a1, a2 string) (string, error) {
	res, err := c.FCall(ctx, "ns_land_mark", nil, sprint, repo, base, n, strconv.FormatInt(gen, 10), run, kind, a1, a2).Slice()
	if err != nil {
		return "", err
	}
	return fmt.Sprint(res[0]), nil
}

type pass struct {
	w    *Worker
	g    git
	res  Result
	gen  int64
	run  string
	T    string
	cand []Candidate
}

// Pass runs one pass: read the queue, fetch, check the remote fence, take the
// lease, arm on a new gen, gate and push oldest first, then walk the mirror
// for landings. A FatalError means nothing was written.
func (w *Worker) Pass(ctx context.Context) (Result, error) {
	c := w.cfg
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	qkey := fmt.Sprintf("s:%s:land:queue:%s:%s", c.Sprint, c.Repo, c.Base)
	queued, err := c.Client.ZRange(ctx, qkey, 0, int64(c.Max-1)).Result()
	if err != nil {
		return Result{}, err
	}
	root := c.TmpRoot
	if root == "" {
		root = os.TempDir()
	}
	tmp, err := os.MkdirTemp(root, "nova-land-")
	if err != nil {
		return Result{}, err
	}
	defer func() { _ = safepath.RemoveUnder(root, tmp) }()
	p := &pass{w: w, g: git{bin: c.Git, dir: tmp, env: c.Env}}
	if err := p.fetch(ctx, tmp, queued); err != nil {
		return Result{}, err
	}
	remoteFence := p.g.revParse(ctx, "refs/nova-land/"+c.Base)
	var remoteGen int64
	var remoteRunner string
	if remoteFence != "" {
		v, r, ok := p.g.fenceGen(ctx, remoteFence)
		remoteRunner = r
		if !ok {
			return Result{}, &FatalError{Msg: "fence unreadable"}
		}
		remoteGen = v
		cur, err := c.Client.Get(ctx, "land:gen").Int64()
		if err != nil && !errors.Is(err, redis.Nil) {
			return Result{}, err
		}
		if remoteGen > cur {
			return Result{}, &FatalError{Msg: fmt.Sprintf("fence gen %d ahead of land:gen %d", remoteGen, cur)}
		}
	}
	if c.DryRun {
		return p.dry(ctx, queued)
	}
	if err := p.take(ctx); err != nil || p.res.Writer != "" {
		return p.res, err
	}
	if p.gen != w.armedGen {
		if c.Hooks.BeforeArm != nil {
			c.Hooks.BeforeArm(p.gen)
		}
		if fenced, err := p.arm(ctx, remoteFence, remoteGen, remoteRunner); err != nil || fenced {
			p.res.Fenced = fenced
			return p.res, err
		}
	}
	// A fence at this gen by this runner is this worker's own last push (an
	// ambiguous push timeout may have applied it): it is the current fence.
	if remoteFence != "" && remoteGen == p.gen && remoteRunner == c.Runner {
		w.fence = remoteFence
	}
	if p.run == "" {
		return p.res, nil
	}
	if err := p.gate(ctx); err != nil || p.res.Fenced {
		return p.res, err
	}
	err = p.walk(ctx)
	return p.res, err
}

// fetch makes the pass's repository (objects borrowed from the mirror through
// alternates, so the mirror is never written) and fetches T, the fences and
// each queued PR's head in one fetch; if a PR head is gone the fetch is
// retried without the PR heads and those PRs skip head:unfetched.
func (p *pass) fetch(ctx context.Context, tmp string, queued []string) error {
	c := p.w.cfg
	if _, err := p.g.run(ctx, nil, "", "init", "-q", "--bare"); err != nil {
		return err
	}
	objs, err := filepath.Abs(filepath.Join(c.Mirror, "objects"))
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(tmp, "objects", "info", "alternates"), []byte(objs+"\n"), 0o644); err != nil {
		return err
	}
	base := []string{"fetch", "-q", "--no-tags", "--no-write-fetch-head", c.Remote,
		"+refs/heads/" + c.Base + ":refs/heads/" + c.Base, "+refs/nova-land/*:refs/nova-land/*"}
	args := append([]string{}, base...)
	for _, n := range queued {
		args = append(args, "+refs/pull/"+n+"/head:refs/pull/"+n+"/head")
	}
	if _, err := p.g.run(ctx, nil, "", args...); err != nil {
		if len(queued) == 0 {
			return err
		}
		if _, err := p.g.run(ctx, nil, "", base...); err != nil {
			return err
		}
	}
	p.T = p.g.revParse(ctx, "refs/heads/"+c.Base)
	if p.T == "" {
		return fmt.Errorf("fetch: %s has no refs/heads/%s", c.Remote, c.Base)
	}
	return nil
}

func (p *pass) take(ctx context.Context) error {
	c := p.w.cfg
	res, err := c.Client.FCall(ctx, "ns_land_take", nil, c.Sprint, c.Repo, c.Base, c.Runner, strconv.Itoa(c.Max)).Slice()
	if err != nil {
		return err
	}
	if fmt.Sprint(res[0]) == "WRITER" {
		p.res.Writer, p.res.WriterGen = fmt.Sprint(res[1]), fmt.Sprint(res[2])
		return nil
	}
	p.gen, _ = strconv.ParseInt(fmt.Sprint(res[1]), 10, 64)
	p.run = fmt.Sprint(res[3])
	p.res.Gen, p.res.Run = p.gen, p.run
	for i := 4; i+3 < len(res); i += 4 {
		p.cand = append(p.cand, Candidate{N: fmt.Sprint(res[i]), Body: fmt.Sprint(res[i+1]),
			Stream: fmt.Sprint(res[i+2]), State: fmt.Sprint(res[i+3])})
	}
	p.res.Offered = len(p.cand)
	return nil
}

// arm is Fencing (f): once per gen, before any fact is read. A remote fence
// at this gen or newer is FENCED; otherwise push this gen's fence under a
// lease on the fetched value (absent: must not exist). A rejection is FENCED.
//
// A remote fence at this very gen written by this runner is this worker's own
// arm (a new Worker in the same process renewing its live lease): it is
// adopted as the current fence, not pushed again.
func (p *pass) arm(ctx context.Context, remoteFence string, remoteGen int64, remoteRunner string) (bool, error) {
	c := p.w.cfg
	if remoteFence != "" && remoteGen == p.gen && remoteRunner == c.Runner {
		p.w.armedGen, p.w.fence = p.gen, remoteFence
		return false, nil
	}
	if remoteFence != "" && remoteGen >= p.gen {
		return true, nil
	}
	f, err := p.g.fenceCommit(ctx, FenceMessage(c.Repo, c.Base, p.gen, c.Runner), "")
	if err != nil {
		return false, err
	}
	ref := "refs/nova-land/" + c.Base
	pctx, cancel := context.WithTimeout(ctx, PushBound)
	defer cancel()
	code, out, errOut, err := p.g.status(pctx, "push", "--porcelain", "--force-with-lease="+ref+":"+remoteFence,
		c.Remote, f+":"+ref)
	if err != nil {
		return false, err
	}
	if code != 0 {
		for _, l := range parsePorcelain(out) {
			if l.flag == '!' {
				return true, nil
			}
		}
		return false, fmt.Errorf("arm push: %s", firstLine(errOut))
	}
	p.w.armedGen, p.w.fence = p.gen, f
	return false, nil
}

type skip struct{ n, reason, head string }

// gate walks the candidates oldest first: facts at H, then claim, merge and
// the fenced atomic push for each PR that passes. Skips are marked in one
// pipeline at the end.
func (p *pass) gate(ctx context.Context) error {
	c := p.w.cfg
	var skips []skip
	defer func() { p.res.Skipped = len(skips) }()
	heads := make([]string, len(p.cand))
	fargs := []interface{}{c.Sprint, c.Repo}
	for i := range p.cand {
		heads[i] = p.g.revParse(ctx, "refs/pull/"+p.cand[i].N+"/head")
		p.cand[i].Head = heads[i]
		fargs = append(fargs, p.cand[i].N, heads[i])
	}
	var facts []interface{}
	if len(p.cand) > 0 {
		var err error
		facts, err = c.Client.FCallRO(ctx, "ns_land_facts", nil, fargs...).Slice()
		if err != nil {
			return err
		}
	}
	stopped := false
	for i, cd := range p.cand {
		H := heads[i]
		if stopped {
			break
		}
		reason := ""
		switch {
		case H == "":
			reason = "head:unfetched"
		case p.g.isAncestor(ctx, H, p.T):
			reason = "in-base"
		case fmt.Sprint(facts[3*i]) != "":
			reason = fmt.Sprint(facts[3*i])
		}
		var tree string
		if reason == "" {
			t, clean, files, err := p.g.mergeTree(ctx, p.T, H)
			if err != nil {
				return err
			}
			if !clean {
				first := "?"
				if len(files) > 0 {
					first = files[0]
				}
				reason = fmt.Sprintf("dirty:%s+%d", first, len(files))
			}
			tree = t
		}
		if reason == "" {
			if r := fmt.Sprint(facts[3*i+1]); r != "" {
				reason = r
			} else if r := fmt.Sprint(facts[3*i+2]); r != "" {
				reason = r
			}
		}
		if reason != "" {
			skips = append(skips, skip{cd.N, reason, H})
			p.cand[i].Reason = reason
			continue
		}
		outcome, err := p.land(ctx, cd, H, tree)
		if err != nil {
			return err
		}
		switch outcome.kind {
		case "fenced":
			p.res.Fenced = true
			return nil
		case "pushed":
			p.res.Pushed++
		case "stop":
			stopped = true
			if outcome.reason != "" {
				skips = append(skips, skip{cd.N, outcome.reason, H})
			}
		case "skip":
			skips = append(skips, skip{cd.N, outcome.reason, H})
		}
		p.cand[i].Reason = outcome.reason
	}
	pipe := c.Client.Pipeline()
	cmds := make([]*redis.Cmd, 0, len(skips)+1)
	list := make([]string, 0, len(skips))
	for _, s := range skips {
		cmds = append(cmds, pipe.FCall(ctx, "ns_land_mark", nil, c.Sprint, c.Repo, c.Base, s.n, strconv.FormatInt(p.gen, 10),
			p.run, "skip", s.reason, s.head))
		list = append(list, "#"+s.n+":"+s.reason)
	}
	cmds = append(cmds, pipe.FCall(ctx, "ns_land_mark", nil, c.Sprint, c.Repo, c.Base, "", strconv.FormatInt(p.gen, 10),
		p.run, "skips", strings.Join(list, " "), ""))
	if _, err := pipe.Exec(ctx); err != nil {
		return err
	}
	for _, cmd := range cmds {
		if v, _ := cmd.Slice(); len(v) > 0 && fmt.Sprint(v[0]) == "FENCED" {
			p.res.Fenced = true
		}
	}
	return nil
}

type outcome struct{ kind, reason string }

// land is steps (d)-(g) for one PR: renew (PTTL above the push bound), claim,
// build the merge and the child fence, and push both atomically under the
// fence lease.
func (p *pass) land(ctx context.Context, cd Candidate, H, tree string) (outcome, error) {
	c := p.w.cfg
	pttl, fenced, err := p.w.Renew(ctx, p.gen)
	if err != nil {
		return outcome{}, err
	}
	if fenced {
		return outcome{kind: "fenced"}, nil
	}
	if pttl <= PushBound.Milliseconds() {
		return outcome{kind: "stop", reason: "lease-short"}, nil
	}
	if c.Hooks.BeforeClaim != nil {
		c.Hooks.BeforeClaim(cd.N)
	}
	res, err := c.Client.FCall(ctx, "ns_land_enq_claim", nil, c.Sprint, c.Repo, c.Base, cd.N, H, c.Runner,
		strconv.FormatInt(p.gen, 10)).Slice()
	if err != nil {
		return outcome{}, err
	}
	switch st := fmt.Sprint(res[0]); st {
	case "FENCED":
		return outcome{kind: "fenced"}, nil
	case "CI", "HOLD", "BODY":
		return outcome{kind: "skip", reason: fmt.Sprint(res[1])}, nil
	case "OK", "DUP", "TAKEOVER":
	default:
		return outcome{}, fmt.Errorf("ns_land_enq_claim: %s", st)
	}
	if c.Hooks.AfterClaim != nil {
		c.Hooks.AfterClaim(cd.N)
	}
	owner, _, _ := strings.Cut(c.Repo, "/")
	msg := fmt.Sprintf("Merge pull request #%s from %s/stream/%s\n\nnova-sprint land run=%s gen=%d", cd.N, owner, cd.Stream, p.run, p.gen)
	M, err := p.g.run(ctx, idEnv(c.Identity, ""), "", "commit-tree", tree, "-p", p.T, "-p", H, "-m", msg)
	if err != nil {
		return outcome{}, err
	}
	h8 := H
	if len(h8) > 8 {
		h8 = h8[:8]
	}
	next, err := p.g.fenceCommit(ctx, FenceMessage(c.Repo, c.Base, p.gen, c.Runner)+" merge="+cd.N+"@"+h8, p.w.fence)
	if err != nil {
		return outcome{}, err
	}
	ref, head := "refs/nova-land/"+c.Base, "refs/heads/"+c.Base
	pctx, cancel := context.WithTimeout(ctx, PushBound)
	defer cancel()
	code, out, errOut, err := p.g.status(pctx, "push", "--atomic", "--porcelain",
		"--force-with-lease="+ref+":"+p.w.fence, c.Remote, M+":"+head, next+":"+ref)
	if pctx.Err() != nil {
		return outcome{kind: "stop"}, nil
	}
	if err != nil {
		return outcome{}, err
	}
	if code != 0 {
		var fenceRej, baseRej string
		for _, l := range parsePorcelain(out) {
			if l.flag != '!' || strings.Contains(l.summary, "atomic push failed") {
				continue
			}
			if l.dst == ref {
				fenceRej = l.summary
			}
			if l.dst == head {
				baseRej = l.summary
			}
		}
		switch {
		case fenceRej != "":
			return outcome{kind: "fenced"}, nil
		case baseRej != "":
			return outcome{kind: "stop", reason: "base-moved"}, nil
		}
		p.res.PushRefused = firstLine(errOut)
		return outcome{kind: "skip", reason: "push:" + firstLine(errOut)}, nil
	}
	p.w.fence = next
	p.T = M
	st, err := Mark(ctx, c.Client, c.Sprint, c.Repo, c.Base, cd.N, p.gen, p.run, "landing", H, M)
	if err != nil {
		return outcome{}, err
	}
	if st == "FENCED" {
		return outcome{kind: "fenced"}, nil
	}
	return outcome{kind: "pushed"}, nil
}

// walk records landings from the mirror alone: a first-parent commit of the
// mirror's base whose second parent is a candidate's H is that PR's merge.
// Subjects are never read.
func (p *pass) walk(ctx context.Context) error {
	c := p.w.cfg
	mg := git{bin: c.Git, dir: c.Mirror, env: c.Env}
	out, err := mg.run(ctx, nil, "", "log", "--first-parent", "-n", "1000", "--format=%H %P", "refs/heads/"+c.Base)
	if err != nil {
		return err
	}
	merges := map[string]string{}
	for _, l := range strings.Split(out, "\n") {
		f := strings.Fields(l)
		if len(f) >= 3 {
			if _, seen := merges[f[2]]; !seen {
				merges[f[2]] = f[0]
			}
		}
	}
	for _, cd := range p.cand {
		M, ok := merges[cd.Head]
		if cd.Head == "" || !ok {
			continue
		}
		st, err := Mark(ctx, c.Client, c.Sprint, c.Repo, c.Base, cd.N, p.gen, p.run, "landed", cd.Head, M)
		if err != nil {
			return err
		}
		if st == "FENCED" {
			p.res.Fenced = true
			return nil
		}
		p.res.Landed++
	}
	return nil
}

// dry prints the candidates with their facts and writes nothing: no lease,
// no push.
func (p *pass) dry(ctx context.Context, queued []string) (Result, error) {
	c := p.w.cfg
	fargs := []interface{}{c.Sprint, c.Repo}
	for _, n := range queued {
		H := p.g.revParse(ctx, "refs/pull/"+n+"/head")
		p.cand = append(p.cand, Candidate{N: n, Head: H})
		fargs = append(fargs, n, H)
	}
	if len(queued) > 0 {
		facts, err := c.Client.FCallRO(ctx, "ns_land_facts", nil, fargs...).Slice()
		if err != nil {
			return Result{}, err
		}
		for i := range p.cand {
			var rs []string
			for j := 0; j < 3; j++ {
				if r := fmt.Sprint(facts[3*i+j]); r != "" {
					rs = append(rs, r)
				}
			}
			if p.cand[i].Head == "" {
				rs = append(rs, "head:unfetched")
			} else if _, clean, files, err := p.g.mergeTree(ctx, p.T, p.cand[i].Head); err == nil && !clean && len(files) > 0 {
				rs = append(rs, fmt.Sprintf("dirty:%s+%d", files[0], len(files)))
			}
			p.cand[i].Reason = strings.Join(rs, ",")
		}
	}
	p.res.Offered = len(p.cand)
	p.res.Candidates = p.cand
	return p.res, nil
}

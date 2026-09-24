// Package publish is the lander's publisher (#3139 rev 7 section 7, build
// item B7): one per repo and base, under land:<repo>:<base>:lease, it turns the
// front GREEN batch of the chain into one fast-forward of the base.
//
// One LandFront call is one pass of 7.1:
//
//  1. the front batch is GREEN and its from_tip equals the tip record, else the
//     chain is voided from it for re-planning (L22);
//  2. ns_land_intent, the linearization point (2.3);
//  3. the train rebuilt from the publisher's own mirror and compared with the
//     receipt's train_head and train_tree;
//  4. the compare-and-swap push, git push --atomic
//     --force-with-lease=refs/heads/<base>:<from_tip>, through PushPrefix (the
//     nova-lander credential by nova-secrets exec);
//  5. verify read-only (ls-remote equals train_head, the tree equals the gated
//     train_tree), ns_pub_step verified;
//  6. ns_land.
//
// An unresolved intent on the base (pub:active) is resumed before anything else
// (takeover, 2.3): 7.2 reconciliation first, then the same push, then ns_land.
// After an ambiguous push only git ls-remote runs, up to 3 times (2, 5, 10 s),
// and its answer picks the path: pushed, verified-descendant, the same push
// again (on the next tick when this tick already pushed: the intent stays, L19),
// or dead. Every step after the intent carries the lease fence, so a resumed
// stale publisher writes nothing and gets FENCED (L28 iii).
package publish

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/land"
	"github.com/redis/go-redis/v9"
)

// Outcome is what one LandFront pass did.
type Outcome string

const (
	Idle     Outcome = "IDLE"             // no batch on the chain and no unresolved intent
	Wait     Outcome = "WAIT"             // the front batch has no GREEN receipt yet
	Landed   Outcome = "LANDED"           // ns_land wrote the landing
	Already  Outcome = "ALREADY"          // ns_land found the batch landed: nothing written (L6)
	Refused  Outcome = "REFUSED"          // ns_land_intent refused (hold, head, policy, inbound); nothing pushed
	Fenced   Outcome = "FENCED"           // lease or writer generation not ours: STALE, nothing written
	Retry    Outcome = "RETRY"            // the push failed and the remote is still from_tip: the intent stays
	Dead     Outcome = "DEAD"             // the remote moved to a sha without the train: intent dead, chain voided
	Voided   Outcome = "VOIDED"           // the front's from_tip is not the tip record: chain voided, no intent
	Frozen   Outcome = "FROZEN"           // ls-remote failed 3 times: no blind retry, the intent stays
	Mismatch Outcome = "NONDETERMINISTIC" // the rebuilt train differs from the receipt: re-planned
)

// Point names a place in one pass where Hook runs: the kill points of L11 and
// the pauses of L28. A non-nil error from Hook ends the pass with that error,
// as a kill would.
type Point string

const (
	BeforeIntent Point = "before-intent"
	AfterIntent  Point = "after-intent" // K3
	AfterPush    Point = "after-push"   // K4: pushed, before any record of it
	AfterVerify  Point = "after-verify" // K5
	AfterLand    Point = "after-land"   // K6
)

// Config is one publisher: repo, base, the lease value it holds and its mirror.
type Config struct {
	Redis  *redis.Client
	Sprint string
	Repo   string
	Base   string
	Lease  string // <gen>:<token>, the value of land:<repo>:<base>:lease this publisher holds

	GitDir string // the publisher's mirror: from_tip and every member head are in it
	Remote string // the remote name or URL in GitDir; default "origin"

	// PushPrefix runs the push under the nova-lander credential, for example
	// {"nova-secrets", "exec", "--as", "nova-lander", "--"}; empty runs git directly.
	// Only the push goes through it; ls-remote and fetch are read-only.
	PushPrefix []string

	// Sleep waits between ls-remote attempts (2, 5, 10 s); tests pass a no-op.
	Sleep func(context.Context, time.Duration) error
	// Hook runs at each Point; nil runs nothing.
	Hook func(Point) error
	// LandCycle is the L of the LANDED event (#3162 section 5); default "0".
	LandCycle string
}

// Result is one pass: its outcome, the batch it acted on, the transition line
// (7.7) and how many pushes it made.
type Result struct {
	Outcome Outcome
	Batch   string
	Line    string
	Pushes  int
}

// Publisher runs the passes.
type Publisher struct{ cfg Config }

// New returns a publisher with defaults filled.
func New(cfg Config) *Publisher {
	if cfg.Remote == "" {
		cfg.Remote = "origin"
	}
	if cfg.Sleep == nil {
		cfg.Sleep = func(ctx context.Context, d time.Duration) error {
			t := time.NewTimer(d)
			defer t.Stop()
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-t.C:
				return nil
			}
		}
	}
	if cfg.LandCycle == "" {
		cfg.LandCycle = "0"
	}
	return &Publisher{cfg: cfg}
}

// lsRemoteWaits are the three waits before each ls-remote attempt (7.2).
var lsRemoteWaits = []time.Duration{2 * time.Second, 5 * time.Second, 10 * time.Second}

// batch is the part of land:<repo>:<base>:batch:<id> a pass reads.
type batch struct {
	id, state, fromTip, trainHead, trainTree, createdAt string
	pubState                                            string // the unresolved intent's state on a resume
	heads                                               []string
	units                                               []string
}

// pass carries one LandFront call.
type pass struct {
	p   *Publisher
	b   batch
	res Result
}

// LandFront runs one pass of 7.1 on the base (see the package comment). An
// error is an infrastructure failure (Redis, the mirror) or a Hook's error.
func (p *Publisher) LandFront(ctx context.Context) (Result, error) {
	c := p.cfg
	active, err := c.Redis.Get(ctx, land.PubActiveKey(c.Repo, c.Base)).Result()
	if err != nil && !errors.Is(err, redis.Nil) {
		return Result{}, err
	}
	if active != "" {
		st, err := c.Redis.HGet(ctx, land.PubBatchKey(c.Repo, c.Base, active), "state").Result()
		if err != nil && !errors.Is(err, redis.Nil) {
			return Result{}, err
		}
		if st == "intent" || st == "pushed" || st == "verified" {
			b, err := p.readBatch(ctx, active)
			if err != nil {
				return Result{}, err
			}
			b.pubState = st
			ps := &pass{p: p, b: b, res: Result{Batch: active}}
			return ps.publish(ctx, true)
		}
	}

	front, err := c.Redis.ZRange(ctx, land.ChainKey(c.Repo, c.Base), 0, 0).Result()
	if err != nil {
		return Result{}, err
	}
	if len(front) == 0 {
		return Result{Outcome: Idle}, nil
	}
	b, err := p.readBatch(ctx, front[0])
	if err != nil {
		return Result{}, err
	}
	ps := &pass{p: p, b: b, res: Result{Batch: b.id}}
	if b.state != "green" {
		return ps.done(Wait, fmt.Sprintf("WAIT b%s state=%s", b.id, b.state)), nil
	}
	tip, err := c.Redis.HGet(ctx, land.TipKey(c.Repo, c.Base), "sha").Result()
	if err != nil && !errors.Is(err, redis.Nil) {
		return Result{}, err
	}
	if b.fromTip != tip {
		if err := p.voidChain(ctx, "", "tip-moved"); err != nil {
			return Result{}, err
		}
		return ps.done(Voided, fmt.Sprintf("VOID b%s from_tip=%s tip=%s", b.id, short(b.fromTip), short(tip))), nil
	}
	if err := p.hook(BeforeIntent); err != nil {
		return ps.res, err
	}
	if _, err := land.CallLandIntent(ctx, c.Redis, c.Sprint, c.Repo, c.Base, b.id, c.Lease); err != nil {
		reason := strings.TrimPrefix(err.Error(), "REFUSED ")
		if reason == err.Error() {
			return ps.res, err
		}
		if strings.Contains(reason, "lease") || strings.Contains(reason, "writer") {
			return ps.done(Fenced, "REFUSED "+reason+" remedy=nova-sprint land status"), nil
		}
		return ps.done(Refused, fmt.Sprintf("REFUSED %s remedy=nova-sprint why %s", reason, firstUnit(b))), nil
	}
	if err := p.hook(AfterIntent); err != nil {
		return ps.res, err
	}
	return ps.publish(ctx, false)
}

// publish runs steps 3-6 on a batch whose intent is cut. resume is a takeover
// of an intent another pass cut: 7.2 reconciliation comes before any push.
func (ps *pass) publish(ctx context.Context, resume bool) (Result, error) {
	p, c, b := ps.p, ps.p.cfg, ps.b
	tr, err := BuildTrain(ctx, TrainParams{GitDir: c.GitDir, FromTip: b.fromTip, Members: b.heads, BatchID: b.id, CreatedAt: b.createdAt})
	if err != nil {
		return ps.res, fmt.Errorf("rebuild train b%s: %w", b.id, err)
	}
	if tr.TrainHead != b.trainHead || tr.TrainTree != b.trainTree {
		return ps.nondeterministic(ctx, tr)
	}

	pushed := false
	if !resume {
		if err := ps.push(ctx); err == nil {
			pushed = true
			if err := p.hook(AfterPush); err != nil {
				return ps.res, err
			}
		}
	}
	for {
		ref, err := ps.lsRemote(ctx)
		if err != nil {
			return ps.done(Frozen, "FROZEN remote-unreachable b"+b.id), nil
		}
		switch ref {
		case b.trainHead:
			return ps.verified(ctx, tr, ref)
		case b.fromTip:
			if pushed || ps.res.Pushes > 0 {
				return ps.done(Retry, fmt.Sprintf("RETRY b%s push refused or failed, %s still %s; intent kept", b.id, c.Base, short(ref))), nil
			}
			if err := ps.push(ctx); err == nil {
				pushed = true
				if err := p.hook(AfterPush); err != nil {
					return ps.res, err
				}
			}
			continue
		}
		desc, err := ps.isDescendant(ctx, ref)
		if err != nil {
			return ps.res, err
		}
		if desc {
			return ps.verified(ctx, tr, ref)
		}
		return ps.dead(ctx, ref)
	}
}

// verified is step 5 then 6, for a remote tip that is the train head or, on
// the verified-descendant path of 7.2, a descendant T of it.
func (ps *pass) verified(ctx context.Context, tr *TrainResult, remote string) (Result, error) {
	p, c, b := ps.p, ps.p.cfg, ps.b
	tree, err := gitOut(ctx, c.GitDir, nil, "rev-parse", b.trainHead+"^{tree}")
	if err != nil {
		return ps.res, err
	}
	if tree != b.trainTree {
		return ps.res, fmt.Errorf("verify b%s: landed tree %s, gated train_tree %s", b.id, tree, b.trainTree)
	}
	if b.pubState != "verified" {
		if ok, err := ps.step(ctx, "pushed"); err != nil || !ok {
			return ps.fenced(err)
		}
	}
	if ok, err := ps.step(ctx, "verified"); err != nil || !ok {
		return ps.fenced(err)
	}
	if err := p.hook(AfterVerify); err != nil {
		return ps.res, err
	}
	r, err := land.CallLand(ctx, c.Redis, c.Sprint, c.Repo, c.Base, b.id, c.Lease, b.trainHead, strings.Join(tr.Commits, ","), c.LandCycle)
	if err != nil {
		return ps.res, err
	}
	switch r {
	case "ALREADY":
		return ps.done(Already, "ALREADY b"+b.id), nil
	case "STALE":
		return ps.done(Fenced, "REFUSED stale remedy=nova-sprint land status"), nil
	case "OK":
	default:
		return ps.res, fmt.Errorf("ns_land b%s: %s", b.id, r)
	}
	if err := p.hook(AfterLand); err != nil {
		return ps.res, err
	}
	line := fmt.Sprintf("LANDED b%s %s=%s units=%s L=%s", b.id, c.Base, short(b.trainHead), strings.Join(b.units, ","), c.LandCycle)
	if remote != b.trainHead {
		// verified-descendant: the tip is T, and every successor not on T re-plans.
		if r, err := land.CallTip(ctx, c.Redis, c.Repo, c.Base, remote, "fetch", c.Lease); err != nil || r != "OK" {
			return ps.fenced(err)
		}
		if err := p.voidChain(ctx, remote, "tip-moved"); err != nil {
			return ps.res, err
		}
		line += " descendant=" + short(remote)
	}
	return ps.done(Landed, line), nil
}

// dead is 7.2's last case: the remote is a sha that does not contain the train.
func (ps *pass) dead(ctx context.Context, remote string) (Result, error) {
	c, b := ps.p.cfg, ps.b
	if ok, err := ps.step(ctx, "dead"); err != nil || !ok {
		return ps.fenced(err)
	}
	if r, err := land.CallTip(ctx, c.Redis, c.Repo, c.Base, remote, "fetch", c.Lease); err != nil || r != "OK" {
		return ps.fenced(err)
	}
	if err := ps.p.voidChain(ctx, "", "dead"); err != nil {
		return ps.res, err
	}
	return ps.done(Dead, fmt.Sprintf("DEAD b%s %s=%s is not a descendant of %s; chain voided", b.id, c.Base, short(remote), short(b.trainHead))), nil
}

// nondeterministic: the rebuilt train is not the receipt's. Nothing is pushed;
// the intent dies and the batch re-plans and re-gates with the reason on its
// VOID event (5.5).
func (ps *pass) nondeterministic(ctx context.Context, tr *TrainResult) (Result, error) {
	c, b := ps.p.cfg, ps.b
	if ok, err := ps.step(ctx, "dead"); err != nil || !ok {
		return ps.fenced(err)
	}
	if err := land.CallBatchVoid(ctx, c.Redis, c.Sprint, c.Repo, c.Base, b.id, "nondeterministic"); err != nil {
		return ps.res, err
	}
	return ps.done(Mismatch, fmt.Sprintf("LAND REFUSED nondeterministic b%s rebuilt=%s receipt=%s", b.id, short(tr.TrainHead), short(b.trainHead))), nil
}

// step writes one fenced publisher step; false is STALE.
func (ps *pass) step(ctx context.Context, state string) (bool, error) {
	c := ps.p.cfg
	r, err := land.CallPubStep(ctx, c.Redis, c.Repo, c.Base, ps.b.id, state, c.Lease)
	if err != nil {
		return false, err
	}
	return r == "OK", nil
}

func (ps *pass) fenced(err error) (Result, error) {
	if err != nil {
		return ps.res, err
	}
	return ps.done(Fenced, "REFUSED stale remedy=nova-sprint land status"), nil
}

func (ps *pass) done(o Outcome, line string) Result {
	ps.res.Outcome, ps.res.Line = o, line
	return ps.res
}

// push is the compare-and-swap of 2.3. Its error is never a verdict: the
// caller reads the remote (7.2) to learn what happened.
func (ps *pass) push(ctx context.Context) error {
	c, b := ps.p.cfg, ps.b
	argv := append([]string{}, c.PushPrefix...)
	argv = append(argv, "git", "-C", c.GitDir, "push", "--atomic", "--quiet",
		"--force-with-lease=refs/heads/"+c.Base+":"+b.fromTip,
		c.Remote, b.trainHead+":refs/heads/"+c.Base)
	ps.res.Pushes++
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("push b%s: %w: %s", b.id, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// lsRemote reads the remote base, up to 3 attempts after 2, 5 and 10 s. The
// first attempt does not wait when nothing has failed yet.
func (ps *pass) lsRemote(ctx context.Context) (string, error) {
	c := ps.p.cfg
	var last error
	for i, wait := range lsRemoteWaits {
		if i > 0 {
			if err := c.Sleep(ctx, wait); err != nil {
				return "", err
			}
		}
		out, err := gitOut(ctx, c.GitDir, nil, "ls-remote", c.Remote, "refs/heads/"+c.Base)
		if err == nil {
			f := strings.Fields(out)
			if len(f) >= 1 && len(f[0]) == 40 {
				return f[0], nil
			}
			err = fmt.Errorf("ls-remote %s: no refs/heads/%s", c.Remote, c.Base)
		}
		last = err
	}
	return "", last
}

// isDescendant fetches T into the mirror and asks whether the train is its ancestor.
func (ps *pass) isDescendant(ctx context.Context, t string) (bool, error) {
	c := ps.p.cfg
	if _, err := gitOut(ctx, c.GitDir, nil, "fetch", "--quiet", c.Remote, "refs/heads/"+c.Base); err != nil {
		return false, err
	}
	cmd := exec.CommandContext(ctx, "git", "merge-base", "--is-ancestor", ps.b.trainHead, t)
	cmd.Dir = c.GitDir
	err := cmd.Run()
	var ee *exec.ExitError
	switch {
	case err == nil:
		return true, nil
	case errors.As(err, &ee) && ee.ExitCode() == 1:
		return false, nil
	default:
		return false, fmt.Errorf("merge-base %s %s: %w", short(ps.b.trainHead), short(t), err)
	}
}

// voidChain voids every batch on the chain whose from_tip is not keep (all of
// them when keep is empty), for the batcher to re-plan.
func (p *Publisher) voidChain(ctx context.Context, keep, reason string) error {
	c := p.cfg
	ids, err := c.Redis.ZRange(ctx, land.ChainKey(c.Repo, c.Base), 0, -1).Result()
	if err != nil {
		return err
	}
	for _, id := range ids {
		if keep != "" {
			ft, err := c.Redis.HGet(ctx, land.BatchKey(c.Repo, c.Base, id), "from_tip").Result()
			if err != nil && !errors.Is(err, redis.Nil) {
				return err
			}
			if ft == keep {
				continue
			}
		}
		if err := land.CallBatchVoid(ctx, c.Redis, c.Sprint, c.Repo, c.Base, id, reason); err != nil {
			return err
		}
	}
	return nil
}

func (p *Publisher) readBatch(ctx context.Context, id string) (batch, error) {
	c := p.cfg
	v, err := c.Redis.HMGet(ctx, land.BatchKey(c.Repo, c.Base, id),
		"state", "from_tip", "train_head", "train_tree", "created_at", "members").Result()
	if err != nil {
		return batch{}, err
	}
	s := func(i int) string {
		if v[i] == nil {
			return ""
		}
		return fmt.Sprint(v[i])
	}
	b := batch{id: id, state: s(0), fromTip: s(1), trainHead: s(2), trainTree: s(3), createdAt: s(4)}
	for _, m := range strings.Split(s(5), ",") {
		unit, head, ok := strings.Cut(m, "@")
		if !ok {
			continue
		}
		b.units = append(b.units, unit)
		b.heads = append(b.heads, head)
	}
	if len(b.heads) == 0 {
		return batch{}, fmt.Errorf("batch %s has no members", id)
	}
	return b, nil
}

func (p *Publisher) hook(pt Point) error {
	if p.cfg.Hook == nil {
		return nil
	}
	return p.cfg.Hook(pt)
}

func firstUnit(b batch) string {
	if len(b.units) == 0 {
		return b.id
	}
	return b.units[0]
}

func short(sha string) string {
	if len(sha) > 8 {
		return sha[:8]
	}
	return sha
}

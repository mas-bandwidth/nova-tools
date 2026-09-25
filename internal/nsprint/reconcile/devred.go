package reconcile

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/civerdict"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/land"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/webhook"
)

// The dev-red duty (nova-tools #3629): when a base branch's own CI at its
// tip is red, hold every stream merge into that base and push ONE fix task
// to the coordinator's queue naming the failing check; clear the hold on
// green. Three dev-red episodes on 2026-09-24 each cost an hour and
// re-merged every open PR because nothing held the base.
//
// Evidence, in order, all from Redis: the CI record ci:<repo>:<sha> (a
// hash whose `verdict` is OK or FAIL and whose `check` names the failing
// check); the GitHub leg ci:<repo>:<sha>:gh that the ci-github consumer
// writes from the webhook stream (internal/nsprint/webhook, #3597: gh green
// is OK, red is FAIL naming gh_fail); the gated receipt
// ci:<repo>:<sha>:<gid> through internal/civerdict. The duty never reads
// GitHub. No evidence is no verdict: the hold neither sets nor clears.

// BasesKey is the set of `<repo>/<base>` pairs the duty watches, written by
// `nova-sprint dev-red watch`. Empty, the duty does nothing.
const BasesKey = "devred:bases"

// CIRecordKey is the plain CI record of one commit, ci:<repo>:<sha> (#3597).
func CIRecordKey(repo, sha string) string { return "ci:" + repo + ":" + sha }

// CIState is what the evidence says about one commit. Verdict is
// civerdict.OK, "FAIL" or "" (no evidence). Check names the failing check,
// test or package when the verdict is FAIL.
type CIState struct {
	Verdict string
	Check   string
}

// FixTask is the one task the duty pushes per red tip. The command layer
// maps it to a task push (kind fix, front of the queue); the duty never
// pushes twice for one sha because the red record carries the task id.
type FixTask struct {
	ID    string // dev-red-<repo>-<base>-<sha8>
	Title string
	Repo  string
	Base  string
	SHA   string
	Check string
	To    string
}

// DevRed is the duty.
type DevRed struct {
	Client *redis.Client
	// Bases are the repo/base pairs to watch; when empty, BasesKey is read
	// each pass (one SMEMBERS).
	Bases []land.RepoBase
	// To is the coordinator's queue the fix task goes to.
	To string
	// Push pushes the fix task; the return is the task id as pushed (the
	// receipt names it). Required: a nil Push is a duty that only reads.
	Push func(ctx context.Context, t FixTask) (string, error)
	// Now is the clock; nil is time.Now.
	Now func() time.Time
}

// Outcome is one base's result in one pass, for the verb's receipt.
type Outcome struct {
	Repo, Base, SHA string
	State           CIState
	// Action is HELD (a new red tip: record written, task pushed), HOLDING
	// (still red, nothing new), CLEARED (green again), GREEN (nothing held),
	// NOTIP (no base tip recorded), NOEVIDENCE (tip known, no CI record) or
	// LEFT (not started: less than the lease write margin left, #3805).
	Action string
	Task   string
	Err    error
}

// Line is the one receipt line per base.
func (o Outcome) Line() string {
	if o.Err != nil {
		return fmt.Sprintf("DEVRED %s/%s ERROR %v", o.Repo, o.Base, o.Err)
	}
	parts := []string{"DEVRED", o.Repo + "/" + o.Base, o.Action}
	if o.SHA != "" {
		parts = append(parts, "sha="+short8(o.SHA))
	}
	if o.State.Check != "" {
		parts = append(parts, "check="+o.State.Check)
	}
	if o.Task != "" {
		parts = append(parts, "task="+o.Task)
	}
	return strings.Join(parts, " ")
}

func short8(s string) string {
	if len(s) > 8 {
		return s[:8]
	}
	return s
}

// Run is the reconcile.Duty: one pass over every watched base. Every write
// here is idempotent on the tip sha (HSETNX-shaped by the sha compare) and a
// stale instance writing the same record is harmless, so the duty needs no
// fence token; the lease bounds its time (#3805): no base starts with less
// than the write margin left. Every read is Redis (#3597), so no read needs
// a budget of its own.
func (d *DevRed) Run(ctx context.Context, l *Lease) (Counts, error) {
	outs, err := d.pass(ctx, l)
	if errors.Is(err, ErrFenced) {
		return Counts{}, err
	}
	var errs []error
	if err != nil {
		errs = append(errs, err)
	}
	for _, o := range outs {
		if o.Err != nil {
			errs = append(errs, fmt.Errorf("%s/%s: %w", o.Repo, o.Base, o.Err))
		}
	}
	return Counts{}, errors.Join(errs...)
}

// Pass runs one pass and returns one Outcome per base, in the order watched.
func (d *DevRed) Pass(ctx context.Context) ([]Outcome, error) { return d.pass(ctx, nil) }

// pass is Pass bounded by the lease l (nil: unbounded). A base not started
// for the lease margin is an Outcome with Action LEFT, and the error names
// how many were left.
func (d *DevRed) pass(ctx context.Context, l *Lease) ([]Outcome, error) {
	if d.Client == nil {
		return nil, fmt.Errorf("dev-red: nil client")
	}
	bases := d.Bases
	if len(bases) == 0 {
		members, err := d.Client.SMembers(ctx, BasesKey).Result()
		if err != nil && !errors.Is(err, redis.Nil) {
			return nil, fmt.Errorf("dev-red: read %s: %w", BasesKey, err)
		}
		for _, m := range members {
			repo, base, ok := strings.Cut(m, "/")
			if ok && repo != "" && base != "" {
				bases = append(bases, land.RepoBase{Repo: repo, Base: base})
			}
		}
	}
	outs := make([]Outcome, 0, len(bases))
	for i, rb := range bases {
		if err := l.Bounded(0); err != nil {
			if errors.Is(err, ErrFenced) {
				return outs, err
			}
			for _, left := range bases[i:] {
				outs = append(outs, Outcome{Repo: left.Repo, Base: left.Base, Action: "LEFT"})
			}
			return outs, fmt.Errorf("dev-red: %d of %d base(s) not started: %w", len(bases)-i, len(bases), err)
		}
		outs = append(outs, d.one(ctx, rb))
	}
	return outs, nil
}

func (d *DevRed) now() time.Time {
	if d.Now != nil {
		return d.Now()
	}
	return time.Now()
}

// one is the duty for one base: tip, evidence, then hold, keep, or clear.
func (d *DevRed) one(ctx context.Context, rb land.RepoBase) Outcome {
	o := Outcome{Repo: rb.Repo, Base: rb.Base}
	c := d.Client
	pipe := c.Pipeline()
	tipCmd := pipe.HGet(ctx, civerdict.TipKey(rb.Repo, rb.Base), "sha")
	redCmd := pipe.HGetAll(ctx, land.RedKey(rb.Repo, rb.Base))
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		o.Err = err
		return o
	}
	sha := strings.TrimSpace(tipCmd.Val())
	red := redCmd.Val()
	if sha == "" {
		o.Action = "NOTIP"
		return o
	}
	o.SHA = sha
	st, err := d.evidence(ctx, rb, sha)
	if err != nil {
		o.Err = err
		return o
	}
	o.State = st
	switch st.Verdict {
	case "FAIL":
		if red["sha"] == sha {
			o.Action, o.Task = "HOLDING", red["task"]
			return o
		}
		t := FixTask{
			ID:    fmt.Sprintf("dev-red-%s-%s-%s", rb.Repo, rb.Base, short8(sha)),
			Title: fmt.Sprintf("dev-red: %s red at %s on %s %s; fix on the base, stream merges wait", st.Check, short8(sha), rb.Repo, rb.Base),
			Repo:  rb.Repo, Base: rb.Base, SHA: sha, Check: st.Check, To: d.To,
		}
		if d.Push == nil {
			o.Err = fmt.Errorf("red at %s and no pusher", short8(sha))
			return o
		}
		id, err := d.Push(ctx, t)
		if err != nil {
			o.Err = fmt.Errorf("push fix task: %w", err)
			return o
		}
		at := strconv.FormatInt(d.now().UnixMilli(), 10)
		if err := c.HSet(ctx, land.RedKey(rb.Repo, rb.Base), "sha", sha, "check", st.Check, "task", id, "to", d.To, "at", at).Err(); err != nil {
			o.Err = err
			return o
		}
		o.Action, o.Task = "HELD", id
	case civerdict.OK:
		if len(red) == 0 {
			o.Action = "GREEN"
			return o
		}
		if err := c.Del(ctx, land.RedKey(rb.Repo, rb.Base)).Err(); err != nil {
			o.Err = err
			return o
		}
		o.Action, o.Task = "CLEARED", red["task"]
	default:
		if len(red) > 0 {
			o.Action, o.Task = "HOLDING", red["task"]
		} else {
			o.Action = "NOEVIDENCE"
		}
	}
	return o
}

// evidence reads the commit's CI state: the plain record and the GitHub
// leg in one pipeline, then the gated receipt. All of it is Redis; the
// lease bounds the duty per base in pass, not per read.
func (d *DevRed) evidence(ctx context.Context, rb land.RepoBase, sha string) (CIState, error) {
	c := d.Client
	pipe := c.Pipeline()
	recCmd := pipe.HGetAll(ctx, CIRecordKey(rb.Repo, sha))
	ghCmd := pipe.HGetAll(ctx, webhook.Key(rb.Repo, sha))
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return CIState{}, fmt.Errorf("read %s and its gh leg: %w", CIRecordKey(rb.Repo, sha), err)
	}
	if st, ok := stateOf(recCmd.Val()); ok {
		return st, nil
	}
	if st, ok := ghStateOf(webhook.Parse(ghCmd.Val())); ok {
		return st, nil
	}
	gated, err := civerdict.ReadHead(ctx, c, rb.Repo, sha, rb.Base)
	if err != nil {
		return CIState{}, fmt.Errorf("read gated receipt: %w", err)
	}
	if st, ok := stateOf(gated); ok {
		return st, nil
	}
	return CIState{}, nil
}

// ghStateOf reads the GitHub leg: green is OK, red is FAIL naming the first
// red check or workflow; pending or absent is no evidence.
func ghStateOf(r webhook.Record) (CIState, bool) {
	switch r.Word {
	case webhook.Green:
		return CIState{Verdict: civerdict.OK}, true
	case webhook.Red:
		check := r.Fail
		if _, name, ok := strings.Cut(check, ":"); ok {
			check = name
		}
		if check == "" {
			check = "ci"
		}
		return CIState{Verdict: "FAIL", Check: check}, true
	}
	return CIState{}, false
}

// stateOf reads a CI hash: verdict OK or FAIL, and the failing name from
// check, test or pkg. Anything else is no evidence.
func stateOf(m map[string]string) (CIState, bool) {
	v := strings.ToUpper(strings.TrimSpace(m[civerdict.Field]))
	switch v {
	case civerdict.OK:
		return CIState{Verdict: civerdict.OK}, true
	case "FAIL", "RED", "FAILURE":
		check := m["check"]
		if check == "" {
			check = m["test"]
		}
		if check == "" {
			check = m["pkg"]
		}
		if check == "" {
			check = "ci"
		}
		return CIState{Verdict: "FAIL", Check: check}, true
	}
	return CIState{}, false
}

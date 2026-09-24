package land

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

// The batcher (Issue #3139 rev 7 §4): landable units into batches, inside `land serve` under the
// publisher lease. Every write goes through a nova_sprint function (ns_batch_plan, ns_batch_bind,
// ns_unit_drop, ns_chain_void); the batcher itself only reads Redis and asks git.

// Batcher defaults (§4.1, §4.3).
const (
	DefaultBatchMax = 16
	DefaultChainMax = 4
	DefaultMaxFiles = 40
)

// DefaultAlonePaths are the conflict-prone paths that put a unit in a batch of its own (§4.1): an
// entry ending in "/" is a directory prefix, anything else an exact path.
var DefaultAlonePaths = []string{"go.mod", "go.sum", ".github/", "vendor/", "Makefile"}

// MergeChecker is the plan-time conflict pre-check (§3.6): `git merge-tree --write-tree <base> <head>`
// in the lander's mirror.
type MergeChecker interface {
	Conflicts(ctx context.Context, base, head string) (bool, error)
}

// GitMergeTree runs the pre-check in one local repository (the lander's mirror).
type GitMergeTree struct{ Dir string }

// Conflicts reports whether head does not merge cleanly onto base: merge-tree exits 1 on a conflict.
func (g GitMergeTree) Conflicts(ctx context.Context, base, head string) (bool, error) {
	cmd := exec.CommandContext(ctx, "git", "merge-tree", "--write-tree", base, head)
	cmd.Dir = g.Dir
	out, err := cmd.CombinedOutput()
	if err == nil {
		return false, nil
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) && ee.ExitCode() == 1 {
		return true, nil
	}
	return false, fmt.Errorf("git merge-tree %s %s: %w: %s", short(base), short(head), err, strings.TrimSpace(string(out)))
}

// Batcher plans batches for one repo and base.
type Batcher struct {
	Client             *redis.Client
	Sprint, Repo, Base string
	Lease              string // the publisher lease value <gen>:<token>

	BatchMax   int      // 0: DefaultBatchMax
	ChainMax   int      // 0: DefaultChainMax
	MaxFiles   int      // 0: DefaultMaxFiles; more files than this plans alone
	AlonePaths []string // nil: DefaultAlonePaths

	Merge MergeChecker     // nil: no pre-check (the gate reports CONFLICT)
	Now   func() time.Time // nil: time.Now
}

// PlannedBatch is one batch ns_batch_plan wrote.
type PlannedBatch struct {
	ID, Class, Parent, FromTip string
	Members                    []string // ordered unit@head
}

// PlanReport is one Plan tick: what was planned, bound, dropped, left waiting or refused, and the
// PLAN lines for the service log.
type PlanReport struct {
	Planned []PlannedBatch
	Bound   []string // batch ids whose from_tip was bound to the parent's train head
	Dropped []string // unit@head reason
	Waiting []string // unit reason
	Refused []string // PLAN REFUSED reason
	Lines   []string
}

type planUnit struct {
	id, head, class string
	files           []string
	alone           bool
	minBatch        int
}

type roundBatch struct {
	class   string
	alone   bool
	members []*planUnit
	files   map[string]bool
}

type chainBatch struct {
	id, state, fromTip, parent, trainHead, members, class string
}

func (b *Batcher) batchMax() int {
	if b.BatchMax > 0 {
		return b.BatchMax
	}
	return DefaultBatchMax
}

func (b *Batcher) chainMax() int {
	if b.ChainMax > 0 {
		return b.ChainMax
	}
	return DefaultChainMax
}

func (b *Batcher) now() time.Time {
	if b.Now != nil {
		return b.Now()
	}
	return time.Now()
}

// StorageSplitPath reports whether p is one of the five storage-split paths under docs/roadmaps/
// (§4.1): only a roadmap batch may touch them.
func StorageSplitPath(p string) bool {
	switch {
	case p == "docs/roadmaps/nova-work.sexp", p == "docs/roadmaps/ingest-map.sexp":
		return true
	}
	// blobs/** and work/<repo>.sexp, work/<repo>.closed.sexp: spelled by segment, since the
	// directories exist only in nova-work's tree.
	rest, ok := strings.CutPrefix(p, "docs/roadmaps/")
	if !ok {
		return false
	}
	seg, tail, _ := strings.Cut(rest, "/")
	switch seg {
	case "blobs":
		return tail != ""
	case "work":
		return strings.HasSuffix(tail, ".sexp") && !strings.Contains(tail, "/")
	}
	return false
}

// alonePath reports whether one file is conflict-prone.
func (b *Batcher) alonePath(f string) bool {
	paths := b.AlonePaths
	if paths == nil {
		paths = DefaultAlonePaths
	}
	for _, a := range paths {
		if strings.HasSuffix(a, "/") {
			if strings.HasPrefix(f, a) || strings.Contains(f, "/"+a) {
				return true
			}
		} else if f == a || strings.HasSuffix(f, "/"+a) {
			return true
		}
	}
	return false
}

// twoConflictDrops reports two conflict drops within 24 h (conflict_drops holds the last two, ms).
func twoConflictDrops(v string, now time.Time) bool {
	parts := splitCSV(v)
	if len(parts) < 2 {
		return false
	}
	for _, p := range parts[len(parts)-2:] {
		ms, err := strconv.ParseInt(p, 10, 64)
		if err != nil || now.Sub(time.UnixMilli(ms)) > 24*time.Hour {
			return false
		}
	}
	return true
}

func splitCSV(v string) []string {
	var out []string
	for _, p := range strings.Split(v, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// InputID is the batch input identity the batcher writes at plan (§5.5): sha256 over from_tip, the
// ordered member heads, class, policy_id and runner_id. The worker extends it with the selection
// graph ids; an empty from_tip (parent not yet gated) has no identity yet.
func InputID(fromTip string, members []string, class, policyID, runnerID string) string {
	if fromTip == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(strings.Join([]string{fromTip, strings.Join(members, ","), class, policyID, runnerID}, "\n")))
	return hex.EncodeToString(sum[:])
}

// Drop drops a unit at head with a reason; a non-empty task (rebase) is queued once on the
// author's queue. It reports whether this call dropped it (false: already dropped at that head,
// or the head moved on).
func (b *Batcher) Drop(ctx context.Context, unit, head, reason, task string) (bool, error) {
	r, err := CallUnitDrop(ctx, b.Client, b.Sprint, unit, b.Repo, b.Base, head, reason, task)
	if err != nil {
		return false, err
	}
	switch r {
	case "OK":
		return true, nil
	case "ALREADY", "STALE":
		return false, nil
	}
	return false, fmt.Errorf("drop %s@%s: %s", unit, short(head), r)
}

// VoidRed takes a red batch out of the chain and voids every batch behind it (§4.3, L16); the
// voided members are landable again and re-plan on the new base at the next tick.
func (b *Batcher) VoidRed(ctx context.Context, batchID string) ([]string, error) {
	return CallChainVoid(ctx, b.Client, b.Sprint, b.Repo, b.Base, batchID, "")
}

func (b *Batcher) loadChain(ctx context.Context) ([]chainBatch, error) {
	ids, err := b.Client.ZRange(ctx, ChainKey(b.Repo, b.Base), 0, -1).Result()
	if err != nil {
		return nil, fmt.Errorf("chain: %w", err)
	}
	pipe := b.Client.Pipeline()
	cmds := make([]*redis.SliceCmd, len(ids))
	for i, id := range ids {
		cmds[i] = pipe.HMGet(ctx, BatchKey(b.Repo, b.Base, id), "state", "from_tip", "parent", "train_head", "members", "class")
	}
	if len(ids) > 0 {
		if _, err := pipe.Exec(ctx); err != nil {
			return nil, fmt.Errorf("chain batches: %w", err)
		}
	}
	out := make([]chainBatch, len(ids))
	for i, id := range ids {
		v := cmds[i].Val()
		s := func(j int) string {
			if j < len(v) && v[j] != nil {
				return fmt.Sprint(v[j])
			}
			return ""
		}
		out[i] = chainBatch{id: id, state: s(0), fromTip: s(1), parent: s(2), trainHead: s(3), members: s(4), class: s(5)}
	}
	return out, nil
}

// Plan runs one batcher tick: bind from_tip on chain batches whose parent now has a train head,
// then shape the landable units into new batches behind the chain, up to chain_max.
func (b *Batcher) Plan(ctx context.Context) (PlanReport, error) {
	var rep PlanReport
	chain, err := b.loadChain(ctx)
	if err != nil {
		return rep, err
	}
	pol, err := b.Client.HMGet(ctx, PolicyKey(b.Repo, b.Base), "policy_id", "runner_id").Result()
	if err != nil {
		return rep, fmt.Errorf("policy: %w", err)
	}
	policyID, runnerID := fmt.Sprint(pol[0]), fmt.Sprint(pol[1])
	if pol[0] == nil {
		policyID = ""
	}
	if pol[1] == nil {
		runnerID = ""
	}

	// 1. Bind (§4.2): batch k's from_tip is batch k-1's train head.
	trainHead := map[string]string{}
	for _, cb := range chain {
		trainHead[cb.id] = cb.trainHead
	}
	for i := range chain {
		cb := &chain[i]
		if cb.fromTip != "" || cb.parent == "" || cb.state != "queued" {
			continue
		}
		ph, ok := trainHead[cb.parent]
		if !ok {
			v, err := b.Client.HGet(ctx, BatchKey(b.Repo, b.Base, cb.parent), "train_head").Result()
			if err != nil && !errors.Is(err, redis.Nil) {
				return rep, fmt.Errorf("parent %s: %w", cb.parent, err)
			}
			ph = v
		}
		if ph == "" {
			continue
		}
		r, err := CallBatchBind(ctx, b.Client, b.Repo, b.Base, cb.id, ph, InputID(ph, splitCSV(cb.members), cb.class, policyID, runnerID))
		if err != nil {
			return rep, fmt.Errorf("bind %s: %w", cb.id, err)
		}
		if r == "OK" {
			cb.fromTip = ph
			rep.Bound = append(rep.Bound, cb.id)
			rep.Lines = append(rep.Lines, fmt.Sprintf("BIND %s from_tip=%s", cb.id, short(ph)))
		}
	}

	slots := b.chainMax() - len(chain)
	if slots <= 0 {
		return rep, nil
	}
	tip, err := b.Client.HGet(ctx, TipKey(b.Repo, b.Base), "sha").Result()
	if err != nil && !errors.Is(err, redis.Nil) {
		return rep, fmt.Errorf("tip: %w", err)
	}
	parent, roundFrom := "", tip
	inChain := map[string]bool{}
	for _, cb := range chain {
		inChain[cb.id] = true
	}
	if len(chain) > 0 {
		last := chain[len(chain)-1]
		parent, roundFrom = last.id, last.trainHead
	}

	// 2. The landable units, oldest first within a tier.
	ids, err := b.Client.ZRange(ctx, LandableKey(b.Sprint, b.Repo, b.Base), 0, -1).Result()
	if err != nil {
		return rep, fmt.Errorf("landable: %w", err)
	}
	pipe := b.Client.Pipeline()
	hs := make([]*redis.MapStringStringCmd, len(ids))
	for i, id := range ids {
		hs[i] = pipe.HGetAll(ctx, UnitKey(b.Sprint, id))
	}
	if len(ids) > 0 {
		if _, err := pipe.Exec(ctx); err != nil {
			return rep, fmt.Errorf("units: %w", err)
		}
	}

	var round []*roundBatch
	placed := map[string]int{} // unit -> round batch index
	now := b.now()
	type pending struct {
		id string
		h  map[string]string
	}
	var queue []pending
	for i, id := range ids {
		h := hs[i].Val()
		if h["state"] != "landable" || h["batch"] != "" || h["head"] == "" {
			continue
		}
		queue = append(queue, pending{id, h})
	}

	// place shapes one unit (§4.1); it returns "" when placed or the reason it waits.
	place := func(u *planUnit) string {
		for j := u.minBatch; j < len(round); j++ {
			rb := round[j]
			if rb.alone || u.alone || rb.class != u.class || len(rb.members) >= b.batchMax() {
				continue
			}
			overlap := false
			for _, f := range u.files {
				if rb.files[f] {
					overlap = true
					break
				}
			}
			if overlap {
				continue
			}
			rb.members = append(rb.members, u)
			for _, f := range u.files {
				rb.files[f] = true
			}
			placed[u.id] = j
			return ""
		}
		if len(round) >= slots {
			return "chain_max"
		}
		rb := &roundBatch{class: u.class, alone: u.alone, files: map[string]bool{}}
		rb.members = append(rb.members, u)
		for _, f := range u.files {
			rb.files[f] = true
		}
		round = append(round, rb)
		placed[u.id] = len(round) - 1
		return ""
	}

	for len(queue) > 0 {
		var deferred []pending
		progress := false
		for _, p := range queue {
			h := p.h
			u := &planUnit{id: p.id, head: h["head"], files: splitCSV(h["files"])}
			roadmap, mixed := false, false
			for _, f := range u.files {
				if StorageSplitPath(f) {
					roadmap = true
				}
			}
			if roadmap {
				for _, f := range u.files {
					if !strings.HasPrefix(f, "docs/roadmaps/") {
						mixed = true
					}
				}
			}
			if mixed {
				if _, err := b.Drop(ctx, u.id, u.head, "roadmap-mixed", "split"); err != nil {
					return rep, err
				}
				rep.Dropped = append(rep.Dropped, u.id+"@"+u.head+" roadmap-mixed")
				rep.Lines = append(rep.Lines, fmt.Sprintf("DROP %s@%s roadmap-mixed", u.id, short(u.head)))
				continue
			}
			u.class = h["class"]
			if u.class == "" {
				u.class = "go"
			}
			if roadmap {
				u.class = "roadmap"
			}

			// Stack parent first (L24).
			if sp := h["stack_parent"]; sp != "" && sp != "none" {
				if j, ok := placed[sp]; ok {
					u.minBatch = j
				} else {
					ps, err := b.Client.HMGet(ctx, UnitKey(b.Sprint, sp), "state", "batch").Result()
					if err != nil {
						return rep, fmt.Errorf("stack parent %s: %w", sp, err)
					}
					st, pb := fmt.Sprint(ps[0]), fmt.Sprint(ps[1])
					switch {
					case st == "landed":
					case ps[1] != nil && pb != "" && inChain[pb]:
					case st == "landable":
						deferred = append(deferred, p) // the parent may still be placed this tick
						continue
					default:
						rep.Waiting = append(rep.Waiting, u.id+" stack-parent="+sp)
						continue
					}
				}
			}

			// Merge-tree pre-check (§3.6, L26): a base conflict drops once with one rebase task;
			// a conflict with the train ahead waits without a drop.
			if b.Merge != nil {
				c, err := b.Merge.Conflicts(ctx, tip, u.head)
				if err != nil {
					return rep, err
				}
				if c {
					if _, err := b.Drop(ctx, u.id, u.head, "conflict", "rebase"); err != nil {
						return rep, err
					}
					rep.Dropped = append(rep.Dropped, u.id+"@"+u.head+" conflict")
					rep.Lines = append(rep.Lines, fmt.Sprintf("DROP %s@%s conflict base=%s", u.id, short(u.head), short(tip)))
					continue
				}
				if roundFrom != "" && roundFrom != tip {
					c, err := b.Merge.Conflicts(ctx, roundFrom, u.head)
					if err != nil {
						return rep, err
					}
					if c {
						rep.Waiting = append(rep.Waiting, u.id+" conflict-ahead="+short(roundFrom))
						continue
					}
				}
			}

			u.alone = h["alone"] == "1" || twoConflictDrops(h["conflict_drops"], now)
			maxFiles := b.MaxFiles
			if maxFiles <= 0 {
				maxFiles = DefaultMaxFiles
			}
			if len(u.files) > maxFiles {
				u.alone = true
			}
			for _, f := range u.files {
				if b.alonePath(f) {
					u.alone = true
				}
			}
			if why := place(u); why != "" {
				rep.Waiting = append(rep.Waiting, u.id+" "+why)
				continue
			}
			progress = true
		}
		if !progress {
			for _, p := range deferred {
				rep.Waiting = append(rep.Waiting, p.id+" stack-parent="+p.h["stack_parent"])
			}
			break
		}
		queue = deferred
	}

	// 3. Write the round behind the chain, in order; a refusal stops the round (its children would
	// name a parent that is not in the chain).
	for j, rb := range round {
		members := make([]string, len(rb.members))
		for i, u := range rb.members {
			members[i] = u.id + "@" + u.head
		}
		from := ""
		if j == 0 {
			from = roundFrom
		}
		id, _, _, err := CallPlan(ctx, b.Client, PlanParams{
			Sprint: b.Sprint, Repo: b.Repo, Base: b.Base, Lease: b.Lease,
			Members: members, Class: rb.class, FromTip: from, Parent: parent,
			InputID: InputID(from, members, rb.class, policyID, runnerID), ChainMax: b.chainMax(),
		})
		var refused *PlanRefusedError
		if errors.As(err, &refused) {
			rep.Refused = append(rep.Refused, refused.Reason)
			rep.Lines = append(rep.Lines, "PLAN REFUSED "+refused.Reason)
			break
		}
		if err != nil {
			return rep, err
		}
		rep.Planned = append(rep.Planned, PlannedBatch{ID: id, Class: rb.class, Parent: parent, FromTip: from, Members: members})
		rep.Lines = append(rep.Lines, fmt.Sprintf("PLAN %s class=%s parent=%s from_tip=%s members=%d", id, rb.class, parent, short(from), len(members)))
		parent = id
	}
	return rep, nil
}

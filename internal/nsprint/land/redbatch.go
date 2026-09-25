package land

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/redis/go-redis/v9"
)

// Red batches (Issue #3139 rev 7 §6, B8). A RED receipt on a chain batch is settled by two
// nova_sprint functions (land_red.lua) the batcher calls each tick, under the publisher lease:
//
//   - ns_batch_split: flaky first (a failing test outside the selection closure reruns the same
//     batch once, recording a hit in flaky:<repo>:<pkg>.<Test>); a rerun red again on that test
//     freezes the base; otherwise the batch and its successors leave the chain and one wall of
//     gates is queued on from_tip at once: the tip alone, every member alone, every prefix.
//   - ns_batch_attribute: once every gate has a receipt, drop the members red alone, else the
//     member that completes the first red prefix; each drop pushes one fix task to its author,
//     and the rest go back to landable for the next plan. After RoundsMax rounds a survivor plans
//     alone.
//
// Neither reads GitHub; the only inputs are the receipts and the unit records.

// DefaultRoundsMax is bisect_rounds_max (§6.1).
const DefaultRoundsMax = 3

// ClosureChecker reports whether a failing test's package is inside the selection closure of the
// red gate (§6.1 step 1): outside it, no member touches the test, so it reruns once as flaky.
type ClosureChecker interface {
	InClosure(ctx context.Context, batchID string, receipt map[string]string, pkg string) (bool, error)
}

// OwnerFunc names the owner of a flaky test (§6.2: the test file's last author from the mirror,
// resolved through friends:login); "" is rowan.
type OwnerFunc func(ctx context.Context, pkg, test string) string

// SplitKey is land:<repo>:<base>:split:<id>, the attribution record of one red batch.
func SplitKey(repo, base, id string) string {
	return fmt.Sprintf("land:%s:%s:split:%s", repo, base, id)
}

// SplitsKey is land:<repo>:<base>:splits, the red batches in attribution (score: split time).
func SplitsKey(repo, base string) string { return fmt.Sprintf("land:%s:%s:splits", repo, base) }

// FlakyTestKey is flaky:<repo>:<pkg>.<Test>, the flaky registry of §6.2.
func FlakyTestKey(repo, test string) string { return "flaky:" + repo + ":" + test }

// SplitGates lists the attribution gates of every red batch in attribution: one ZRANGE and one
// pipelined HGET per split, no scan.
func SplitGates(ctx context.Context, c *redis.Client, repo, base string) ([]string, error) {
	ids, err := c.ZRange(ctx, SplitsKey(repo, base), 0, -1).Result()
	if err != nil || len(ids) == 0 {
		return nil, err
	}
	pipe := c.Pipeline()
	cmds := make([]*redis.StringCmd, len(ids))
	for i, id := range ids {
		cmds[i] = pipe.HGet(ctx, SplitKey(repo, base, id), "gates")
	}
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return nil, err
	}
	var out []string
	for _, cmd := range cmds {
		out = append(out, splitCSV(cmd.Val())...)
	}
	return out, nil
}

// SplitFailing parses a receipt's failing field, "<pkg> <Test>", into the registry name
// <pkg>.<Test>; a bare step name has no test and is never flaky.
func SplitFailing(failing string) (pkg, test string, ok bool) {
	f := strings.Fields(failing)
	if len(f) < 2 {
		return "", "", false
	}
	return f[0], f[1], true
}

// CallBatchSplit calls ns_batch_split; the reply's first word is RERUN, FROZEN, SPLIT, DROPPED or
// STALE. A lost lease is a *RefusedError.
func CallBatchSplit(ctx context.Context, c *redis.Client, sprint, repo, base, batchID, lease, mode string, extra ...string) ([]string, error) {
	args := append([]any{sprint, repo, base, batchID, lease, mode}, toAny(extra)...)
	res, err := c.FCall(ctx, "ns_batch_split", nil, args...).StringSlice()
	if err != nil {
		return nil, err
	}
	if len(res) >= 2 && res[0] == "REFUSED" {
		return nil, &RefusedError{Fn: "ns_batch_split", Reason: res[1]}
	}
	if len(res) == 0 {
		return nil, fmt.Errorf("ns_batch_split %s: empty reply", batchID)
	}
	return res, nil
}

// CallBatchAttribute calls ns_batch_attribute; the reply's first word is OK, PENDING, FROZEN,
// ALREADY or NOTFOUND. A lost lease is a *RefusedError.
func CallBatchAttribute(ctx context.Context, c *redis.Client, sprint, repo, base, batchID, lease string, roundsMax int) ([]string, error) {
	res, err := c.FCall(ctx, "ns_batch_attribute", nil, sprint, repo, base, batchID, lease, strconv.Itoa(roundsMax)).StringSlice()
	if err != nil {
		return nil, err
	}
	if len(res) >= 2 && res[0] == "REFUSED" {
		return nil, &RefusedError{Fn: "ns_batch_attribute", Reason: res[1]}
	}
	if len(res) == 0 {
		return nil, fmt.Errorf("ns_batch_attribute %s: empty reply", batchID)
	}
	return res, nil
}

func toAny(v []string) []any {
	out := make([]any, len(v))
	for i, s := range v {
		out[i] = s
	}
	return out
}

// RedReport is one red tick: the service log lines, one per transition (RERUN, FROZEN, SPLIT,
// DROP, KEEP, PENDING, REFUSED), and the units dropped and kept.
type RedReport struct {
	Dropped []string // unit reason
	Kept    []string // unit round
	Pending []string // red batch ids still gating
	Lines   []string
}

func (b *Batcher) roundsMax() int {
	if b.RoundsMax > 0 {
		return b.RoundsMax
	}
	return DefaultRoundsMax
}

// RedTick settles red batches: every red chain batch is split (rerun, freeze or the attribution
// wall), then every split whose gates all have receipts is attributed. It reads Redis and writes
// only through ns_batch_split and ns_batch_attribute; a lost lease ends the tick with a REFUSED
// line and nothing written.
func (b *Batcher) RedTick(ctx context.Context) (RedReport, error) {
	var rep RedReport
	chain, err := b.loadChain(ctx)
	if err != nil {
		return rep, err
	}
	for _, cb := range chain {
		if cb.state != "red" {
			continue
		}
		line, err := b.splitRed(ctx, cb.id, cb.fromTip)
		if err != nil {
			return b.redRefused(rep, err)
		}
		rep.Lines = append(rep.Lines, line)
		if f := strings.Fields(line); len(f) >= 3 && f[0] == "DROP" {
			rep.Dropped = append(rep.Dropped, f[1]+" alone")
		}
		// A split voids everything behind it: the rest of this chain snapshot is gone.
		if !strings.HasPrefix(line, "RERUN") {
			break
		}
	}
	ids, err := b.Client.ZRange(ctx, SplitsKey(b.Repo, b.Base), 0, -1).Result()
	if err != nil {
		return rep, fmt.Errorf("splits: %w", err)
	}
	for _, id := range ids {
		res, err := CallBatchAttribute(ctx, b.Client, b.Sprint, b.Repo, b.Base, id, b.Lease, b.roundsMax())
		if err != nil {
			return b.redRefused(rep, err)
		}
		switch res[0] {
		case "PENDING":
			rep.Pending = append(rep.Pending, id)
		case "FROZEN":
			rep.Lines = append(rep.Lines, fmt.Sprintf("FROZEN %s/%s %s", b.Repo, b.Base, strings.Join(res[1:], " ")))
		case "OK":
			for _, l := range res[1:] {
				f := strings.Fields(l)
				switch {
				case len(f) >= 3 && f[0] == "DROP":
					rep.Dropped = append(rep.Dropped, f[1]+" "+f[2])
				case len(f) >= 3 && f[0] == "KEEP":
					rep.Kept = append(rep.Kept, f[1]+" "+f[2])
				}
				rep.Lines = append(rep.Lines, l+" red="+id)
			}
		}
	}
	return rep, nil
}

func (b *Batcher) redRefused(rep RedReport, err error) (RedReport, error) {
	var refused *RefusedError
	if errors.As(err, &refused) {
		rep.Lines = append(rep.Lines, refused.Fn+" REFUSED "+refused.Reason)
		return rep, nil
	}
	return rep, err
}

// splitRed chooses the split mode for one red chain batch from its receipt (§6.1 step 1, §6.2).
func (b *Batcher) splitRed(ctx context.Context, id, fromTip string) (string, error) {
	bh, err := b.Client.HMGet(ctx, BatchKey(b.Repo, b.Base, id), "receipt", "rerun").Result()
	if err != nil {
		return "", fmt.Errorf("red batch %s: %w", id, err)
	}
	rkey, rerun := str(bh[0]), str(bh[1])
	receipt := map[string]string{}
	if rkey != "" {
		if receipt, err = b.Client.HGetAll(ctx, rkey).Result(); err != nil {
			return "", fmt.Errorf("receipt %s: %w", rkey, err)
		}
	}
	pkg, test, named := SplitFailing(receipt["failing"])
	name := pkg + "." + test
	outside := false
	if named && b.Closure != nil {
		in, err := b.Closure.InClosure(ctx, id, receipt, pkg)
		if err != nil {
			return "", fmt.Errorf("closure %s: %w", id, err)
		}
		outside = !in
	}
	var res []string
	switch {
	case outside && rerun == "":
		owner := ""
		if b.Owner != nil {
			owner = b.Owner(ctx, pkg, test)
		}
		if owner == "" {
			owner = "rowan"
		}
		res, err = CallBatchSplit(ctx, b.Client, b.Sprint, b.Repo, b.Base, id, b.Lease, "rerun", name, owner)
	case outside && containsCSV(rerun, name):
		// Red twice on one tree with no member touching the test: the base is red (§6.2, §8.4).
		res, err = CallBatchSplit(ctx, b.Client, b.Sprint, b.Repo, b.Base, id, b.Lease, "freeze",
			fmt.Sprintf("base-red %s at %s: red twice on one tree, no member touches it", name, short(fromTip)))
	default:
		res, err = CallBatchSplit(ctx, b.Client, b.Sprint, b.Repo, b.Base, id, b.Lease, "attr")
	}
	if err != nil {
		return "", err
	}
	switch res[0] {
	case "RERUN":
		return fmt.Sprintf("RERUN %s tests=%s %s", id, name, strings.Join(res[2:], " ")), nil
	case "FROZEN":
		return fmt.Sprintf("FROZEN %s/%s red=%s voided=%s", b.Repo, b.Base, id, strings.Join(res[1:], ",")), nil
	case "SPLIT":
		return fmt.Sprintf("SPLIT %s gates=%d", id, len(res)-1), nil
	case "DROPPED":
		return fmt.Sprintf("DROP %s alone red=%s task=%s", strings.Join(res[1:2], ""), id, strings.Join(res[2:], "")), nil
	}
	return fmt.Sprintf("SPLIT %s %s", id, strings.Join(res, " ")), nil
}

func containsCSV(csv, v string) bool {
	for _, p := range splitCSV(csv) {
		if p == v {
			return true
		}
	}
	return false
}

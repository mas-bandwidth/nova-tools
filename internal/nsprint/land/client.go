package land

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/redis/go-redis/v9"
)

// UnitHeadParams holds fields written by ns_unit_head.
type UnitHeadParams struct {
	Sprint      string
	Unit        string
	Repo        string
	Base        string
	Branch      string
	Head        string
	BaseSHA     string
	StackParent string
	Files       string
	PathsHash   string
	Security    string
	Class       string
	PR          string
	Author      string
}

// CallUnitHead calls ns_unit_head.
func CallUnitHead(ctx context.Context, c *redis.Client, p UnitHeadParams) (int64, error) {
	res, err := c.FCall(ctx, "ns_unit_head", nil,
		p.Sprint, p.Unit, p.Repo, p.Base, p.Branch, p.Head, p.BaseSHA,
		p.StackParent, p.Files, p.PathsHash, p.Security, p.Class, p.PR, p.Author,
	).Slice()
	if err != nil {
		return 0, err
	}
	if len(res) < 2 || res[0] != "OK" {
		return 0, fmt.Errorf("ns_unit_head failed: %v", res)
	}
	seq, err := strconv.ParseInt(fmt.Sprint(res[1]), 10, 64)
	if err != nil {
		return 0, err
	}
	return seq, nil
}

// CallUnitEval calls ns_unit_eval.
func CallUnitEval(ctx context.Context, c *redis.Client, sprint, unit, repo, base string, tier int) (float64, error) {
	res, err := c.FCall(ctx, "ns_unit_eval", nil, sprint, unit, repo, base, strconv.Itoa(tier)).Slice()
	if err != nil {
		return 0, err
	}
	if len(res) < 2 {
		return 0, fmt.Errorf("unexpected ns_unit_eval reply: %v", res)
	}
	if res[0] == "REFUSED" {
		return 0, fmt.Errorf("REFUSED %v", res[1])
	}
	if res[0] != "OK" {
		return 0, fmt.Errorf("ns_unit_eval failed: %v", res)
	}
	score, err := strconv.ParseFloat(fmt.Sprint(res[1]), 64)
	if err != nil {
		return 0, err
	}
	return score, nil
}

// CallHold calls ns_hold.
func CallHold(ctx context.Context, c *redis.Client, sprint, unit, holder, head, kind, reason, url, files, doneWhen, origin string) (int64, bool, error) {
	res, err := c.FCall(ctx, "ns_hold", nil, sprint, unit, holder, head, kind, reason, url, files, doneWhen, origin).Slice()
	if err != nil {
		return 0, false, err
	}
	if len(res) < 3 || res[0] != "OK" {
		return 0, false, fmt.Errorf("ns_hold failed: %v", res)
	}
	seq, err := strconv.ParseInt(fmt.Sprint(res[1]), 10, 64)
	if err != nil {
		return 0, false, err
	}
	postLand := fmt.Sprint(res[2]) == "1"
	return seq, postLand, nil
}

// CallRelease calls ns_release.
func CallRelease(ctx context.Context, c *redis.Client, sprint, unit, holder, releasedBy, releaseKind, releaseReason, releaseURL string) (int64, error) {
	res, err := c.FCall(ctx, "ns_release", nil, sprint, unit, holder, releasedBy, releaseKind, releaseReason, releaseURL).Slice()
	if err != nil {
		return 0, err
	}
	if len(res) < 2 || res[0] != "OK" {
		return 0, fmt.Errorf("ns_release failed: %v", res)
	}
	seq, err := strconv.ParseInt(fmt.Sprint(res[1]), 10, 64)
	if err != nil {
		return 0, err
	}
	return seq, nil
}

// CallRead calls ns_read.
func CallRead(ctx context.Context, c *redis.Client, sprint, unit, who, head, verdict, score, kind, files, doneWhen string) (int64, error) {
	res, err := c.FCall(ctx, "ns_read", nil, sprint, unit, who, head, verdict, score, kind, files, doneWhen).Slice()
	if err != nil {
		return 0, err
	}
	if len(res) < 2 || res[0] != "OK" {
		return 0, fmt.Errorf("ns_read failed: %v", res)
	}
	seq, err := strconv.ParseInt(fmt.Sprint(res[1]), 10, 64)
	if err != nil {
		return 0, err
	}
	return seq, nil
}

// CallPolicySet calls ns_policy_set.
func CallPolicySet(ctx context.Context, c *redis.Client, repo, base, policyID, requiredSetID, runnerID string) error {
	res, err := c.FCall(ctx, "ns_policy_set", nil, repo, base, policyID, requiredSetID, runnerID).Text()
	if err != nil {
		return err
	}
	if res != "OK" {
		return fmt.Errorf("ns_policy_set failed: %s", res)
	}
	return nil
}

// CallWriter calls ns_writer.
func CallWriter(ctx context.Context, c *redis.Client, repo, base, toOwner, by string) (int64, error) {
	res, err := c.FCall(ctx, "ns_writer", nil, repo, base, toOwner, by).Slice()
	if err != nil {
		return 0, err
	}
	if len(res) < 2 || res[0] != "OK" {
		return 0, fmt.Errorf("ns_writer failed: %v", res)
	}
	gen, err := strconv.ParseInt(fmt.Sprint(res[1]), 10, 64)
	if err != nil {
		return 0, err
	}
	return gen, nil
}

// CallGateReceiptWrite calls ns_gate_receipt_write.
func CallGateReceiptWrite(ctx context.Context, c *redis.Client, repo, head, gid, verdict, kind, base, baseSHA, requiredSetID, policyID, runnerID, receipt, bench, pkg, test string) (string, error) {
	return c.FCall(ctx, "ns_gate_receipt_write", nil, repo, head, gid, verdict, kind, base, baseSHA, requiredSetID, policyID, runnerID, receipt, bench, pkg, test).Text()
}

// CallBatchPlan calls ns_batch_plan.
func CallBatchPlan(ctx context.Context, c *redis.Client, sprint, repo, base, batchID, leaseVal, membersCSV, pathsCSV, class, fromTip, inputID string) (token, entryID string, err error) {
	res, err := c.FCall(ctx, "ns_batch_plan", nil, sprint, repo, base, batchID, leaseVal, membersCSV, pathsCSV, class, fromTip, inputID).Slice()
	if err != nil {
		return "", "", err
	}
	if len(res) < 2 {
		return "", "", fmt.Errorf("unexpected ns_batch_plan reply: %v", res)
	}
	if res[0] == "REFUSED" {
		return "", "", fmt.Errorf("REFUSED %v", res[1])
	}
	if res[0] != "OK" || len(res) < 3 {
		return "", "", fmt.Errorf("ns_batch_plan failed: %v", res)
	}
	return fmt.Sprint(res[1]), fmt.Sprint(res[2]), nil
}

// CallGateClaim calls ns_gate_claim.
func CallGateClaim(ctx context.Context, c *redis.Client, repo, base, batchID string, attempt int, token, bench, slot string) (string, error) {
	return c.FCall(ctx, "ns_gate_claim", nil, repo, base, batchID, strconv.Itoa(attempt), token, bench, slot).Text()
}

// CallGateReceipt calls ns_gate_receipt.
func CallGateReceipt(ctx context.Context, c *redis.Client, repo, base, batchID string, attempt int, token, verdict, bench, worker, trainHead, trainTree, inputID, selection, steps, failing, flakyRerun, coreS string) (string, error) {
	return c.FCall(ctx, "ns_gate_receipt", nil, repo, base, batchID, strconv.Itoa(attempt), token, verdict, bench, worker, trainHead, trainTree, inputID, selection, steps, failing, flakyRerun, coreS).Text()
}

// CallRequeue calls ns_requeue.
func CallRequeue(ctx context.Context, c *redis.Client, repo, base, batchID string) (token, entryID string, err error) {
	res, err := c.FCall(ctx, "ns_requeue", nil, repo, base, batchID).Slice()
	if err != nil {
		return "", "", err
	}
	if len(res) < 3 || res[0] != "OK" {
		return "", "", fmt.Errorf("ns_requeue failed: %v", res)
	}
	return fmt.Sprint(res[1]), fmt.Sprint(res[2]), nil
}

// CallBatchVoid calls ns_batch_void (lease-fenced like every batcher write, 2.3): a stale caller
// gets a *RefusedError and nothing is written.
func CallBatchVoid(ctx context.Context, c *redis.Client, sprint, repo, base, batchID, leaseVal, reason string) error {
	res, err := c.FCall(ctx, "ns_batch_void", nil, sprint, repo, base, batchID, leaseVal, reason).StringSlice()
	if err != nil {
		return err
	}
	if len(res) >= 2 && res[0] == "REFUSED" {
		return &RefusedError{Fn: "ns_batch_void", Reason: res[1]}
	}
	if len(res) == 0 || res[0] != "OK" {
		return fmt.Errorf("ns_batch_void %s: %v", batchID, res)
	}
	return nil
}

// CallLandIntent calls ns_land_intent.
func CallLandIntent(ctx context.Context, c *redis.Client, sprint, repo, base, batchID, leaseVal string) (int64, error) {
	res, err := c.FCall(ctx, "ns_land_intent", nil, sprint, repo, base, batchID, leaseVal).Slice()
	if err != nil {
		return 0, err
	}
	if len(res) < 2 {
		return 0, fmt.Errorf("unexpected ns_land_intent reply: %v", res)
	}
	if res[0] == "REFUSED" {
		return 0, fmt.Errorf("REFUSED %v", res[1])
	}
	if res[0] != "OK" {
		return 0, fmt.Errorf("ns_land_intent failed: %v", res)
	}
	seq, err := strconv.ParseInt(fmt.Sprint(res[1]), 10, 64)
	if err != nil {
		return 0, err
	}
	return seq, nil
}

// CallPubState calls ns_pub_state.
func CallPubState(ctx context.Context, c *redis.Client, repo, base, batchID, state string) error {
	res, err := c.FCall(ctx, "ns_pub_state", nil, repo, base, batchID, state).Text()
	if err != nil {
		return err
	}
	if res != "OK" {
		return fmt.Errorf("ns_pub_state failed: %s", res)
	}
	return nil
}

// CallLand calls ns_land.
func CallLand(ctx context.Context, c *redis.Client, sprint, repo, base, batchID, leaseVal, trainHead, mergeSHAsCSV, landCycle string) (string, error) {
	return c.FCall(ctx, "ns_land", nil, sprint, repo, base, batchID, leaseVal, trainHead, mergeSHAsCSV, landCycle).Text()
}

// PlanParams names one ns_batch_plan call (4.4). An empty BatchID is minted as b<seq>; ChainMax 0
// means the spec's starting depth, 4.
type PlanParams struct {
	Sprint, Repo, Base, BatchID, Lease string
	Members                            []string // ordered unit@head
	Paths                              []string
	Class, FromTip, InputID, Parent    string
	ChainMax                           int
}

// PlanRefusedError is ns_batch_plan's refusal; Reason is the PLAN REFUSED text (overlap=<path>,
// roadmap-path=<p>, stack-parent=<u>, no unit, chain_max, ...).
type PlanRefusedError struct{ Reason string }

func (e *PlanRefusedError) Error() string { return "REFUSED " + e.Reason }

// CallPlan calls ns_batch_plan with the parent and chain depth; it returns the batch id it wrote.
func CallPlan(ctx context.Context, c *redis.Client, p PlanParams) (batchID, token, entryID string, err error) {
	chainMax := ""
	if p.ChainMax > 0 {
		chainMax = strconv.Itoa(p.ChainMax)
	}
	res, err := c.FCall(ctx, "ns_batch_plan", nil, p.Sprint, p.Repo, p.Base, p.BatchID, p.Lease,
		strings.Join(p.Members, ","), strings.Join(p.Paths, ","), p.Class, p.FromTip, p.InputID, p.Parent, chainMax).Slice()
	if err != nil {
		return "", "", "", err
	}
	if len(res) >= 2 && res[0] == "REFUSED" {
		return "", "", "", &PlanRefusedError{Reason: fmt.Sprint(res[1])}
	}
	if len(res) < 4 || res[0] != "OK" {
		return "", "", "", fmt.Errorf("ns_batch_plan failed: %v", res)
	}
	return fmt.Sprint(res[3]), fmt.Sprint(res[1]), fmt.Sprint(res[2]), nil
}

// RefusedError is a nova_sprint write function's refusal: the writer gen or the publisher lease
// did not match (lease mismatch, lease gen mismatch, writer owner not nova-sprint), so it wrote
// nothing.
type RefusedError struct{ Fn, Reason string }

func (e *RefusedError) Error() string { return e.Fn + " REFUSED " + e.Reason }

func refusedText(fn, r string) (string, error) {
	if reason, ok := strings.CutPrefix(r, "REFUSED "); ok {
		return "", &RefusedError{Fn: fn, Reason: reason}
	}
	return r, nil
}

// CallBatchBind calls ns_batch_bind under the lease: OK or STALE; a lost lease is a *RefusedError.
func CallBatchBind(ctx context.Context, c *redis.Client, repo, base, batchID, leaseVal, fromTip, inputID string) (string, error) {
	r, err := c.FCall(ctx, "ns_batch_bind", nil, repo, base, batchID, leaseVal, fromTip, inputID).Text()
	if err != nil {
		return "", err
	}
	return refusedText("ns_batch_bind", r)
}

// CallUnitDrop calls ns_unit_drop under the lease: OK, ALREADY, STALE or NOTFOUND; a lost lease is a
// *RefusedError. A non-empty task queues one task of that kind on q:<author>.
func CallUnitDrop(ctx context.Context, c *redis.Client, sprint, unit, repo, base, leaseVal, head, reason, task string) (string, error) {
	r, err := c.FCall(ctx, "ns_unit_drop", nil, sprint, unit, repo, base, leaseVal, head, reason, task).Text()
	if err != nil {
		return "", err
	}
	return refusedText("ns_unit_drop", r)
}

// CallChainVoid calls ns_chain_void under the lease; it returns the batches voided behind batchID,
// in chain order. A lost lease is a *RefusedError.
func CallChainVoid(ctx context.Context, c *redis.Client, sprint, repo, base, batchID, leaseVal, reason string) ([]string, error) {
	res, err := c.FCall(ctx, "ns_chain_void", nil, sprint, repo, base, batchID, leaseVal, reason).StringSlice()
	if err != nil {
		return nil, err
	}
	if len(res) >= 2 && res[0] == "REFUSED" {
		return nil, &RefusedError{Fn: "ns_chain_void", Reason: res[1]}
	}
	if len(res) == 0 || res[0] != "OK" {
		return nil, fmt.Errorf("ns_chain_void %s: %v", batchID, res)
	}
	return res[1:], nil
}

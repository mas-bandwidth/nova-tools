package land

import (
	"context"
	"fmt"
	"strconv"

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
	if res[0] == "REUSE" {
		return "REUSE", fmt.Sprint(res[1]), nil
	}
	if res[0] != "OK" || len(res) < 3 {
		return "", "", fmt.Errorf("ns_batch_plan failed: %v", res)
	}
	return fmt.Sprint(res[1]), fmt.Sprint(res[2]), nil
}

// GateTakeResult holds the outcome of calling ns_gate_take.
type GateTakeResult struct {
	Status   string // "OK", "NOBUDGET", "NODATA", "STALE", "VOID"
	Base     string
	BatchID  string
	Attempt  int
	Token    string
	EntryID  string
	Reason   string
}

// CallGateTake calls ns_gate_take (spec 5.2).
func CallGateTake(ctx context.Context, c *redis.Client, repo, bench, slot, class string, cpuMilli, memMB int) (GateTakeResult, error) {
	res, err := c.FCall(ctx, "ns_gate_take", nil, repo, bench, slot, class, strconv.Itoa(cpuMilli), strconv.Itoa(memMB)).Slice()
	if err != nil {
		return GateTakeResult{}, err
	}
	if len(res) == 0 {
		return GateTakeResult{Status: "NODATA"}, nil
	}
	status := fmt.Sprint(res[0])
	switch status {
	case "OK":
		if len(res) < 6 {
			return GateTakeResult{}, fmt.Errorf("unexpected ns_gate_take OK reply: %v", res)
		}
		att, _ := strconv.Atoi(fmt.Sprint(res[3]))
		return GateTakeResult{
			Status:  "OK",
			Base:    fmt.Sprint(res[1]),
			BatchID: fmt.Sprint(res[2]),
			Attempt: att,
			Token:   fmt.Sprint(res[4]),
			EntryID: fmt.Sprint(res[5]),
		}, nil
	case "NOBUDGET":
		return GateTakeResult{Status: "NOBUDGET"}, nil
	case "NODATA":
		return GateTakeResult{Status: "NODATA"}, nil
	case "STALE", "VOID":
		batchID := ""
		if len(res) > 1 {
			batchID = fmt.Sprint(res[1])
		}
		return GateTakeResult{Status: status, BatchID: batchID}, nil
	default:
		return GateTakeResult{Status: status}, nil
	}
}

// CallGateClaim calls ns_gate_claim.
func CallGateClaim(ctx context.Context, c *redis.Client, repo, base, batchID string, attempt int, token, bench, slot string) (string, error) {
	return c.FCall(ctx, "ns_gate_claim", nil, repo, base, batchID, strconv.Itoa(attempt), token, bench, slot).Text()
}

// GateReceiptGIDParams holds optional gid receipt parameters for CallGateReceiptWithGID.
type GateReceiptGIDParams struct {
	GID           string
	Kind          string
	Head          string
	BaseSHA       string
	RequiredSetID string
	PolicyID      string
	RunnerID      string
	Pkg           string
	Test          string
}

// CallGateReceipt calls ns_gate_receipt.
func CallGateReceipt(ctx context.Context, c *redis.Client, repo, base, batchID string, attempt int, token, verdict, bench, worker, trainHead, trainTree, inputID, selection, steps, failing, flakyRerun, coreS string) (string, error) {
	return CallGateReceiptWithGID(ctx, c, repo, base, batchID, attempt, token, verdict, bench, worker, trainHead, trainTree, inputID, selection, steps, failing, flakyRerun, coreS, nil)
}

// CallGateReceiptWithGID calls ns_gate_receipt with optional gid receipt parameters.
func CallGateReceiptWithGID(ctx context.Context, c *redis.Client, repo, base, batchID string, attempt int, token, verdict, bench, worker, trainHead, trainTree, inputID, selection, steps, failing, flakyRerun, coreS string, gidParams *GateReceiptGIDParams) (string, error) {
	args := []any{
		repo, base, batchID, strconv.Itoa(attempt), token, verdict, bench, worker,
		trainHead, trainTree, inputID, selection, steps, failing, flakyRerun, coreS,
	}
	if gidParams != nil && gidParams.GID != "" {
		args = append(args,
			gidParams.GID, gidParams.Kind, gidParams.Head, gidParams.BaseSHA,
			gidParams.RequiredSetID, gidParams.PolicyID, gidParams.RunnerID,
			gidParams.Pkg, gidParams.Test,
		)
	}
	return c.FCall(ctx, "ns_gate_receipt", nil, args...).Text()
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

// CallBatchVoid calls ns_batch_void.
func CallBatchVoid(ctx context.Context, c *redis.Client, sprint, repo, base, batchID, reason string) error {
	res, err := c.FCall(ctx, "ns_batch_void", nil, sprint, repo, base, batchID, reason).Text()
	if err != nil {
		return err
	}
	if res != "OK" {
		return fmt.Errorf("ns_batch_void failed: %s", res)
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

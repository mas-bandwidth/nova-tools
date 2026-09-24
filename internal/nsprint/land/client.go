package land

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/pr"
	"github.com/redis/go-redis/v9"
)

// ErrUnresolved is wrapped when ns_unit_head met a write-once reap field
// (paths, card_type, cut_at) with a different value: the stored value is kept
// and s:<S>:unresolved names the field (nova-tools#3091).
var ErrUnresolved = errors.New("UNRESOLVED")

// ErrNoUnit is returned by CallRead when the unit hash does not exist: the
// read record is written, no unit field is (nova-tools#3091).
var ErrNoUnit = errors.New("NOUNIT")

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
	// Card is the label of the card whose TASK: line names the unit (arg 15).
	// Empty: paths, card_type and cut_at stay absent (MISSING).
	Card string
}

// CallUnitHead calls ns_unit_head. With a Card, it reads that card's PATHS
// and canonicalizes them with pr.CanonJSON (arg 16); ns_unit_head reads the
// card's card_type and cut_at itself. A refused PATHS still writes the unit,
// leaves paths absent and returns the seq with the refusal (wrapping
// pr.ErrRefused). A write-once field that met a different value returns the
// seq with an error wrapping ErrUnresolved that names the field.
func CallUnitHead(ctx context.Context, c *redis.Client, p UnitHeadParams) (int64, error) {
	var pathsJSON string
	var pathsErr error
	if p.Card != "" {
		raw, err := c.HGet(ctx, "s:"+p.Sprint+":card:"+p.Card, "paths").Result()
		switch {
		case err == nil:
			pathsJSON, pathsErr = pr.CanonJSON(raw)
		case !errors.Is(err, redis.Nil):
			return 0, err
		}
	}
	res, err := c.FCall(ctx, "ns_unit_head", nil,
		p.Sprint, p.Unit, p.Repo, p.Base, p.Branch, p.Head, p.BaseSHA,
		p.StackParent, p.Files, p.PathsHash, p.Security, p.Class, p.PR, p.Author,
		p.Card, pathsJSON,
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
	var errs []error
	if len(res) >= 3 && fmt.Sprint(res[2]) == "UNRESOLVED" {
		fields := make([]string, 0, len(res)-3)
		for _, f := range res[3:] {
			fields = append(fields, fmt.Sprint(f))
		}
		errs = append(errs, fmt.Errorf("%w %s", ErrUnresolved, strings.Join(fields, " ")))
	}
	if pathsErr != nil {
		errs = append(errs, pathsErr)
	}
	return seq, errors.Join(errs...)
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

// CallRead calls ns_read. It returns the seq and ErrNoUnit when the unit hash
// does not exist (the read record is written, no unit field is).
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
	if len(res) >= 3 && fmt.Sprint(res[2]) == "NOUNIT" {
		return seq, ErrNoUnit
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

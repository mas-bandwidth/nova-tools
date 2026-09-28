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

// ErrStaleHead is returned by CallUnitMergeable when the word was observed
// at a head that is not the unit's current head: nothing is written.
var ErrStaleHead = errors.New("STALE")

// Mergeable words, the forge's vocabulary (Forge).
const (
	MergeableYes      = "MERGEABLE"
	MergeableConflict = "CONFLICTING"
	MergeableUnknown  = "UNKNOWN"
)

// CallUnitMergeable calls ns_unit_mergeable, the one writer of a unit's
// mergeable word (nova-tools #3092 rev 7): word must be MERGEABLE,
// CONFLICTING or UNKNOWN, and head the unit's current head (ErrStaleHead
// otherwise; ErrNoUnit when the unit hash is absent). An unchanged word at
// the same head writes nothing and returns seq 0.
func CallUnitMergeable(ctx context.Context, c *redis.Client, sprint, unit, head, word string) (int64, error) {
	res, err := c.FCall(ctx, "ns_unit_mergeable", nil, sprint, unit, head, word).Slice()
	if err != nil {
		return 0, err
	}
	if len(res) == 0 {
		return 0, fmt.Errorf("ns_unit_mergeable: empty reply")
	}
	switch fmt.Sprint(res[0]) {
	case "OK":
		if len(res) < 2 {
			return 0, fmt.Errorf("ns_unit_mergeable: %v", res)
		}
		return strconv.ParseInt(fmt.Sprint(res[1]), 10, 64)
	case "SAME":
		return 0, nil
	case "NOUNIT":
		return 0, fmt.Errorf("ns_unit_mergeable %s: %w", unit, ErrNoUnit)
	case "STALE":
		return 0, fmt.Errorf("ns_unit_mergeable %s at %s: %w (unit head %v)", unit, head, ErrStaleHead, res[1:])
	}
	return 0, fmt.Errorf("ns_unit_mergeable %s: %v", unit, res)
}

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

// CallRelease calls ns_release with no may-hold roster: the holder's own
// release, a repair-scoped one, or a login:<x> hold's (#3139 3.4, 3.5).
func CallRelease(ctx context.Context, c *redis.Client, sprint, unit, holder, releasedBy, releaseKind, releaseReason, releaseURL string) (int64, error) {
	return CallReleaseAs(ctx, c, sprint, unit, holder, releasedBy, releaseKind, releaseReason, releaseURL, nil)
}

// CallReleaseAs calls ns_release with the lane's may-hold roster (arg 8): a
// down friend's hold is released only by a reader on it (3.4, L29c).
func CallReleaseAs(ctx context.Context, c *redis.Client, sprint, unit, holder, releasedBy, releaseKind, releaseReason, releaseURL string, mayHold []string) (int64, error) {
	res, err := c.FCall(ctx, "ns_release", nil, sprint, unit, holder, releasedBy, releaseKind, releaseReason, releaseURL, strings.Join(mayHold, ",")).Slice()
	if err != nil {
		return 0, err
	}
	if len(res) < 2 {
		return 0, fmt.Errorf("unexpected ns_release reply: %v", res)
	}
	if res[0] == "REFUSED" {
		return 0, fmt.Errorf("REFUSED %v", res[1])
	}
	if res[0] != "OK" {
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

// GateTakeResult holds the outcome of calling ns_gate_take.
type GateTakeResult struct {
	Status  string // "OK", "NOBUDGET", "NODATA", "STALE", "VOID"
	Base    string
	BatchID string
	Attempt int
	Token   string
	EntryID string
	Reason  string
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

package land

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/redis/go-redis/v9"
)

// UnitEvalResult represents the outcome of evaluating a unit for landability (§3.3).
type UnitEvalResult struct {
	Unit        string
	Head        string
	Landable    bool
	Reason      string
	WhoCanMove  string
	GID         string
	CIStatus    string // "OK", "stale", "missing", "nopolicy", "failed"
	ReadsAtHead int
	HoldsOpen   int
}

// EvaluateUnit evaluates one unit according to the 7 conditions of §3.3.
// If all conditions pass, it calls ns_unit_eval to transition to landable.
func EvaluateUnit(ctx context.Context, c *redis.Client, sprint, unit string, pol *BasePolicy) (*UnitEvalResult, error) {
	ukey := UnitKey(sprint, unit)
	u, err := c.HGetAll(ctx, ukey).Result()
	if err != nil {
		return nil, fmt.Errorf("read unit %s: %w", unit, err)
	}
	if len(u) == 0 {
		return nil, fmt.Errorf("unit %s not found", unit)
	}

	repo := u["repo"]
	base := u["base"]
	head := u["head"]
	baseSHA := u["base_sha"]
	state := u["state"]

	res := &UnitEvalResult{
		Unit: unit,
		Head: head,
	}

	// 7. Settled / cancelled check
	if state == "settled" || state == "cancelled" {
		res.Landable = false
		res.Reason = fmt.Sprintf("unit %s", state)
		return res, nil
	}

	// 6. Drop key check: if dropped, inputs must have changed
	if state == "dropped" {
		dropKey := u["drop_key"]
		h8 := head
		if len(h8) > 8 {
			h8 = h8[:8]
		}
		currSeq := u["seq"]
		expectedDropKey := fmt.Sprintf("%s:%s", h8, currSeq)
		if dropKey == expectedDropKey {
			res.Landable = false
			res.Reason = fmt.Sprintf("dropped with key %s: inputs unchanged", dropKey)
			res.WhoCanMove = u["author"]
			return res, nil
		}
	}

	// Supersede (§3.4, L29): a reader's APPROVE at the unit's current head
	// releases that reader's own open HOLD at an older head before the holds
	// condition is read. The reads come from the unit's readers index
	// (s:<S>:readers:<unit>, ns_read), one pipeline, no KEYS scan (§2.2).
	reads, err := unitReads(ctx, c, sprint, unit)
	if err != nil {
		return nil, fmt.Errorf("reads %s: %w", unit, err)
	}
	superseded, err := supersedeAtHead(ctx, c, sprint, unit, head, reads)
	if err != nil {
		return nil, fmt.Errorf("supersede %s: %w", unit, err)
	}
	if superseded > 0 {
		u["holds_open"] = c.HGet(ctx, ukey, "holds_open").Val()
	}

	// 1. CI receipt for expected identity (GID lookup, §3.3 / §3.7 / L31b)
	pkey := PolicyKey(repo, base)
	pRec, err := c.HGetAll(ctx, pkey).Result()
	if err != nil || len(pRec) == 0 {
		res.Landable = false
		res.CIStatus = "nopolicy"
		res.Reason = "ci nopolicy"
		res.WhoCanMove = "coordinator"
		return res, nil
	}

	policyID := pRec["policy_id"]
	requiredSetID := pRec["required_set_id"]
	runnerID := pRec["runner_id"]

	expectedGID := GID("single", base, baseSHA, requiredSetID, policyID, runnerID)
	res.GID = expectedGID

	ciRecKey := CIKey(repo, head, expectedGID)
	ciRec, err := c.HGetAll(ctx, ciRecKey).Result()
	if err == nil && len(ciRec) > 0 {
		verdict := ciRec["verdict"]
		if verdict == "OK" {
			res.CIStatus = "OK"
		} else {
			res.CIStatus = "failed"
			res.Landable = false
			res.Reason = fmt.Sprintf("ci %s", verdict)
			res.WhoCanMove = u["author"]
			return res, nil
		}
	} else {
		// Key absent. Check if any GIDs exist for this head (stale vs missing)
		gidsKey := CIGIDsKey(repo, head)
		gids, _ := c.SMembers(ctx, gidsKey).Result()
		if len(gids) > 0 {
			res.CIStatus = "stale"
			res.Reason = fmt.Sprintf("ci stale have=%s", strings.Join(gids, ","))
		} else {
			res.CIStatus = "missing"
			res.Reason = "ci missing"
		}

		// Queue the one CI single for this identity (deduplicated in ns_ci_single)
		if _, err := QueueCISingle(ctx, c, repo, base, unit, head, expectedGID); err != nil {
			return nil, fmt.Errorf("queue ci single %s: %w", unit, err)
		}

		res.Landable = false
		res.WhoCanMove = "workers"
		return res, nil
	}

	// 2. Reads at head >= readers (§3.3, Glenn ruling default 0)
	readersRequired := 0
	landBar := 10
	if pol != nil {
		readersRequired = pol.Readers
		if pol.LandBar > 0 {
			landBar = pol.LandBar
		}
	}

	res.ReadsAtHead = countApprovals(reads, head, landBar)
	if readersRequired > 0 && res.ReadsAtHead < readersRequired {
		res.Landable = false
		h8 := head
		if len(h8) > 8 {
			h8 = h8[:8]
		}
		res.Reason = fmt.Sprintf("reads %d/%d at %s", res.ReadsAtHead, readersRequired, h8)
		res.WhoCanMove = "readers"
		return res, nil
	}

	// 3. holds_open = 0 (§3.3)
	holdsOpen, _ := strconv.Atoi(u["holds_open"])
	res.HoldsOpen = holdsOpen
	if holdsOpen > 0 {
		res.Landable = false
		res.Reason = fmt.Sprintf("holds %d open", holdsOpen)
		res.WhoCanMove = "holder"
		return res, nil
	}

	// 4. Card done record at head (§3.3)
	cardDone := u["card_done"]
	doneFlag := u["done"]
	if cardDone != head && doneFlag != "1" {
		// Check key card:<unit>:done
		hasDoneKey, _ := c.Exists(ctx, fmt.Sprintf("card:%s:done", unit)).Result()
		if hasDoneKey == 0 {
			res.Landable = false
			res.Reason = "card not done at head"
			res.WhoCanMove = u["author"]
			return res, nil
		}
	}

	// 5. Stack parent landed or earlier (§3.3)
	stackParent := u["stack_parent"]
	if stackParent != "" && stackParent != "none" {
		// Resolve parent PR to unit if necessary
		resolvedParent := stackParent
		if strings.HasPrefix(stackParent, "#") {
			prStr := strings.TrimPrefix(stackParent, "#")
			if prNum, err := strconv.Atoi(prStr); err == nil {
				if pu := c.Get(ctx, PRUnitKey(sprint, repo, prNum)).Val(); pu != "" {
					resolvedParent = pu
				}
			}
		}
		parentState := c.HGet(ctx, UnitKey(sprint, resolvedParent), "state").Val()
		if parentState != "landed" {
			res.Landable = false
			res.Reason = fmt.Sprintf("stack parent %s (%s)", stackParent, parentState)
			res.WhoCanMove = "parent"
			return res, nil
		}
	}

	// All conditions met: promote to landable via ns_unit_eval!
	tier := 0
	if tStr := u["tier"]; tStr != "" {
		tier, _ = strconv.Atoi(tStr)
	}
	_, err = CallUnitEval(ctx, c, sprint, unit, repo, base, tier)
	if err != nil {
		res.Landable = false
		res.Reason = fmt.Sprintf("ns_unit_eval: %v", err)
		return res, nil
	}

	res.Landable = true
	res.Reason = "landable"
	return res, nil
}

// QueueCISingle queues the one ci single for a unit's expected identity
// through ns_ci_single (§3.3, L31b): QUEUED the first time, ALREADY on every
// later pass for the same head and gid (nothing written), HAVE when the
// receipt exists. The gates entry carries batch ci:<gid>, attempt 1 and a
// token from land:<repo>:tok.
func QueueCISingle(ctx context.Context, c *redis.Client, repo, base, unit, head, gid string) (string, error) {
	res, err := c.FCall(ctx, "ns_ci_single", nil, repo, base, unit, head, gid).StringSlice()
	if err != nil {
		return "", err
	}
	if len(res) < 1 {
		return "", fmt.Errorf("ns_ci_single: empty reply")
	}
	return res[0], nil
}

// ReadersKey is the unit's readers index (who has a read record), written
// by ns_read.
func ReadersKey(sprint, unit string) string {
	return "s:" + sprint + ":readers:" + unit
}

type unitRead struct {
	who  string
	read map[string]string
}

// unitReads reads every read record on the unit through its readers index:
// one SMEMBERS and one pipeline of HGETALLs.
func unitReads(ctx context.Context, c *redis.Client, sprint, unit string) ([]unitRead, error) {
	who, err := c.SMembers(ctx, ReadersKey(sprint, unit)).Result()
	if err != nil || len(who) == 0 {
		return nil, err
	}
	sort.Strings(who)
	pipe := c.Pipeline()
	cmds := make([]*redis.MapStringStringCmd, len(who))
	for i, w := range who {
		cmds[i] = pipe.HGetAll(ctx, ReadKey(sprint, unit, w))
	}
	if _, err := pipe.Exec(ctx); err != nil {
		return nil, err
	}
	out := make([]unitRead, 0, len(who))
	for i, w := range who {
		out = append(out, unitRead{who: w, read: cmds[i].Val()})
	}
	return out, nil
}

// countApprovals counts typed APPROVEs at head scoring >= landBar; JEV never counts.
func countApprovals(reads []unitRead, head string, landBar int) int {
	n := 0
	for _, r := range reads {
		if strings.EqualFold(r.who, "jev") {
			continue
		}
		if r.read["head"] == head && r.read["verdict"] == "APPROVE" {
			if score, _ := strconv.Atoi(r.read["score"]); score >= landBar {
				n++
			}
		}
	}
	return n
}

// supersedeAtHead releases, for each reader whose APPROVE is at the unit's
// current head, that reader's own open HOLD at another (so older) head. An
// APPROVE at an older head never releases a HOLD at the current one.
func supersedeAtHead(ctx context.Context, c *redis.Client, sprint, unit, head string, reads []unitRead) (int, error) {
	n := 0
	for _, r := range reads {
		if r.read["verdict"] != "APPROVE" || r.read["head"] != head || head == "" {
			continue
		}
		rel, err := SupersedeHoldsOnApprove(ctx, c, sprint, unit, r.who, head, func(newer, older string) bool {
			return newer == head && older != head
		})
		if err != nil {
			return n, err
		}
		n += len(rel)
	}
	return n, nil
}

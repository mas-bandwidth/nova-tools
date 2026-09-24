package land

import (
	"context"
	"fmt"
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

		// Queue CI single if needed
		queueCISingle(ctx, c, repo, base, unit, head, expectedGID)

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

	approvedReads := 0
	if readersRequired > 0 {
		// Count approvals at current head
		readPattern := fmt.Sprintf("s:%s:read:%s:*", sprint, unit)
		keys, _ := c.Keys(ctx, readPattern).Result()
		for _, k := range keys {
			parts := strings.Split(k, ":")
			who := parts[len(parts)-1]
			if strings.EqualFold(who, "jev") {
				continue // JEV never counted
			}
			r, err := c.HGetAll(ctx, k).Result()
			if err != nil {
				continue
			}
			if r["head"] == head && r["verdict"] == "APPROVE" {
				score, _ := strconv.Atoi(r["score"])
				if score >= landBar {
					approvedReads++
				}
			}
		}
		res.ReadsAtHead = approvedReads
		if approvedReads < readersRequired {
			res.Landable = false
			h8 := head
			if len(h8) > 8 {
				h8 = h8[:8]
			}
			res.Reason = fmt.Sprintf("reads %d/%d at %s", approvedReads, readersRequired, h8)
			res.WhoCanMove = "readers"
			return res, nil
		}
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

func queueCISingle(ctx context.Context, c *redis.Client, repo, base, unit, head, gid string) {
	// Add entry to land:<repo>:gates if not already present
	_ = c.XAdd(ctx, &redis.XAddArgs{
		Stream: GatesStream(repo),
		Values: map[string]interface{}{
			"base":     base,
			"unit":     unit,
			"head":     head,
			"gid":      gid,
			"kind":     "single",
			"priority": "ci",
		},
	}).Err()
}

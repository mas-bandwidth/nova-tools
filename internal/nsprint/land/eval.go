package land

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// EvalPass runs one evaluation pass across all units in sprint index s:<S>:units (§3).
func EvalPass(ctx context.Context, c *redis.Client, sprint, repo string, polRepo *RepoPolicy) (evaluated int, landable int, err error) {
	// First check inbound freshness
	fresh, reason, err := CheckInboundFreshness(ctx, c, time.Now())
	if err != nil {
		return 0, 0, fmt.Errorf("inbound freshness check: %w", err)
	}
	if !fresh {
		return 0, 0, fmt.Errorf("evaluation refused: %s", reason)
	}

	unitsKey := UnitsSetKey(sprint)
	units, err := c.SMembers(ctx, unitsKey).Result()
	if err != nil {
		return 0, 0, fmt.Errorf("list units: %w", err)
	}

	for _, u := range units {
		// Get base for unit
		base := c.HGet(ctx, UnitKey(sprint, u), "base").Val()
		var basePol *BasePolicy
		if polRepo != nil && polRepo.Bases != nil {
			basePol = polRepo.Bases[base]
		}

		res, err := EvaluateUnit(ctx, c, sprint, u, basePol)
		if err != nil {
			continue
		}
		evaluated++
		if res.Landable {
			landable++
		}
	}

	return evaluated, landable, nil
}

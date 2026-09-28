package fold

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/task"
	"github.com/redis/go-redis/v9"
)

// OwnerEst holds estimate vs actual fold metrics for one owner or for all tasks.
type OwnerEst struct {
	Owner      string
	N          int
	Est        int
	Actual     int
	Error      int
	Pct        string // "+40", "-17", "0", "-"
	Unest      int
	Unmeasured int
}

// Line formats the metrics as an EST line.
func (e OwnerEst) Line(isAll bool) string {
	if isAll {
		return fmt.Sprintf("EST all n=%d est=%d actual=%d error=%s pct=%s unest=%d unmeasured=%d",
			e.N, e.Est, e.Actual, FormatSigned(e.Error), e.Pct, e.Unest, e.Unmeasured)
	}
	return fmt.Sprintf("EST owner=%s n=%d est=%d actual=%d error=%s pct=%s unest=%d unmeasured=%d",
		e.Owner, e.N, e.Est, e.Actual, FormatSigned(e.Error), e.Pct, e.Unest, e.Unmeasured)
}

// FormatSigned formats an integer with an explicit sign (+N, -N or 0).
func FormatSigned(n int) string {
	if n > 0 {
		return fmt.Sprintf("+%d", n)
	}
	return strconv.Itoa(n)
}

// ComputeEstimates reads closed tasks from s:<sprint>:idx:task:closed in 2 round
// trips and computes estimate fold metrics grouped per owner and overall.
func ComputeEstimates(ctx context.Context, client *redis.Client, sprint string) ([]OwnerEst, OwnerEst, error) {
	if client == nil {
		return nil, OwnerEst{}, errors.New("est: nil redis client")
	}

	all := OwnerEst{Owner: "all", Pct: "-"}
	members, err := client.SMembers(ctx, "s:"+sprint+":idx:task:closed").Result()
	if err != nil && !errors.Is(err, redis.Nil) {
		return nil, OwnerEst{}, fmt.Errorf("est: smembers: %w", err)
	}
	if len(members) == 0 {
		return nil, all, nil
	}

	pipe := client.Pipeline()
	cmds := make([]*redis.SliceCmd, len(members))
	for i, id := range members {
		cmds[i] = pipe.HMGet(ctx, "task:"+id, "owner", "est", "claimed_at", "closed_at", "state", "evidence")
	}
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return nil, OwnerEst{}, fmt.Errorf("est: hmget: %w", err)
	}

	byOwner := make(map[string]*OwnerEst)

	for _, cmd := range cmds {
		val := cmd.Val()
		if len(val) < 6 {
			continue
		}
		owner := strField(val, 0)
		estStr := strField(val, 1)
		claimedAtStr := strField(val, 2)
		closedAtStr := strField(val, 3)
		state := strField(val, 4)
		evidence := strField(val, 5)

		// Closed tasks only, not cancelled, and evidence does not start with blocked:
		if state != "closed" {
			continue
		}
		if strings.HasPrefix(evidence, "blocked:") {
			continue
		}
		if owner == "" {
			owner = "unknown"
		}

		oe, exists := byOwner[owner]
		if !exists {
			oe = &OwnerEst{Owner: owner, Pct: "-"}
			byOwner[owner] = oe
		}

		hasEst := task.IsValidEst(estStr)
		var estVal int
		if hasEst {
			estVal, _ = strconv.Atoi(estStr)
		} else {
			oe.Unest++
			all.Unest++
		}

		hasMeasurement := false
		var actualVal int
		if claimedAtStr != "" && closedAtStr != "" {
			claimedAt, err1 := strconv.ParseInt(claimedAtStr, 10, 64)
			closedAt, err2 := strconv.ParseInt(closedAtStr, 10, 64)
			if err1 == nil && err2 == nil {
				hasMeasurement = true
				diff := closedAt - claimedAt
				if diff < 0 {
					diff = 0
				}
				actualVal = int(math.Floor(float64(diff)/60000.0 + 0.5))
			}
		}
		if !hasMeasurement {
			oe.Unmeasured++
			all.Unmeasured++
		}

		if hasEst && hasMeasurement {
			oe.N++
			oe.Est += estVal
			oe.Actual += actualVal

			all.N++
			all.Est += estVal
			all.Actual += actualVal
		}
	}

	var owners []string
	for o := range byOwner {
		owners = append(owners, o)
	}
	sort.Strings(owners)

	var result []OwnerEst
	for _, o := range owners {
		oe := byOwner[o]
		if oe.Est > 0 {
			oe.Error = oe.Actual - oe.Est
			pctVal := int(math.Round(float64(100*oe.Error) / float64(oe.Est)))
			oe.Pct = FormatSigned(pctVal)
		} else {
			oe.Actual = 0
			oe.Error = 0
			oe.Pct = "-"
		}
		result = append(result, *oe)
	}

	if all.Est > 0 {
		all.Error = all.Actual - all.Est
		pctVal := int(math.Round(float64(100*all.Error) / float64(all.Est)))
		all.Pct = FormatSigned(pctVal)
	} else {
		all.Actual = 0
		all.Error = 0
		all.Pct = "-"
	}

	return result, all, nil
}

// EstimateLines returns formatted EST output lines. If ownerFilter is specified,
// only that owner's line is returned; otherwise, lines for all owners sorted
// alphabetically are returned, followed by the EST all line.
func EstimateLines(ctx context.Context, client *redis.Client, sprint string, ownerFilter string) ([]string, error) {
	ownerList, all, err := ComputeEstimates(ctx, client, sprint)
	if err != nil {
		return nil, err
	}
	if ownerFilter != "" {
		for _, o := range ownerList {
			if o.Owner == ownerFilter {
				return []string{o.Line(false)}, nil
			}
		}
		return []string{OwnerEst{Owner: ownerFilter, Pct: "-"}.Line(false)}, nil
	}

	var lines []string
	for _, o := range ownerList {
		lines = append(lines, o.Line(false))
	}
	lines = append(lines, all.Line(true))
	return lines, nil
}

func strField(v []any, i int) string {
	if i >= len(v) || v[i] == nil {
		return ""
	}
	s, _ := v[i].(string)
	return s
}

package backpressure

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/redis/go-redis/v9"
)

// InterimTypeGateAfter is how many PRs a type may have produced with none
// landed before a new cut is refused. Interim until policy says otherwise.
const InterimTypeGateAfter = 40

// PolicyTypeGateAfter is the s:<S>:policy field for that threshold.
const PolicyTypeGateAfter = "type_gate_after"

// ErrCutRefused is a new cut of a type that has reached the gate with nothing
// landed. One landed PR lifts it. The cut is not written.
var ErrCutRefused = errors.New("cut refused")

// TypePRsKey is the set of PR ids a work type has produced.
func TypePRsKey(sprint, workType string) string {
	return "s:" + sprint + ":type:" + workType + ":prs"
}

// TypeLandedKey is the set of that type's PRs that have landed.
func TypeLandedKey(sprint, workType string) string {
	return "s:" + sprint + ":type:" + workType + ":landed"
}

// CheckCut reports whether a new card of workType may be cut. It reads the
// type's PR set and its landed set and writes nothing. A type with fewer than
// type_gate_after PRs is not paused. At or past the gate, zero landed refuses
// until the landed set is non-empty.
func CheckCut(ctx context.Context, st *store.Store, sprint, workType string) error {
	client, err := redisClient(st)
	if err != nil {
		return err
	}
	if err := checkSprint(sprint); err != nil {
		return err
	}
	if err := checkType(workType); err != nil {
		return err
	}
	pipe := client.Pipeline()
	policy := pipe.HGetAll(ctx, PolicyKey(sprint))
	prs := pipe.SCard(ctx, TypePRsKey(sprint, workType))
	landed := pipe.SCard(ctx, TypeLandedKey(sprint, workType))
	if _, err := pipe.Exec(ctx); err != nil && err != redis.Nil {
		return fmt.Errorf("backpressure: type gate %s %s: %w", sprint, workType, err)
	}
	fields, err := policy.Result()
	if err != nil && err != redis.Nil {
		return fmt.Errorf("backpressure: policy %s: %w", sprint, err)
	}
	after, err := gateAfter(fields)
	if err != nil {
		return err
	}
	nPRs, err := prs.Result()
	if err != nil {
		return fmt.Errorf("backpressure: type prs %s: %w", workType, err)
	}
	nLanded, err := landed.Result()
	if err != nil {
		return fmt.Errorf("backpressure: type landed %s: %w", workType, err)
	}
	if nPRs >= int64(after) && nLanded == 0 {
		return fmt.Errorf("%w: type %s has 0 of %d landed; new cuts paused until one lands", ErrCutRefused, workType, nPRs)
	}
	return nil
}

func gateAfter(fields map[string]string) (int, error) {
	v, ok := fields[PolicyTypeGateAfter]
	if !ok {
		return InterimTypeGateAfter, nil
	}
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil || n < 1 {
		return 0, fmt.Errorf("backpressure: policy %s=%q: want a positive integer", PolicyTypeGateAfter, v)
	}
	return n, nil
}

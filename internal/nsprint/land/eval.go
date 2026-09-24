package land

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/redis/go-redis/v9"
)

// EvalConfig is one evaluator pass's inputs (§3.1): the sprint and repo, the
// repo policy, and the bench mirror (git) the heads and files come from.
type EvalConfig struct {
	Sprint    string
	Repo      string
	Policy    *RepoPolicy
	MirrorDir string        // ~/nova-bench/mirror/<repo>.git; empty: no mirror reads
	Consumer  string        // ev:github consumer name in group "land"; default "eval"
	FetchEach time.Duration // reconcile fetch interval (3.2); default 60 s
	Now       func() time.Time
}

// EvalReport is what one pass did.
type EvalReport struct {
	Inbound   *InboundResult
	Missed    []string // INBOUND MISSED lines from the git reconcile
	Evaluated int
	Landable  int
}

// EvalPass runs one evaluation pass across all units in sprint index s:<S>:units (§3)
// with no mirror; RunEval is the full pass.
func EvalPass(ctx context.Context, c *redis.Client, sprint, repo string, polRepo *RepoPolicy) (evaluated int, landable int, err error) {
	rep, err := RunEval(ctx, c, EvalConfig{Sprint: sprint, Repo: repo, Policy: polRepo})
	if rep == nil {
		return 0, 0, err
	}
	return rep.Evaluated, rep.Landable, err
}

// RunEval is one evaluator pass (§3.1-3.4), in order:
//  1. drain ev:github as group "land" (ConsumeInbound): webhook heads from the
//     mirror, inbound login:<x> holds and their releases, then the beat;
//  2. the git reconcile (3.2, L32): fetch refs/heads/* into the mirror at most
//     every FetchEach, read every unit branch, and write INBOUND MISSED and the
//     head from git for a ref that moved with no delivery;
//  3. refuse the pass when the inbound consumer is not fresh (3.2);
//  4. EvaluateUnit on every unit in s:<S>:units (supersede, gid lookup, the
//     landable rule, 3.3-3.4).
func RunEval(ctx context.Context, c *redis.Client, cfg EvalConfig) (*EvalReport, error) {
	now := time.Now
	if cfg.Now != nil {
		now = cfg.Now
	}
	every := cfg.FetchEach
	if every <= 0 {
		every = 60 * time.Second
	}
	rep := &EvalReport{}

	in, err := ConsumeInbound(ctx, c, cfg.Sprint, cfg.Repo, cfg.MirrorDir, cfg.Consumer)
	rep.Inbound = in
	var errs []error
	if err != nil {
		// A failed entry stays pending and the beat says so; the freshness
		// check below refuses the pass on it.
		errs = append(errs, fmt.Errorf("inbound: %w", err))
	}

	units, err := c.SMembers(ctx, UnitsSetKey(cfg.Sprint)).Result()
	if err != nil {
		return rep, fmt.Errorf("list units: %w", err)
	}
	sort.Strings(units)

	if cfg.MirrorDir != "" {
		missed, err := reconcileUnits(ctx, c, cfg, units, every)
		rep.Missed = missed
		if err != nil {
			errs = append(errs, fmt.Errorf("reconcile: %w", err))
		}
	}

	fresh, reason, err := CheckInboundFreshness(ctx, c, now())
	if err != nil {
		return rep, fmt.Errorf("inbound freshness check: %w", err)
	}
	if !fresh {
		errs = append(errs, fmt.Errorf("evaluation refused: %s", reason))
		return rep, errors.Join(errs...)
	}

	for _, u := range units {
		base := c.HGet(ctx, UnitKey(cfg.Sprint, u), "base").Val()
		var basePol *BasePolicy
		if cfg.Policy != nil && cfg.Policy.Bases != nil {
			basePol = cfg.Policy.Bases[base]
		}

		res, err := EvaluateUnit(ctx, c, cfg.Sprint, u, basePol)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		rep.Evaluated++
		if res.Landable {
			rep.Landable++
		}
	}

	return rep, errors.Join(errs...)
}

// reconcileUnits is the git half of 3.2: one reconcile fetch per interval,
// then every unit branch of this repo read from the mirror.
func reconcileUnits(ctx context.Context, c *redis.Client, cfg EvalConfig, units []string, every time.Duration) ([]string, error) {
	if _, err := FetchMirror(ctx, cfg.MirrorDir, every); err != nil {
		return nil, err
	}
	pipe := c.Pipeline()
	cmds := make([]*redis.SliceCmd, len(units))
	for i, u := range units {
		cmds[i] = pipe.HMGet(ctx, UnitKey(cfg.Sprint, u), "repo", "branch")
	}
	if len(units) > 0 {
		if _, err := pipe.Exec(ctx); err != nil {
			return nil, err
		}
	}
	var refs []string
	for _, cmd := range cmds {
		v := cmd.Val()
		repo, _ := v[0].(string)
		branch, _ := v[1].(string)
		if repo == cfg.Repo && branch != "" {
			refs = append(refs, "refs/heads/"+branch)
		}
	}
	gitRefs, err := MirrorRefs(ctx, cfg.MirrorDir, refs)
	if err != nil {
		return nil, err
	}
	return ReconcileMirror(ctx, c, cfg.Sprint, cfg.Repo, cfg.MirrorDir, gitRefs)
}

package taskcard

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

// DutyWorkingKey is the Redis set holding active duty card IDs.
const DutyWorkingKey = "duty:working"

// Default duty completion estimates.
const (
	DefaultLandEST = 15 * time.Minute
	DefaultOpsEST  = 5 * time.Minute
)

// DutyOpts specifies parameters for starting a duty card.
type DutyOpts struct {
	ID     string        // optional explicit ID; derived if empty
	Stream string        // "ops" for coordinator duties, or stream name (e.g. "dev")
	Op     string        // for ops duties: "fn-deploy", "self-update", "fleet-roll", "sprint-clear"
	Actor  string        // default: "coordinator"
	Title  string        // human-readable title
	EST    time.Duration // estimated completion duration; 0 selects default
	Now    time.Time     // timestamp; zero uses time.Now()
}

// Duty represents a duty card that has been started.
type Duty struct {
	ID        string
	Stream    string
	Friend    string
	EST       time.Duration
	CreatedAt time.Time
}

// ActiveDuty describes an active working duty card in Redis.
type ActiveDuty struct {
	ID        string
	Stream    string
	Friend    string
	EST       time.Duration
	CreatedAt time.Time
	Age       time.Duration
	Overdue   bool
}

// ParseEST parses an EST string or falls back to stream defaults.
func ParseEST(s, stream string) time.Duration {
	if d, err := time.ParseDuration(s); err == nil && d > 0 {
		return d
	}
	if stream == "ops" {
		return DefaultOpsEST
	}
	return DefaultLandEST
}

// StartDuty pushes a duty card to ready, moves it to working, and records it
// in duty:working.
func StartDuty(ctx context.Context, c redis.Cmdable, opts DutyOpts) (*Duty, error) {
	if opts.Actor == "" {
		opts.Actor = "coordinator"
	}
	if opts.Now.IsZero() {
		opts.Now = time.Now()
	}
	if opts.Stream == "" {
		opts.Stream = "ops"
	}
	if opts.EST <= 0 {
		if opts.Stream == "ops" {
			opts.EST = DefaultOpsEST
		} else {
			opts.EST = DefaultLandEST
		}
	}
	if opts.ID == "" {
		if opts.Stream == "ops" {
			op := opts.Op
			if op == "" {
				op = "duty"
			}
			opts.ID = fmt.Sprintf("ops:%s-%d", op, opts.Now.UnixMilli())
		} else {
			opts.ID = fmt.Sprintf("%s:land", opts.Stream)
			if exists, _ := c.Exists(ctx, Key(opts.ID)).Result(); exists > 0 {
				where, _ := c.HGet(ctx, Key(opts.ID), "where").Result()
				if where == "working" {
					return nil, fmt.Errorf("duty %s is already working", opts.ID)
				}
				opts.ID = fmt.Sprintf("%s:land-%d", opts.Stream, opts.Now.UnixMilli())
			}
		}
	}
	if opts.Title == "" {
		if opts.Stream == "ops" {
			if opts.Op != "" {
				opts.Title = opts.Op
			} else {
				opts.Title = "ops duty"
			}
		} else {
			opts.Title = "land stream " + opts.Stream
		}
	}

	req := PushRequest{
		ID:     opts.ID,
		Where:  "ready",
		Stream: opts.Stream,
		Friend: opts.Actor,
		Kind:   "duty",
		Title:  opts.Title,
		By:     opts.Actor,
		Why:    "duty start",
		Fields: []string{
			"est", opts.EST.String(),
			"est_ms", strconv.FormatInt(opts.EST.Milliseconds(), 10),
		},
	}
	if _, err := Push(ctx, c, req); err != nil {
		return nil, fmt.Errorf("push duty card: %w", err)
	}

	if _, err := Move(ctx, c, opts.ID, "working", Opts{
		As:  opts.Actor,
		By:  opts.Actor,
		Why: "duty start",
	}); err != nil {
		return nil, fmt.Errorf("move duty card to working: %w", err)
	}

	if err := c.SAdd(ctx, DutyWorkingKey, opts.ID).Err(); err != nil {
		return nil, fmt.Errorf("sadd %s: %w", DutyWorkingKey, err)
	}

	return &Duty{
		ID:        opts.ID,
		Stream:    opts.Stream,
		Friend:    opts.Actor,
		EST:       opts.EST,
		CreatedAt: opts.Now,
	}, nil
}

// LandDuty moves a working duty card to landed and removes it from duty:working,
// recording the wall duration.
func LandDuty(ctx context.Context, c redis.Cmdable, id, actor, sha, why string, wall time.Duration) error {
	if actor == "" {
		actor = "coordinator"
	}
	if sha == "" {
		sha = "-"
	}
	exists, err := c.Exists(ctx, Key(id)).Result()
	if err != nil || exists == 0 {
		_ = c.SRem(ctx, DutyWorkingKey, id).Err()
		return nil
	}
	where, _ := c.HGet(ctx, Key(id), "where").Result()
	if where == "landed" {
		_ = c.SRem(ctx, DutyWorkingKey, id).Err()
		return nil
	}
	wallStr := wall.Truncate(time.Millisecond).String()
	wallMS := strconv.FormatInt(wall.Milliseconds(), 10)
	opts := Opts{
		Sha: sha,
		By:  actor,
		Why: why,
		Fields: []string{
			"wall", wallStr,
			"wall_ms", wallMS,
		},
	}
	_, err = Move(ctx, c, id, "landed", opts)
	_ = c.SRem(ctx, DutyWorkingKey, id).Err()
	if err != nil {
		return fmt.Errorf("land duty card %s: %w", id, err)
	}
	return nil
}

// CancelDuty moves a duty card to done (fail) and removes it from duty:working.
func CancelDuty(ctx context.Context, c redis.Cmdable, id, actor, why string) error {
	if actor == "" {
		actor = "coordinator"
	}
	exists, err := c.Exists(ctx, Key(id)).Result()
	if err != nil || exists == 0 {
		_ = c.SRem(ctx, DutyWorkingKey, id).Err()
		return nil
	}
	where, _ := c.HGet(ctx, Key(id), "where").Result()
	if where == "done" {
		_ = c.SRem(ctx, DutyWorkingKey, id).Err()
		return nil
	}
	opts := Opts{
		OK:  "fail",
		By:  actor,
		Why: why,
	}
	_, err = Move(ctx, c, id, "done", opts)
	_ = c.SRem(ctx, DutyWorkingKey, id).Err()
	if err != nil {
		return fmt.Errorf("cancel duty card %s: %w", id, err)
	}
	return nil
}

package reconcile

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/table"
	"github.com/redis/go-redis/v9"
)

// TableCheckDuty checks the live sprint table against the Redis sets once per minute (#4341).
// If drift is detected, it writes drift to the sprint's status line (hash s:<S> field drift).
// If no drift is detected, it clears the drift field.
type TableCheckDuty struct {
	Client redis.UniversalClient
	Every  time.Duration // default 1 minute
	Sprint string        // optional override, default open sprint
	Now    func() time.Time
	Check  func(ctx context.Context, client redis.UniversalClient, sprint string, now time.Time) (*table.CheckResult, error)

	mu   sync.Mutex
	last time.Time
}

// NewTableCheckDuty returns a TableCheckDuty with default 1 minute interval.
func NewTableCheckDuty(client redis.UniversalClient) *TableCheckDuty {
	return &TableCheckDuty{
		Client: client,
		Every:  time.Minute,
	}
}

func (d *TableCheckDuty) now() time.Time {
	if d.Now != nil {
		return d.Now()
	}
	return time.Now()
}

// Run executes the duty pass under lease l.
func (d *TableCheckDuty) Run(ctx context.Context, l *Lease) (Counts, error) {
	if d == nil || d.Client == nil || l == nil {
		return Counts{}, errors.New("table-check: client and lease are required")
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	now := d.now()
	every := d.Every
	if every <= 0 {
		every = time.Minute
	}
	if !d.last.IsZero() && now.Sub(d.last) < every {
		return Counts{}, nil
	}
	d.last = now

	checkFn := d.Check
	if checkFn == nil {
		checkFn = table.CheckSets
	}
	res, err := checkFn(ctx, d.Client, d.Sprint, now)
	if err != nil {
		return Counts{Refused: 1}, fmt.Errorf("table-check: %w", err)
	}
	sprint := res.Sprint
	if sprint == "" {
		return Counts{}, nil
	}
	key := "s:" + sprint
	if res.HasDrift() {
		if err := d.Client.HSet(ctx, key, "drift", "DRIFT").Err(); err != nil {
			return Counts{Refused: 1}, fmt.Errorf("table-check: set drift: %w", err)
		}
	} else {
		if err := d.Client.HDel(ctx, key, "drift").Err(); err != nil && !errors.Is(err, redis.Nil) {
			return Counts{Refused: 1}, fmt.Errorf("table-check: clear drift: %w", err)
		}
	}
	return Counts{}, nil
}

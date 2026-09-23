// Package sprint opens and closes a sprint in the nova-sprint store (#2939).
//
// A sprint is open when its hash s:<S> has status=open and <S> is a member of
// the sprints set. The claim function (task_claim.lua) and the deal pass read
// the status; the table reads the set. Open also places <S> in sprint:order
// (ZADD NX, score = open time in ms) because task take without --sprint and
// the deal pass walk that order; an existing order score is kept. Each verb is
// one MULTI/EXEC round trip, so a reader never sees the status without the
// set membership, and every verb is idempotent.
package sprint

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/redis/go-redis/v9"
)

// Status is the one-word state a verb prints.
type Status string

const (
	Open   Status = "open"
	Closed Status = "closed"
	Absent Status = "absent"
)

// nameRE is the card identity's sprint shape (card/identity.go).
var nameRE = regexp.MustCompile(`^[a-z0-9-]{1,40}$`)

// ValidName reports whether s can name a sprint.
func ValidName(s string) bool { return nameRE.MatchString(s) }

func check(st *store.Store, name string) error {
	if st == nil || st.Client() == nil {
		return errors.New("sprint: store is required")
	}
	if !ValidName(name) {
		return fmt.Errorf("sprint name %q is not [a-z0-9-]{1,40}", name)
	}
	return nil
}

// SetOpen sets s:<S> status=open, adds <S> to sprints and, when absent, to
// sprint:order, in one transaction.
func SetOpen(ctx context.Context, st *store.Store, name string, now time.Time) (Status, error) {
	if err := check(st, name); err != nil {
		return "", err
	}
	_, err := st.Client().TxPipelined(ctx, func(p redis.Pipeliner) error {
		p.HSet(ctx, "s:"+name, "status", string(Open))
		p.SAdd(ctx, "sprints", name)
		p.ZAddNX(ctx, "sprint:order", redis.Z{Score: float64(now.UnixMilli()), Member: name})
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("sprint open %s: %w", name, err)
	}
	return Open, nil
}

// SetClosed sets s:<S> status=closed and removes <S> from sprints in one
// transaction. sprint:order keeps its entry: every reader of the order also
// checks status=open, and a reopen keeps its place.
func SetClosed(ctx context.Context, st *store.Store, name string) (Status, error) {
	if err := check(st, name); err != nil {
		return "", err
	}
	_, err := st.Client().TxPipelined(ctx, func(p redis.Pipeliner) error {
		p.HSet(ctx, "s:"+name, "status", string(Closed))
		p.SRem(ctx, "sprints", name)
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("sprint close %s: %w", name, err)
	}
	return Closed, nil
}

// Get reads s:<S> status: absent when the field is missing, otherwise the
// stored word (open, closed, or another state a later verb writes).
func Get(ctx context.Context, st *store.Store, name string) (Status, error) {
	if err := check(st, name); err != nil {
		return "", err
	}
	v, err := st.Client().HGet(ctx, "s:"+name, "status").Result()
	if errors.Is(err, redis.Nil) {
		return Absent, nil
	}
	if err != nil {
		return "", fmt.Errorf("sprint status %s: %w", name, err)
	}
	return Status(v), nil
}

// Line is the one line every sprint verb prints.
func Line(name string, s Status) string { return name + " status=" + string(s) }

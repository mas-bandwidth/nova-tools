package reconcile

// This file: the fsck duty (nova-tools#3925, "no ghost cards"). Glenn
// 2026-09-25 12:25 PM ET: "What do we have to do to stop ghost cards showing
// up, and make sure that the fleet table is accurate?" The card record is
// the truth; every table cell is a view of it (02_card_move.lua). Once per
// FsckEvery the duty walks EVERY sprint (ns_fsck_sprints: the open ones,
// every one in sprint:order, and every sprint a card id in a bench, stream
// or friend view names), in three round trips:
//
//  1. FCALL_RO ns_fsck_sprints: the names and each one's status;
//  2. one pipeline: ns_card_repair per sprint (drift repaired from the
//     record, every registered bench's views swept), then ns_sprint_retire
//     per sprint that is not open (a sprint closed by any path: its cards
//     not done move to done/fail through the one move), then one
//     ns_card_members_repair: every member of every ws, bench and friend
//     set that is not the id of an existing record (MEMBER-NOT-A-CARD,
//     nova-tools#4054) is removed with a ws:log receipt;
//  3. the fenced ns_fsck_finding: proc:reconciler fsck_at, and a repair over
//     0 is a finding there (a writer broke the invariant: a bug to fix).
//
// Counts.Repaired is fixed (the members removed included) + retired, so the
// loop prints its DUTY line only when the duty fixed something.

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/card"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
)

// FsckEvery is the fsck duty's cadence.
const FsckEvery = 60 * time.Second

// Fsck is the fsck duty. Every is its cadence (FsckEvery when zero); the
// first Run walks at once.
type Fsck struct {
	Client *redis.Client
	Every  time.Duration

	mu     sync.Mutex
	last   time.Time
	loaded bool
	// Last is the most recent walk that ran (tests and the receipt).
	Last FsckWalk
}

// FsckWalk is one walk: the sprints walked, what repair fixed, what retire
// moved, and the first drift and refusal lines.
type FsckWalk struct {
	Sprints int
	Fixed   int
	Retired int
	// NotACard is the members removed from the table sets (counted in Fixed).
	NotACard int
	Lines    []string
}

// Line is the finding text: fixed=<n> retired=<n> sprints=<n> then the
// first lines.
func (w FsckWalk) Line() string {
	s := fmt.Sprintf("fixed=%d retired=%d sprints=%d", w.Fixed, w.Retired, w.Sprints)
	if len(w.Lines) > 0 {
		s += " first=" + strings.Join(w.Lines[:min(len(w.Lines), 3)], "; ")
	}
	return s
}

// Run walks when FsckEvery has passed since the last walk.
func (f *Fsck) Run(ctx context.Context, l *Lease) (Counts, error) {
	if f == nil || f.Client == nil || l == nil {
		return Counts{}, errors.New("fsck: no store or no lease")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	every := f.Every
	if every <= 0 {
		every = FsckEvery
	}
	if !f.last.IsZero() && time.Since(f.last) < every {
		return Counts{}, nil
	}
	f.last = time.Now()
	if !f.loaded {
		if err := fn.LoadMissing(ctx, f.Client); err != nil {
			return Counts{}, err
		}
		f.loaded = true
	}
	w, err := FsckAll(ctx, f.Client, l.Token())
	f.Last = w
	return Counts{Repaired: w.Fixed + w.Retired}, err
}

// FsckAll is one walk over every sprint (the three round trips above).
func FsckAll(ctx context.Context, c *redis.Client, token string) (FsckWalk, error) {
	pairs, err := c.FCallRO(ctx, "ns_fsck_sprints", nil).StringSlice()
	if err != nil {
		return FsckWalk{}, fmt.Errorf("fsck sprints: %w", err)
	}
	type row struct {
		name           string
		repair, retire *redis.Cmd
	}
	var rows []row
	pipe := c.Pipeline()
	for i := 0; i+1 < len(pairs); i += 2 {
		r := row{name: pairs[i], repair: pipe.FCall(ctx, "ns_card_repair", nil, pairs[i])}
		if st := pairs[i+1]; st != "" && st != "open" && st != "opening" {
			r.retire = pipe.FCall(ctx, "ns_sprint_retire", nil, pairs[i], token)
		}
		rows = append(rows, r)
	}
	members := pipe.FCall(ctx, "ns_card_members_repair", nil, "fsck-duty")
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) && members.Err() != nil &&
		!anyCmd(rows, func(r row) *redis.Cmd { return r.repair }) {
		return FsckWalk{}, fmt.Errorf("fsck walk: %w", err)
	}
	w := FsckWalk{Sprints: len(rows)}
	var errs []string
	if mem, err := members.StringSlice(); err != nil {
		errs = append(errs, fmt.Sprintf("members: %v", err))
	} else if rep, err := card.ParseMembers("ns_card_members_repair", mem); err != nil {
		errs = append(errs, err.Error())
	} else {
		w.NotACard = int(rep.Removed)
		w.Fixed += w.NotACard
		if rep.Bad > 0 {
			w.Lines = append(w.Lines, rep.Lines...)
		}
	}
	for _, r := range rows {
		rep, err := r.repair.StringSlice()
		switch {
		case err != nil:
			errs = append(errs, fmt.Sprintf("repair %s: %v", r.name, err))
		case len(rep) < card.FsckFields || rep[0] != "FSCK":
			errs = append(errs, fmt.Sprintf("repair %s: reply %q", r.name, rep))
		default:
			fixed, _ := strconv.Atoi(rep[12])
			w.Fixed += fixed
			if fixed > 0 {
				w.Lines = append(w.Lines, rep[card.FsckFields:]...)
			}
		}
		if r.retire == nil {
			continue
		}
		ret, err := r.retire.StringSlice()
		switch {
		case err != nil:
			errs = append(errs, fmt.Sprintf("retire %s: %v", r.name, err))
		case len(ret) > 0 && ret[0] == "FENCED":
			return w, fmt.Errorf("fsck retire %s: %w", r.name, ErrFenced)
		case len(ret) >= 2 && ret[0] == "RETIRED":
			n, _ := strconv.Atoi(ret[1])
			w.Retired += n
			if n > 0 {
				w.Lines = append(w.Lines, fmt.Sprintf("retired %d of closed %s", n, r.name))
			}
			for _, x := range ret[2:] {
				errs = append(errs, "retire "+r.name+": "+x)
			}
		}
	}
	reply, err := c.FCall(ctx, "ns_fsck_finding", nil, token, w.Fixed, w.Retired, w.Line()).StringSlice()
	if err != nil {
		errs = append(errs, fmt.Sprintf("finding: %v", err))
	} else if len(reply) > 0 && reply[0] == "FENCED" {
		return w, fmt.Errorf("fsck finding: %w", ErrFenced)
	}
	if len(errs) > 0 {
		return w, errors.New("fsck: " + strings.Join(errs, "; "))
	}
	return w, nil
}

// anyCmd reports whether a pipeline's error was per command (some command
// answered), not a lost connection.
func anyCmd[T any](rows []T, cmd func(T) *redis.Cmd) bool {
	for _, r := range rows {
		if cmd(r).Err() == nil {
			return true
		}
	}
	return false
}

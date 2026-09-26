package stream

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"sync"
	"time"
)

// One writer per stream (nova-tools #4324): land:merge:<stream> owner is the
// one atomic claim every writer of stream/<slug> takes before it builds,
// pushes or merges: the land duty for its pass (DutyOwner), the land watch
// before it cuts a merge or escalation card (CardOwner, the card's child
// then runs nova-sprint land --card), and a hand run of nova-sprint land
// (HandOwner, renewed while it runs). A claim held live by another is
// refused with OwnedError; land_stream.lua's claim op is the arbiter.

// OwnerKey is the stream's claim and merge card record, land:merge:<stream>.
func OwnerKey(stream string) string { return "land:merge:" + stream }

// CardOwner is a merge or escalation card's claim.
func CardOwner(id string) string { return "card:" + id }

// DutyOwner is the land duty's claim: live while lease:land:<repo> holds
// token.
func DutyOwner(repo, token string) string { return "duty:" + repo + ":" + token }

// HandOwner is a hand run's claim: by, the host and the process.
func HandOwner(by string) string {
	host, _ := os.Hostname()
	return fmt.Sprintf("hand:%s@%s:%d", by, host, os.Getpid())
}

// DefaultHandTTL is a hand claim's life between renewals (every third).
const DefaultHandTTL = 2 * time.Minute

// OwnedError is a claim another owner holds live: the stream is theirs.
type OwnedError struct{ Stream, Owner string }

func (e *OwnedError) Error() string {
	return fmt.Sprintf("LAND-OWNER stream=%s owner=%s", field(e.Stream), field(e.Owner))
}

func ownerKeys(streams []string) ([]string, map[string]string) {
	keys := make([]string, 0, len(streams))
	byKey := map[string]string{}
	for _, s := range streams {
		k := OwnerKey(s)
		keys = append(keys, k)
		byKey[k] = s
	}
	return keys, byKey
}

// Claim takes every stream for owner in one atomic call, or returns an
// *OwnedError naming the live holder (nothing written). until is when a
// card claim still being pushed, or a hand claim, ends unless renewed.
func Claim(ctx context.Context, c Client, streams []string, owner string, now, until time.Time) error {
	keys, byKey := ownerKeys(streams)
	res, err := eval(ctx, c, "claim", map[string]any{"keys": keys, "owner": owner,
		"now": strconv.FormatInt(now.UnixMilli(), 10), "until": strconv.FormatInt(until.UnixMilli(), 10)})
	if err != nil {
		return err
	}
	if res[0] == "HELD" && len(res) >= 3 {
		return &OwnedError{Stream: byKey[res[1]], Owner: res[2]}
	}
	if res[0] != "OK" {
		return fmt.Errorf("land_stream.lua claim: %v", res)
	}
	return nil
}

// Release drops owner's claim on every stream it still holds; it returns
// how many it dropped.
func Release(ctx context.Context, c Client, streams []string, owner string) (int, error) {
	keys, _ := ownerKeys(streams)
	res, err := eval(ctx, c, "release", map[string]any{"keys": keys, "owner": owner})
	if err != nil {
		return 0, err
	}
	n, _ := strconv.Atoi(res[len(res)-1])
	return n, nil
}

// Hold claims the streams for a hand owner and renews the claim every third
// of ttl until release is called; release drops it. now is the clock (nil
// is time.Now). A renewal that finds the claim taken stops renewing (the
// run's next write is then a second writer's: its lease lapsed).
func Hold(ctx context.Context, c Client, streams []string, owner string, ttl time.Duration, now func() time.Time) (release func(), err error) {
	if now == nil {
		now = time.Now
	}
	if ttl <= 0 {
		ttl = DefaultHandTTL
	}
	t := now()
	if err := Claim(ctx, c, streams, owner, t, t.Add(ttl)); err != nil {
		return nil, err
	}
	rctx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		tick := time.NewTicker(ttl / 3)
		defer tick.Stop()
		for {
			select {
			case <-rctx.Done():
				return
			case <-tick.C:
				t := now()
				if Claim(rctx, c, streams, owner, t, t.Add(ttl)) != nil {
					return
				}
			}
		}
	}()
	return func() {
		cancel()
		wg.Wait()
		_, _ = Release(context.WithoutCancel(ctx), c, streams, owner)
	}, nil
}

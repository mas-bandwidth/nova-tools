package lifecycle

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"time"
)

// Claim linearizes READY->CLAIMED: it compare-and-swaps the card's current rev
// and, in that same event, mints an unguessable attempt. A missing card is
// READY at rev 0. A racer against a claimed card is a conflict and writes nothing.
func (s *Store) Claim(card, bench, route string) (string, int, error) {
	if s == nil {
		return "", 0, fmt.Errorf("lifecycle: store is nil")
	}
	if !validID(card) || !validID(bench) || !validID(route) {
		return "", 0, fmt.Errorf("%w: card, bench and route must be file-safe identities", ErrRefused)
	}
	var attempt string
	var rev int
	err := s.mutate(func() error {
		state := Ready
		curRev := 0
		n := 0
		fence := 0
		if p := s.cards[card]; p != nil {
			state = p.State
			curRev = p.Rev
			n = p.Attempts
			fence = p.FenceEpoch
		}
		if state != Ready {
			return fmt.Errorf("%w: card %s is %s, not READY", ErrConflict, card, state)
		}
		id, err := mintID()
		if err != nil {
			return err
		}
		if err := os.MkdirAll(s.attemptDir(id), 0o755); err != nil {
			return err
		}
		attempt = id
		rev = curRev + 1
		n++
		ev := Event{
			Card:        card,
			Attempt:     strptr(attempt),
			Prior:       strptr(Ready),
			New:         Claimed,
			Rev:         rev,
			Bench:       strptr(bench),
			Route:       strptr(route),
			Source:      strptr(card),
			Limits:      &Limits{Attempts: n, Max: MaxAttempts},
			At:          stamp(time.Now()),
			Idempotency: "claim:" + card + ":" + attempt,
			FenceEpoch:  fence,
		}
		return s.commit(ev)
	})
	if err != nil {
		return "", 0, err
	}
	return attempt, rev, nil
}

func mintID() (string, error) {
	buf := make([]byte, idBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("lifecycle: minting identity: %w", err)
	}
	id := hex.EncodeToString(buf)
	if len(id) != idHexLen {
		return "", fmt.Errorf("lifecycle: minted identity has width %d, want %d", len(id), idHexLen)
	}
	return id, nil
}

package friend

import (
	"context"
	"time"
)

// BeatTimeout leaves room before the next one-second beat. The sender honors
// its context; a failed attempt records no session proof (docs/SPEC-FRIEND.md,
// The beat; tla/Presence.tla, BeatFresh).
const BeatTimeout = 900 * time.Millisecond

// Heartbeat sends immediately and on every one-second tick, independently of
// bus reads, session checks and filesystem walks. Each send is bounded and
// serialized; an error belongs to that attempt and never stops the next one
// (docs/SPEC-FRIEND.md, The beat; tla/Presence.tla, Heartbeat).
func Heartbeat(ctx context.Context, send func(context.Context) error) error {
	ticker := time.NewTicker(BeatEvery)
	defer ticker.Stop()
	for {
		if ctx.Err() != nil {
			return nil
		}
		call, cancel := context.WithTimeout(ctx, BeatTimeout)
		_ = send(call) // ignored: the sender records this attempt's error; liveness retries every tick
		cancel()
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

// RunningNone is the friend-beat --running value for an explicit empty list.
// The server's beat allow-list refuses an empty value, and a lone "-" is not a
// card id. An omitted --running is unknown and keeps the last list; this value
// clears it (the daemon's last lane ended).
const RunningNone = "-"

// BeatReport is what the daemon knows without reading the bus or disk on the
// heartbeat path (docs/SPEC-FRIEND.md, The beat). Counts are absent until known.
// Running nil means the jobs are unknown; a non-nil empty slice means none are
// running and the next beat clears the stored list.
type BeatReport struct {
	Width          int
	Started        time.Time
	Running        []string
	Working, Queue *int
}

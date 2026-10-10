package sprint

import (
	"crypto/sha256"
	"encoding/binary"
	"time"
)

// RouteRestFor is how long a rested route should stay rested.
const RouteRestFor = 5 * time.Minute

// restEnd computes the time when a rested route or provider should resume.
// It adds ±20% deterministic jitter based on route name and start time,
// ensuring replays produce the same end time.
func restEnd(route string, start time.Time, now time.Time) time.Time {
	// Derive a deterministic hash from route name and start time.
	h := sha256.New()
	h.Write([]byte(route))
	binary.Write(h, binary.BigEndian, start.UnixNano())
	sum := h.Sum(nil)

	// Use first 8 bytes to get a value in [0, 1).
	val := binary.BigEndian.Uint64(sum[:8])
	frac := float64(val) / float64(^uint64(0))

	// Map frac from [0,1) to [-0.2, 0.2].
	jitter := (frac*2 - 1) * 0.2

	base := now.Add(RouteRestFor)
	jittered := base.Add(time.Duration(float64(RouteRestFor) * jitter))
	return jittered
}

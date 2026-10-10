package sprint

import (
	"time"
)

// ProviderRestFor is how long a rested provider should stay rested.
const ProviderRestFor = 5 * time.Minute

// providerRestEnd computes the time when a rested provider should resume.
// It uses the same jitter logic as route restEnd for consistency.
func providerRestEnd(provider string, start time.Time, now time.Time) time.Time {
	return restEnd(provider, start, now)
}

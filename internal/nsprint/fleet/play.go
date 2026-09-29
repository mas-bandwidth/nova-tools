package fleet

import (
	"context"

	"github.com/redis/go-redis/v9"
)

// PlayRunner is the seam that triggers fleet convergence.
type PlayRunner func(ctx context.Context, client *redis.Client) error

// Play triggers fleet convergence via the play runner seam.
var Play PlayRunner = DefaultPlay

// DefaultPlay is the default play runner for fleet convergence.
func DefaultPlay(ctx context.Context, client *redis.Client) error {
	return nil
}

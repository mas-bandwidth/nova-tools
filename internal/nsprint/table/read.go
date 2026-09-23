package table

import (
	"context"
	"fmt"

	"github.com/redis/go-redis/v9"
)

// Read takes one consistent server snapshot. Callers must load the
// nova_sprint function library during deployment; rendering is read-only.
func Read(ctx context.Context, client *redis.Client) (*Snapshot, error) {
	raw, err := client.FCallRo(ctx, "ns_snapshot", []string{}).Result()
	if err != nil {
		return nil, fmt.Errorf("snapshot: %w", err)
	}
	list, ok := raw.([]any)
	if !ok {
		return nil, fmt.Errorf("snapshot: unexpected reply %T", raw)
	}
	return Parse(list)
}

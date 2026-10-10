// Package sprint contains the fleet properties that the sprint table reads.
// The tick calls DropLegacy to remove properties it no longer reads.
package sprint

import (
	"context"
	"log"
	"strings"

	"github.com/redis/go-redis/v9"
)

// dropLegacyPrefix is the prefix of legacy properties the sprint no longer reads.
// These were left behind when rests moved to one property per provider.
const dropLegacyPrefix = "route_rest_"

// DropLegacy drops properties with the legacy prefix from table t.
// It logs one line saying what it dropped.
func DropLegacy(ctx context.Context, c redis.Cmdable, t string) error {
	key := "table:" + t + ":props"
	// Get all properties.
	props, err := c.HGetAll(ctx, key).Result()
	if err != nil {
		return err
	}
	// Find legacy properties to drop.
	var legacy []string
	for p := range props {
		if strings.HasPrefix(p, dropLegacyPrefix) {
			legacy = append(legacy, p)
		}
	}
	if len(legacy) == 0 {
		return nil
	}
	// Drop them in one call.
	var fields []string
	for _, p := range legacy {
		fields = append(fields, p)
	}
	if err := c.HDel(ctx, key, fields...).Err(); err != nil {
		return err
	}
	log.Printf("sprint: dropped %d legacy properties from table %s: %s",
		len(legacy), t, strings.Join(legacy, ", "))
	return nil
}

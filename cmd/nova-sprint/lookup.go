package main

import (
	"context"
	"errors"
	"fmt"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/taskcard"
	"github.com/redis/go-redis/v9"
)

// cardField reads one field of task:<id>. Absent, unknown and failed never
// collapse (#4399 round 5, Stella's audit of the numeric --pr lookup): only
// redis.Nil is absent ("", nil); any other error comes back naming the
// operation and the key it read, with the line that inspects the record,
// so a refusal prints the store's failure (WRONGTYPE, a dial, an ACL) and
// never reads it as "no value".
func cardField(ctx context.Context, c redis.Cmdable, id, field string) (string, error) {
	key := taskcard.Key(id)
	v, err := c.HGet(ctx, key, field).Result()
	switch {
	case err == nil:
		return v, nil
	case errors.Is(err, redis.Nil):
		return "", nil
	}
	return "", fmt.Errorf("HGET %s %s failed: %v; next: nova-sprint redis TYPE %s, then HGETALL %s, shows the record", key, field, err, key, key)
}

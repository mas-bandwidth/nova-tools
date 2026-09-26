package friend_test

import (
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/friend"
	"github.com/redis/go-redis/v9"
)

func apLifeLog(t *testing.T, client *redis.Client) int {
	t.Helper()
	n := 0
	for _, m := range fsCapLog(t, client) {
		if m.Values["actor"] == friend.ActorLife {
			n++
		}
	}
	return n
}

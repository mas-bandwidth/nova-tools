package land

import (
	"context"

	"github.com/redis/go-redis/v9"
)

// CallPubStep calls ns_pub_step: pushed, verified or dead on an unresolved
// intent under the publisher fence. It returns OK or STALE.
func CallPubStep(ctx context.Context, c *redis.Client, repo, base, batchID, state, leaseVal string) (string, error) {
	return c.FCall(ctx, "ns_pub_step", nil, repo, base, batchID, state, leaseVal).Text()
}

// CallTip calls ns_tip: the tip record read from the remote, under the
// publisher fence. It returns OK or STALE.
func CallTip(ctx context.Context, c *redis.Client, repo, base, sha, by, leaseVal string) (string, error) {
	return c.FCall(ctx, "ns_tip", nil, repo, base, sha, by, leaseVal).Text()
}

// PubActiveKey names the one unresolved intent on a base.
func PubActiveKey(repo, base string) string {
	return "land:" + repo + ":" + base + ":pub:active"
}

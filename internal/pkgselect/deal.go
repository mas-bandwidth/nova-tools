package pkgselect

import (
	"fmt"
	"strings"
)

// Deal returns shard's packages (1-based, of shards) from pkgs, the live
// packages in `go list` order (stable: go list sorts).
//
// THE HEAVY PACKAGES FIRST, ONE PER SHARD. heavy names packages by a trailing
// path (`cmd/nova-worker` matches `<module>/cmd/nova-worker`); the k-th heavy name
// takes shard k (mod shards), so no two heavy packages share a shard while
// there are shards for them, and every other package goes round-robin in list
// order, continuing after the heavy ones: the first takes shard
// len(heavy) mod shards. A count-only deal kept the heaviest packages together
// and the leg holding them crossed the job cap. If two names match one package
// the later name wins. internal/ci: TestHostedDealSplitsTheHeavyPackages and
// TestCertificationRaceShardsPartitionTheLiveTree.
func Deal(pkgs []string, heavy []string, shards, shard int) ([]string, error) {
	if shards < 1 || shard < 1 || shard > shards {
		return nil, fmt.Errorf("deal: shard %d of %d is not a shard (want 1 <= shard <= shards)", shard, shards)
	}
	var out []string
	r := 0
	for _, p := range pkgs {
		k := 0
		for j, h := range heavy {
			if strings.HasSuffix(p, "/"+h) {
				k = j + 1
			}
		}
		var s int
		if k > 0 {
			s = (k - 1) % shards
		} else {
			s = (len(heavy) + r) % shards
			r++
		}
		if s == shard-1 {
			out = append(out, p)
		}
	}
	return out, nil
}

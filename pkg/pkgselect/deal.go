package pkgselect

import (
	"fmt"
	"strings"
)

// Deal returns shard's packages (1-based, of shards) from pkgs, the live
// packages in `go list` order (stable: go list sorts).
//
// THE HEAVY PACKAGES FIRST, ONE PER SHARD, AND ALONE. heavy names packages by a
// trailing path (`cmd/nova-swarm` matches `<module>/cmd/nova-swarm`); the k-th
// heavy name takes shard k (mod shards), so no two heavy packages share a shard
// while there are shards for them. Every other package goes round-robin in list
// order over the shards no heavy name took (shards len(heavy)+1 .. shards): a
// heavy package's test binary has its runner to itself. `go test` runs up to
// GOMAXPROCS package binaries at once, each with GOMAXPROCS parallel tests, so on
// a 3-core hosted runner a heavy package dealt beside eight light ones got about a
// third of the machine for its own -timeout, and internal/ci (75 s of summed test
// time at GOMAXPROCS=3 on an M5, 28 s wall alone) crossed the 75 s cap on
// macos-latest (certification run 38259191532 at 2cea177b3). Only when every shard
// holds a heavy package (len(heavy) >= shards) does the round-robin continue over
// all of them, after the heavy ones, as before. A count-only deal kept the heaviest
// packages together and the leg holding them crossed the job cap. If two names
// match one package the later name wins. internal/ci:
// TestHostedDealSplitsTheHeavyPackages and
// TestCertificationRaceShardsPartitionTheLiveTree.
func Deal(pkgs []string, heavy []string, shards, shard int) ([]string, error) {
	if shards < 1 || shard < 1 || shard > shards {
		return nil, fmt.Errorf("deal: shard %d of %d is not a shard (want 1 <= shard <= shards)", shard, shards)
	}
	// free is the shards the round-robin may use: the ones no heavy name took,
	// or every shard when the heavy names fill them all.
	first, free := len(heavy), shards-len(heavy)
	if free <= 0 {
		first, free = len(heavy)%shards, shards
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
			s = (first + r%free) % shards
			r++
		}
		if s == shard-1 {
			out = append(out, p)
		}
	}
	return out, nil
}

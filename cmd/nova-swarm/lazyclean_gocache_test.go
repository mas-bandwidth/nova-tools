package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/pkg/gocache"
)

// The member's build cache limit (2026-10-04): --gocache-limit, default gocache.Limit; a
// cache over it with every entry in use loses nothing and is said once an hour, never once
// a round. The rounds are explicit calls on a fixed clock; nothing here waits.

// buildEntry writes one Go build cache output entry of size bytes, last used at.
func buildEntry(t *testing.T, dir, seed string, size int, at time.Time) string {
	t.Helper()
	sum := sha256.Sum256([]byte(seed))
	h := hex.EncodeToString(sum[:])
	path := filepath.Join(dir, h[:2], h+"-d")
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(strings.Repeat("x", size)), 0o644))
	require.NoError(t, os.Chtimes(path, at, at))
	return path
}

// TestTheLazyTrimHoldsTheMembersLimitAndSaysAllInUseOnceAnHour pins the lazy round's use of
// the limit: the member's own (cacheLimit) when given, gocache.Limit when not; a cache over
// it whose entries are all younger than the floor keeps every entry, and the round says so
// once, then not again within the hour.
func TestTheLazyTrimHoldsTheMembersLimitAndSaysAllInUseOnceAnHour(t *testing.T) {
	t.Parallel()
	assert.Equal(t, gocache.Limit, (&nativeRunner{}).gocacheLimit(), "no --gocache-limit: the default")
	assert.Equal(t, int64(20*gib), gocache.Limit, "the default is 20 GiB")

	p := newPool(t)
	p.r.cacheLimit = 4 << 10
	now := time.Date(2026, 10, 4, 13, 45, 0, 0, time.UTC)
	dir := p.r.goBuildCache()
	var paths []string
	for i := range 10 { // 10 KiB, every entry used in the last 45 minutes
		paths = append(paths, buildEntry(t, dir, "e"+strconv.Itoa(i), 1024, now.Add(-time.Duration(i*5)*time.Minute)))
	}
	rounds := gocache.Subdirs/cacheDirsPerRound + 20 // measured, then twenty rounds over the limit
	for i := range rounds {
		p.r.lazy(now.Add(time.Duration(i) * lazyEvery))
	}
	lines := strings.Split(strings.TrimSpace(p.errb.String()), "\n")
	require.Len(t, lines, 1, "said once, not once a round: %q", p.errb.String())
	assert.Equal(t, "CLEAN go build cache: over the limit, all entries in use: raise the limit (10.0 KiB now, limit 4.0 KiB, every entry used in the last two hours; nothing is removed; said once an hour; nova-swarm member --gocache-limit <GiB>)", lines[0])
	for _, path := range paths {
		assert.FileExists(t, path, "an entry a build may read is never removed")
	}
}

// TestMemberRefusesAGocacheLimitUnderOneGiB pins the flag's refusal, saying what it wants.
func TestMemberRefusesAGocacheLimitUnderOneGiB(t *testing.T) {
	t.Parallel()
	var out, errb bytes.Buffer
	code := run(append(memberFull(t.TempDir()), "--gocache-limit", "0"), bytes.NewReader(nil), &out, &errb, time.Now())
	require.Equal(t, 2, code)
	assert.Contains(t, errb.String(), "--gocache-limit is the GiB the shared Go build cache is held under: 1 or more (default 20)")
}

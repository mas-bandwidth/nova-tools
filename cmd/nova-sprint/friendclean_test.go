package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/gocache"
	"github.com/mas-bandwidth/nova-tools/internal/testkit"
)

func pathThere(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

// A friend's build cache, <name>-working/.cache/go-build, is held under the limit as the
// pool's is: least recently used first, never an entry used in the last two hours.
func TestFriendCleanCapsAFriendsGoBuildCache(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC()
	root := t.TempDir()
	cache := filepath.Join(root, "ada-working", ".cache", "go-build")
	hex := "0123456789abcdef"
	entry := func(i int, age time.Duration) string {
		sub := "0" + string(hex[i])
		name := strings.Repeat(string(hex[i]), 64) + "-d"
		testkit.Tree(t, cache, map[string]string{sub + "/" + name: strings.Repeat("x", 1000)})
		p := filepath.Join(cache, sub, name)
		require.NoError(t, os.Chtimes(p, now.Add(-age), now.Add(-age)))
		return p
	}
	old := []string{entry(0, 30*time.Hour), entry(1, 20*time.Hour), entry(2, 10*time.Hour)}
	fresh := entry(3, time.Hour)
	for _, dry := range []bool{true, false} {
		cl := &friendClean{root: root, days: cleanDays, dry: dry, now: now, cache: gocache.Bounds{Limit: 2500, Slack: 500, Remove: 1 << 30}}
		cl.cleanCache("ada", filepath.Join(root, "ada-working"))
		assert.Equal(t, int64(2000), cl.freed, "dry=%v", dry)
		assert.Contains(t, strings.Join(cl.lines, "\n"), "FRIENDS-CLEAN CACHE friend=ada")
		assert.Equal(t, dry, pathThere(old[0]), "dry=%v", dry)
		assert.Equal(t, dry, pathThere(old[1]), "dry=%v", dry)
		assert.True(t, pathThere(old[2]), "the trim stops once under the limit less the slack")
		assert.True(t, pathThere(fresh))
	}
}

// Every refusal names what it wants and removes nothing.
func TestFriendCleanRefusals(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.a.friends = friendRows()
	dir := t.TempDir()
	for _, tc := range []struct {
		line string
		code int
		says string
	}{
		{"friend clean --root " + dir + " --days 0", 2, "--days is at least 1"},
		{"friend clean --root " + filepath.Join(dir, "nope"), 2, "--root wants the directory"},
		{"friend clean --root " + dir + " ada", 2, "takes no words"},
		{"friend clean --root " + dir, exitCannotRead, "holds no friend row"},
		{"friend clean --root " + dir + " --pg postgres://x@h/db --file f.json", 2, "exclusive"},
		{"friend clean --root " + dir + " --file " + filepath.Join(dir, "absent.json"), exitCannotRead, "the config cannot be read"},
	} {
		code, _, errs := ta.do(tc.line)
		assert.Equal(t, tc.code, code, "%s: %s", tc.line, errs)
		assert.Contains(t, errs, tc.says, tc.line)
	}
}

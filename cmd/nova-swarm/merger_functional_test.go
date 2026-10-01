//go:build functional

package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
	"github.com/mas-bandwidth/nova-tools/internal/testbin"
	"github.com/redis/go-redis/v9"
)

// fakeGreenGH is the gh the merger reads a head's checks with: one check run,
// concluded green.
const fakeGreenGH = `#!/bin/sh
echo '{"total_count":1,"check_runs":[{"name":"ci","status":"completed","conclusion":"success"}]}'
`

// TestMergerFunctionalLandsTheQueueOnARealStore is the merger against the real
// sprint: a store in the container, one card worked, read ok twice and accepted
// by the tick's pump, its head pushed to origin's sprint/a-1.w1; the built
// `nova-swarm member --merger` builds sprint/a.e<epoch>, pushes it, reads green
// checks through its gh, fast-forwards origin's main and feeds `merge --batch 1`
// as itself, and the next tick lands the card (docs/SPEC-SWARM.md, `member
// --merger`; tla/Merger.tla).
func TestMergerFunctionalLandsTheQueueOnARealStore(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	addr := testutil.Start(t)
	c := redis.NewClient(&redis.Options{Addr: addr})
	defer c.Close()
	require.NoError(t, fn.Load(ctx, c))
	d := &memberDrive{t: t, addr: addr, bin: builtSprint(t)}

	root := t.TempDir()
	cfg := filepath.Join(root, "gitconfig")
	write(t, cfg, "[user]\n\tname = merger\n\temail = merger@example.com\n")
	origin := filepath.Join(root, "origin.git")
	seed := filepath.Join(root, "seed")
	runGit(t, "", "init", "-q", "-b", "main", "--", seed)
	write(t, filepath.Join(seed, "f"), "base\n")
	gitAs(t, seed, "add", "f")
	gitAs(t, seed, "commit", "-q", "-m", "base")
	runGit(t, "", "clone", "-q", "--bare", "--", seed, origin)
	base := gitAs(t, seed, "rev-parse", "HEAD")
	write(t, filepath.Join(seed, "a"), "the work of a-1\n")
	gitAs(t, seed, "add", "a")
	gitAs(t, seed, "commit", "-q", "-m", "the work of a-1")
	head := gitAs(t, seed, "rev-parse", "HEAD")
	runGit(t, seed, "push", "-q", origin, "HEAD:refs/heads/sprint/a-1.w1")

	first, rest, _ := strings.Cut(memberCard, "\n")
	d.must("init", "--members", "m1:2", "--readers", "reader-a,reader-b")
	d.must("add", "--stream", "a", "--count", "1", "--brief", first+"\nbase-repo: "+origin+"\nBASE: main\n"+rest)
	d.must("start")
	as := func(actor string, args ...string) {
		t.Helper()
		code, out, errb := d.sprint(actor, args...)
		require.Equal(t, 0, code, "%s %v\n%s%s", actor, args, out, errb)
	}
	as("m1", "fleet", "beat", "m1") // m1 is up: the tick deals to it
	for i := 0; i < 5 && !strings.Contains(d.must("queue", "--as", "m1"), "a-1.w1"); i++ {
		d.must("tick")
	}
	epoch := fmt.Sprint(d.where().Epoch)
	as("m1", "take", "--as", "m1", "--epoch", epoch)
	as("m1", "finish", "--as", "m1", "a-1.w1@1", "--epoch", epoch, "--head", head, "--branch", "sprint/a-1.w1", "--report", "done")
	// a reader's queue is its beat: both are up, and the tick asks them
	asked := func() bool {
		qa, qb := d.must("queue", "--as", "reader-a"), d.must("queue", "--as", "reader-b")
		return strings.Contains(qa, "a-1") && strings.Contains(qb, "a-1")
	}
	for i := 0; i < 5 && !asked(); i++ {
		d.must("tick")
	}
	for _, r := range []string{"reader-a", "reader-b"} {
		as(r, "read", "--as", r, "--begin", "--epoch", epoch)
		as(r, "read", "--as", r, "--ok", "--epoch", epoch)
	}
	// the pump accepts a card with two ok reads at the next tick (the machine is RUNNING)
	w := d.where()
	for i := 0; i < 5 && cellInt(w, "merge", "a", "queued") == 0; i++ {
		d.must("tick")
		w = d.where()
	}
	require.Equal(t, 1, cellInt(w, "merge", "a", "queued"), "%+v\n%s", w.Tables, d.must("card", "a-1"))
	require.Equal(t, "merging", strings.TrimSpace(w.Tables["merge"]["a"]["state"]), "%+v", w.Tables["merge"])

	gh := filepath.Join(root, "gh")
	require.NoError(t, testbin.WriteExecutable(gh, []byte(fakeGreenGH), 0o755))
	cmd := exec.CommandContext(ctx, builtTool, "member", "--merger", "--as", "merger", "--root", filepath.Join(root, "merger"),
		"--sprint", d.bin, "--gh", gh, "--ticks", "2", "--every", "100ms")
	cmd.Env = append(os.Environ(), "NOVA_SPRINT_REDIS="+addr, "GIT_CONFIG_GLOBAL="+cfg, "GIT_CONFIG_NOSYSTEM=1")
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "%s", out)
	assert.Contains(t, string(out), "merge a batch=a-1 branch=sprint/a.e"+epoch+" head=")
	assert.Contains(t, string(out), "merge a batch batch=a-1 exit=0")

	landedHead := strings.TrimSpace(runGit(t, origin, "rev-parse", "refs/heads/main"))
	assert.NotEqual(t, base, landedHead)
	assert.Equal(t, strings.TrimSpace(runGit(t, origin, "rev-parse", "refs/heads/sprint/a.e"+epoch)), landedHead, "main fast-forwarded to the stream branch")
	runGit(t, origin, "merge-base", "--is-ancestor", head, landedHead)
	w = d.where()
	assert.Equal(t, 1, cellInt(w, "merge", "a", "merged"), "%+v", w.Tables["merge"])
	d.must("tick") // the landing reaches the work table at the next tick's pump
	w = d.where()
	assert.Equal(t, int64(1), w.Landed, "%+v", w.Tables)
	card := d.must("card", "a-1")
	assert.Contains(t, card, "merged into a and landed by merger", "the fact was fed as the merger")
}

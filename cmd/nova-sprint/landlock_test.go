package main

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// One lander per clone: a pass of land holds the lock beside the clone to its end, a second
// pass is refused naming the first, a lock whose holder is gone is taken over and said, a
// hand land beside the server's run --land is refused naming the server, and a dry run
// takes no lock (docs/SPEC-SPRINT.md, section 7, land-one-lander-now.w1).
func TestLandRefusesWhileAnotherLanderHoldsTheClone(t *testing.T) {
	t.Parallel()
	land := func(r *landRig) string { return "land --repo-dir " + r.clone + " --base main" }
	queuedOne := func(r *landRig) {
		r.ok("add --stream s1 --count 1 --one")
		r.queued(map[string]string{"s1-1": r.head("s1-1", "main", "a.txt", "a\n")}, "s1-1")
	}
	t.Run("a second land on the clone is refused naming the first", func(t *testing.T) {
		t.Parallel()
		r := newLandRig(t)
		queuedOne(r)
		first := &lander{a: r.a}
		require.Empty(t, first.lockClone(r.clone))
		before := r.git(r.remote, "rev-parse", "main")
		code, _, errs := r.do(land(r))
		assert.Equal(t, 1, code)
		assert.Contains(t, errs, "the clone "+r.clone+" is held by another lander, pid "+fmt.Sprint(os.Getpid())+" (nova-sprint land, since ")
		assert.Equal(t, before, r.git(r.remote, "rev-parse", "main"), "nothing was pushed")
		assert.Equal(t, map[string]string{"s1-1": "merging/queued"}, r.places("s1-1"))
		assert.FileExists(t, landLockPath(r.clone), "the first pass's lock is its own to release")

		first.unlockClones()
		assert.Contains(t, r.ok(land(r)), "LAND OK stream=s1 cards=1 base=main")
		assert.Equal(t, map[string]string{"s1-1": "landed/merged"}, r.places("s1-1"))
		assert.NoFileExists(t, landLockPath(r.clone), "the pass releases its lock at its end")
	})
	t.Run("a lock whose holder is gone is taken over and said", func(t *testing.T) {
		t.Parallel()
		r := newLandRig(t)
		queuedOne(r)
		stale := fmt.Sprintf(`{"pid":%d,"started":"-","verb":"land","token":"gone","at":"2026-10-04T15:21:00Z"}`+"\n", math.MaxInt32)
		require.NoError(t, os.WriteFile(landLockPath(r.clone), []byte(stale), 0o600))
		out := r.ok(land(r))
		assert.Contains(t, out, fmt.Sprintf("LAND TAKEOVER stream=s1 dir=%s pid=%d verb=land since=2026-10-04T15:21:00Z", r.clone, math.MaxInt32))
		assert.Contains(t, out, "LAND OK stream=s1 cards=1 base=main")
		assert.Equal(t, map[string]string{"s1-1": "landed/merged"}, r.places("s1-1"))
		assert.NoFileExists(t, landLockPath(r.clone))
	})
	t.Run("a lock that names no lander is refused, not taken", func(t *testing.T) {
		t.Parallel()
		r := newLandRig(t)
		queuedOne(r)
		require.NoError(t, os.WriteFile(landLockPath(r.clone), []byte("junk\n"), 0o600))
		code, _, errs := r.do(land(r))
		assert.Equal(t, 1, code)
		assert.Contains(t, errs, "the lock "+landLockPath(r.clone)+" names no lander")
		assert.Equal(t, map[string]string{"s1-1": "merging/queued"}, r.places("s1-1"))
	})
	t.Run("a hand land beside the server's run --land is refused naming the server", func(t *testing.T) {
		t.Parallel()
		r := newLandRig(t)
		// the server's pass, with nothing queued, records itself
		r.a.landLazy = true
		r.ok(land(r))
		r.a.landLazy = false
		require.FileExists(t, landServerPath(filepath.Join(r.dir, "land")))
		queuedOne(r)

		code, out, errs := r.do(land(r))
		assert.Equal(t, 1, code)
		assert.Empty(t, out)
		assert.Contains(t, errs, "nova-sprint land REFUSED: the server, pid "+fmt.Sprint(os.Getpid())+" (nova-sprint run --land, since ")
		assert.Contains(t, errs, "; run: nova-sprint where")
		assert.Equal(t, map[string]string{"s1-1": "merging/queued"}, r.places("s1-1"))

		// a dry run beside it is not refused, and takes no lock
		assert.Contains(t, r.ok(land(r)+" --dry-run"), "dry_run=yes")
		assert.NoFileExists(t, landLockPath(r.clone))

		// the server's own pass lands
		r.a.landLazy = true
		assert.Contains(t, r.ok(land(r)), "LAND OK stream=s1 cards=1 base=main")
		r.a.landLazy = false
		assert.Equal(t, map[string]string{"s1-1": "landed/merged"}, r.places("s1-1"))
	})
}

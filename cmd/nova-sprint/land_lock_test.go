package main

import (
	"os"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Two landers never share a clone (landlock.go): while one land holds the clone between
// its merges and its push, a second land by hand refuses at once, before it reads
// anything, naming the holder (its pid and label), and the server's lander (the land
// loop's land) refuses the batch, its clone named as in use, nothing fetched, pushed or
// reported; the first land then lands its whole batch, its merges and its tip its own
// (2026-10-04 3:28-3:38 PM ET: a land by hand and the server's lander in one clone
// committed one stream's merge on another's branch and reported a card landed whose merge
// no branch of origin held).
func TestTwoLandersNeverShareAClone(t *testing.T) {
	t.Parallel()
	r := newLandRig(t)
	heads := landTwo(r)
	reached, release := make(chan struct{}), make(chan struct{})
	r.a.beforePush = func(attempt int) {
		if attempt == 1 {
			close(reached)
			<-release
		}
	}
	type result struct {
		code      int
		out, errs string
	}
	first := make(chan result)
	go func() {
		code, out, errs := r.do("land --repo-dir " + r.clone + " --base main")
		first <- result{code, out, errs}
	}()
	<-reached
	pid := "pid=" + strconv.Itoa(os.Getpid())

	code, out, errs := r.do("land --repo-dir " + r.clone + " --base main")
	assert.Equal(t, 1, code, out+errs)
	assert.Contains(t, errs, "nova-sprint land REFUSED: the lander of ")
	assert.Contains(t, errs, "is in use by another lander: "+pid)
	assert.Contains(t, errs, `label="nova-sprint land by coordinator (pid `)
	assert.Contains(t, errs, "nothing was read, fetched, pushed or reported")

	// the server's lander: the loop's land takes no root lock of its own (the loop holds
	// it), so it meets the clone's
	r.a.landLazy = true
	code, out, errs = r.do("land --repo-dir " + r.clone + " --base main")
	r.a.landLazy = false
	assert.Equal(t, 1, code, out+errs)
	assert.Contains(t, errs, "LAND REFUSED stream=s1 cards=2 base=main tip=- ids=s1-1..s1-2 ")
	assert.Contains(t, errs, "reason=the clone "+r.clone+" is in use by another lander: "+pid)

	close(release)
	got := <-first
	require.Equal(t, 0, got.code, got.out+got.errs)
	assert.Contains(t, got.out, "LAND OK stream=s1 cards=2 base=main")
	assert.Equal(t, []string{"land s1-2 (sprint stream s1)", "land s1-1 (sprint stream s1)", "base"}, r.mainLog())
	assert.Equal(t, heads["s1-2"], r.git(r.remote, "rev-parse", "main^2"), "the pushed tip is the batch's own last merge")
	assert.Equal(t, map[string]string{"s1-1": "landed/merged", "s1-2": "landed/merged"}, r.places("s1-1", "s1-2"))
	r.clean()
}

// While the server's lander loop holds the land root (holdLanderLock), a land by hand
// refuses at once, naming it; when it lets go, the land by hand lands.
func TestALandByHandRefusesWhileTheServersLanderRuns(t *testing.T) {
	t.Parallel()
	r := newLandRig(t)
	landTwo(r)
	held, why := r.a.holdLanderLock()
	require.True(t, held, why)
	before := r.git(r.remote, "rev-parse", "main")
	code, out, errs := r.do("land --repo-dir " + r.clone + " --base main")
	assert.Equal(t, 1, code, out+errs)
	assert.Contains(t, errs, `is in use by another lander: pid=`+strconv.Itoa(os.Getpid()))
	assert.Contains(t, errs, `label="nova-sprint run --land (the server's lander, pid `)
	assert.Equal(t, before, r.git(r.remote, "rev-parse", "main"), "nothing was pushed")
	assert.Equal(t, map[string]string{"s1-1": "merging/queued", "s1-2": "merging/queued"}, r.places("s1-1", "s1-2"))

	again, _ := r.a.holdLanderLock()
	assert.True(t, again, "the loop holds it already: taking it again is a no-op")
	require.NoError(t, r.a.landerLock.Unlock())
	r.a.landerLock = nil
	out = r.ok("land --repo-dir " + r.clone + " --base main")
	assert.Contains(t, out, "LAND OK stream=s1 cards=2 base=main")
	r.clean()
}

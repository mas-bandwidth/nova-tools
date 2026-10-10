package main

import (
	"bytes"
	"context"
	"testing"

	"github.com/mas-bandwidth/nova-tools/pkg/sprintwire"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestServerReviewLandingAllowsATickAndCoordinatorWriteBeforePush(t *testing.T) {
	t.Parallel()
	r := newLandRig(t)
	r.ok("add --stream s1 --count 2")
	r.queued(map[string]string{
		"s1-1": r.head("s1-1", "main", "a.txt", "a\n"),
		"s1-2": r.head("s1-2", "main", "b.txt", "b\n"),
	}, "s1-1", "s1-2")
	r.ok("start")
	r.a.serveAddr = "mem:0"
	interleaved := 0
	r.a.beforePush = func(int) {
		require.True(t, r.a.serial.TryLock(), "the landing must release the server while Git runs")
		func() {
			defer r.a.serial.Unlock()
			r.ok("tick")
		}()
		res := r.a.serveFrom(sprintwire.Request{Verbs: [][]string{{"rank", "s1-2", "--first", "--actor", "coordinator"}}}, true).Results
		require.Len(t, res, 1)
		require.Equal(t, 0, res[0].Code, "%s%s", res[0].Stdout, res[0].Stderr)
		interleaved++
	}
	var out bytes.Buffer
	code := r.a.landRound(context.Background(), "mem:0", []string{"--repo-dir", r.clone, "--base", "main"}, &out)
	require.Equal(t, 0, code, out.String())
	assert.Equal(t, 1, interleaved)
	// While RUNNING, the existing engine drains producer updates on the next tick.
	r.a.serial.Lock()
	r.ok("tick")
	r.a.serial.Unlock()
	assert.Equal(t, map[string]string{"s1-1": "landed/merged", "s1-2": "landed/merged"}, r.places("s1-1", "s1-2"))
	assert.Equal(t, []string{"land s1-2 (sprint stream s1)", "land s1-1 (sprint stream s1)", "base"}, r.mainLog())
	assert.Contains(t, out.String(), "LAND OK")
	r.clean()
}

//go:build functional

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/pkg/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/pkg/nsprint/testutil"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// On the store: accept --read-ok accepts every primary with two ok reads in
// one step; the tick that finds the sprint done writes the done notice to the
// coordinator and stops the machine; and inbox --wait, blocked before that
// tick, wakes on its tick-end note and shows the inbox with the notice.
func TestReadOkDoneAndTheWakeOnTheStore(t *testing.T) {
	t.Parallel()
	addr := testutil.Start(t)
	c := redis.NewClient(&redis.Options{Addr: addr})
	defer c.Close()
	ctx := context.Background()
	require.NoError(t, fn.Load(ctx, c))
	env := map[string]string{"NOVA_SPRINT_REDIS": addr, "NOVA_SPRINT_ACTOR": "coordinator"}
	world, waiter := newApp(func(k string) string { return env[k] }), newApp(func(k string) string { return env[k] })
	defer world.close()
	defer waiter.close()
	do := func(line string) string {
		t.Helper()
		var out, errb bytes.Buffer
		code := world.run(strings.Fields(line), &out, &errb)
		require.Equal(t, 0, code, "%s: exit %d\n%s%s", line, code, out.String(), errb.String())
		return out.String()
	}
	for _, l := range []string{"init --readers reader-a,reader-b --members m1", "add --stream s1 --count 2", "fleet beat m1", "start",
		// the first tick's fleet update (last in the tick) brings m1 up; the
		// second tick's pump deals to it
		"tick", "tick",
		"take --as m1 --limit 5 --epoch 0"} {
		do(l)
	}
	var q struct{ Cards []queueCard }
	err := json.Unmarshal([]byte(do("queue --as m1 --json")), &q)
	require.NoError(t, err, "m1's queue: %v %+v", err, q)
	require.Len(t, q.Cards, 2, "m1's queue: %v %+v", err, q)
	words := []string{"finish", "--as", "m1", "--epoch", "0"}
	for _, c := range q.Cards {
		words = append(words, c.ID+"@1")
	}
	do(strings.Join(words, " "))
	for _, l := range []string{"queue --as reader-a", "queue --as reader-b", "ask", "read --as reader-a --ok --limit 10 --epoch 0", "read --as reader-b --ok --limit 10 --epoch 0"} {
		do(l)
	}
	require.Contains(t, do("accept --read-ok"), "ACCEPT OK moved=2", "accept --read-ok on the store")
	do("merge --stream s1 --batch 10 --epoch 0")

	// the coordinator waits; the tick that finds the sprint done wakes it
	type waited struct {
		code     int
		out, err string
	}
	got := make(chan waited, 1)
	go func() {
		var out, errb bytes.Buffer
		code := waiter.run([]string{"inbox", "--wait", "--timeout", "60s"}, &out, &errb)
		got <- waited{code, out.String(), errb.String()}
	}()
	blocked := false
	for i := 0; i < 500 && !blocked; i++ { // the waiter is in its XREAD BLOCK
		list, err := c.ClientList(ctx).Result()
		require.NoError(t, err)
		blocked = strings.Contains(list, "cmd=xread")
		if !blocked {
			time.Sleep(20 * time.Millisecond)
		}
	}
	require.True(t, blocked, "the waiter never blocked on the notes")
	do("tick")
	assert.Contains(t, do("where"), "DONE", "the machine did not stop itself at done")
	select {
	case w := <-got:
		assert.Equal(t, 0, w.code, "inbox --wait: exit %d\n%s%s", w.code, w.out, w.err)
		assert.NotContains(t, w.out, "no tick end", "inbox --wait: exit %d\n%s%s", w.code, w.out, w.err)
		assert.Contains(t, w.out, "the sprint is done", "inbox --wait: exit %d\n%s%s", w.code, w.out, w.err)
	case <-time.After(60 * time.Second):
		t.Fatal("inbox --wait did not wake on the tick end")
	}
}

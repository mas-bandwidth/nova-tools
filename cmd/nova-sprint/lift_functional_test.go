//go:build functional

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
	"github.com/redis/go-redis/v9"
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
	if err := fn.Load(ctx, c); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{"NOVA_SPRINT_REDIS": addr, "NOVA_SPRINT_ACTOR": "coordinator"}
	world, waiter := newApp(func(k string) string { return env[k] }), newApp(func(k string) string { return env[k] })
	defer world.close()
	defer waiter.close()
	do := func(line string) string {
		t.Helper()
		var out, errb bytes.Buffer
		if code := world.run(strings.Fields(line), &out, &errb); code != 0 {
			t.Fatalf("%s: exit %d\n%s%s", line, code, out.String(), errb.String())
		}
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
	if err := json.Unmarshal([]byte(do("queue --as m1 --json")), &q); err != nil || len(q.Cards) != 2 {
		t.Fatalf("m1's queue: %v %+v", err, q)
	}
	words := []string{"finish", "--as", "m1", "--epoch", "0"}
	for _, c := range q.Cards {
		words = append(words, c.ID+"@1")
	}
	do(strings.Join(words, " "))
	for _, l := range []string{"ask", "read --as reader-a --ok --limit 10 --epoch 0", "read --as reader-b --ok --limit 10 --epoch 0"} {
		do(l)
	}
	if out := do("accept --read-ok"); !strings.Contains(out, "ACCEPT OK moved=2") {
		t.Fatalf("accept --read-ok on the store:\n%s", out)
	}
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
		if err != nil {
			t.Fatal(err)
		}
		blocked = strings.Contains(list, "cmd=xread")
		if !blocked {
			time.Sleep(20 * time.Millisecond)
		}
	}
	if !blocked {
		t.Fatal("the waiter never blocked on the notes")
	}
	do("tick")
	if out := do("where"); !strings.Contains(out, "DONE") {
		t.Errorf("the machine did not stop itself at done:\n%s", out)
	}
	select {
	case w := <-got:
		if w.code != 0 || strings.Contains(w.out, "no tick end") || !strings.Contains(w.out, "the sprint is done") {
			t.Errorf("inbox --wait: exit %d\n%s%s", w.code, w.out, w.err)
		}
	case <-time.After(60 * time.Second):
		t.Fatal("inbox --wait did not wake on the tick end")
	}
}

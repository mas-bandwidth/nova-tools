//go:build functional

package pr_test

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/card"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/land"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
	"github.com/redis/go-redis/v9"
)

const (
	testSprint  = "reap-3091"
	testRepo    = "nova-tools"
	testBase    = "dev"
	testBaseSHA = "1111111111111111111111111111111111111111"
	h1          = "a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1"
	h2          = "b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2"
)

// harness is one throwaway redis-server with the nova_sprint library loaded.
// Every record is built through the real functions: ns_card_push (directly or
// through card.Push), ns_unit_head, ns_read and the lander chain to ns_land.
type harness struct {
	t    *testing.T
	ctx  context.Context
	c    *redis.Client
	addr string
	S    string
	*lander
}

// lander is the per-server lander state every subtest shares.
type lander struct {
	lease  string
	landed int
}

// at is the same harness reporting to t, so a subtest's helper failures stop
// that subtest and never call FailNow on its parent.
func (h *harness) at(t *testing.T) *harness {
	c := *h
	c.t = t
	return &c
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	addr := testutil.Start(t)
	c := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = c.Close() })
	ctx := context.Background()
	if err := fn.Load(ctx, c); err != nil {
		t.Fatalf("load fn library: %v", err)
	}
	return &harness{t: t, ctx: ctx, c: c, addr: addr, S: testSprint, lander: &lander{}}
}

func (h *harness) cardKey(label string) string { return "s:" + h.S + ":card:" + label }

// pushCard writes a card through ns_card_push, the one writer of card_type and cut_at.
func (h *harness) pushCard(label, paths, cardType string) {
	h.t.Helper()
	keys := []string{h.cardKey(label), "s:" + h.S + ":pool", "s:" + h.S + ":waiting", "s:" + h.S + ":log", "s:" + h.S + ":idx:card:queued"}
	reply, err := h.c.FCall(h.ctx, "ns_card_push", keys,
		label, "payload-"+label, "0", testBase, testBaseSHA, paths, "mas-bandwidth/nova-tools", "model", "", cardType).Text()
	if err != nil || !strings.HasPrefix(reply, "OK place=") {
		h.t.Fatalf("ns_card_push %s: reply %q err %v", label, reply, err)
	}
}

func (h *harness) head(unit, head, cardLabel, prNum string) (int64, error) {
	return land.CallUnitHead(h.ctx, h.c, land.UnitHeadParams{
		Sprint: h.S, Unit: unit, Repo: testRepo, Base: testBase, Branch: "card-" + unit,
		Head: head, BaseSHA: testBaseSHA, PR: prNum, Author: "emma", Card: cardLabel,
	})
}

func (h *harness) mustHead(unit, head, cardLabel string) int64 {
	h.t.Helper()
	seq, err := h.head(unit, head, cardLabel, "")
	if err != nil {
		h.t.Fatalf("ns_unit_head %s@%s card=%q: %v", unit, head, cardLabel, err)
	}
	return seq
}

func (h *harness) rec(unit string) map[string]string {
	h.t.Helper()
	rec, err := h.c.HGetAll(h.ctx, land.UnitKey(h.S, unit)).Result()
	if err != nil {
		h.t.Fatalf("hgetall %s: %v", unit, err)
	}
	return rec
}

func (h *harness) field(unit, name string) (string, bool) {
	h.t.Helper()
	v, err := h.c.HGet(h.ctx, land.UnitKey(h.S, unit), name).Result()
	if err == redis.Nil {
		return "", false
	}
	if err != nil {
		h.t.Fatalf("hget %s %s: %v", unit, name, err)
	}
	return v, true
}

// pushCardBody pushes a card through card.Push (lint, then ns_card_push), so
// the KIND: and TYPE: lines are parsed the way production parses them.
func (h *harness) pushCardBody(srv *httptest.Server, label string, extra map[string]string) {
	h.t.Helper()
	var b strings.Builder
	fmt.Fprintf(&b, "RESULT: %s sha=0123456789ab\n", label)
	for _, k := range []string{"KIND", "TYPE"} {
		if v, ok := extra[k]; ok {
			fmt.Fprintf(&b, "%s: %s\n", k, v)
		}
	}
	fmt.Fprintf(&b, "BASE: %s\nbase-repo: %s/acme/public.git\nbase-sha: %s\nPATHS: internal/nsprint/pr\nDEPENDS-ON: none\nDONE-WHEN: go test ./internal/nsprint/pr passes\n",
		testBase, srv.URL, testBaseSHA)
	res := card.Push(h.ctx, h.c, h.S, []byte(b.String()))
	if res.Code != 0 || res.Stderr != "" {
		h.t.Fatalf("card push %s: exit %d stdout %q stderr %q", label, res.Code, res.Stdout, res.Stderr)
	}
}

// monitor opens a MONITOR connection on the throwaway server and returns its
// command lines.
func (h *harness) monitor() <-chan string {
	h.t.Helper()
	conn, err := net.Dial("tcp", h.addr)
	if err != nil {
		h.t.Fatalf("monitor dial: %v", err)
	}
	h.t.Cleanup(func() { _ = conn.Close() })
	if _, err := conn.Write([]byte("MONITOR\r\n")); err != nil {
		h.t.Fatalf("monitor: %v", err)
	}
	r := bufio.NewReader(conn)
	ok, err := r.ReadString('\n')
	if err != nil || strings.TrimSpace(ok) != "+OK" {
		h.t.Fatalf("monitor reply %q: %v", ok, err)
	}
	ch := make(chan string, 1024)
	go func() {
		defer close(ch)
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				return
			}
			ch <- strings.TrimSpace(line)
		}
	}()
	return ch
}

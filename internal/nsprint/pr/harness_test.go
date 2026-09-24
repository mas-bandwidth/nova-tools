package pr_test

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/card"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/land"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/pr"
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

func (h *harness) read(unit, who, head, verdict, kind string) error {
	_, err := land.CallRead(h.ctx, h.c, h.S, unit, who, head, verdict, "9", kind, "", "")
	return err
}

func (h *harness) mustRead(unit, who, head, verdict, kind string) {
	h.t.Helper()
	if err := h.read(unit, who, head, verdict, kind); err != nil {
		h.t.Fatalf("ns_read %s %s %s@%s: %v", unit, who, verdict, head, err)
	}
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

func (h *harness) set(unit, name, value string) {
	h.t.Helper()
	if err := h.c.HSet(h.ctx, land.UnitKey(h.S, unit), name, value).Err(); err != nil {
		h.t.Fatalf("hset %s %s: %v", unit, name, err)
	}
}

func (h *harness) inputs(unit string) pr.Input {
	h.t.Helper()
	in, err := pr.Inputs(h.rec(unit))
	if err != nil {
		h.t.Fatalf("Inputs(%s): %v", unit, err)
	}
	return in
}

func (h *harness) redisNow() time.Time {
	h.t.Helper()
	now, err := h.c.Time(h.ctx).Result()
	if err != nil {
		h.t.Fatalf("redis TIME: %v", err)
	}
	return now
}

func atoi(t *testing.T, s string) int64 {
	t.Helper()
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		t.Fatalf("not a whole number: %q", s)
	}
	return n
}

// landing is one unit landed through the real lander functions.
type landing struct {
	batch, trainHead, mergeSHA string
}

// land takes one unit from ns_unit_eval through ns_batch_plan, the gate,
// ns_land_intent and ns_land, and returns what a replay needs.
func (h *harness) land(unit, head string) landing {
	h.t.Helper()
	if h.lease == "" {
		gen, err := land.CallWriter(h.ctx, h.c, testRepo, testBase, "nova-sprint", "fixture")
		if err != nil {
			h.t.Fatalf("writer: %v", err)
		}
		h.lease = fmt.Sprintf("%d:lease-token-1", gen)
		if err := h.c.Set(h.ctx, land.LeaseKey(testRepo, testBase), h.lease, time.Hour).Err(); err != nil {
			h.t.Fatalf("lease: %v", err)
		}
		if err := h.c.HSet(h.ctx, land.TipKey(testRepo, testBase), "sha", testBaseSHA, "at", "1", "by", "init").Err(); err != nil {
			h.t.Fatalf("tip: %v", err)
		}
		if err := land.CallPolicySet(h.ctx, h.c, testRepo, testBase, "pol-1", "req-1", "runner-1"); err != nil {
			h.t.Fatalf("policy: %v", err)
		}
	}
	// A fresh inbound consumer for this intent.
	if err := h.c.HSet(h.ctx, "ev:github:consumer:land", "pending", "0", "at", strconv.FormatInt(h.redisNow().Unix(), 10)).Err(); err != nil {
		h.t.Fatalf("consumer: %v", err)
	}
	h.landed++
	l := landing{
		batch:     fmt.Sprintf("batch-%d", h.landed),
		trainHead: fmt.Sprintf("%040x", 0x7000+h.landed),
		mergeSHA:  fmt.Sprintf("%040x", 0x8000+h.landed),
	}
	tip, err := h.c.HGet(h.ctx, land.TipKey(testRepo, testBase), "sha").Result()
	if err != nil {
		h.t.Fatalf("tip: %v", err)
	}
	if _, err := land.CallUnitEval(h.ctx, h.c, h.S, unit, testRepo, testBase, 0); err != nil {
		h.t.Fatalf("eval %s: %v", unit, err)
	}
	token, _, err := land.CallBatchPlan(h.ctx, h.c, h.S, testRepo, testBase, l.batch, h.lease, unit+"@"+head, "", "go", tip, "in-"+l.batch)
	if err != nil {
		h.t.Fatalf("plan %s: %v", unit, err)
	}
	if res, err := land.CallGateClaim(h.ctx, h.c, testRepo, testBase, l.batch, 1, token, "bench-1", "slot-1"); err != nil || res != "OK" {
		h.t.Fatalf("claim %s: %s %v", unit, res, err)
	}
	if res, err := land.CallGateReceipt(h.ctx, h.c, testRepo, testBase, l.batch, 1, token, "GREEN", "bench-1", "worker-1", l.trainHead, "tree", "in-"+l.batch, "", "", "", "", "1"); err != nil || res != "OK" {
		h.t.Fatalf("receipt %s: %s %v", unit, res, err)
	}
	if _, err := land.CallLandIntent(h.ctx, h.c, h.S, testRepo, testBase, l.batch, h.lease); err != nil {
		h.t.Fatalf("intent %s: %v", unit, err)
	}
	if res, err := land.CallLand(h.ctx, h.c, h.S, testRepo, testBase, l.batch, h.lease, l.trainHead, l.mergeSHA, "1"); err != nil || res != "OK" {
		h.t.Fatalf("ns_land %s: %s %v", unit, res, err)
	}
	return l
}

// repoServer answers the card lint's public-repository probe on loopback.
func repoServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/acme/public" {
			w.WriteHeader(http.StatusOK)
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv
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

// until reads monitor lines until one contains marker. The bound is generous;
// the test asserts the marker line arrives, not how fast.
func until(t *testing.T, ch <-chan string, marker string) []string {
	t.Helper()
	var lines []string
	deadline := time.After(2 * time.Minute)
	for {
		select {
		case line, ok := <-ch:
			if !ok {
				t.Fatalf("monitor closed before %q", marker)
			}
			if strings.Contains(line, marker) {
				return lines
			}
			lines = append(lines, line)
		case <-deadline:
			t.Fatalf("monitor never showed %q", marker)
		}
	}
}

// commandName is the first quoted token of a MONITOR line:
// +<ts> [0 127.0.0.1:port] "HGETALL" "s:..." -> HGETALL.
func commandName(line string) string {
	i := strings.Index(line, "] \"")
	if i < 0 {
		return ""
	}
	rest := line[i+3:]
	j := strings.IndexByte(rest, '"')
	if j < 0 {
		return ""
	}
	return strings.ToUpper(rest[:j])
}

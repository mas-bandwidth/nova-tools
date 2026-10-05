package main

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// keyAnswer is a transport that answers openrouter's key endpoint with body (a day's usage of
// $10.00 until set) and keeps the URLs and keys it was asked with: no socket is opened.
type keyAnswer struct {
	urls, keys []string
	body       string
}

func (k *keyAnswer) RoundTrip(r *http.Request) (*http.Response, error) {
	k.urls = append(k.urls, r.URL.String())
	k.keys = append(k.keys, r.Header.Get("Authorization"))
	body := k.body
	if body == "" {
		body = `{"data":{"usage":2250.18,"usage_daily":10,"usage_monthly":1388.85}}`
	}
	return &http.Response{StatusCode: 200, Header: http.Header{}, Request: r, Body: io.NopCloser(strings.NewReader(body))}, nil
}

const costReconcileKey = "sk-or-v1-fakefakefakefakefakefakefake0123"

// costReconcileApp is the test app with an openrouter route and an opencode one, the fake key
// answer, the seat's key in its environment, and the store the run loop writes to.
func costReconcileApp(t *testing.T) (*testApp, *keyAnswer, func() string) {
	ta := newTestApp(t)
	ta.ok("init --readers reader-a --members m1")
	ta.m.SetRoutes([]sprint.Route{
		{Name: "flash-or", Tier: "flash", Provider: "openrouter", Model: "m", Enabled: true},
		{Name: "flash-oc", Tier: "flash", Provider: "opencode", Model: "m", Enabled: true},
	})
	fake := &keyAnswer{}
	ta.a.transport = fake
	base := ta.a.getenv
	ta.a.getenv = func(k string) string {
		if k == "OPENROUTER_API_KEY" {
			return costReconcileKey
		}
		return base(k)
	}
	st, err := ta.a.store(common{redis: "mem:0", actor: sprint.MachineActor})
	require.NoError(t, err)
	return ta, fake, func() string {
		var out bytes.Buffer
		ta.a.reconcileCosts(context.Background(), st, &out)
		return out.String()
	}
}

// The run loop's reconciliation (docs/SPEC-SPRINT.md, "What a card cost") reads openrouter's
// own count of today's usage (GET /api/v1/key, usage_daily) through the seat's key, sets it
// beside the sprint's records of the same day, and opens ONE judgment on a gap past the
// bound: a second read past it opens none, and a read back within it closes the one. The
// provider with no usage endpoint is unknown, and the key is never said.
func TestTheRunLoopReconcilesTheProvidersUsageAndRaisesOneJudgmentOnAGap(t *testing.T) {
	t.Parallel()
	ta, fake, reconcile := costReconcileApp(t)
	out := reconcile()
	assert.Equal(t, []string{OpenRouterKeyURL}, fake.urls, "one read, openrouter's key endpoint")
	assert.Equal(t, []string{"Bearer " + costReconcileKey}, fake.keys)
	assert.Contains(t, out, " COST RECONCILE opencode=unknown openrouter=$10.00 notes=1\n")
	assert.NotContains(t, out, costReconcileKey, "the key is never said")
	inbox := ta.ok("inbox")
	assert.Equal(t, 1, strings.Count(inbox, sprint.NCostGap), "one judgment:\n%s", inbox)
	assert.Contains(t, inbox, "provider openrouter counted $10.00 on "+ta.a.now().UTC().Format(time.DateOnly)+" and the sprint's cost records of it hold $0.00")

	out = reconcile()
	assert.Contains(t, out, " COST RECONCILE opencode=unknown openrouter=$10.00 notes=0\n", "no second judgment while the one is open")
	assert.Equal(t, 1, strings.Count(ta.ok("inbox"), sprint.NCostGap))

	fake.body = `{"data":{"usage_daily":0}}`
	reconcile()
	inbox = ta.ok("inbox")
	assert.Contains(t, inbox, "INBOX OK judgments=0 ", "a read within the bound closes it")
	assert.Contains(t, inbox, "answered by cost reconcile")
}

// The loop reconciles at once, rests on its own clock for CostReconcileEvery between reads,
// and ends when its context is done.
func TestTheCostReconcileLoopReadsEachHourUntilItsContextIsDone(t *testing.T) {
	t.Parallel()
	ta, fake, _ := costReconcileApp(t)
	st, err := ta.a.store(common{redis: "mem:0", actor: sprint.MachineActor})
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	var rests []time.Duration
	ta.a.after = func(d time.Duration) <-chan time.Time {
		rests = append(rests, d)
		fired := make(chan time.Time, 1)
		if len(rests) == 1 {
			fired <- ta.a.now()
			return fired
		}
		cancel()
		return fired
	}
	var out bytes.Buffer
	ta.a.costReconcileLoop(ctx, st, &out)
	assert.Equal(t, []time.Duration{time.Hour, time.Hour}, rests)
	assert.Len(t, fake.keys, 2, "a read each hour")
	assert.Equal(t, 1, strings.Count(out.String(), "COST RECONCILE every 1h0m0s: "), out.String())
	assert.Equal(t, 2, strings.Count(out.String(), " COST RECONCILE opencode=unknown openrouter=$10.00 notes="), out.String())
}

// A provider answer not of the shape, or no key, is unknown with why: nothing is opened.
func TestAUsageReadThatIsNotTheShapeIsUnknown(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 5, 3, 0, 0, 0, time.UTC)
	for _, c := range []struct{ name, body, key, why string }{
		{"no usage_daily", `{"data":{"usage":2250.18}}`, costReconcileKey, "answered no data.usage_daily"},
		{"no key", ``, "", "OPENROUTER_API_KEY is not in the run loop's environment"},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			rd := readProviderUsage(context.Background(), &keyAnswer{body: c.body}, "openrouter", func(string) string { return c.key }, now)
			assert.False(t, rd.Known)
			assert.Equal(t, "2026-10-05", rd.Day)
			assert.Contains(t, rd.Note, c.why)
		})
	}
}

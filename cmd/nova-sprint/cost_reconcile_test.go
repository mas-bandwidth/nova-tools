package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// cost reconcile runs the reconciliation once (docs/SPEC-SPRINT.md, "What a card cost"):
// each provider the routes name is read through the seat's key (openrouter's key endpoint,
// today's usage) and set beside the sprint's records of the day; each provider's gap is a
// line, a gap past the bound is one judgment on the provider, a read back within it closes
// the judgment, and a provider with no usage endpoint is said unknown.
func TestCostReconcileSetsEachProvidersDayBesideTheRecords(t *testing.T) {
	t.Parallel()
	ta, fake, _ := balanceApp(t,
		sprint.Route{Name: "flash-or", Tier: "flash", Provider: "openrouter", Model: "m", Enabled: true},
		sprint.Route{Name: "flash-oc", Tier: "flash", Provider: "opencode", Model: "m", Enabled: true})
	day := ta.a.now().UTC().Format("2006-01-02")

	fake.body = `{"data":{"usage":1250.5,"usage_daily":12.5}}`
	dry := ta.ok("cost reconcile --dry-run")
	assert.Contains(t, dry, "COST provider=openrouter day="+day+" provider_usd=$12.50 records=$0.00 gap=$12.50 share=100.0%\n")
	assert.Contains(t, dry, "COST RECONCILE DRY-RUN providers=2 notes=1: nothing was written\n")
	assert.NotContains(t, ta.ok("inbox"), sprint.NCostGap, "a dry run opens no judgment")
	out := ta.ok("cost reconcile")
	assert.Contains(t, out, "COST provider=openrouter day="+day+" provider_usd=$12.50 records=$0.00 gap=$12.50 share=100.0%\n")
	assert.Contains(t, out, "COST provider=opencode unknown: opencode Zen publishes no balance endpoint")
	assert.Contains(t, out, "COST RECONCILE OK providers=2 notes=1\n", "one judgment, the gap's")
	require.NotEmpty(t, fake.keys)
	assert.Equal(t, "Bearer sk-or-v1-fakefakefakefakefakefakefake0123", fake.keys[len(fake.keys)-1], "read through the seat's key")
	assert.NotContains(t, out, "sk-or-v1", "the key is never said")
	// openGaps is the open gap judgments the inbox lists
	openGaps := func() int {
		n := 0
		for _, l := range strings.Split(ta.ok("inbox"), "\n") {
			if strings.HasPrefix(l, "JUDGMENT ") && strings.Contains(l, sprint.NCostGap) {
				n++
			}
		}
		return n
	}
	assert.Equal(t, 1, openGaps(), "one judgment on the provider")
	assert.Contains(t, ta.ok("inbox"), "provider openrouter counted $12.50 on "+day)

	// a second read past the bound opens no second judgment
	ta.ok("cost reconcile")
	assert.Equal(t, 1, openGaps())

	// a read back within the bound closes it; --json carries the records
	fake.body = `{"data":{"usage_daily":0.5}}`
	var got struct {
		Providers []sprint.CostReconcileRecord `json:"providers"`
		Notes     int                          `json:"notes"`
	}
	require.NoError(t, json.Unmarshal([]byte(ta.ok("cost reconcile --json")), &got))
	require.Len(t, got.Providers, 2)
	assert.Equal(t, "opencode", got.Providers[0].Provider)
	assert.False(t, got.Providers[0].Known)
	assert.Equal(t, "openrouter", got.Providers[1].Provider)
	assert.True(t, got.Providers[1].Known)
	assert.InDelta(t, 0.5, got.Providers[1].Used, 1e-9)
	assert.Zero(t, openGaps(), "a gap under the floor closes the judgment")

	// no provider named: nothing read, nothing written
	bare := newTestApp(t)
	bare.ok("init --readers reader-a --members m1")
	assert.Equal(t, "COST RECONCILE OK providers=0 notes=0: the routes name no provider (nova-sprint routes)\n", bare.ok("cost reconcile"))
}

func TestCostReconcileMissingKeyNamesTheExactSecretsCommand(t *testing.T) {
	t.Parallel()
	for _, suffix := range []string{"", " --dry-run", " --json"} {
		t.Run(suffix, func(t *testing.T) {
			t.Parallel()
			ta, fake, _ := balanceApp(t, sprint.Route{Name: "route-a", Provider: "openrouter", Model: "vendor/m"})
			base := ta.a.getenv
			ta.a.getenv = func(k string) string {
				if k == "OPENROUTER_API_KEY" {
					return ""
				}
				return base(k)
			}
			code, out, errOut := ta.do("cost reconcile" + suffix)
			assert.Equal(t, 2, code)
			assert.Empty(t, out)
			assert.Contains(t, errOut, "nova-secrets exec --only OPENROUTER_API_KEY -- nova-sprint cost reconcile")
			assert.Empty(t, fake.keys, "no provider request is sent with an absent key")
		})
	}
}

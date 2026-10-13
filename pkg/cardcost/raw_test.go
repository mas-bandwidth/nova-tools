package cardcost

import (
	"encoding/base64"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"strings"
	"testing"
)

func TestTypedReceiptsPreserveOriginalCountersAndMarginalCost(t *testing.T) {
	t.Parallel()
	u := NoUsage()
	u.Billing = BillingSubscription
	u.ReceiptVersion = ReceiptVersion
	u.Actual, u.ActualBy = "3.75", ActualByHarness
	u.Raw = &RawUsage{Version: RawVersion, Records: []RawRecord{{Harness: "muse", Execution: "fleet", SourceSchema: "muse-jsonl-v1", ProducerVersion: "fixture", Billing: BillingSubscription, Tokens: map[string]int64{"cached_tokens": 0, "output_tokens": 7}}}}
	spend, err := EncodeSpend(u)
	require.NoError(t, err)
	parsed, err := ParseSpendChecked(spend)
	require.NoError(t, err)
	require.NotNil(t, parsed.Raw)
	assert.Equal(t, u.Raw, parsed.Raw)
	assert.NotContains(t, parsed.Raw.Records[0].Tokens, "cache_read_tokens")
	assert.Equal(t, "3.75", parsed.Actual)
	assert.Equal(t, "0", parsed.MarginalUSD())
	priced := parsed.Priced("", Prices{Billing: BillingPlan, Input: "3"})
	assert.Equal(t, "0", priced.Predicted)
	assert.Equal(t, BillingSubscription, priced.Billing)
	total := NoTotal().Add(priced)
	assert.Equal(t, "3.75", total.Actual)
	assert.Equal(t, "0", total.Charged)
	again := priced.Priced("changed", Prices{Billing: BillingMetered})
	assert.Equal(t, "0", total.Repriced(priced, again).Charged)
	merged, err := MergeRaw([]Usage{parsed, parsed})
	require.NoError(t, err)
	require.Len(t, merged.Records, 2)
	assert.Equal(t, int64(7), merged.Records[1].Tokens["output_tokens"])
	line, err := ParseUsageChecked(parsed.String())
	require.NoError(t, err)
	assert.Equal(t, parsed.Raw, line.Raw)
}

func TestTypedReceiptsRefuseMalformedOriginalCounters(t *testing.T) {
	t.Parallel()
	valid := `{"version":1,"records":[{"harness":"muse","execution":"fleet","source_schema":"muse-jsonl-v1","producer_version":"fixture","billing":"subscription","tokens":{"cached_tokens":0}}]}`
	cases := map[string]string{
		"null":      strings.Replace(valid, `"cached_tokens":0`, `"cached_tokens":null`, 1),
		"negative":  strings.Replace(valid, `"cached_tokens":0`, `"cached_tokens":-1`, 1),
		"overflow":  strings.Replace(valid, `"cached_tokens":0`, `"cached_tokens":9223372036854775808`, 1),
		"fraction":  strings.Replace(valid, `"cached_tokens":0`, `"cached_tokens":1.5`, 1),
		"duplicate": strings.Replace(valid, `"cached_tokens":0`, `"cached_tokens":0,"cached_tokens":1`, 1),
		"unknown":   strings.Replace(valid, `"cached_tokens":0`, `"total_tokens":0`, 1),
		"future":    strings.Replace(valid, `"version":1`, `"version":2`, 1),
		"friend":    strings.Replace(valid, `"execution":"fleet"`, `"execution":"friend"`, 1),
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, err := ParseSpendChecked("receipt_version:2,billing:subscription,raw_usage:" + base64.RawURLEncoding.EncodeToString([]byte(raw)))
			assert.Error(t, err)
		})
	}
	for _, line := range []string{"receipt_version=3 billing=subscription", "receipt_version=2", "receipt_version=2 billing=subscription input=-1", "receipt_version=2 billing=subscription billing=plan"} {
		assert.Error(t, ValidateUsage(line))
	}
	_, err := ParseSpendChecked("receipt_version:2,billing:plan,raw_usage:" + base64.RawURLEncoding.EncodeToString([]byte(valid)))
	assert.Error(t, err)
}

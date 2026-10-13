package member

import (
	"github.com/mas-bandwidth/nova-tools/pkg/cardcost"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSubscriptionReceiptSurvivesDurableRecoveryAndContractFlag(t *testing.T) {
	t.Parallel()
	raw := &cardcost.RawUsage{Version: 1, Records: []cardcost.RawRecord{{Harness: "muse", Execution: "fleet", SourceSchema: "muse-jsonl-v1", ProducerVersion: "fixture", Billing: cardcost.BillingSubscription, Tokens: map[string]int64{"cached_tokens": 0}}}}
	encoded, err := cardcost.EncodeRaw(raw)
	require.NoError(t, err)
	job := t.TempDir()
	header := []string{"provider", "model", "tokens_in", "tokens_out", "cache_write", "cache_read", "reasoning", "usd", "receipt_version", "billing", "raw_usage"}
	row := []string{"meta", "fixture", "-", "-", "-", "-", "-", "2.50", "2", "subscription", encoded}
	require.NoError(t, os.WriteFile(filepath.Join(job, ReceiptName), []byte(strings.Join(header, "\t")+"\n"+strings.Join(row, "\t")+"\n"), 0600))
	u, found, err := ReceiptUsage(job)
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, raw, u.Raw)
	assert.Equal(t, "2.5", u.Actual)
	assert.Equal(t, "0", u.MarginalUSD())
	assert.Equal(t, []string{"--usage", u.String(), "--receipt-contract", "2"}, usageArgs(Result{Usage: u.String()}))
	assert.Equal(t, []string{"--usage", "receipt_version=garbled", "--receipt-contract", "2"}, usageArgs(Result{Usage: "receipt_version=garbled"}))
}

func TestDurableReceiptRefusesMixedLegacyAndTypedRows(t *testing.T) {
	t.Parallel()
	job := t.TempDir()
	header := "provider\tmodel\ttokens_in\ttokens_out\tcache_write\tcache_read\treasoning\tusd\treceipt_version\tbilling\traw_usage\n"
	rows := "meta\tfixture\t1\t-\t-\t-\t-\t-\t-\t-\t-\nmeta\tfixture\t2\t-\t-\t-\t-\t-\t2\tsubscription\t-\n"
	require.NoError(t, os.WriteFile(filepath.Join(job, ReceiptName), []byte(header+rows), 0600))
	_, _, err := ReceiptUsage(job)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "mixed legacy and typed")
}

func TestTypedExactAmountPreservesPrecisionZeroAbsenceAndLegacyFallback(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name, exact, display, want string
		typed, withExact           bool
	}{
		{"precision", "0.12345678", "0.1235", "0.12345678", true, true},
		{"measured zero", "0", "0.0000", "0", true, true},
		{"absent", "-", "0.0000", "", true, true},
		{"legacy historical precision", "-", "0.1235", "0.1235", false, true},
		{"old column fallback", "", "0.1235", "0.1235", false, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			job := t.TempDir()
			header := []string{"provider", "model", "tokens_in", "tokens_out", "cache_write", "cache_read", "reasoning", "usd", "receipt_version", "billing", "raw_usage"}
			version, billing := "-", "-"
			if test.typed {
				version, billing = "2", "subscription"
			}
			row := []string{"fixture", "fixture", "1", "-", "-", "-", "-", test.display, version, billing, "-"}
			if test.withExact {
				header = append(header, "actual_usd")
				row = append(row, test.exact)
			}
			require.NoError(t, os.WriteFile(filepath.Join(job, ReceiptName), []byte(strings.Join(header, "\t")+"\n"+strings.Join(row, "\t")+"\n"), 0600))
			u, found, err := ReceiptUsage(job)
			require.NoError(t, err)
			require.True(t, found)
			assert.Equal(t, test.want, u.Actual)
			if test.want == "" {
				assert.Empty(t, u.ActualBy)
			} else {
				assert.Equal(t, cardcost.ActualByHarness, u.ActualBy)
			}
			if test.typed {
				assert.Equal(t, "0", u.MarginalUSD())
			}
		})
	}
}

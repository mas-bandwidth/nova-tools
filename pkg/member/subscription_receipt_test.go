package member

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/pkg/cardcost"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSubscriptionDurableReceiptPreservesOriginalRawRuns(t *testing.T) {
	t.Parallel()
	raw := &cardcost.RawUsage{Version: 1, Records: []cardcost.RawRecord{{Harness: "muse", Execution: "fleet", SourceSchema: "muse-jsonl-v1", ProducerVersion: "paired-fake-source", Billing: "subscription", Tokens: map[string]int64{"input_tokens": 12, "output_tokens": 7, "cached_tokens": 5, "cache_write_tokens": 3, "cache_read_tokens": 5, "reasoning_tokens": 2}}}}
	word, err := cardcost.EncodeRaw(raw)
	require.NoError(t, err)
	header := []string{"provider", "model", "tokens_in", "tokens_out", "cache_write", "cache_read", "reasoning", "usd", "receipt_version", "billing", "raw_usage"}
	row := []string{"p", "m", "12", "7", "3", "5", "2", "0", "2", "subscription", word}
	job := t.TempDir()
	text := strings.Join(header, "\t") + "\n" + strings.Join(row, "\t") + "\n" + strings.Join(row, "\t") + "\n"
	require.NoError(t, os.WriteFile(filepath.Join(job, ReceiptName), []byte(text), 0600))
	u, found, err := ReceiptUsage(job)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, "0", u.Actual, "reported zero remains present")
	require.Equal(t, "harness", u.ActualBy)
	require.Equal(t, int64(24), u.Tokens.Input)
	require.Len(t, u.Raw.Records, 2, "one original raw record per run, never a sum")
	for _, r := range u.Raw.Records {
		require.Equal(t, int64(5), r.Tokens["cached_tokens"])
	}
	require.Contains(t, usageArgs(Result{Usage: u.String()}), "--receipt-contract")
	encoded, err := cardcost.EncodeSpend(u)
	require.NoError(t, err)
	replayed, err := cardcost.ParseSpendChecked(encoded)
	require.NoError(t, err)
	require.Equal(t, u.Raw, replayed.Raw)
	require.Equal(t, "0", replayed.MarginalUSD())
	require.Contains(t, usageArgs(Result{Usage: "receipt_version=999 raw_usage=bad"}), "--receipt-contract", "malformed typed receipts cannot silently fall back")
	mixedRow := append([]string(nil), row...)
	mixedRow[8], mixedRow[9], mixedRow[10] = "-", "-", "-"
	mixed := strings.Join(header, "\t") + "\n" + strings.Join(row, "\t") + "\n" + strings.Join(mixedRow, "\t") + "\n"
	require.NoError(t, os.WriteFile(filepath.Join(job, ReceiptName), []byte(mixed), 0600))
	_, found, err = ReceiptUsage(job)
	require.Error(t, err)
	require.False(t, found)
	require.Contains(t, err.Error(), "mixed legacy and typed")

	// Old rows stay legacy: a projection cannot invent original raw counters.
	legacy := strings.Join(header[:8], "\t") + "\n" + strings.Join(row[:8], "\t") + "\n"
	require.NoError(t, os.WriteFile(filepath.Join(job, ReceiptName), []byte(legacy), 0600))
	u, found, err = ReceiptUsage(job)
	require.NoError(t, err)
	require.True(t, found)
	require.Nil(t, u.Raw)
	require.Zero(t, u.ReceiptVersion)
	require.NotContains(t, usageArgs(Result{Usage: u.String()}), "--receipt-contract")
}

func TestSubscriptionDamagedTypedHeaderRetainsContract(t *testing.T) {
	t.Parallel()
	job := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(job, ReceiptName), []byte("provider\tmodel\traw_usage\n"), 0600))
	u, found, err := ReceiptUsage(job)
	require.Error(t, err)
	require.False(t, found)
	require.Equal(t, 2, u.ReceiptVersion)
	require.Nil(t, u.Raw)
	require.Equal(t, cardcost.Unreported, u.Tokens.Input)
	require.Contains(t, usageArgs(Result{Usage: u.String()}), "--receipt-contract", "damaged capable receipt cannot downgrade into legacy")
}

func TestSubscriptionDurableReceiptExactDollars(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, version, exact, want, by string }{
		{"precision", "2", "0.12345678", "0.12345678", "harness"},
		{"zero", "2", "0", "0", "harness"},
		{"absent", "2", "-", "", ""},
		{"legacy-fallback", "-", "0.12345678", "0.1235", "harness"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			job := t.TempDir()
			header := "provider\tmodel\ttokens_in\ttokens_out\tcache_write\tcache_read\treasoning\tusd\treceipt_version\tbilling\traw_usage\tactual_usd\n"
			billing := "subscription"
			if tc.version == "-" {
				billing = "-"
			}
			row := strings.Join([]string{"p", "m", "12", "-", "-", "-", "-", "0.1235", tc.version, billing, "-", tc.exact}, "\t") + "\n"
			require.NoError(t, os.WriteFile(filepath.Join(job, ReceiptName), []byte(header+row), 0600))
			u, found, err := ReceiptUsage(job)
			require.NoError(t, err)
			require.True(t, found)
			require.Equal(t, tc.want, u.Actual)
			require.Equal(t, tc.by, u.ActualBy)
			if tc.version == "2" {
				require.Equal(t, "0", u.MarginalUSD())
			}
		})
	}
}

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

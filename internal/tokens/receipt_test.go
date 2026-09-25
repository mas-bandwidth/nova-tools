package tokens

import (
	"strings"
	"testing"
)

// TestReceiptsFormatAndValidation pins the usage-receipt row format: seventeen columns in
// a fixed order, a 32-hex receipt id, a stage drawn from the six declared stages, and a
// Format/Parse round trip that is the identity.
func TestReceiptsFormatAndValidation(t *testing.T) {
	if got, want := len(ReceiptColumns), 17; got != want {
		t.Fatalf("ReceiptColumns has %d entries, want %d", got, want)
	}
	head := "day\tmodel\trepo\treceipt\tnode\tstage\tsession\tturns\tinput_tokens\toutput_tokens\tcache_read_tokens\tcache_write_tokens\treasoning_tokens\ttotal_tokens\tcost_usd\tcreated_at\tsource"
	if ReceiptHeaderLine != head {
		t.Fatalf("ReceiptHeaderLine = %q, want %q", ReceiptHeaderLine, head)
	}

	line := strings.Join([]string{
		"2026-09-15", "grok-model-example", "nova-tools",
		"abcdef0123456789abcdef0123456789", "node-1", "implementation", "sess-1",
		"3", "1000", "100", "800", "0", "40", "1100",
		"0.001234", "2026-09-15T00:05:00Z", "grok-usage",
	}, "\t")

	got, err := ParseReceiptRow(line)
	if err != nil {
		t.Fatalf("ParseReceiptRow(%q) error: %v", line, err)
	}
	if got.Day != "2026-09-15" || got.Model != "grok-model-example" || got.Repo != "nova-tools" {
		t.Errorf("day/model/repo = %q/%q/%q", got.Day, got.Model, got.Repo)
	}
	if got.Receipt != "abcdef0123456789abcdef0123456789" {
		t.Errorf("receipt = %q", got.Receipt)
	}
	if got.Node != "node-1" || got.Stage != StageImplementation || got.Session != "sess-1" {
		t.Errorf("node/stage/session = %q/%q/%q", got.Node, got.Stage, got.Session)
	}
	if got.Turns != 3 || got.InputTokens != 1000 || got.OutputTokens != 100 ||
		got.CacheReadTokens != 800 || got.CacheWriteTokens != 0 ||
		got.ReasoningTokens != 40 || got.TotalTokens != 1100 {
		t.Errorf("counts = turns %d input %d output %d cr %d cw %d reason %d total %d",
			got.Turns, got.InputTokens, got.OutputTokens, got.CacheReadTokens,
			got.CacheWriteTokens, got.ReasoningTokens, got.TotalTokens)
	}
	if got.CostUsd != 1234 {
		t.Errorf("cost_usd = %d micro-dollars, want 1234", got.CostUsd)
	}
	if got.CreatedAt != "2026-09-15T00:05:00Z" || got.Source != "grok-usage" {
		t.Errorf("created_at/source = %q/%q", got.CreatedAt, got.Source)
	}

	if back := FormatReceiptRow(got); back != line {
		t.Errorf("FormatReceiptRow round trip:\n got %q\nwant %q", back, line)
	}

	t.Run("wrong column count is refused", func(t *testing.T) {
		bad := strings.Join([]string{"2026-09-15", "m", "r", "abcdef0123456789abcdef0123456789", "n", "implementation", "s", "1", "1", "1", "0", "0", "0", "1", "0.000001", "t", "src", "extra"}, "\t")
		if _, err := ParseReceiptRow(bad); err == nil {
			t.Fatal("want an error for 18 columns, got none")
		}
		if _, err := ParseReceiptRow(""); err == nil {
			t.Fatal("want an error for an empty line, got none")
		}
	})

	t.Run("invalid stage is refused", func(t *testing.T) {
		bad := strings.Join([]string{
			"2026-09-15", "m", "r",
			"abcdef0123456789abcdef0123456789", "n", "not-a-stage", "s",
			"1", "1", "1", "0", "0", "0", "1", "0.000001", "t", "src",
		}, "\t")
		if _, err := ParseReceiptRow(bad); err == nil {
			t.Fatal("want an error for an unknown stage, got none")
		}
	})

	t.Run("invalid receipt id is refused", func(t *testing.T) {
		for _, id := range []string{
			"abcdef0123456789abcdef01234567",    // 31 characters
			"abcdef0123456789abcdef0123456789x", // 'x' is not hex
			"ABCDEF0123456789ABCDEF0123456789",  // uppercase is not accepted
		} {
			bad := strings.Join([]string{
				"2026-09-15", "m", "r", id, "n", "implementation", "s",
				"1", "1", "1", "0", "0", "0", "1", "0.000001", "t", "src",
			}, "\t")
			if _, err := ParseReceiptRow(bad); err == nil {
				t.Errorf("want an error for receipt id %q, got none", id)
			}
		}
	})
}

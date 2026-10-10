package tokens

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestTokensProviderCoverGoogleTimestampColumns pins the main google export path:
// a timestamp column with offset maps to its UTC day, each type column fills its Counts,
// blank or "-" cells are absent, non-integer cells are skipped, Model is trimmed,
// Repo is Unattributed, and Reports are in type order regardless of column order.
func TestTokensProviderCoverGoogleTimestampColumns(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "google_export.csv")
	content := `timestamp,model,input_tokens,output_tokens,cache_write_tokens,cache_read_tokens,reasoning_tokens
2026-01-15T14:30:00-05:00,  model-name  ,1000,200,50,0,10
2026-01-15T12:00:00Z,my-model,,-,-,5,0
2026-01-15T10:00:00Z,another-model,abc,100,0,0,0`
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))

	s := ReadProvider("google", "acct", path, nil)

	assert.Equal(t, "google:acct", s.Label)
	assert.Equal(t, UTC, s.Basis)
	assert.Equal(t, 3, len(s.Stream))
	assert.Equal(t, 0, len(s.Unreadables))
	assert.Equal(t, 0, len(s.Unparseds)) // third row skips its non-integer cell, it is not a refusal
	assert.Equal(t, []Type{Input, Output, CacheWrite, CacheRead, Reasoning}, s.Reports)

	// Check first row
	m := s.Stream[0]
	assert.Equal(t, "2026-01-15", m.Day)
	assert.Equal(t, "model-name", m.Model)
	assert.Equal(t, Unattributed, m.Repo)
	v, ok := m.Counts.Get(Input)
	assert.True(t, ok)
	assert.Equal(t, int64(1000), v)
	v, ok = m.Counts.Get(Output)
	assert.True(t, ok)
	assert.Equal(t, int64(200), v)
	// CacheWrite should have 50
	v, ok = m.Counts.Get(CacheWrite)
	assert.True(t, ok)
	assert.Equal(t, int64(50), v)
	// CacheRead is a literal 0, so it is present and zero; only blank or "-" is absent
	v, ok = m.Counts.Get(CacheRead)
	assert.True(t, ok)
	assert.Equal(t, int64(0), v)

	// Check second row: blank and "-" cells are absent
	m = s.Stream[1]
	assert.Equal(t, "2026-01-15", m.Day)
	// Input should be absent (was blank)
	_, ok = m.Counts.Get(Input)
	assert.False(t, ok)
	// Output should be absent (was "-")
	_, ok = m.Counts.Get(Output)
	assert.False(t, ok)
	// cache_read should have 5; cache_write was "-" and is absent
	v, ok = m.Counts.Get(CacheRead)
	assert.True(t, ok)
	assert.Equal(t, int64(5), v)
	_, ok = m.Counts.Get(CacheWrite)
	assert.False(t, ok)
}

// TestTokensProviderCoverOpenAIDateZone pins the openai export with date column and zone declaration.
func TestTokensProviderCoverOpenAIDateZone(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "openai_export.csv")
	content := `# timezone: Europe/Berlin
date,model,prompt_tokens,completion_tokens
2026-01-15,my-model,500,100`
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))

	s := ReadProvider("openai", "acct", path, nil)

	assert.Equal(t, "Europe/Berlin", s.Basis)
	assert.Equal(t, 1, len(s.Stream))
	assert.Equal(t, 0, len(s.Unreadables))
	m := s.Stream[0]
	assert.Equal(t, "2026-01-15", m.Day)
}

// TestTokensProviderCoverCRLFLineEnds pins that CRLF line endings are read as LF.
func TestTokensProviderCoverCRLFLineEnds(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "crlf_export.csv")
	content := "timestamp,model,input_tokens\r\n2026-01-15T10:00:00Z,my-model,100\r\n"
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))

	s := ReadProvider("google", "acct", path, nil)

	assert.Equal(t, 1, len(s.Stream))
	assert.Equal(t, 0, len(s.Unreadables))
}

// TestTokensProviderCoverQuotedFieldWithHash pins that a quoted field with inner # is kept.
func TestTokensProviderCoverQuotedFieldWithHash(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "quoted_export.csv")
	content := `timestamp,model,input_tokens
2026-01-15T10:00:00Z,"model with # comment",100`
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))

	s := ReadProvider("google", "acct", path, nil)

	assert.Equal(t, 1, len(s.Stream))
	assert.Equal(t, 0, len(s.Unreadables))
	m := s.Stream[0]
	assert.Equal(t, "model with # comment", m.Model)
}

// TestTokensProviderCoverUnknownKindRefusal pins unknown parser kind.
func TestTokensProviderCoverUnknownKindRefusal(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "fake.csv")
	require.NoError(t, os.WriteFile(path, []byte("test"), 0o644))

	s := ReadProvider("unknown", "acct", path, nil)

	assert.Equal(t, 0, len(s.Stream))
	assert.Equal(t, 1, len(s.Unreadables))
	assert.Contains(t, s.Unreadables[0].Why, "no unknown parser")
	assert.Contains(t, s.Unreadables[0].Why, "google, openai, xai")
}

// TestTokensProviderCoverMissingFileRefusal pins missing file.
func TestTokensProviderCoverMissingFileRefusal(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "missing.csv")

	s := ReadProvider("google", "acct", path, nil)

	assert.Equal(t, 0, len(s.Stream))
	assert.Equal(t, 1, len(s.Unreadables))
}

// TestTokensProviderCoverEmptyFileRefusal pins empty file.
func TestTokensProviderCoverEmptyFileRefusal(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "empty.csv")
	require.NoError(t, os.WriteFile(path, []byte(""), 0o644))

	s := ReadProvider("google", "acct", path, nil)

	assert.Equal(t, 0, len(s.Stream))
	assert.Equal(t, 1, len(s.Unreadables))
}

// TestTokensProviderCoverBareQuoteRefusal pins bare quote that CSV reader rejects.
func TestTokensProviderCoverBareQuoteRefusal(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "bare_quote.csv")
	require.NoError(t, os.WriteFile(path, []byte(`"`), 0o644))

	s := ReadProvider("google", "acct", path, nil)

	assert.Equal(t, 0, len(s.Stream))
	assert.Equal(t, 1, len(s.Unreadables))
}

// TestTokensProviderCoverUnknownColumnRefusal pins unknown column names the column and what parser reads.
func TestTokensProviderCoverUnknownColumnRefusal(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "unknown_col.csv")
	content := `timestamp,model,unknown_col
2026-01-15T10:00:00Z,my-model,100`
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))

	s := ReadProvider("google", "acct", path, nil)

	assert.Equal(t, 0, len(s.Stream))
	assert.Equal(t, 1, len(s.Unreadables))
	assert.Contains(t, s.Unreadables[0].Why, "unknown_col")
}

// TestTokensProviderCoverNoModelColumnRefusal pins missing model column.
func TestTokensProviderCoverNoModelColumnRefusal(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "no_model.csv")
	content := `timestamp,input_tokens
2026-01-15T10:00:00Z,100`
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))

	s := ReadProvider("google", "acct", path, nil)

	assert.Equal(t, 0, len(s.Stream))
	assert.Equal(t, 1, len(s.Unreadables))
	assert.Contains(t, s.Unreadables[0].Why, "no model column")
}

// TestTokensProviderCoverNoTimestampOrDateRefusal pins neither timestamp nor date column.
func TestTokensProviderCoverNoTimestampOrDateRefusal(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "no_ts.csv")
	content := `model,input_tokens
my-model,100`
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))

	s := ReadProvider("google", "acct", path, nil)

	assert.Equal(t, 0, len(s.Stream))
	assert.Equal(t, 1, len(s.Unreadables))
	assert.Contains(t, s.Unreadables[0].Why, "neither a timestamp")
}

// TestTokensProviderCoverDateNoZoneRefusal pins date column with no zone line.
func TestTokensProviderCoverDateNoZoneRefusal(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "date_no_zone.csv")
	content := `date,model,input_tokens
2026-01-15,my-model,100`
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))

	s := ReadProvider("google", "acct", path, nil)

	assert.Equal(t, 0, len(s.Stream))
	assert.Equal(t, 1, len(s.Unreadables))
	assert.Contains(t, s.Unreadables[0].Why, "declares no zone")
}

// TestTokensProviderCoverZoneWhitespaceRefusal pins declared zone with whitespace.
func TestTokensProviderCoverZoneWhitespaceRefusal(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "zone_ws.csv")
	content := `# timezone: E u r o p e / B e r l i n
date,model,input_tokens
2026-01-15,my-model,100`
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))

	s := ReadProvider("google", "acct", path, nil)

	assert.Equal(t, 0, len(s.Stream))
	assert.Equal(t, 1, len(s.Unreadables))
	assert.Contains(t, s.Unreadables[0].Why, "not a zone name without whitespace")
}

// TestTokensProviderCoverWrongFieldCountUnparsed pins row with wrong field count.
func TestTokensProviderCoverWrongFieldCountUnparsed(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "wrong_fields.csv")
	content := `timestamp,model,input_tokens
2026-01-15T10:00:00Z,my-model,100,extra_field`
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))

	s := ReadProvider("google", "acct", path, nil)

	assert.Equal(t, 0, len(s.Stream))
	assert.Equal(t, 0, len(s.Unreadables))
	assert.Equal(t, 1, len(s.Unparseds))
}

// TestTokensProviderCoverBadTimestampUnparsed pins timestamp not RFC 3339.
func TestTokensProviderCoverBadTimestampUnparsed(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "bad_ts.csv")
	content := `timestamp,model,input_tokens
not-a-timestamp,my-model,100`
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))

	s := ReadProvider("google", "acct", path, nil)

	assert.Equal(t, 0, len(s.Stream))
	assert.Equal(t, 0, len(s.Unreadables))
	assert.Equal(t, 1, len(s.Unparseds))
}

// TestTokensProviderCoverBadDateUnparsed pins date not YYYY-MM-DD.
func TestTokensProviderCoverBadDateUnparsed(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "bad_date.csv")
	content := `# timezone: UTC
date,model,input_tokens
not-a-date,my-model,100`
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))

	s := ReadProvider("google", "acct", path, nil)

	assert.Equal(t, 0, len(s.Stream))
	assert.Equal(t, 0, len(s.Unreadables))
	assert.Equal(t, 1, len(s.Unparseds))
}

// TestTokensProviderCoverRealLineNumbers pins that data rows are reported at real file line.
func TestTokensProviderCoverRealLineNumbers(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "real_lines.csv")
	content := `# timezone: UTC
date,model,input_tokens
bad-date,my-model,100`
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))

	s := ReadProvider("google", "acct", path, nil)

	assert.Equal(t, 0, len(s.Stream))
	assert.Equal(t, 0, len(s.Unreadables))
	assert.Equal(t, 1, len(s.Unparseds))
	// First data row is at line 3 (line 1 is comment, line 2 is header)
	assert.Equal(t, 3, s.Unparseds[0].Line)
}

// TestTokensProviderCoverReadXaiJSONNoTurns pins JSON with no turns key.
func TestTokensProviderCoverReadXaiJSONNoTurns(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "no_turns.json")
	content := `{"sessionId":"test"}`
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))

	s := ReadProvider("xai", "acct", path, nil)

	assert.Equal(t, 0, len(s.Stream))
	assert.Equal(t, 1, len(s.Unreadables))
	assert.Contains(t, s.Unreadables[0].Why, "grok usage JSON")
}

// TestTokensProviderCoverReadXaiJSONTurnsNotArray pins turns not an array.
func TestTokensProviderCoverReadXaiJSONTurnsNotArray(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "turns_not_array.json")
	content := `{"turns":"not an array"}`
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))

	s := ReadProvider("xai", "acct", path, nil)

	assert.Equal(t, 0, len(s.Stream))
	assert.Equal(t, 1, len(s.Unreadables))
}

// TestTokensProviderCoverReadXaiJSONTurnNotObject pins turn not an object.
func TestTokensProviderCoverReadXaiJSONTurnNotObject(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "turn_not_object.json")
	content := `{"turns":["not an object"]}`
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))

	s := ReadProvider("xai", "acct", path, nil)

	assert.Equal(t, 0, len(s.Stream))
	assert.Equal(t, 1, len(s.Unparseds))
	assert.Contains(t, s.Unparseds[0].Text, "not a JSON object")
}

// TestTokensProviderCoverReadXaiJSONNoEndedAt pins turn with no endedAt.
func TestTokensProviderCoverReadXaiJSONNoEndedAt(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "no_ended_at.json")
	content := `{"turns":[{"inputTokens":100}]}`
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))

	s := ReadProvider("xai", "acct", path, nil)

	assert.Equal(t, 0, len(s.Stream))
	assert.Equal(t, 1, len(s.Unparseds))
	assert.Contains(t, s.Unparseds[0].Text, "endedAt")
}

// TestTokensProviderCoverReadXaiJSONNegativeCost pins negative costUsdTicks is not Priced.
func TestTokensProviderCoverReadXaiJSONNegativeCost(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "negative_cost.json")
	content := `{"turns":[{"endedAt":"2026-01-15T10:00:00Z","inputTokens":100,"costUsdTicks":-100}]}`
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))

	s := ReadProvider("xai", "acct", path, nil)

	assert.Equal(t, 1, len(s.Stream))
	m := s.Stream[0]
	assert.False(t, m.Priced)
}

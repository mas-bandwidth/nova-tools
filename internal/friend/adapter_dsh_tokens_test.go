package friend

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/cardcost"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const dshSampleTranscript = `{"type":"session","data":{"id":"session-123"}}
{"type":"user/message","data":{"text":"run first step"}}
{"type":"assistant/message","seq":1,"data":{"turn":1,"usage":{"inputTokens":1200,"outputTokens":200,"cacheReadTokens":5000,"cacheWriteTokens":100,"reasoningTokens":50,"totalTokens":6550}}}
{"type":"tool/call","data":{"name":"bash"}}
{"type":"tool/result","data":{"stdout":"ok"}}
{"type":"assistant/message","seq":2,"data":{"turn":2,"usage":{"inputTokens":800,"outputTokens":300,"cacheReadTokens":6000,"cacheWriteTokens":0,"reasoningTokens":25,"totalTokens":7125}}}
{"type":"assistant/message","seq":3,"data":{"turn":3}}
`

func TestTokensOfDSH(t *testing.T) {
	t.Run("sums assistant usage across turns", func(t *testing.T) {
		got, err := TokensOfDSH(dshSampleTranscript)
		require.NoError(t, err)
		assert.Equal(t, "dsh", got.Harness)
		assert.Equal(t, 1, got.Sessions)
		assert.EqualValues(t, 2000, got.Input)
		assert.EqualValues(t, 11000, got.CacheRead)
		assert.EqualValues(t, 100, got.CacheWrite)
		assert.EqualValues(t, 500, got.Output)
		assert.EqualValues(t, 75, got.Reasoning)
		assert.EqualValues(t, 13675, got.Total())

		// Sub extracts the card's increment after base
		baseTranscript := `{"type":"assistant/message","data":{"usage":{"inputTokens":1200,"outputTokens":200,"cacheReadTokens":5000,"cacheWriteTokens":100,"reasoningTokens":50}}}`
		base, err := TokensOfDSH(baseTranscript)
		require.NoError(t, err)
		spent := got.Sub(base)
		assert.Equal(t, "dsh", spent.Harness)
		assert.EqualValues(t, 800, spent.Input)
		assert.EqualValues(t, 6000, spent.CacheRead)
		assert.EqualValues(t, 0, spent.CacheWrite)
		assert.EqualValues(t, 300, spent.Output)
		assert.EqualValues(t, 25, spent.Reasoning)
	})

	t.Run("empty transcript yields zero tokens", func(t *testing.T) {
		got, err := TokensOfDSH("")
		require.NoError(t, err)
		assert.Equal(t, "dsh", got.Harness)
		assert.EqualValues(t, 0, got.Total())
	})
}

func TestTokensFromDSH(t *testing.T) {
	t.Run("refuses invalid session id", func(t *testing.T) {
		_, err := TokensFromDSH(context.Background(), nil, "/sessions", "/work", "../escape")
		assert.Error(t, err)
	})

	t.Run("reads through zstd run mock", func(t *testing.T) {
		root := t.TempDir()
		work := filepath.Join(root, "work")
		sessions := filepath.Join(root, "sessions")
		bucket := filepath.Join(sessions, DSHSessionKey(work))
		sessionID := "session-abc123"
		sessionDir := filepath.Join(bucket, sessionID)
		require.NoError(t, os.MkdirAll(sessionDir, 0o700))
		zstdFile := filepath.Join(sessionDir, "session.v4.jsonl.zstd")
		require.NoError(t, os.WriteFile(zstdFile, []byte("compressed"), 0o600))

		var gotProg string
		var gotArgs []string
		run := func(_ context.Context, dir, prog string, args []string, _ string) (string, int, error) {
			gotProg, gotArgs = prog, args
			return dshSampleTranscript, 0, nil
		}

		got, err := TokensFromDSH(context.Background(), run, sessions, work, sessionID)
		require.NoError(t, err)
		assert.Equal(t, "zstd", gotProg)
		assert.Equal(t, []string{"-dc", zstdFile}, gotArgs)
		assert.Equal(t, "dsh", got.Harness)
		assert.EqualValues(t, 2000, got.Input)
		assert.EqualValues(t, 11000, got.CacheRead)
		assert.EqualValues(t, 500, got.Output)
	})

	t.Run("reads uncompressed jsonl file directly", func(t *testing.T) {
		root := t.TempDir()
		work := filepath.Join(root, "work")
		sessions := filepath.Join(root, "sessions")
		bucket := filepath.Join(sessions, DSHSessionKey(work))
		sessionID := "session-direct"
		sessionDir := filepath.Join(bucket, sessionID)
		require.NoError(t, os.MkdirAll(sessionDir, 0o700))
		jsonlFile := filepath.Join(sessionDir, "session.v4.jsonl")
		require.NoError(t, os.WriteFile(jsonlFile, []byte(dshSampleTranscript), 0o600))

		got, err := TokensFromDSH(context.Background(), nil, sessions, work, sessionID)
		require.NoError(t, err)
		assert.Equal(t, "dsh", got.Harness)
		assert.EqualValues(t, 2000, got.Input)
	})
}

func TestTokensFromDSHSessionFile_RealZstdFixture(t *testing.T) {
	fixturePath := filepath.Join("testdata", "session.v4.jsonl.zstd")
	if _, err := os.Stat(fixturePath); err != nil {
		t.Skip("fixture not found:", err)
	}

	tokens, model, err := TokensFromDSHSessionFile(context.Background(), nil, fixturePath)
	require.NoError(t, err)
	assert.Equal(t, "dsh", tokens.Harness)
	assert.Equal(t, "deepseek-chat", model)
	assert.EqualValues(t, 1200, tokens.Input)
	assert.EqualValues(t, 5000, tokens.CacheRead)
	assert.EqualValues(t, 100, tokens.CacheWrite)
	assert.EqualValues(t, 200, tokens.Output)
	assert.EqualValues(t, 50, tokens.Reasoning)
	assert.EqualValues(t, 6550, tokens.Total())
}

func TestFindDSHSessionFile(t *testing.T) {
	root := t.TempDir()
	bucket := filepath.Join(root, "bucket")
	require.NoError(t, os.MkdirAll(bucket, 0o755))

	// 1. Bare hex directory containing uncompressed session file
	bareHexDir := filepath.Join(bucket, "37a8f92b0c1e4d5f6a7b8c9d0e1f2a3b")
	require.NoError(t, os.MkdirAll(bareHexDir, 0o755))
	bareHexFile := filepath.Join(bareHexDir, "session.v4.jsonl")
	bareHexContent := "{\"type\":\"user/message\",\"data\":{\"text\":\"Lane: zhi one-shot card-barehex 1234567890.\"}}\n"
	require.NoError(t, os.WriteFile(bareHexFile, []byte(bareHexContent), 0o644))

	// 2. session-* directory containing mock zstd session file
	namedDir := filepath.Join(bucket, "session-named-001")
	require.NoError(t, os.MkdirAll(namedDir, 0o755))
	namedZstdFile := filepath.Join(namedDir, "session.v4.jsonl.zstd")
	// Write with zstd magic header so decode recognizes it
	zstdMagicHeader := []byte{0x28, 0xb5, 0x2f, 0xfd, 0x00, 0x00}
	require.NoError(t, os.WriteFile(namedZstdFile, zstdMagicHeader, 0o644))

	fakeExec := func(_ context.Context, dir, prog string, args []string, stdin string) (string, int, error) {
		if prog == "zstd" {
			return "{\"type\":\"user/message\",\"data\":{\"text\":\"Lane: zhi one-shot card-named 1234567890.\"}}\n", 0, nil
		}
		return "", 1, fmt.Errorf("unexpected prog %s", prog)
	}

	t.Run("finds session in bare hex directory", func(t *testing.T) {
		found, err := FindDSHSessionFile(context.Background(), nil, bucket, "zhi one-shot card-barehex 1234567890", "card-barehex")
		require.NoError(t, err)
		assert.Equal(t, bareHexFile, found)
	})

	t.Run("finds session in session-* directory with zstd decoding", func(t *testing.T) {
		found, err := FindDSHSessionFile(context.Background(), fakeExec, bucket, "zhi one-shot card-named 1234567890", "card-named")
		require.NoError(t, err)
		assert.Equal(t, namedZstdFile, found)
	})

	t.Run("returns error when no session matches and never falls back to newest mtime", func(t *testing.T) {
		_, err := FindDSHSessionFile(context.Background(), fakeExec, bucket, "zhi one-shot card-nonexistent 1234567890", "card-nonexistent")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "no matching dsh session file found")
	})
}

func TestDSHPublishCost(t *testing.T) {
	route := RoutePrice{
		Name:  "flash-deepseek",
		Found: true,
		Prices: cardcost.Prices{
			Input:             "0.25",
			Output:            "1.00",
			CacheRead:         "0.025",
			ReasoningAsOutput: true,
		},
	}

	lt := LaneTokens{
		Tokens: cardcost.Tokens{
			Input:      1_000_000,
			CacheRead:  2_000_000,
			CacheWrite: 0,
			Output:     100_000,
			Reasoning:  0,
		},
		Harness:  "dsh",
		Sessions: 1,
	}

	report, tokens, cost := CostLine(lt, route, "deepseek/deepseek-flash")
	assert.Equal(t, "cost: $0.40 (dsh: -)", cost)
	assert.Equal(t, "tokens: input=1000000 cache_read=2000000 cache_write=0 output=100000 reasoning=0 model=deepseek/deepseek-flash harness=dsh", tokens)
	assert.Equal(t, "Cost: $0.40 (dsh: -) tokens input=1000000 cache_read=2000000 cache_write=0 output=100000 reasoning=0 model=deepseek/deepseek-flash harness=dsh price_route=flash-deepseek", report)

	box := t.TempDir()
	in := "Verdict: LAND\nHead: " + strings.Repeat("d", 40) + "\n\ndsh task landed\n"
	require.NoError(t, os.WriteFile(filepath.Join(box, "REPORT.md"), []byte(in), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(box, "RESULT.md"), []byte("RESULT: ok\n"), 0o644))

	require.NoError(t, PublishCost(box, lt, route, "deepseek/deepseek-flash"))

	rep, err := os.ReadFile(filepath.Join(box, "REPORT.md"))
	require.NoError(t, err)
	res, err := os.ReadFile(filepath.Join(box, "RESULT.md"))
	require.NoError(t, err)

	assert.Contains(t, string(rep), "Cost: $0.40 (dsh: -)")
	assert.Contains(t, string(rep), "harness=dsh")
	assert.Contains(t, string(res), "cost: $0.40 (dsh: -)")
	assert.Contains(t, string(res), "tokens: input=1000000")
	assert.Contains(t, string(res), "harness=dsh")
}

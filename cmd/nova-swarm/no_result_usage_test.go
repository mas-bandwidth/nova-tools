package main

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/cardcontract"
	"github.com/mas-bandwidth/nova-tools/internal/cardcost"
	"github.com/mas-bandwidth/nova-tools/internal/secrets"
	"github.com/mas-bandwidth/nova-tools/internal/swarm"
)

// The durable capture is the request-id source, even when native's summary is lost.
func TestNoResultChildRecoversEveryDistinctCapturedGenerationOnce(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	logPath := filepath.Join(root, "native.log")
	require.NoError(t, os.WriteFile(logPath, []byte("native ended without a summary\n"), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(root, "harness-output.log"), []byte(`{"id":"gen-one"}`+"\n"+`{"id":"gen-one"}`+"\n"+`{"id":"gen-two"}`), 0600))
	var ids []string
	child := nativeChild{job: root, logPath: logPath, model: "openrouter/vendor/m", results: filepath.Join(root, "results"), generation: func(ctx context.Context, id string) (cardcost.GenerationUsage, error) {
		_, bounded := ctx.Deadline()
		assert.True(t, bounded)
		ids = append(ids, id)
		return cardcost.GenerationUsage{ID: id, Model: "vendor/m", PromptTokens: 100, CacheReadTokens: -1, MaxPromptTokens: 100, OutputTokens: 20, ReasoningTokens: -1, CostUSD: "0.125"}, nil
	}}
	got := cardcost.ParseUsage(child.Result().Usage)
	assert.Equal(t, []string{"gen-one", "gen-two"}, ids)
	assert.Equal(t, "0.25", got.Actual)
	assert.Equal(t, cardcost.ActualByGeneration, got.ActualBy)
	assert.Equal(t, int64(200), got.Tokens.Input)
	assert.Equal(t, int64(40), got.Tokens.Output)
	assert.Equal(t, int64(2), got.Tokens.Requests)
	assert.Equal(t, "gen-one,gen-two", got.GenerationID)
	assert.Equal(t, child.Result().Usage, got.String())
	assert.Len(t, ids, 2, "the cached terminal result never fetches or bills twice")
}

func TestNoResultRecoveryUsesCompleteQuotesOrTheExactPrompt(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"prompt only", "partial quotes", "tokens without invoice", "mixed partial metadata", "mismatched id", "too many quotes"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			cardPath := filepath.Join(root, "card.md")
			card := []byte("KIND: fix\nREPO: example/tool\nrepair the named invariant\n")
			require.NoError(t, os.WriteFile(cardPath, card, 0600))
			require.NoError(t, os.WriteFile(filepath.Join(root, "JOB.md"), []byte(strings.Repeat("these instructions are read separately\n", 100)), 0600))
			log := []byte("generation_id=gen-one generation_id=gen-two")
			calls := 0
			var lookup func(context.Context, string) (cardcost.GenerationUsage, error)
			if name != "prompt only" {
				lookup = func(_ context.Context, id string) (cardcost.GenerationUsage, error) {
					calls++
					if name == "partial quotes" && id == "gen-two" {
						return cardcost.GenerationUsage{}, errors.New("quote unavailable")
					}
					quotedID := id
					if name == "mismatched id" {
						quotedID = "gen-other"
					}
					g := cardcost.GenerationUsage{ID: quotedID, PromptTokens: 100, CacheReadTokens: -1, MaxPromptTokens: 100, OutputTokens: 20, ReasoningTokens: -1, CostUSD: "0.5"}
					if name == "tokens without invoice" {
						g.CostUSD = ""
					}
					if name == "mixed partial metadata" {
						if id == "gen-one" {
							g.PromptTokens = -1
							g.OutputTokens = -1
						} else {
							g.CostUSD = ""
						}
					}
					return g, nil
				}
			}
			if name == "too many quotes" {
				var b strings.Builder
				for i := 0; i <= maxGenerationQuotes; i++ {
					b.WriteString("generation_id=gen-")
					b.WriteString(strings.Repeat("x", i+1))
					b.WriteByte('\n')
				}
				log = []byte(b.String())
			}
			u := cardcost.ParseUsage(priceRunWithoutUsage("", log, root, cardPath, "openrouter/vendor/m", lookup))
			if name == "tokens without invoice" {
				assert.Empty(t, u.Actual)
				assert.Equal(t, int64(200), u.Tokens.Input)
				assert.Contains(t, u.String(), "usage_source=generation")
			} else {
				assert.Empty(t, u.Actual, "a partial quote is never the whole run's charge")
				assert.False(t, u.Tokens.Reported(), "estimated tokens do not pretend to be a report")
				assert.Equal(t, int64(len(cardcontract.Prompt(root, swarm.CardPrompt(card)))), u.PromptBytes)
				assert.Contains(t, u.String(), "usage_source=prompt")
			}
			if name == "too many quotes" {
				assert.Zero(t, calls)
				assert.Contains(t, u.String(), "generation_usage=too-many-requests")
			}
		})
	}
}

type noResultGenerationTransport struct {
	t      *testing.T
	status int
	body   string
	calls  int
}

func (f *noResultGenerationTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	f.calls++
	assert.Equal(f.t, http.MethodGet, req.Method)
	assert.Equal(f.t, "/api/v1/generation", req.URL.Path)
	assert.Equal(f.t, "gen-one", req.URL.Query().Get("id"))
	assert.Equal(f.t, "Bearer fixture-key", req.Header.Get("Authorization"))
	return &http.Response{StatusCode: f.status, Body: io.NopCloser(strings.NewReader(f.body)), Header: http.Header{"Location": []string{"https://example.invalid/redirect"}}, Request: req}, nil
}

func TestNoResultGenerationReadUsesTheHeldKeyAndBoundedTransport(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		status int
		body   string
		ok     bool
	}{
		{"exact quote", 200, `{"data":{"id":"gen-one","total_cost":0.125}}`, true},
		{"refusal hides body", 401, `fixture-key private detail`, false},
		{"redirect refused", 302, `fixture-key`, false},
		{"oversize refused", 200, strings.Repeat("x", (64<<10)+1), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := &noResultGenerationTransport{t: t, status: tc.status, body: tc.body}
			r := nativeRunner{held: map[string]secrets.Secret{"OPENROUTER_API_KEY": secrets.NewSecret("fixture-key")}, generationTransport: f}
			g, err := r.generationUsage(t.Context(), "gen-one")
			if tc.ok {
				require.NoError(t, err)
				assert.Equal(t, "0.125", g.CostUSD)
			} else {
				require.Error(t, err)
				assert.NotContains(t, err.Error(), "fixture-key")
			}
			assert.Equal(t, 1, f.calls)
		})
	}
}

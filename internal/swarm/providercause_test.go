package swarm

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The cause of a provider failure is on the record (providercause.go): its class, the HTTP
// status when there is one, and the provider's own words. The rows marked "captured" are
// error shapes a harness wrote on a bench on 2026-10-01 (its log's ERROR lines, its
// session's error on the failed message, its own output's last words); the rows marked
// "documented" are a class no bench run produced, in the provider's documented shape.

// TestCauseFromTheHarnessLogAndOutput pins the class, status and message read from text:
// a harness log's error line, or the harness's own last words.
func TestCauseFromTheHarnessLogAndOutput(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, text string
		want       ProviderCause
	}{
		{"captured: a stream error, the provider's h2 failure",
			`message="stream error" providerID=opencode modelID=kimi-k2.7-code session.id=ses_x small=false agent=build mode=primary error.error.message="Streaming response failed: [internal_error] Stream error: h2 protocol error: error reading a body from connection" error.error.type=server_error`,
			ProviderCause{Cause5xx, 0, "Streaming response failed: [internal_error] Stream error: h2 protocol error: error reading a body from connection"}},
		{"captured: the provider's server error",
			`message="stream error" providerID=openrouter modelID=google/gemini-3.8-flash error.error.message="The server had an error while processing your request." error.error.type=server_error error.error.param=null error.error.code=server_error`,
			ProviderCause{Cause5xx, 0, "The server had an error while processing your request."}},
		{"captured: an upstream endpoint unavailable",
			`message="stream error" providerID=opencode modelID=kimi-k2.7-code error.error="AI_APICallError: Upstream request failed: Endpoint is unavailable."`,
			ProviderCause{Cause5xx, 0, "Upstream request failed: Endpoint is unavailable."}},
		{"captured: a gateway timeout",
			`message="stream error" providerID=openrouter modelID=qwen/qwen3.7-flash error.error.code=504 error.error.message="The operation was aborted" error.error.metadata.error_type=timeout`,
			ProviderCause{CauseTimeout, 504, "The operation was aborted"}},
		{"captured: the response headers timed out",
			`message="stream error" providerID=openrouter modelID=qwen/qwen3.7-flash error.error="ProviderHeaderTimeoutError: Provider response headers timed out after 45000ms"`,
			ProviderCause{CauseTimeout, 0, "ProviderHeaderTimeoutError: Provider response headers timed out after 45000ms"}},
		{"captured: rate limited upstream",
			`message="stream error" providerID=openrouter modelID=qwen/qwen3.7-flash error.error="AI_APICallError: [Alibaba] qwen/qwen3.7-flash is temporarily rate-limited upstream. Please retry shortly, or add your own key to accumulate your rate limits: https://provider.invalid/settings"`,
			ProviderCause{CauseRateLimited, 0, "[Alibaba] qwen/qwen3.7-flash is temporarily rate-limited upstream. Please retry shortly, or add your own key [redacted]"}},
		{"captured: the harness's own UnknownError, which names no cause",
			"}\n}\n    \"ref\": \"err_6d7eea8c\"\n    \"message\": \"Unexpected server error. Check server logs for details.\",\n  \"data\": {\n  \"name\": \"UnknownError\",",
			ProviderCause{CauseOther, 0, "UnknownError: Unexpected server error. Check server logs for details."}},
		{"documented: an unknown model id (OpenRouter 404)",
			`message="stream error" error.error="AI_APICallError: No endpoints found for acme/not-a-model." error.error.code=404`,
			ProviderCause{CauseUnknownModel, 404, "No endpoints found for acme/not-a-model."}},
		{"documented: an unknown model id, no status",
			`level=ERROR error.error.message="The model 'acme-9' does not exist or you do not have access to it."`,
			ProviderCause{CauseUnknownModel, 0, "The model 'acme-9' does not exist or you do not have access to it."}},
		{"documented: a refused key, the key's value removed",
			`error.error.message="Incorrect API key provided: sk-proj-abcdefghijklmnopqrstuvwx. You can find your API key at the dashboard." statusCode=401`,
			ProviderCause{CauseAuth, 401, "Incorrect API key [redacted]"}},
		{"documented: a short hex key echoed in the provider's words, everything after key: dropped",
			`error.error.message="Invalid API key: 0123456789abcdef0123456789abcdef" statusCode=401`,
			ProviderCause{CauseAuth, 401, "Invalid API key [redacted]"}},
		{"a URL's address is no status",
			`error.error.message="connection refused at http://127.0.0.1:4000/v1"`,
			ProviderCause{CauseOther, 0, "connection refused at http://127.0.0.1:4000/v1"}},
		{"documented: out of credit (402)",
			`error.error.message="Insufficient Balance" statusCode=402`,
			ProviderCause{CauseCredit, 402, "Insufficient Balance"}},
		{"documented: a 429 that is a spent quota",
			`error.error.message="You exceeded your current quota, please check your plan and billing details." error.error.code=429`,
			ProviderCause{CauseCredit, 429, "You exceeded your current quota, please check your plan and billing details."}},
		{"documented: a 429 rate limit",
			`error.error.message="Rate limit reached for requests" statusCode=429`,
			ProviderCause{CauseRateLimited, 429, "Rate limit reached for requests"}},
		{"documented: an overloaded provider (529)",
			`error.error.message="Overloaded" statusCode=529`,
			ProviderCause{Cause5xx, 529, "Overloaded"}},
		{"an error the classes do not name",
			`error.error.message="the request was malformed" statusCode=400`,
			ProviderCause{CauseOther, 400, "the request was malformed"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, CauseFromText(tc.text))
		})
	}
}

// TestCauseFromTheSessionsRecord pins the cause read from the error the harness's session
// keeps on the message the provider failed: the status is the provider's own.
func TestCauseFromTheSessionsRecord(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, raw string
		want      ProviderCause
		ok        bool
	}{
		{"captured: a 410 whose body is the provider's server error",
			`{"name":"APIError","data":{"message":"Upstream request failed: Endpoint is unavailable.","statusCode":410,"isRetryable":false,"responseHeaders":{"server":"cloudflare"},"responseBody":"{\"error\":{\"type\":\"server_error\",\"message\":\"Upstream request failed: Endpoint is unavailable.\"}}","metadata":{"url":"https://provider.invalid/v1/chat/completions"}}}`,
			ProviderCause{Cause5xx, 410, "Upstream request failed: Endpoint is unavailable."}, true},
		{"documented: an unknown model (400 with the model's words)",
			`{"name":"APIError","data":{"message":"acme/not-a-model is not a valid model ID","statusCode":400,"responseBody":"{\"error\":{\"message\":\"acme/not-a-model is not a valid model ID\",\"code\":400}}"}}`,
			ProviderCause{CauseUnknownModel, 400, "acme/not-a-model is not a valid model ID"}, true},
		{"documented: out of credit, said by the body of a 429",
			`{"name":"APIError","data":{"message":"Too Many Requests","statusCode":429,"responseBody":"{\"error\":{\"type\":\"insufficient_quota\"}}"}}`,
			ProviderCause{CauseCredit, 429, "Too Many Requests"}, true},
		{"documented: the harness's own auth error, no status",
			`{"name":"ProviderAuthError","data":{"providerID":"acme","message":"no API key configured"}}`,
			ProviderCause{CauseAuth, 0, "no API key [redacted]"}, true},
		{"documented: a 503",
			`{"name":"APIError","data":{"message":"Service Unavailable","statusCode":503}}`,
			ProviderCause{Cause5xx, 503, "Service Unavailable"}, true},
		{"no error", ``, ProviderCause{}, false},
		{"not json", `not json`, ProviderCause{}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, ok := CauseFromSessionError(tc.raw)
			assert.Equal(t, tc.ok, ok)
			assert.Equal(t, tc.want, got)
		})
	}
}

// TestTheCausesReasonIsBoundedAndOneLine pins the reason's shape: class and status first,
// the message last, one line, cut with the cut said, and a dash for what is unknown.
func TestTheCausesReasonIsBoundedAndOneLine(t *testing.T) {
	t.Parallel()
	c := CauseFromText(`error.error.message="` + strings.Repeat("overloaded ", 40) + "\nsecond line" + `" statusCode=503`)
	assert.Equal(t, Cause5xx, c.Class)
	assert.LessOrEqual(t, len(c.Message), providerMsgBytes)
	assert.NotContains(t, c.Message, "\n")
	assert.Contains(t, c.Message, "...+", "the cut is said")
	assert.True(t, strings.HasPrefix(c.Reason(), "provider: class=provider-5xx status=503 msg=overloaded "), c.Reason())
	assert.Equal(t, "provider: class=other status=- msg=-", ProviderCause{}.Reason())
}

// TestTheHandbackLineCarriesTheCause pins the 5xx hand-back's line: its fields as before,
// then the cause, read from the harness's error and never from the card's output before it
// (a path that says timeout is the card's, not the provider's).
func TestTheHandbackLineCarriesTheCause(t *testing.T) {
	t.Parallel()
	tail := "STEP 3 reading internal/timeout/rate_limit.go\n" + unknownErrorTail
	h, ok := ProviderHandback(ProviderExit{Tail: []byte(tail), Job: t.TempDir(), RC: 1, Wall: 2 * time.Second, Route: "zen/glm-5"})
	require.True(t, ok)
	assert.Equal(t, ProviderCause{CauseOther, 0, "UnknownError: Unexpected server error. Check server logs for details."}, h.Cause)
	assert.Equal(t, "NATIVE PROVIDER-5XX label=c1 ref=err_fb35c63e wall=2.00s route=zen/glm-5 next=- avoid=zen/glm-5 "+
		"reason=provider: class=other status=- msg=UnknownError: Unexpected server error. Check server logs for details.", h.Line("c1"))

	h, ok = ProviderHandback(ProviderExit{Tail: []byte("STEP 1 timeout.go\n502 Bad Gateway\n"), Job: t.TempDir(), RC: 1, Route: "zen/glm-5"})
	require.True(t, ok)
	assert.Equal(t, Cause5xx, h.Cause.Class)
	assert.Equal(t, "502 Bad Gateway", h.Cause.Message)
}

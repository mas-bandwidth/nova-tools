package swarm

import (
	"testing"
)

// Issue #2001 & #2011: ClassifyProviderFailure detects provider-side infrastructure errors
// (such as UnknownError JSON, unexpected server errors, 5xx gateway faults, and 429 rate limits)
// in the captured logs and extracts provider error refs.
func TestClassifyProviderFailure(t *testing.T) {
	cases := []struct {
		name    string
		log     string
		wantWhy string
		wantRef string
		wantOK  bool
	}{
		{
			name: "issue 2001 exact json unknown error with ref",
			log: `Error: {
  "name": "UnknownError",
  "data": {
    "message": "Unexpected server error. Check server logs for details.",
    "ref": "err_29c29bd4"
  }
}`,
			wantWhy: "unexpected-server-error",
			wantRef: "err_29c29bd4",
			wantOK:  true,
		},
		{
			name:    "5xx with ref key-value",
			log:     "Unexpected server error: the provider answered 503; ref=err_fake_5xx\n",
			wantWhy: "unexpected-server-error",
			wantRef: "err_fake_5xx",
			wantOK:  true,
		},
		{
			name:    "internal server error without ref",
			log:     "error: internal server error while processing prompt\n",
			wantWhy: "unexpected-server-error",
			wantRef: "",
			wantOK:  true,
		},
		{
			name:    "http 502 bad gateway",
			log:     "The gateway returned 502 Bad Gateway for endpoint\n",
			wantWhy: "provider-5xx",
			wantRef: "",
			wantOK:  true,
		},
		{
			name:    "http 529 site overloaded",
			log:     "upstream service returned 529\n",
			wantWhy: "provider-5xx",
			wantRef: "",
			wantOK:  true,
		},
		{
			name:    "http 429 rate limit",
			log:     "fake harness: error: the provider answered HTTP 429 Too Many Requests (rate limit)\n",
			wantWhy: "rate-limit",
			wantRef: "",
			wantOK:  true,
		},
		{
			name:    "token quota / rate limit exceeded",
			log:     "Error: Rate limit reached: input token limit exceeded\n",
			wantWhy: "rate-limit",
			wantRef: "",
			wantOK:  true,
		},
		{
			name:    "resource exhausted error",
			log:     "rpc error: code = ResourceExhausted desc = quota exceeded\n",
			wantWhy: "rate-limit",
			wantRef: "",
			wantOK:  true,
		},
		{
			name:    "bare unknown error",
			log:     `{"name": "UnknownError", "data": {}}`,
			wantWhy: "unknown-error",
			wantRef: "",
			wantOK:  true,
		},
		{
			name:    "structured marker rate limit with ref",
			log:     "[PROVIDER_ERROR] rate-limit ref=err_struct_429\n",
			wantWhy: "rate-limit",
			wantRef: "err_struct_429",
			wantOK:  true,
		},
		{
			name:    "structured marker 503 server error",
			log:     "[PROVIDER_ERROR] provider-503 ref=err_struct_503\n",
			wantWhy: "provider-5xx",
			wantRef: "err_struct_503",
			wantOK:  true,
		},
		{
			name:    "http 504 gateway timeout on gateway runner line",
			log:     "The gateway returned 504 Gateway Timeout for endpoint\n",
			wantWhy: "provider-5xx",
			wantRef: "",
			wantOK:  true,
		},
		{
			name:    "http 504 gateway timeout on error prefix line",
			log:     "error: 504 Gateway Timeout from upstream proxy\n",
			wantWhy: "provider-5xx",
			wantRef: "",
			wantOK:  true,
		},
		{
			name:    "structured marker 504 gateway timeout with ref",
			log:     "[PROVIDER_ERROR] 504 Gateway Timeout ref=err_gw_504\n",
			wantWhy: "provider-5xx",
			wantRef: "err_gw_504",
			wantOK:  true,
		},
		{
			name:    "econnreset on runner error line",
			log:     "error: read tcp 127.0.0.1:8080: ECONNRESET\n",
			wantWhy: "unexpected-server-error",
			wantRef: "",
			wantOK:  true,
		},
		{
			name:    "connection reset by peer on runner error line",
			log:     "error: read tcp 10.0.0.1:443: connection reset by peer\n",
			wantWhy: "unexpected-server-error",
			wantRef: "",
			wantOK:  true,
		},
		{
			name:    "connection reset by peer on the provider runner line with ref",
			log:     "the provider: connection reset by peer ref=err_conn_peer\n",
			wantWhy: "unexpected-server-error",
			wantRef: "err_conn_peer",
			wantOK:  true,
		},
		{
			name:    "rpc error connection reset by peer",
			log:     "rpc error: code = Unavailable desc = connection reset by peer\n",
			wantWhy: "unexpected-server-error",
			wantRef: "",
			wantOK:  true,
		},
		{
			name:    "structured marker ECONNRESET with ref",
			log:     "[PROVIDER_ERROR] ECONNRESET ref=err_struct_rst\n",
			wantWhy: "unexpected-server-error",
			wantRef: "err_struct_rst",
			wantOK:  true,
		},
		{
			name:    "structured marker connection reset by peer with ref",
			log:     "[PROVIDER_ERROR] connection reset by peer ref=err_struct_peer\n",
			wantWhy: "unexpected-server-error",
			wantRef: "err_struct_peer",
			wantOK:  true,
		},
		{
			name:   "negative control: transcript prose reviewed lines 429 through 500",
			log:    "reviewed lines 429 through 500 in internal/swarm/native.go\n",
			wantOK: false,
		},
		{
			name:   "negative control: transcript prose quoting HTTP 429 and 500 cases",
			log:    "We verified test cases for handling HTTP 429 and HTTP 500 properly.\n",
			wantOK: false,
		},
		{
			name:   "negative control: transcript prose discussing UnknownError",
			log:    "We considered whether an UnknownError could be returned by the upstream endpoint.\n",
			wantOK: false,
		},
		{
			name:   "negative control: prompt discussion of quoted JSON UnknownError",
			log:    "prompt: test how mock handles '{\"name\": \"UnknownError\"}'\n",
			wantOK: false,
		},
		{
			name:   "negative control: transcript prose quoting JSON UnknownError",
			log:    "The server did not return {\"name\": \"UnknownError\"} during the run.\n",
			wantOK: false,
		},
		{
			name:   "negative control: escaped JSON UnknownError inside code string",
			log:    "code = \"\\\"name\\\": \\\"UnknownError\\\"\"\n",
			wantOK: false,
		},
		{
			name:   "negative control: prompt discussion of 502 Bad Gateway",
			log:    "prompt: test how the client handles 502 Bad Gateway responses\n",
			wantOK: false,
		},
		{
			name:   "negative control: transcript prose discussing 502 Bad Gateway",
			log:    "We reviewed upstream 502 Bad Gateway error handling and added retries.\n",
			wantOK: false,
		},
		{
			name:   "negative control: prompt discussion of 503 Service Unavailable",
			log:    "prompt: implement backoff when 503 Service Unavailable is received\n",
			wantOK: false,
		},
		{
			name:   "negative control: transcript prose discussing 503 Service Unavailable",
			log:    "The service was healthy; no 503 Service Unavailable encountered during testing.\n",
			wantOK: false,
		},
		{
			name:   "negative control: prompt discussion of 504 Gateway Timeout",
			log:    "prompt: investigate why requests triggered 504 Gateway Timeout\n",
			wantOK: false,
		},
		{
			name:   "negative control: transcript prose discussing 504 Gateway Timeout",
			log:    "Discussion of 504 Gateway Timeout mitigation using circuit breakers.\n",
			wantOK: false,
		},
		{
			name:   "negative control: prompt discussion of ECONNRESET",
			log:    "prompt: handle ECONNRESET gracefully in HTTP client\n",
			wantOK: false,
		},
		{
			name:   "negative control: transcript prose discussing ECONNRESET",
			log:    "Client socket observed ECONNRESET during reconnect test.\n",
			wantOK: false,
		},
		{
			name:   "negative control: prompt discussion of Connection reset by peer",
			log:    "prompt: explain why connection reset by peer happens on closed socket\n",
			wantOK: false,
		},
		{
			name:   "negative control: transcript prose discussing Connection reset by peer",
			log:    "We saw connection reset by peer in local mock server logs without error.\n",
			wantOK: false,
		},
		{
			name:   "negative control: transcript prose discussing rate-limit",
			log:    "Implemented client-side rate-limit backoff.\n",
			wantOK: false,
		},
		{
			name:   "negative control: error prefix with prose lines 429 through 500",
			log:    "Error: reviewed lines 429 through 500 without issues\n",
			wantOK: false,
		},
		{
			name:   "negative control: normal test failure",
			log:    "=== RUN TestSomething\n--- FAIL: TestSomething (0.01s)\nFAIL\n",
			wantOK: false,
		},
		{
			name:   "negative control: clean exit",
			log:    "Everything built and passed.\n",
			wantOK: false,
		},
		{
			name:   "negative control: empty capture",
			log:    "",
			wantOK: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pf, ok := ClassifyProviderFailure([]byte(tc.log))
			if ok != tc.wantOK {
				t.Fatalf("ClassifyProviderFailure() ok = %v, want %v", ok, tc.wantOK)
			}
			if !tc.wantOK {
				return
			}
			if pf.Why != tc.wantWhy {
				t.Errorf("ClassifyProviderFailure() Why = %q, want %q", pf.Why, tc.wantWhy)
			}
			if pf.Ref != tc.wantRef {
				t.Errorf("ClassifyProviderFailure() Ref = %q, want %q", pf.Ref, tc.wantRef)
			}
		})
	}
}

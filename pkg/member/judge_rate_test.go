package member

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// A rate-limited run is a provider failure and never the card's, whether or not it left a
// shaped result -- unless it pushed a head, which stays the card's own (a-rate-limited-
// refusal-never-fails-the-cardc; Judge).
func TestARateLimitedRunIsAProviderFailure(t *testing.T) {
	t.Parallel()
	rateLine := "provider: class=rate-limited status=429 msg=Too many requests"
	for _, tc := range []struct {
		name   string
		pu     Push
		want   Finish
		reason string
	}{
		{"shaped, no pushed head", Push{}, FinishFailed, EndProvider},
		{"shaped, a pushed head", Push{Sha: "0123456789abcdef0123456789abcdef01234567"}, FinishOK, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fin, why := Judge(Result{Ran: true, Shaped: true, Verdict: "ok", End: EndProvider, Provider: rateLine}, tc.pu)
			assert.Equal(t, tc.want, fin, why)
			if tc.reason != "" {
				assert.Contains(t, why, tc.reason)
				assert.Contains(t, why, "class=rate-limited")
			}
		})
	}
}

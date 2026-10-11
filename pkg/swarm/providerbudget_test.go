package swarm

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestLimitRefusal(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		status int
		body   string
		want   bool
	}{
		{429, "", true},
		{400, "Your current concurrency is 9/12, which exceeds your concurrency limit", true},
		{503, "Too Many Requests", true},
		{402, "Insufficient Balance", false},
		{429, `{"error":{"type":"insufficient_quota"}}`, false},
		{429, `{"error":{"message":"You exceeded your current quota, please check your plan and billing details."}}`, false},
		{429, `{"error":{"message":"Rate limit reached for requests"}}`, true},
		{400, "rate limit: insufficient quota", false},
		{401, "bad key", false},
		{500, "internal", false},
		{200, "too many requests", false},
	} {
		assert.Equal(t, c.want, limitRefusal(c.status, []byte(c.body)), "limitRefusal(%d, %q)", c.status, c.body)
	}
}

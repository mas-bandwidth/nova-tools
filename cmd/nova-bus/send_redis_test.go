package main

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/bus"
	"github.com/stretchr/testify/require"
)

func TestRedisNoteBodyOmitsGeneratedDateButRetainsCanonicalNote(t *testing.T) {
	t.Parallel()
	n := bus.Note{
		Header: bus.Header{From: "Ada", To: "Bo", Date: "Mon Jan  2 15:04:05 UTC 2006", ID: "op-id", Re: []string{"root-id"}, Subject: "A finding"},
		Body:   "The date belongs to the Redis publish record.\n",
	}
	got := redisNoteBody(n)
	require.NotEmpty(t, got)
	require.False(t, containsLine(got, "Date:"), "Date header must be stored separately from the retry-stable body")
	require.True(t, containsLine(got, "Id: op-id"))
	require.True(t, containsLine(got, "To: Bo"))
}

func TestRedisRecipientsUsesBusAddressSyntax(t *testing.T) {
	t.Parallel()
	got, err := redisRecipients("Ada (day shift, west host), Bo")
	require.NoError(t, err)
	require.Equal(t, []string{"Ada", "Bo"}, got)
	_, err = redisRecipients("all")
	require.ErrorContains(t, err, "name each recipient explicitly")
}

func TestRedisSendRequiresStableOperationBeforeOpeningStore(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	code := run([]string{"send", "--redis", "127.0.0.1:6379", "--stdin", "--as", "Ada"}, strings.NewReader("From: Ada\nTo: Bo\nSubject: hello\n\nbody\n"), &stdout, &stderr, time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC))
	require.Equal(t, 2, code)
	require.Contains(t, stderr.String(), "--op is required")
	require.Empty(t, stdout.String())
}

func containsLine(s, want string) bool {
	for _, line := range strings.Split(s, "\n") {
		if line == want || strings.HasPrefix(line, want) {
			return true
		}
	}
	return false
}

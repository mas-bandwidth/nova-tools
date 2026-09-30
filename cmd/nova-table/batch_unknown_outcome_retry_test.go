package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/mas-bandwidth/nova-tools/internal/redisconn"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// makeUnknownOutcome creates an ErrUnknownOutcome error formatted like ntable.ApplyBatch.
func makeUnknownOutcome(table, opID string) error {
	return fmt.Errorf("table %q batch %q: %w: connection reset by peer (changed=unknown); resend the same manifest with the same operation id %q: it returns the original receipt if the batch was applied and applies it if it was not; run: nova-table show '%s'",
		table, opID, ntable.ErrUnknownOutcome, opID, table)
}

// testApp creates an application stubbed so it does not require a running Redis instance.
func testApp(applyErr error) *application {
	app := &application{}
	app.openClient = func(ctx context.Context, verb, addr string, stderr io.Writer) (*connection, *redis.Client, int) {
		return &connection{counter: &redisconn.Trips{}}, nil, 0
	}
	app.applyBatch = func(ctx context.Context, c redis.Cmdable, manifest ntable.BatchManifest) (ntable.Receipt, error) {
		return ntable.Receipt{}, applyErr
	}
	return app
}

func extractNextCommand(refusal string) string {
	idx := strings.LastIndex(refusal, "; run: ")
	if idx < 0 {
		return ""
	}
	return strings.TrimSpace(refusal[idx+len("; run: "):])
}

func TestBatchUnknownOutcomeInlineJSON(t *testing.T) {
	t.Parallel()

	rawJSON := `{"schema":1,"table":"demo","epoch":"0","expected_table_revision":"3","operation_id":"op-inline","members":[{"id":"a","expect":{}}]}`
	app := testApp(makeUnknownOutcome("demo", "op-inline"))

	cases := []struct {
		name     string
		args     []string
		wantNext string
	}{
		{
			name:     "with redis flag",
			args:     []string{"batch", "--redis", "127.0.0.1:6379", rawJSON},
			wantNext: "nova-table batch --redis 127.0.0.1:6379 '" + rawJSON + "'",
		},
		{
			name:     "with redis and actor flags",
			args:     []string{"batch", "--redis", "127.0.0.1:6379", "--actor", "alice", rawJSON},
			wantNext: "nova-table batch --redis 127.0.0.1:6379 --actor alice '" + rawJSON + "'",
		},
		{
			name:     "with flags after json arg",
			args:     []string{"batch", rawJSON, "--redis", "127.0.0.1:6379", "--epoch", "0"},
			wantNext: "nova-table batch --redis 127.0.0.1:6379 --epoch 0 '" + rawJSON + "'",
		},
		{
			name:     "with receipt false and json output flags",
			args:     []string{"batch", "--redis", "127.0.0.1:6379", "--receipt=false", "--json", rawJSON},
			wantNext: "nova-table batch --redis 127.0.0.1:6379 --receipt=false --json '" + rawJSON + "'",
		},
		{
			name:     "no flags",
			args:     []string{"batch", rawJSON},
			wantNext: "nova-table batch '" + rawJSON + "'",
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var stdout, stderr bytes.Buffer
			code := app.run(tc.args, &stdout, &stderr)
			require.Equal(t, 2, code, "exit code")
			errStr := stderr.String()
			assert.Contains(t, errStr, "resend the same manifest with the same operation id")
			assert.Contains(t, errStr, "changed=unknown")
			gotNext := extractNextCommand(errStr)
			assert.Equal(t, tc.wantNext, gotNext)
		})
	}
}

func TestBatchUnknownOutcomeInlineJSONWithSingleQuotes(t *testing.T) {
	t.Parallel()

	rawJSON := `{"schema":1,"table":"demo","epoch":"0","expected_table_revision":"3","operation_id":"op-quote","members":[{"id":"it's","expect":{}}]}`
	app := testApp(makeUnknownOutcome("demo", "op-quote"))

	var stdout, stderr bytes.Buffer
	code := app.run([]string{"batch", "--redis", "127.0.0.1:6379", rawJSON}, &stdout, &stderr)
	require.Equal(t, 2, code, "exit code")
	errStr := stderr.String()
	assert.Contains(t, errStr, "resend the same manifest with the same operation id")

	escaped := strings.ReplaceAll(rawJSON, "'", `'\''`)
	wantNext := "nova-table batch --redis 127.0.0.1:6379 '" + escaped + "'"
	gotNext := extractNextCommand(errStr)
	assert.Equal(t, wantNext, gotNext)

	// Verify that shellWords can parse the next command correctly back to the original JSON.
	words, err := shellWords(gotNext)
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(words), 2)
	lastWord := words[len(words)-1]
	assert.Equal(t, rawJSON, lastWord)
}

func TestBatchUnknownOutcomeStdin(t *testing.T) {
	t.Parallel()

	rawJSON := `{"schema":1,"table":"demo","epoch":"0","expected_table_revision":"3","operation_id":"op-stdin","members":[{"id":"a","expect":{}}]}`

	cases := []struct {
		name     string
		args     []string
		wantNext string
	}{
		{
			name:     "with redis flag",
			args:     []string{"batch", "--redis", "127.0.0.1:6379", "-"},
			wantNext: "nova-table batch --redis 127.0.0.1:6379 -",
		},
		{
			name:     "with redis and actor flags",
			args:     []string{"batch", "--redis", "127.0.0.1:6379", "--actor", "bob", "-"},
			wantNext: "nova-table batch --redis 127.0.0.1:6379 --actor bob -",
		},
		{
			name:     "with flags after stdin dash",
			args:     []string{"batch", "-", "--redis", "127.0.0.1:6379"},
			wantNext: "nova-table batch --redis 127.0.0.1:6379 -",
		},
		{
			name:     "no flags",
			args:     []string{"batch", "-"},
			wantNext: "nova-table batch -",
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			app := testApp(makeUnknownOutcome("demo", "op-stdin"))
			app.in = strings.NewReader(rawJSON)

			var stdout, stderr bytes.Buffer
			code := app.run(tc.args, &stdout, &stderr)
			require.Equal(t, 2, code, "exit code")
			errStr := stderr.String()
			assert.Contains(t, errStr, "resend the same manifest with the same operation id")
			assert.Contains(t, errStr, "changed=unknown")
			gotNext := extractNextCommand(errStr)
			assert.Equal(t, tc.wantNext, gotNext)
		})
	}
}

func TestBatchUnknownOutcomeFile(t *testing.T) {
	t.Parallel()

	rawJSON := `{"schema":1,"table":"demo","epoch":"0","expected_table_revision":"3","operation_id":"op-file","members":[{"id":"a","expect":{}}]}`

	dir := t.TempDir()
	filePath := filepath.Join(dir, "manifest.json")
	require.NoError(t, os.WriteFile(filePath, []byte(rawJSON), 0644))

	quotedFilePath := filepath.Join(dir, "it's a manifest.json")
	require.NoError(t, os.WriteFile(quotedFilePath, []byte(rawJSON), 0644))

	cases := []struct {
		name     string
		args     []string
		wantNext string
	}{
		{
			name:     "with redis flag",
			args:     []string{"batch", "--redis", "127.0.0.1:6379", filePath},
			wantNext: "nova-table batch --redis 127.0.0.1:6379 '" + filePath + "'",
		},
		{
			name:     "with redis and actor flags",
			args:     []string{"batch", "--redis", "127.0.0.1:6379", "--actor", "carol", filePath},
			wantNext: "nova-table batch --redis 127.0.0.1:6379 --actor carol '" + filePath + "'",
		},
		{
			name:     "with flags after file arg",
			args:     []string{"batch", filePath, "--redis", "127.0.0.1:6379"},
			wantNext: "nova-table batch --redis 127.0.0.1:6379 '" + filePath + "'",
		},
		{
			name:     "file with single quotes and spaces in path",
			args:     []string{"batch", "--redis", "127.0.0.1:6379", quotedFilePath},
			wantNext: "nova-table batch --redis 127.0.0.1:6379 '" + strings.ReplaceAll(quotedFilePath, "'", `'\''`) + "'",
		},
		{
			name:     "no flags",
			args:     []string{"batch", filePath},
			wantNext: "nova-table batch '" + filePath + "'",
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			app := testApp(makeUnknownOutcome("demo", "op-file"))

			var stdout, stderr bytes.Buffer
			code := app.run(tc.args, &stdout, &stderr)
			require.Equal(t, 2, code, "exit code")
			errStr := stderr.String()
			assert.Contains(t, errStr, "resend the same manifest with the same operation id")
			assert.Contains(t, errStr, "changed=unknown")
			gotNext := extractNextCommand(errStr)
			assert.Equal(t, tc.wantNext, gotNext)
		})
	}
}

func TestBatchUnknownOutcomeReplacesSendAgainWording(t *testing.T) {
	t.Parallel()

	// Verify that even if the error text used the older "send the same manifest again with the same operation id"
	// wording, cmdBatch ensures the advice says "resend the same manifest with the same operation id".
	legacyErr := fmt.Errorf("table \"demo\" batch \"op-leg\": the store did not confirm the batch: %w: timeout (changed=unknown); send the same manifest again with the same operation id \"op-leg\": it returns the original receipt if the batch was applied and applies it if it was not; run: nova-table show 'demo'",
		ntable.ErrUnknownOutcome)

	rawJSON := `{"schema":1,"table":"demo","epoch":"0","expected_table_revision":"3","operation_id":"op-leg","members":[{"id":"a","expect":{}}]}`
	app := testApp(legacyErr)

	var stdout, stderr bytes.Buffer
	code := app.run([]string{"batch", "--redis", "127.0.0.1:6379", rawJSON}, &stdout, &stderr)
	require.Equal(t, 2, code, "exit code")
	errStr := stderr.String()
	assert.Contains(t, errStr, "resend the same manifest with the same operation id")
	assert.NotContains(t, errStr, "send the same manifest again with the same operation id")
	wantNext := "nova-table batch --redis 127.0.0.1:6379 '" + rawJSON + "'"
	gotNext := extractNextCommand(errStr)
	assert.Equal(t, wantNext, gotNext)
}

package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/fuse"
	"github.com/mas-bandwidth/nova-tools/pkg/testkit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fuseReceipt struct {
	Result struct {
		Verb    string   `json:"verb"`
		Status  string   `json:"status"`
		Exit    int      `json:"exit"`
		Remedy  string   `json:"remedy"`
		Why     []string `json:"why"`
		Reasons []string `json:"reasons"`
	} `json:"result"`
	Facts map[string]any `json:"facts"`
	Items []struct {
		Kind   string         `json:"kind"`
		Fields map[string]any `json:"fields"`
		Text   string         `json:"text"`
	} `json:"items"`
	More []struct {
		Kind   string `json:"kind"`
		Shown  int    `json:"shown"`
		Total  int    `json:"total"`
		Remedy string `json:"remedy"`
	} `json:"more"`
}

func decodeFuseReceipt(t *testing.T, out, stderr string) fuseReceipt {
	t.Helper()
	assert.Empty(t, stderr)
	var got fuseReceipt
	require.NoError(t, json.Unmarshal([]byte(out), &got), "one standard JSON receipt: %s", out)
	return got
}

func TestFuseStatusJSON(t *testing.T) {
	t.Parallel()

	box := absentBoxIn(t)
	testkit.WriteFile(t, box, `{"lockdown":{"at":"2026-09-09T18:27:40Z","reason":"global stop"},"quarantine":{"a":{"at":"2026-09-09T18:27:41Z","reason":"one"},"b":{"at":"2026-09-09T18:27:42Z","reason":"two"},"c":{"at":"2026-09-09T18:27:43Z","reason":"three"}}}`)

	code, out, stderr := capture(t, []string{"status", "--box", box, "--max", "1", "--json"}, nowish())
	assert.Equal(t, 0, code, "a readable blown box is reportable")
	got := decodeFuseReceipt(t, out, stderr)
	assert.Equal(t, "status", got.Result.Verb)
	assert.Equal(t, "ok", got.Result.Status)
	assert.Equal(t, 0, got.Result.Exit)
	assert.Equal(t, "blown", got.Facts["lockdown"])
	assert.Equal(t, float64(3), got.Facts["quarantines"])
	require.Len(t, got.Items, 2, "one lockdown plus one capped quarantine")
	assert.Equal(t, "lockdown", got.Items[0].Kind)
	assert.Equal(t, "global stop", got.Items[0].Fields["reason"])
	assert.Equal(t, "quarantine", got.Items[1].Kind)
	assert.Equal(t, "a", got.Items[1].Fields["surface"])
	require.Len(t, got.More, 1)
	assert.Equal(t, "quarantine", got.More[0].Kind)
	assert.Equal(t, 1, got.More[0].Shown)
	assert.Equal(t, 3, got.More[0].Total)
	assert.NotContains(t, out, "expiry")

	code, legacy, stderr := capture(t, []string{"status", "--box", box, "--max", "1"}, nowish())
	assert.Equal(t, 0, code)
	assert.Empty(t, stderr)
	assert.Equal(t, "STATUS OK lockdown=blown since=2026-09-09T18:27:40Z quarantines=3: global stop\nSTATUS OK quarantine=a since=2026-09-09T18:27:41Z: one\nSTATUS MORE kind=quarantine shown=1 total=3 --max <n> raises the ceiling, --max 0 lists every quarantine\n", legacy)

	missing := absentBoxIn(t)
	code, out, stderr = capture(t, []string{"status", "--box", missing, "--json"}, nowish())
	assert.Equal(t, 2, code)
	got = decodeFuseReceipt(t, out, stderr)
	assert.Equal(t, "refused", got.Result.Status)
	assert.Equal(t, 2, got.Result.Exit)
	assert.Equal(t, "unknown", got.Facts["lockdown"])
	assert.NotEqual(t, "clear", got.Facts["lockdown"])

	malformed := absentBoxIn(t)
	testkit.WriteFile(t, malformed, `{"lockdown":`)
	code, out, stderr = capture(t, []string{"status", "--box", malformed, "--json"}, nowish())
	assert.Equal(t, 2, code)
	got = decodeFuseReceipt(t, out, stderr)
	assert.Equal(t, "refused", got.Result.Status)
	assert.Equal(t, "unknown", got.Facts["lockdown"])
}

func TestFuseCheckAndReceiptsJSON(t *testing.T) {
	t.Parallel()

	now := nowish()
	box := boxIn(t)
	code, out, stderr := capture(t, []string{"check", "--box", box, "--json", "surface"}, now)
	assert.Equal(t, 0, code)
	got := decodeFuseReceipt(t, out, stderr)
	assert.Equal(t, "ok", got.Result.Status)
	assert.Equal(t, "clear", got.Facts["decision"])
	assert.Equal(t, "surface", got.Facts["checked"])

	code, out, stderr = capture(t, []string{"quarantine", "--box", box, "--json", "surface", "first reason"}, now)
	assert.Equal(t, 0, code)
	got = decodeFuseReceipt(t, out, stderr)
	assert.Equal(t, "ok", got.Result.Status)
	assert.Equal(t, "recorded_and_verified", got.Facts["receipt"])
	require.Len(t, got.Items, 1)
	assert.Equal(t, "2026-09-09T18:27:40Z", got.Items[0].Fields["at"])
	assert.Equal(t, "first reason", got.Items[0].Fields["reason"])
	stored, err := fuse.ReadBox(box)
	require.NoError(t, err)
	assert.Equal(t, "first reason", stored.Quarantine["surface"].Reason)

	code, out, stderr = capture(t, []string{"quarantine", "--box", box, "--json", "surface", "replacement reason"}, now.Add(time.Hour))
	assert.Equal(t, 0, code)
	got = decodeFuseReceipt(t, out, stderr)
	assert.Equal(t, "already_quarantined", got.Facts["receipt"])
	assert.Equal(t, "first reason", got.Items[0].Fields["reason"], "receipt describes the stored record")
	assert.Equal(t, "replacement reason", got.Items[0].Fields["requested_reason"])
	stored, err = fuse.ReadBox(box)
	require.NoError(t, err)
	assert.Equal(t, "first reason", stored.Quarantine["surface"].Reason)

	code, out, stderr = capture(t, []string{"quarantine", "--box", box, "--dry-run", "--json", "surface", "third reason"}, now.Add(2*time.Hour))
	assert.Equal(t, 0, code)
	got = decodeFuseReceipt(t, out, stderr)
	assert.Equal(t, "already_quarantined", got.Facts["receipt"])
	assert.Equal(t, true, got.Facts["dry_run"])
	assert.Equal(t, "first reason", got.Items[0].Fields["reason"])
	stored, err = fuse.ReadBox(box)
	require.NoError(t, err)
	assert.Equal(t, "first reason", stored.Quarantine["surface"].Reason)

	code, out, stderr = capture(t, []string{"quarantine", "--box", box, "--dry-run", "--json", "another", "planned reason"}, now)
	assert.Equal(t, 0, code)
	got = decodeFuseReceipt(t, out, stderr)
	assert.Equal(t, "dry_run", got.Facts["receipt"])
	assert.Contains(t, got.Items[0].Text, "nothing written")
	stored, err = fuse.ReadBox(box)
	require.NoError(t, err)
	assert.NotContains(t, stored.Quarantine, "another")

	code, out, stderr = capture(t, []string{"check", "--box", box, "--json", "surface"}, now)
	assert.Equal(t, 1, code)
	got = decodeFuseReceipt(t, out, stderr)
	assert.Equal(t, "failed", got.Result.Status)
	assert.Equal(t, 1, got.Result.Exit)
	assert.Equal(t, "blown", got.Facts["decision"])
	assert.Equal(t, "first reason", got.Items[0].Fields["reason"])

	missing := absentBoxIn(t)
	code, out, stderr = capture(t, []string{"check", "--box", missing, "--json", "surface"}, now)
	assert.Equal(t, 2, code)
	got = decodeFuseReceipt(t, out, stderr)
	assert.Equal(t, "refused", got.Result.Status)
	assert.Equal(t, 2, got.Result.Exit)
	assert.Contains(t, got.Result.Why[0], "no box")

	code, out, stderr = capture(t, []string{"check", "--json", "surface"}, now)
	assert.Equal(t, 2, code)
	got = decodeFuseReceipt(t, out, stderr)
	assert.Equal(t, "refused", got.Result.Status)
	require.NotEmpty(t, got.Result.Why)
	assert.Contains(t, strings.Join(got.Result.Why, " "), "--box is required")

	code, out, stderr = capture(t, []string{"quarantine", "--json", "surface"}, now)
	assert.Equal(t, 2, code)
	got = decodeFuseReceipt(t, out, stderr)
	assert.Equal(t, "refused", got.Result.Status)
	assert.Contains(t, strings.Join(got.Result.Why, " "), "--box is required")
	assert.Contains(t, strings.Join(got.Result.Why, " "), "surface and a reason")

	malformed := absentBoxIn(t)
	testkit.WriteFile(t, malformed, `[]`)
	code, out, stderr = capture(t, []string{"check", "--box", malformed, "--json", "surface"}, now)
	assert.Equal(t, 2, code)
	got = decodeFuseReceipt(t, out, stderr)
	assert.Equal(t, "refused", got.Result.Status)
	assert.Equal(t, "unknown", got.Facts["decision"])
}

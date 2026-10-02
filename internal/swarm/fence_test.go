package swarm

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ISSUE #644. A card the harness's own fence stopped is NOT a card whose model published
// nothing, and the two were one token -- `no-result` -- for every one of the eight cards
// that died this way on 2026-09-16.

// TestFenceRejectionReadsTheHarnesssOwnWords: the line OpenCode 1.18.20 prints, colours and
// all, is the one this parses; a capture with no rejection in it says so.
func TestFenceRejectionReadsTheHarnesssOwnWords(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		in   string
		want string
		ok   bool
	}{
		{
			name: "the harness's own line",
			in:   "\x1b[33;1m!\x1b[0m  permission requested: external_directory (/Users/glenn/rowan-working/swarm-root/1/jobs/*); auto-rejecting\nError: The user rejected permission to use this specific tool call.\n",
			want: "/Users/glenn/rowan-working/swarm-root/1/jobs/*", ok: true,
		},
		{
			name: "the no-wall bench's /sys read",
			in:   "permission requested: external_directory (/sys/kernel/security/*); auto-rejecting\n",
			want: "/sys/kernel/security/*", ok: true,
		},
		{
			name: "several patterns: the first is where the model stopped",
			in:   "permission requested: external_directory (/a/*, /b/*); auto-rejecting\n",
			want: "/a/*", ok: true,
		},
		{name: "a quiet capture", in: "the model said things\nand wrote a result\n"},
		{name: "an empty capture"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := FenceRejection([]byte(tc.in))
			require.Equal(t, tc.ok, ok, "FenceRejection = %q,%v; want %q,%v", got, ok, tc.want, tc.ok)
			require.Equal(t, tc.want, got, "FenceRejection = %q,%v; want %q,%v", got, ok, tc.want, tc.ok)
		})
	}
}

// TestFencePermissionNamesTheWholeJobAndNothingAboveIt: the block a job runs under names the
// job directory in both wildcard spellings and the read-only paths the card asked for, still
// asks about everything else, and NAMES NOTHING ABOVE THE JOB -- a sibling job in the same
// slot is another card's work, and a fence rule is no place to hand it over.
func TestFencePermissionNamesTheWholeJobAndNothingAboveIt(t *testing.T) {
	t.Parallel()

	block := FencePermission("/root/1/jobs/a", []string{"/sys/kernel/security/lsm"})
	external, ok := block[FenceExternalDirectory].(map[string]any)
	require.True(t, ok, "the block is keyed by the permission the harness asks under: %v", block)
	for _, want := range []string{
		"/root/1/jobs/a/*", "/root/1/jobs/a/**",
		"/sys/kernel/security/lsm", "/sys/kernel/security/*",
	} {
		assert.Equal(t, FenceAllow, external[want], "%s is allowed; the block holds %v", want, external)
	}
	for _, never := range []string{"/root/1/jobs/*", "/root/1/jobs/**", "/root/1/*", "/root/*"} {
		_, named := external[never]
		assert.False(t, named, "%s is ABOVE the job and is never named: %v", never, external)
	}
	assert.Equal(t, FenceDeny, external["*"], "every other path is denied without prompting (a deny is a tool error the model routes around; an ask auto-rejects and ends the run): %v", external)
}

// TestFencePermissionDeniesExternalDirectory: the harness's own fence is DENY, never ASK. An
// `ask` in a non-interactive `run` is auto-rejected and the model stops -- the whole run ends
// and the card's commits are stranded. A `deny` is a tool error returned to the model, which
// notes it, works inside the job instead, and continues (issue #918).
func TestFencePermissionDeniesExternalDirectory(t *testing.T) {
	t.Parallel()

	block := FencePermission("/root/1/jobs/a", nil)
	external, ok := block[FenceExternalDirectory].(map[string]any)
	require.True(t, ok, "the block is keyed by the permission the harness asks under: %v", block)
	require.Equal(t, FenceDeny, external["*"], "a path outside the job is denied, never asked about: %v", external)
	assert.Equal(t, FenceDeny, block[FenceWebfetch], "webfetch is denied too, so no permission is left to prompt: %v", block)
}

// TestMergeFencePermissionKeepsTheCarriedConfig: a provider config a caller carried keeps
// its providers and its own rules, and the job's fence rules are added to them. A config
// this side cannot parse is returned unchanged, and says so.
func TestMergeFencePermissionKeepsTheCarriedConfig(t *testing.T) {
	t.Parallel()

	carried := []byte(`{"provider":{"ollama":{"options":{"baseURL":"http://localhost:11434/v1"}}},"permission":{"read":{"*":"allow"},"external_directory":{"/opt/toolchains/*":"allow"}}}`)
	out, ok := MergeFencePermission(carried, "/root/1/jobs/a", nil)
	require.True(t, ok, "a JSON object config merges: %s", out)
	var cfg map[string]any
	err := json.Unmarshal(out, &cfg)
	require.NoError(t, err, "the merged config is JSON: %v\n%s", err, out)
	_, named := cfg["provider"].(map[string]any)["ollama"]
	assert.True(t, named, "the carried provider survives the merge:\n%s", out)
	perm := cfg["permission"].(map[string]any)
	_, named = perm["read"]
	assert.True(t, named, "a person's own rules survive the merge:\n%s", out)
	external := perm[FenceExternalDirectory].(map[string]any)
	assert.Equal(t, FenceAllow, external["/opt/toolchains/*"], "a pattern the caller allowed is still allowed:\n%s", out)
	assert.Equal(t, FenceAllow, external["/root/1/jobs/a/**"], "the job's own directory is allowed:\n%s", out)

	if got, ok := MergeFencePermission([]byte("not json at all"), "/root/1/jobs/a", nil); ok || string(got) != "not json at all" {
		t.Errorf("a config this side cannot read is carried verbatim and says so, got %q,%v", got, ok)
	}
}

// TestCardReadPathsReadsTheCardsOwnLine: the `READ:` line, and nothing inferred from prose.
func TestCardReadPathsReadsTheCardsOwnLine(t *testing.T) {
	t.Parallel()

	card := []byte("do the thing\n" +
		"READ: /sys/kernel/security/lsm /proc/self/status\n" +
		"- READ: `/etc/os-release`\n" +
		"this card reads relative paths like repo/x too\n" +
		"READ: repo/x\n" +
		"READ: /sys/kernel/security/lsm\n")
	got := CardReadPaths(card)
	want := []string{"/sys/kernel/security/lsm", "/proc/self/status", "/etc/os-release"}
	require.Len(t, got, len(want), "CardReadPaths = %v, want %v", got, want)
	for i := range want {
		require.Equal(t, want[i], got[i], "CardReadPaths = %v, want %v", got, want)
	}
}

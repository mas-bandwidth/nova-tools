package friend

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The lanes' directories are allowed in the project config, so a headless
// opencode run never auto-rejects a tool call there (measured 2026-10-04: a
// path through the symlink in the home directory, external_directory): a
// new file names each path, an existing file keeps what it held, and a
// second call changes nothing.
func TestAllowDirsWritesTheFriendsPathsIntoTheProjectConfig(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	wrote, err := AllowDirs(dir, []string{"/w/bob", "/Users/x/bob-working/"})
	require.NoError(t, err)
	assert.True(t, wrote)
	var cfg map[string]any
	raw, err := os.ReadFile(filepath.Join(dir, OpenCodeConfig))
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(raw, &cfg))
	assert.Equal(t, map[string]any{"/w/bob/**": "allow", "/Users/x/bob-working/**": "allow"}, cfg["permission"].(map[string]any)["external_directory"])
	wrote, err = AllowDirs(dir, []string{"/w/bob"})
	require.NoError(t, err)
	assert.False(t, wrote, "nothing new: the file is left alone")

	other := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(other, OpenCodeConfig), []byte(`{"model":"inception/mercury-2.5","permission":{"bash":"allow","external_directory":{"/tmp/**":"deny"}}}`), 0o644))
	_, err = AllowDirs(other, []string{"/w/bob"})
	require.NoError(t, err)
	raw, err = os.ReadFile(filepath.Join(other, OpenCodeConfig))
	require.NoError(t, err)
	cfg = nil
	require.NoError(t, json.Unmarshal(raw, &cfg))
	assert.Equal(t, "inception/mercury-2.5", cfg["model"], "what the file held is kept")
	perm := cfg["permission"].(map[string]any)
	assert.Equal(t, "allow", perm["bash"])
	assert.Equal(t, map[string]any{"/tmp/**": "deny", "/w/bob/**": "allow"}, perm["external_directory"])

	all := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(all, OpenCodeConfig), []byte(`{"permission":{"external_directory":"allow"}}`), 0o644))
	wrote, err = AllowDirs(all, []string{"/w/bob"})
	require.NoError(t, err)
	assert.False(t, wrote, "every directory is allowed already")
}

// A lane's session is opened by a run with no --session, and is the session
// the listing gained; a card's turn names it, and a refused permission in its
// output is the turn's Rejected.
func TestOpenCodeOpensALaneSessionAndDeliversIntoIt(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	var calls [][]string
	lists := 0
	run := func(_ context.Context, d, name string, args []string, stdin string) (string, int, error) {
		calls = append(calls, append([]string{d, name}, args...))
		assert.Empty(t, stdin, "stdin is /dev/null")
		switch {
		case args[0] == "session":
			lists++
			if lists == 1 {
				return `[{"id":"old","directory":"` + dir + `","updated":5}]`, 0, nil
			}
			return `[{"id":"old","directory":"` + dir + `","updated":30},{"id":"new","directory":"` + dir + `","updated":20},{"id":"else","directory":"/w/ada","updated":40}]`, 0, nil
		case strings.Contains(strings.Join(args, " "), "card c1"):
			return "\x1b[91m**Blocked:** Permission to read `/Users/x/bob-working/jobs` was rejected\x1b[0m\nstopping\n", 0, nil
		}
		return "ready\n", 0, nil
	}
	o := &OpenCode{Dir: dir, Run: run, Allow: []string{"/Users/x/bob-working"}}
	id, err := o.OpenSession(context.Background(), "You are bob.")
	require.NoError(t, err)
	assert.Equal(t, "new", id, "the session the listing gained, not the newest")
	assert.Equal(t, []string{dir, "opencode", "run", "You are bob."}, calls[1], "a run with no --session opens one")
	raw, err := os.ReadFile(filepath.Join(dir, OpenCodeConfig))
	require.NoError(t, err)
	assert.Contains(t, string(raw), `"/Users/x/bob-working/**": "allow"`)
	assert.Contains(t, string(raw), `"`+dir+`/**": "allow"`)

	lt, err := o.DeliverTo(context.Background(), "new", "one card this turn, card c1")
	require.NoError(t, err)
	assert.Equal(t, []string{dir, "opencode", "run", "--session", "new", "one card this turn, card c1"}, calls[len(calls)-1])
	assert.Equal(t, "**Blocked:** Permission to read `/Users/x/bob-working/jobs` was rejected", lt.Rejected)
	lt, err = o.DeliverTo(context.Background(), "new", "another")
	require.NoError(t, err)
	assert.Empty(t, lt.Rejected)

	refusing := func(_ context.Context, _, _ string, args []string, _ string) (string, int, error) {
		if args[0] == "session" {
			return "[]", 0, nil
		}
		return freddyRefusal, 1, nil
	}
	_, err = (&OpenCode{Dir: dir, Run: refusing}).OpenSession(context.Background(), "seed")
	var refused ProviderRefused
	assert.ErrorAs(t, err, &refused, "a provider refusing the seed is said as such")
}

// A wall refusal happens before OpenCode can list or open a native session. Say
// its bounded reason and the missing-deny remedy without copying wall output,
// which can contain private paths, into the daemon's record.
func TestOpenCodeSessionOpenNamesWallRefusalWithoutEchoingOutput(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, output, want string
	}{
		{"missing deny", "WALL REFUSED reason=bad_profile the friend profile denies nothing: private-path /private/x\n", "nova-friend run --deny-self"},
		{"other wall refusal", "WALL REFUSED reason=bad_net private-path /private/x\n", "friend wall refused reason=bad_net"},
		{"not a wall refusal", "private-path /private/x\n", "opencode session list exited 125"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			o := &OpenCode{Dir: t.TempDir(), Run: func(_ context.Context, _, _ string, args []string, _ string) (string, int, error) {
				assert.Equal(t, []string{"session", "list", "--format", "json"}, args)
				return tc.output, 125, nil
			}}
			_, err := o.OpenSession(t.Context(), "seed")
			require.ErrorContains(t, err, tc.want)
			assert.NotContains(t, err.Error(), "/private/x")
		})
	}
}

// A read is one run in its own session with the model of its tier, no listing read.
func TestOpenCodeRunsAReadAsOneShotWithTheTiersModel(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	var calls [][]string
	run := func(_ context.Context, d, name string, args []string, _ string) (string, int, error) {
		calls = append(calls, append([]string{d, name}, args...))
		return "done\n", 0, nil
	}
	o := &OpenCode{Dir: dir, Run: run}
	lt, err := o.RunRead(context.Background(), "prov/m", "do the read")
	require.NoError(t, err)
	assert.Zero(t, lt.Exit)
	assert.Equal(t, [][]string{{dir, "opencode", "run", "--model", "prov/m", "do the read"}}, calls)
	_, err = o.RunRead(context.Background(), "", "again")
	require.NoError(t, err)
	assert.Equal(t, []string{dir, "opencode", "run", "again"}, calls[1])
}

// An OpenCode API friend's lanes are priced from opencode's own session record
// (docs/SPEC-FRIEND.md, the Claude lanes, the OpenCode lane): after each run
// `opencode export <session>` is read and the run's cost is what the session's
// assistant messages gained, one record line per run; a limit line with its
// reset beside it is UsageLimited until that reset, as a Claude lane's is.
func TestAnOpenCodeLanePricesEveryRunFromItsSessionRecordAndPausesAtItsLimit(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	costs := map[string][]string{} // each session's assistant messages' costs, as its record holds them
	lists, turn := 0, ""
	var exports []string
	run := func(_ context.Context, _, _ string, args []string, _ string) (string, int, error) {
		switch args[0] {
		case "session":
			lists++
			if lists == 1 {
				return "[]", 0, nil
			}
			return `[{"id":"ses_a","directory":"` + dir + `","updated":20}]`, 0, nil
		case "export":
			exports = append(exports, args[1])
			msgs := []string{`{"info":{"role":"user","cost":0},"parts":[]}`}
			for _, c := range costs[args[1]] {
				msgs = append(msgs, `{"info":{"role":"assistant","cost":`+c+`,"tokens":{"input":10,"output":2}},"parts":[]}`)
			}
			return "Exporting session: " + args[1] + "\n" + `{"info":{"id":"` + args[1] + `"},"messages":[` + strings.Join(msgs, ",") + "]}\n", 0, nil
		}
		costs["ses_a"] = append(costs["ses_a"], turn)
		if turn == "limit" {
			costs["ses_a"] = costs["ses_a"][:len(costs["ses_a"])-1]
			return "Error: Insufficient AI Credits. Your credits will refresh in 3 hours\n", 1, nil
		}
		return "done\n", 0, nil
	}
	var out strings.Builder
	p := &OpenCodePriced{OpenCode: &OpenCode{Dir: dir, Run: run, Out: &out}}
	var lanes LaneHarness = p
	turn = "0.0100"
	id, err := lanes.OpenSession(t.Context(), "You are freddy.")
	require.NoError(t, err)
	require.Equal(t, "ses_a", id)
	turn = "0.0250"
	_, err = lanes.DeliverTo(t.Context(), id, "card c1")
	require.NoError(t, err)
	assert.Equal(t, []string{"ses_a", "ses_a"}, exports, "every run is priced from its session's record")
	assert.InDelta(t, 0.035, p.Spent(), 1e-9)
	assert.Contains(t, out.String(), "opencode: session=ses_a cost=$0.0100 total=$0.0100\n")
	assert.Contains(t, out.String(), "opencode: session=ses_a cost=$0.0250 total=$0.0350\n", "a run's cost is what its session gained")

	turn = "limit"
	before := time.Now()
	_, err = lanes.DeliverTo(t.Context(), id, "card c2")
	var limited UsageLimited
	require.ErrorAs(t, err, &limited, "a limit with its reset is a pause until it, not out of funds")
	assert.Equal(t, "ses_a", limited.Session)
	assert.WithinDuration(t, before.Add(3*time.Hour), limited.Until, time.Minute)
	assert.Contains(t, out.String(), "opencode: session=ses_a cost=$0.0000 total=$0.0350\n", "a limited run is priced too")
	var spender Spender = p
	assert.Equal(t, "spend: harness=opencode runs=3 cost_usd=0.0350 limited_until="+limited.Until.UTC().Format(time.RFC3339), spender.SpendLine(), "the daemon's beat says what the lanes cost and the limit they stopped at")
	assert.Empty(t, (&OpenCodePriced{OpenCode: &OpenCode{Dir: dir, Run: run}}).SpendLine(), "no run, nothing to say")
}

package friend

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeClaude is a claude binary that records how it was called (its arguments,
// its CLAUDE_CONFIG_DIR, its stdin), counts its runs, and prints the stream-json
// of run n from runs (1-based; the last for any later run), doing the card's
// END step when the run says so: what `claude -p --output-format stream-json`
// prints, its rate_limit_event among the events (docs/SPEC-FRIEND.md, the
// headless Claude lane).
func fakeClaude(t *testing.T, dir string, runs []string, result string) (bin, calls string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the fake claude is a shell script")
	}
	calls = filepath.Join(dir, "calls")
	require.NoError(t, os.MkdirAll(calls, 0o755))
	var b strings.Builder
	b.WriteString("#!/bin/sh\n")
	b.WriteString("n=$(ls " + calls + " | grep -c args)\nn=$((n+1))\n")
	b.WriteString(`for a in "$@"; do printf '%s\n' "$a"; done > ` + calls + "/args.$n\n")
	b.WriteString(`printf '%s\n' "$CLAUDE_CONFIG_DIR" > ` + calls + "/config.$n\n")
	b.WriteString("cat > " + calls + "/stdin.$n\n")
	for i, out := range runs {
		cond := "[ $n -eq " + strconv.Itoa(i+1) + " ]"
		if i == len(runs)-1 {
			cond = "[ $n -ge " + strconv.Itoa(i+1) + " ]"
		}
		b.WriteString("if " + cond + "; then\ncat <<'EOF'\n" + out + "\nEOF\n")
		if i == 0 && result != "" {
			b.WriteString("mkdir -p " + filepath.Dir(result) + " && echo 'RESULT: done' > " + result + "\n")
		}
		if strings.Contains(out, `"is_error":true`) {
			b.WriteString("exit 1\n")
		}
		b.WriteString("exit 0\nfi\n")
	}
	bin = filepath.Join(dir, "bin", "claude")
	require.NoError(t, os.MkdirAll(filepath.Dir(bin), 0o755))
	require.NoError(t, os.WriteFile(bin, []byte(b.String()), 0o755))
	return bin, calls
}

// One card on a headless Claude Code account: the lane's session is opened by
// no run at all (each card is its own `claude -p`), the card's turn is one run
// with the trimmed call (--strict-mcp-config --disable-slash-commands
// --no-chrome --tools Bash Read Write Edit Grep Glob), on the account's config
// directory, the model the card's tier names, the prompt on stdin; the run is
// priced from its result event and its limits read from its rate_limit_event,
// both said on the record and kept in the state directory. A run that meets
// the limit (rate_limit_event status rejected) says so to OnLimit with the
// limit's own resetsAt, and no run starts again until then.
func TestAHeadlessClaudeLaneRunsACardPricesItAndReadsItsLimit(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	dir, config, state := filepath.Join(root, "bob"), filepath.Join(root, "config"), filepath.Join(root, "state")
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "inbox", "c1~15"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte("# bob\n"), 0o644))
	brief := filepath.Join(dir, "inbox", "c1~15", "BRIEF.md")
	require.NoError(t, os.WriteFile(brief, []byte("STATUS: card c1\nRESULT: c1 tier: pro\n"), 0o644))
	card := Card{ID: "c1", Brief: brief, Outbox: filepath.Join(dir, "outbox", "c1~15")}
	now := time.Date(2026, 10, 4, 18, 0, 0, 0, time.UTC)
	fiveHour := now.Add(2 * time.Hour).Unix()
	weekly := now.Add(72 * time.Hour).Unix()

	allowed := strings.Join([]string{
		`{"type":"system","subtype":"init","session_id":"s1","model":"claude-sonnet-5-5","tools":["Bash","Read","Write","Edit","Grep","Glob"]}`,
		`{"type":"assistant","message":{"content":[{"type":"text","text":"working"}]},"session_id":"s1"}`,
		`{"type":"rate_limit_event","rate_limit_info":{"status":"allowed","resetsAt":` + i64(fiveHour) + `,"rateLimitType":"five_hour","utilization":0.42},"session_id":"s1"}`,
		`{"type":"rate_limit_event","rate_limit_info":{"status":"allowed_warning","resetsAt":` + i64(weekly) + `,"rateLimitType":"seven_day","utilization":0.81},"session_id":"s1"}`,
		`{"type":"result","subtype":"success","is_error":false,"num_turns":7,"result":"done","session_id":"s1","total_cost_usd":0.1234,"usage":{"input_tokens":12,"cache_creation_input_tokens":3400,"cache_read_input_tokens":12700,"output_tokens":800}}`,
	}, "\n")
	rejected := strings.Join([]string{
		`{"type":"system","subtype":"init","session_id":"s2","model":"claude-sonnet-5-5"}`,
		`{"type":"rate_limit_event","rate_limit_info":{"status":"rejected","resetsAt":` + i64(fiveHour) + `,"rateLimitType":"five_hour","utilization":1},"session_id":"s2"}`,
		`{"type":"result","subtype":"success","is_error":true,"num_turns":1,"result":"Claude AI usage limit reached","session_id":"s2","total_cost_usd":0,"usage":{"input_tokens":0,"output_tokens":0}}`,
	}, "\n")
	bin, calls := fakeClaude(t, root, []string{allowed, rejected}, card.Result())

	var out strings.Builder
	var limits []ClaudeLimit
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c := &Claude{Dir: dir, ConfigDir: config, Program: bin, StateDir: state, Run: RealExec, Out: &out, Now: func() time.Time { return now },
		OnLimit: func(l ClaudeLimit) { limits = append(limits, l); cancel() }}
	var lh LaneHarness = c // a headless Claude account has lanes

	id, err := lh.OpenSession(ctx, LaneSeed("bob", 1, 2, filepath.Join(dir, "AGENTS.md"), ""))
	require.NoError(t, err)
	assert.NotEmpty(t, id)
	assert.NoFileExists(t, filepath.Join(calls, "args.1"), "opening a lane runs nothing: each card is its own run")

	lt, err := lh.DeliverTo(ctx, id, CardText(card, 1, 2, "nova-bus send --as bob --to ada", "", "", nil))
	require.NoError(t, err)
	assert.Equal(t, 0, lt.Exit)
	assert.FileExists(t, card.Result(), "the run did the card")
	args, err := os.ReadFile(filepath.Join(calls, "args.1"))
	require.NoError(t, err)
	assert.Equal(t, strings.Join([]string{"-p", "--output-format", "stream-json", "--verbose", "--model", "claude-sonnet-5-5",
		"--permission-mode", "bypassPermissions", "--strict-mcp-config", "--disable-slash-commands", "--no-chrome",
		"--tools", "Bash", "Read", "Write", "Edit", "Grep", "Glob"}, "\n")+"\n", string(args), "the trimmed call, the model the tier names")
	cfg, err := os.ReadFile(filepath.Join(calls, "config.1"))
	require.NoError(t, err)
	assert.Equal(t, config+"\n", string(cfg), "the account is the config directory")
	stdin, err := os.ReadFile(filepath.Join(calls, "stdin.1"))
	require.NoError(t, err)
	assert.Contains(t, string(stdin), "You are bob", "the lane's seed heads the card")
	assert.Contains(t, string(stdin), "Its brief is "+brief)
	assert.NotContains(t, string(stdin), "Answer this turn with the one word", "a one-shot run is no seed turn")

	run := c.Last()
	assert.Equal(t, "0.1234", run.CostUSD, "priced from the result event")
	assert.Equal(t, 7, run.Turns)
	assert.Equal(t, []ClaudeLimit{
		{Type: "five_hour", Status: "allowed", Utilization: "0.42", ResetsAt: time.Unix(fiveHour, 0).UTC()},
		{Type: "seven_day", Status: "allowed_warning", Utilization: "0.81", ResetsAt: time.Unix(weekly, 0).UTC()},
	}, run.Limits, "both windows, as the rate_limit_event said them")
	assert.Contains(t, out.String(), "CLAUDE RUN session="+id+" card=c1 model=claude-sonnet-5-5 exit=0 cost_usd=0.1234 turns=7 tokens_in=12 tokens_out=800 cache_write=3400 cache_read=12700 limit=five_hour:allowed:0.42:2026-10-04T20:00:00Z,seven_day:allowed_warning:0.81:2026-10-07T18:00:00Z")
	_, limited := c.Limited(now)
	assert.False(t, limited)
	kept, found, err := ReadClaudeLimits(state)
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, "0.1234", kept.CostUSD, "the account's spend so far")
	assert.Equal(t, 1, kept.Runs)
	assert.Equal(t, run.Limits, kept.Limits)

	// the limit: the run says so, OnLimit hears it with the limit's own reset, and no run starts until then
	lt, err = lh.DeliverTo(ctx, id, CardText(card, 1, 2, "nova-bus send --as bob --to ada", "", "", nil))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "five_hour limit until 2026-10-04T20:00:00Z")
	require.Len(t, limits, 1)
	assert.Equal(t, ClaudeLimit{Type: "five_hour", Status: "rejected", Utilization: "1", ResetsAt: time.Unix(fiveHour, 0).UTC()}, limits[0])
	l, limited := c.Limited(now.Add(time.Hour))
	assert.True(t, limited)
	assert.Equal(t, time.Unix(fiveHour, 0).UTC(), l.ResetsAt)
	_, limited = c.Limited(time.Unix(fiveHour, 0))
	assert.False(t, limited, "the limit ends at its reset")
	_, err = lh.DeliverTo(context.Background(), id, "another card")
	require.Error(t, err)
	assert.NoFileExists(t, filepath.Join(calls, "args.3"), "at the limit nothing runs")
	assert.Contains(t, out.String(), "CLAUDE LIMIT five_hour rejected until 2026-10-04T20:00:00Z")
	kept, _, err = ReadClaudeLimits(state)
	require.NoError(t, err)
	assert.Equal(t, 2, kept.Runs)
	assert.Equal(t, "2026-10-04T20:00:00Z", kept.Until.Format(time.RFC3339))
}

func i64(n int64) string { return strconv.FormatInt(n, 10) }

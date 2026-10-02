package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/bus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A cold reader gets from nothing to a first send with the binary alone: the banner and
// `send -h` state the roster file, what a lane is, and one example roster, and the example
// the banner prints is a roster the tool loads.
func TestBannerStatesTheRosterAndLanes(t *testing.T) {
	t.Parallel()
	for name, text := range map[string]string{
		"help":    invoke(t, "", "help").stdout,
		"send -h": invoke(t, "", "send", "-h").stdout,
	} {
		for _, want := range []string{"participants.json", `"lane":"from-ada"`, "git_name", "git_email", "no lane"} {
			assert.Containsf(t, text, want, "%s does not say %q", name, want)
		}
	}
	help := invoke(t, "", "help").stdout
	for _, want := range []string{"first send, from nothing", "git init -q -b main bus", "nova-bus send --bus bus --file d.md --as Ada --remote origin --branch main --no-push", "`nova-bus send -h` has"} {
		assert.Containsf(t, help, want, "the banner's first send does not say %q", want)
	}
	send := invoke(t, "", "send", "-h").stdout
	for _, want := range []string{"ROSTER AND LANES", "a lane", `"groups"`} {
		assert.Containsf(t, send, want, "send -h's roster paragraph does not say %q", want)
	}
}

// Every roster the banner and send -h print is a roster the tool loads, with Ada able to send.
func TestTheRostersTheBannerPrintsLoad(t *testing.T) {
	t.Parallel()
	lines := strings.Split(invoke(t, "", "help").stdout+invoke(t, "", "send", "-h").stdout, "\n")
	var rosters []string
	for i := 0; i < len(lines); i++ {
		text := strings.TrimSpace(lines[i])
		at := strings.Index(text, `{"participants":[`)
		if at < 0 {
			continue
		}
		// a roster is its first line through the line that closes the object; the first
		// send's is the argument of a printf
		cur := strings.TrimSuffix(text[at:], "' > bus/participants.json")
		for !strings.HasSuffix(cur, "}") || strings.Count(cur, "{") != strings.Count(cur, "}") {
			i++
			require.Falsef(t, i >= len(lines), "a roster in the banner never closes: %q", cur)
			cur += strings.TrimSpace(lines[i])
		}
		rosters = append(rosters, cur)
	}
	require.Falsef(t, len(rosters) < 3, "the banner and send -h print %d rosters, want the first send's, send -h's synopsis and its paragraph", len(rosters))
	for _, roster := range rosters {
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, bus.ConfigName), []byte(roster), 0o600))
		cfg, err := bus.LoadConfig(dir)
		if err != nil {
			assert.Failf(t, "assertion failed", "the banner's roster does not load: %v\n%s", err, roster)
			continue
		}
		ada, ok := cfg.Lookup("Ada")
		assert.Falsef(t, !ok || ada.Lane != "from-ada", "the banner's roster gives Ada no lane: %+v", ada)
	}
}

// A first run with nothing given names every problem it has: the missing flags and the
// receipt word count, whose refusal names the file and the variable that supply it.
func TestInboxAndWaitNameEveryMissingThingInOneRun(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		verb string
		want []string
	}{
		{"inbox", []string{"--as is required", "--bus is required", "--receipt-max-words must be given"}},
		{"wait", []string{"--as is required", "--branch is required", "--bus is required", "--remote is required", "--receipt-max-words must be given"}},
	} {
		r := invoke(t, "", c.verb).mustCode(t, 2)
		for _, w := range c.want {
			assert.Containsf(t, r.stderr, w, "%s with nothing: stderr has no %q:\n%s", c.verb, w, r.stderr)
		}
		assert.Falsef(t, !strings.Contains(r.stderr, "<bus>/.nova-bus/defaults") || !strings.Contains(r.stderr, "NOVA_BUS_RECEIPT_MAX_WORDS"), "%s: the refusal does not name the file and the variable that supply the count:\n%s", c.verb, r.stderr)
	}
	// the flag given: the count is no longer a problem, the rest still are
	r := invoke(t, "", "inbox", "--receipt-max-words", "40").mustCode(t, 2)
	assert.Falsef(t, strings.Contains(r.stderr, "receipt-max-words") || !strings.Contains(r.stderr, "--as is required"), "inbox with the count given: %s", r.stderr)
}

// The banner says where the receipt word count comes from, and what it names is real: the
// file line is read from <bus>/.nova-bus/defaults, so a bus that carries it needs no flag.
func TestReceiptWordCountSourcesAreTheOnesTheBannerNames(t *testing.T) {
	t.Parallel()
	help := invoke(t, "", "help").stdout
	assert.NotContains(t, help, "no default receipt word count", "the banner says there is no default receipt word count while two sources supply one")
	for _, want := range []string{"receipt-max-words=<n>", "<bus>/.nova-bus/defaults", "NOVA_BUS_RECEIPT_MAX_WORDS"} {
		assert.Containsf(t, help, want, "the banner does not name %q", want)
	}
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".nova-bus"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".nova-bus", "defaults"), []byte("# defaults\nreceipt-max-words=25\n"), 0o600))
	r := invoke(t, "", "inbox", "--bus", dir, "--as", "Ada")
	assert.NotContainsf(t, r.stderr, "receipt-max-words must be given", "a bus whose defaults file carries the count is still asked for the flag:\n%s", r.stderr)
}

// One reader serves both keys of <bus>/.nova-bus/defaults: the first line naming a key is
// read, comments and blank lines are skipped, and a missing file or key is absent.
func TestTheDefaultsFileIsReadOnce(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".nova-bus"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".nova-bus", "defaults"), []byte("# defaults\n\nhost = air\nreceipt-max-words=25\nreceipt-max-words=99\n"), 0o600))
	for _, tc := range []struct {
		dir, key, want string
		ok             bool
	}{
		{dir, "host", "air", true},
		{dir, "receipt-max-words", "25", true},
		{dir, "nothing", "", false},
		{t.TempDir(), "host", "", false},
		{"", "host", "", false},
	} {
		got, ok := fromDefaults(tc.dir, tc.key)
		assert.Equal(t, tc.want, got, tc.key)
		assert.Equal(t, tc.ok, ok, tc.key)
	}
	n, ok := receiptMaxWordsFromDefaults(dir)
	assert.True(t, ok)
	assert.Equal(t, 25, n)
	assert.Equal(t, "air", hostFromDefaults(dir))
}

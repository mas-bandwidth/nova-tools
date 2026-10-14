package main

import (
	"bytes"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stuckPSFixture stands for ps: a nova-sandbox wrapper (5207) and its walled
// child (5210), the bench host case; a child too young (6000) and one too busy
// (6001) that the bounds must drop; and a process outside every wall (9999).
const stuckPSFixture = ` 5207     1 01-22:27:51 10:39.62 SN /Users/glenn/.local/bin/nova-sandbox --write /private/tmp/m5repro-v/data --write /private/tmp/m5repro-v/tmp --tmp /private/tmp/m5repro-v/tmp --cwd /private/tmp/m5repro-v/data --read /Users/glenn/nova-bench/harness-v1.18.20 -- /Users/glenn/nova-bench/harness-v1.18.20/opencode --version
 5210  5207 01-22:27:51  0:00.01 SN /Users/glenn/nova-bench/harness-v1.18.20/opencode.bin --version
 6000  5207 00:00:10  0:00.01 S  /Users/glenn/nova-bench/harness-v1.18.20/opencode.bin --too-young
 6001  5207 01-22:27:51  2:00.00 S  /Users/glenn/nova-bench/harness-v1.18.20/opencode.bin --too-busy
 9999     1 00:10:00  0:00.01 S  /usr/bin/other --outside
`

// stuckReadFixture reads one recorded fixture under testdata/stuck.
func stuckReadFixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile("testdata/stuck/" + name)
	require.NoError(t, err, "testdata/stuck/%s does not read", name)
	return string(b)
}

// TestStuckNamesTheDeniedMachLookupsOfAWalledLaunch pins the stuck verb on
// recorded text and runs no process: the same readers the verb uses are stepped
// with a ps fixture, /usr/bin/sample's text and the unified log's sandbox lines.
func TestStuckNamesTheDeniedMachLookupsOfAWalledLaunch(t *testing.T) {
	t.Parallel()

	sample := stuckReadFixture(t, "sample.txt")
	logText := stuckReadFixture(t, "sandbox-log.txt")

	t.Run("the denied mach-lookups are grouped by name and process with their counts", func(t *testing.T) {
		t.Parallel()
		got := parseMachDenials(logText)
		want := []machDenial{
			{Name: "com.apple.apsd", Process: "parsecd", Count: 1},
			{Name: "com.apple.logd", Process: "opencode.bin", Count: 6},
			{Name: "com.apple.system.logger", Process: "opencode.bin", Count: 1},
			{Name: "com.apple.system.notification_center", Process: "opencode", Count: 1},
			{Name: "com.apple.system.notification_center", Process: "opencode.bin", Count: 2},
		}
		require.Equal(t, want, got, "the grouped denials are %+v, want %+v", got, want)

		res := stuckBuild(stuckOpts{MinAge: stuckMinAge, MaxCPU: stuckMaxCPU},
			"darwin", stuckPSFixture, func(int) string { return sample }, logText, builtinProfile(), "26.0")
		var b bytes.Buffer
		renderStuck(&b, res)
		out := b.String()
		assert.Contains(t, out, "DENIED mach-lookup name=com.apple.logd process=opencode.bin count=6 not-allowed-by-profile", "the logd denial line is missing or unmarked:\n%s", out)
		assert.Contains(t, out, "DENIED mach-lookup name=com.apple.apsd process=parsecd count=1 not-allowed-by-profile", "the apsd denial line is missing:\n%s", out)
	})

	t.Run("a name the profile allows is not marked, one absent from it is", func(t *testing.T) {
		t.Parallel()
		allowed := profileMachNames(builtinProfile())
		assert.True(t, allowed["com.apple.system.logger"], "the profile's own mach-lookup list is missing com.apple.system.logger: %v", allowed)
		assert.False(t, allowed["com.apple.logd"], "com.apple.logd is not in the profile's mach-lookup list but was read as one")
		res := stuckBuild(stuckOpts{MinAge: stuckMinAge, MaxCPU: stuckMaxCPU},
			"darwin", stuckPSFixture, func(int) string { return sample }, logText, builtinProfile(), "26.0")
		var b bytes.Buffer
		renderStuck(&b, res)
		for _, line := range strings.Split(b.String(), "\n") {
			if strings.Contains(line, "name=com.apple.system.logger") {
				assert.NotContains(t, line, "not-allowed-by-profile", "the system.logger name is in the profile but was marked: %s", line)
			}
			if strings.Contains(line, "name=com.apple.logd") {
				assert.Contains(t, line, "not-allowed-by-profile", "the logd name is absent from the profile but was not marked: %s", line)
			}
		}
	})

	t.Run("the verdict names the denied operation, none, and no match", func(t *testing.T) {
		t.Parallel()
		denied := stuckBuild(stuckOpts{MinAge: stuckMinAge, MaxCPU: stuckMaxCPU},
			"darwin", stuckPSFixture, func(int) string { return sample }, logText, builtinProfile(), "26.0")
		var b bytes.Buffer
		renderStuck(&b, denied)
		assert.Contains(t, b.String(), "STUCK LAUNCH pid=5210 state=SN cpu=0:00.01 age=1-22:27:51 top=mach_msg2_trap denied=mach-lookup:com.apple.logd x6", "the denied verdict line is wrong:\n%s", b.String())

		none := stuckBuild(stuckOpts{MinAge: stuckMinAge, MaxCPU: stuckMaxCPU},
			"darwin", stuckPSFixture, func(int) string { return sample }, "", builtinProfile(), "26.0")
		b.Reset()
		renderStuck(&b, none)
		assert.Contains(t, b.String(), "denied=none", "a window with no denial does not say denied=none:\n%s", b.String())

		noMatch := stuckBuild(stuckOpts{PID: 4242, MinAge: stuckMinAge, MaxCPU: stuckMaxCPU},
			"darwin", stuckPSFixture, func(int) string { return sample }, logText, builtinProfile(), "26.0")
		b.Reset()
		renderStuck(&b, noMatch)
		assert.Contains(t, b.String(), "STUCK NONE", "a ps fixture with no match does not say STUCK NONE:\n%s", b.String())
	})

	t.Run("a process younger than min-age or busier than max-cpu is not selected", func(t *testing.T) {
		t.Parallel()
		rows := parsePS(stuckPSFixture)
		got := selectStuck(rows, stuckOpts{MinAge: stuckMinAge, MaxCPU: stuckMaxCPU})
		require.Len(t, got, 1, "the scan selected %d processes, want only the stuck one: %+v", len(got), got)
		assert.Equal(t, 5210, got[0].PID, "the scan selected pid %d, want the walled child 5210", got[0].PID)

		byPid := selectStuck(rows, stuckOpts{PID: 6000, MinAge: stuckMinAge, MaxCPU: stuckMaxCPU})
		assert.Empty(t, byPid, "a process younger than --min-age was selected by --pid: %+v", byPid)
		busy := selectStuck(rows, stuckOpts{PID: 6001, MinAge: stuckMinAge, MaxCPU: stuckMaxCPU})
		assert.Empty(t, busy, "a process over --max-cpu was selected by --pid: %+v", busy)
		assert.Equal(t, time.Second, stuckMaxCPU, "the default --max-cpu is not one second")
	})

	t.Run("a non-darwin goos says unsupported and guesses nothing", func(t *testing.T) {
		t.Parallel()
		res := stuckBuild(stuckOpts{}, "linux", stuckPSFixture, func(int) string { return sample }, logText, builtinProfile(), "")
		require.True(t, res.Unsupported, "a linux goos was not marked unsupported")
		var b bytes.Buffer
		renderStuck(&b, res)
		assert.Equal(t, "STUCK UNSUPPORTED platform=linux: sample and the unified log are darwin's\n", b.String(), "the unsupported line is wrong")
	})

	t.Run("the verb is in the help table with a firstrun example", func(t *testing.T) {
		t.Parallel()
		doc, ok := verbDocs["stuck"]
		require.True(t, ok, "stuck is not in the help table (verbDocs)") // verbDocs row
		assert.NotEmpty(t, doc.exits, "stuck's help table row has no exit line")
		example, ok := verbExample["stuck"]
		require.True(t, ok, "stuck has no firstrun example (verbExample)")
		assert.True(t, strings.HasPrefix(example, "nova-sandbox stuck "), "stuck's example is not a runnable stuck line: %q", example)
		assert.True(t, helpVerbs["stuck"], "stuck is not a verb `help <verb>` answers for")
		assert.Contains(t, usage, "nova-sandbox stuck", "the banner's usage does not name the stuck verb")
	})
}

package update

import (
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/pkg/testbin"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// This fake is nova-bus send on the Redis bus as the caller sees it: one child, the note on
// stdin, SEND OK id= on stdout. testCleanup, when set, runs once every test has (the
// functional tier's tree binaries).
var testCleanup func()
var fakeBusHang func()

func TestMain(m *testing.M) {
	if os.Getenv("NOVA_UPDATE_FAKE_BUS") == "1" && strings.HasPrefix(filepath.Base(os.Args[0]), "nova-bus") {
		fakeBus()
		return
	}
	// the runtime starts its signal goroutine on the first signal.Notify of the process; a
	// verb under test (tool.RunContext) notifies, and when the first one ran inside a
	// synctest bubble the goroutine joined that bubble and the next signal use outside it
	// was a fatal error (2026-10-05, CI shard 4/4; TestSnapshotIsBoundedByTheClock first
	// under -shuffle). Started here, outside every bubble, once.
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, os.Interrupt)
	signal.Stop(sigs)
	// A race-built helper child otherwise sleeps at exit; every helper this
	// binary spawns inherits the setting.
	if os.Getenv("GORACE") == "" {
		os.Setenv("GORACE", "atexit_sleep_ms=0") // ignored: a tuning for race-built helpers; when it does not take, they keep the default exit sleep
	}
	code := m.Run()
	if testCleanup != nil {
		testCleanup()
	}
	os.Exit(code)
}
func fakeBus() {
	input, _ := io.ReadAll(os.Stdin)
	verb := os.Args[1]
	log, _ := os.OpenFile(os.Getenv("NOVA_UPDATE_BUS_CALLS"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	_, _ = fmt.Fprintln(log, verb) // ignored: test fixture log file
	// The whole argv too, on its own line, so a test can assert what bounds the
	// reporter handed the bus rather than trusting that it handed any.
	_, _ = fmt.Fprintln(log, "argv "+strings.Join(os.Args[1:], " ")) // ignored: test fixture log file
	_ = log.Close()                                                  // ignored: test fixture log file
	mode := os.Getenv("NOVA_UPDATE_BUS_MODE")
	if verb != "send" {
		fmt.Fprintf(os.Stderr, "BUS REFUSED: unknown verb %q\n", verb)
		os.Exit(2)
	}
	if mode == "uncertain" || mode == "send-fail" {
		fmt.Fprintln(os.Stderr, "SEND FAIL synthetic refusal")
		os.Exit(1)
	}
	if mode == "send-alien" {
		fmt.Fprintln(os.Stderr, "gobbledegook tell-nobody-this")
		os.Exit(1)
	}
	if mode == "send-shouty" {
		fmt.Fprintf(os.Stderr, "SEND FAIL %s\nand a second line\nand a third\n", strings.Repeat("y", 4000))
		os.Exit(1)
	}
	if mode == "hang" {
		if fakeBusHang == nil {
			os.Exit(20)
		}
		fakeBusHang()
	}
	note := string(input)
	if !strings.HasSuffix(note, "\n") {
		note += "\n"
	}
	fmt.Printf("SEND OK id=%s to=x cc=- at=2026-10-04T17:00:00Z\n", "fixture-"+shaText(note)[:12])
	os.Exit(0)
}

// busRig is a nova-bus stand-in placed in its own directory. A test hands the
// bus to the code under test through the Environment its with method builds,
// so the stand-in is found on that child's PATH and no process-wide variable
// is touched.
type busRig struct{ dir, log string }

func fakeBusPath(t *testing.T) busRig {
	t.Helper()
	dir := t.TempDir()
	name := "nova-bus"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	raw, e := os.Executable()
	if e != nil {
		require.NoError(t, e, e)
	}
	if e = testbin.Place(raw, filepath.Join(dir, name)); e != nil {
		require.NoError(t, e, e)
	}
	return busRig{dir: dir, log: filepath.Join(dir, "calls")}
}

// with returns env whose children find the stand-in first on PATH and run it
// as the fake bus, with any further KEY=value pairs on top.
func (b busRig) with(env Environment, kv ...string) Environment {
	env.Env = append(os.Environ(), "PATH="+b.dir+string(os.PathListSeparator)+os.Getenv("PATH"),
		"NOVA_UPDATE_FAKE_BUS=1", "GORACE=atexit_sleep_ms=0", "NOVA_UPDATE_BUS_CALLS="+b.log)
	env.Env = append(env.Env, kv...)
	return env
}
func calls(t *testing.T, p string) int {
	t.Helper()
	b, e := os.ReadFile(p)
	if e != nil {
		require.NoError(t, e, e)
	}
	s := string(b)
	return strings.Count(s, "send\n")
}

// A send the bus did not confirm records no delivery, so the next --send sends again, the
// newer observation included, and a confirmed one records the id it was given.
func TestAnUnconfirmedSendRecordsNothingAndTheNextSendSends(t *testing.T) {
	t.Parallel()
	rig := fakeBusPath(t)
	log := rig.log
	p := manifest(t, row("x", "tool", printer(t, "v1.0.0"), "npm:unused", "none"))
	sp := filepath.Join(t.TempDir(), "s.json")
	args := []string{"report", "--file", p, "--send", "--snapshot", sp, "--as", "fixture", "--to", "integrator"}
	run(t, rig.with(Environment{}, "NOVA_UPDATE_BUS_MODE=uncertain"), args...)
	s, _ := readSnapshot(sp)
	require.Empty(t, s.Delivered)
	_ = os.WriteFile(p, []byte(Header+"\n"+row("x", "tool", printer(t, "v2.0.0"), "npm:unused", "none")+"\n"), 0600) // ignored: test setup
	if c, _, _ := run(t, rig.with(Environment{}, "NOVA_UPDATE_BUS_MODE=uncertain"), args...); c != 1 {
		require.EqualValues(t, 1, c, c)
	}
	require.Equal(t, 2, calls(t, log))
	c, o, e := run(t, rig.with(Environment{}, "NOVA_UPDATE_BUS_MODE=ok"), args...)
	if c != 0 {
		require.EqualValuesf(t, 0, c, "%d %s %s", c, o, e)
	}
	require.Equal(t, 3, calls(t, log))
	s, _ = readSnapshot(sp)
	require.Len(t, s.Delivered, 1)
	for _, v := range s.Delivered {
		require.Equal(t, "v2.0.0", v.Observed["x"].Raw)
		require.NotEmpty(t, v.ID)
		require.Contains(t, o, v.ID)
	}
}
func TestDeliveryScopeIsTheSenderTheRecipientsAndTheBench(t *testing.T) {
	t.Parallel()

	o := options{as: "a", to: "c,b", host: "studio"}
	same := o
	same.to = "b,c"
	if snapshotScope(o) != snapshotScope(same) {
		require.Fail(t, fmt.Sprintln("recipient order changed scope"))
	}
	other := o
	other.host = "air"
	if snapshotScope(o) == snapshotScope(other) {
		require.Fail(t, fmt.Sprintln("bench silently shared delivery state"))
	}
}

// A second value for one field of a snapshot is an ambiguous identity. The
// reader must refuse it, and must do so without quoting the offending key or
// any of its content back into the diagnostic.
func TestStrictDecodingRefusesAmbiguousAndWrongInput(t *testing.T) {
	t.Parallel()

	secret := "tell-nobody"
	dir := t.TempDir()
	dup := filepath.Join(dir, "dup.json")
	if e := os.WriteFile(dup, []byte(`{"observed":{},"observed":{"x":{"raw":"`+secret+`","status":"tool","at":"t"}},"delivered":{}}`), 0600); e != nil {
		require.NoError(t, e, e)
	}
	s, err := readSnapshot(dup)
	if err == nil {
		require.Errorf(t, err, "ambiguous snapshot accepted: %v", s)
	}
	if strings.Contains(err.Error(), secret) {
		require.Failf(t, "", "snapshot diagnostic echoed content: %v", err)
	}
	if b, _ := os.ReadFile(dup); !strings.Contains(string(b), secret) {
		require.Fail(t, fmt.Sprintln("refusal did not preserve the snapshot byte for byte"))
	}
}

// A failed delivery used to say "exit 1" and nothing else. The bus's own first
// line now reaches the caller's diagnostic -- one line, clipped, and no binary
// on PATH can turn one event into two.
func TestTheBusOwnWordsReachTheCallerBoundedToOneLine(t *testing.T) {
	t.Parallel()
	if got := busSaid(ProcessResult{Stderr: "SEND FAIL one\nSEND FAIL two\n"}); got != "SEND FAIL one" {
		require.EqualValuesf(t, "SEND FAIL one", got, "%q", got)
	}
	// Stdout is the fallback when the bus said nothing on stderr: a SEND OK for
	// some other id is the bus's grammar and worth relaying.
	if got := busSaid(ProcessResult{Stdout: "SEND OK id=other\nmore"}); got != "SEND OK id=other" {
		require.EqualValuesf(t, "SEND OK id=other", got, "%q", got)
	}
	if got := busSaid(ProcessResult{}); got != "nothing" {
		require.EqualValuesf(t, "nothing", got, "%q", got)
	}
	if got := busSaid(ProcessResult{Stderr: "SEND FAIL " + strings.Repeat("x", 500)}); len(got) != 203 || !strings.HasSuffix(got, "...") {
		require.Failf(t, "", "unbounded: %d bytes: %q", len(got), got)
	}
	for _, mode := range []string{"send-fail", "send-shouty"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			rig := fakeBusPath(t)
			p := manifest(t, row("x", "tool", printer(t, "v1.2.3"), "npm:unused", "none"))
			c, _, errout := run(t, rig.with(Environment{}, "NOVA_UPDATE_BUS_MODE="+mode), "report", "--file", p, "--send", "--snapshot",
				filepath.Join(t.TempDir(), "s.json"), "--as", "fixture", "--to", "integrator")
			if c != 1 {
				require.EqualValues(t, 1, c, c)
			}
			need(t, errout, "the bus said: SEND FAIL")
			note := ""
			for _, line := range strings.Split(errout, "\n") {
				if strings.HasPrefix(line, "REPORT NOTE") {
					if note != "" {
						require.EqualValuesf(t, "", note, "one refusal became two lines:\n%s", errout)
					}
					note = line
				}
			}
			if note == "" {
				require.NotEqualValuesf(t, "", note, "no REPORT NOTE line:\n%s", errout)
			}
			if len(note) > 600 {
				require.LessOrEqualf(t, len(note), 600, "the refusal line is %d bytes, which is not bounded: %s", len(note), note)
			}
		})
	}
}

// Go's decoder matches a struct field case-insensitively, so refusing only
// BYTE-identical duplicate keys left the ambiguity it was written to close: two
// keys that fold to one field still carried two values, and the LAST won. The
// witness the cold read measured is the first case below.
func TestStrictDecodingRefusesKeysThatFoldTogether(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	for name, body := range map[string]string{
		"observed and Observed":   `{"observed":{"x":{"raw":"one","status":"tool","at":"t"}},"Observed":{"x":{"raw":"tell-nobody","status":"tool","at":"t"}},"delivered":{}}`,
		"nested raw and Raw":      `{"observed":{"x":{"raw":"one","Raw":"tell-nobody","status":"tool","at":"t"}},"delivered":{}}`,
		"delivered and DELIVERED": `{"observed":{},"delivered":{},"DELIVERED":{}}`,
	} {
		p := filepath.Join(dir, strings.ReplaceAll(name, " ", "-")+".json")
		if err := os.WriteFile(p, []byte(body), 0600); err != nil {
			require.NoError(t, err, err)
		}
		s, err := readSnapshot(p)
		if err == nil {
			assert.Errorf(t, err, "%s accepted: %+v", name, s)
		}
		if err != nil && strings.Contains(err.Error(), "tell-nobody") {
			assert.Failf(t, "", "%s echoed content: %v", name, err)
		}
	}
}

// Rule 23's --timeout bounds one version probe. It was also bounding the bus
// child, so a default --send killed its own delivery at five seconds. The
// delivery allowance is what is left of the budget, and the bus is handed finite
// retry controls that fit inside it.
func TestTheBusIsHandedFiniteBoundsOutOfTheRemainingBudget(t *testing.T) {
	t.Parallel()
	rig := fakeBusPath(t)
	log := rig.log
	p := manifest(t, row("x", "tool", printer(t, "v1.2.3"), "npm:unused", "none"))
	c, out, errs := run(t, rig.with(Environment{}), "report", "--file", p, "--send", "--snapshot",
		filepath.Join(t.TempDir(), "s.json"), "--as", "fixture", "--to", "integrator", "--budget", "60s")
	if c != 0 {
		require.EqualValuesf(t, 0, c, "%d %s %s", c, out, errs)
	}
	b, err := os.ReadFile(log)
	if err != nil {
		require.NoError(t, err, err)
	}
	sendArgv := ""
	for _, line := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(line, "argv send ") {
			sendArgv = line
		}
	}
	if sendArgv == "" {
		require.NotEqualValuesf(t, "", sendArgv, "no send argv was logged:\n%s", b)
	}
	for _, want := range []string{"--as ", "--to ", "--subject ", "--stdin"} {
		assert.Contains(t, sendArgv, want)
	}
	for _, gone := range []string{"prepare", "--prepared-stdin", "--attempts", "--git-timeout", "--remote", "--branch", "--bus "} {
		assert.NotContains(t, sendArgv, gone)
	}
}

// A binary on PATH called nova-bus that answers in no grammar this tool knows
// must not get to write on the caller's event line.
func TestALineOutsideTheBusGrammarIsNotRelayed(t *testing.T) {
	t.Parallel()
	if got := busSaid(ProcessResult{Stderr: "SEND FAIL from-x/n.md: a named reason"}); got != "SEND FAIL from-x/n.md: a named reason" {
		require.EqualValuesf(t, "SEND FAIL from-x/n.md: a named reason", got, "the bus's own grammar was not relayed: %q", got)
	}
	// Leading whitespace is normalised away before the grammar is checked, so the
	// cases below are lines whose CONTENT is in no grammar this tool knows.
	for _, alien := range []string{"gobbledegook tell-nobody-this", "bash: nova-bus: command not found", "Traceback (most recent call last):", "sendfail", "the bus says SEND FAIL later on"} {
		got := busSaid(ProcessResult{Stderr: alien})
		if strings.Contains(got, "tell-nobody-this") || got != "a line outside the bus's refusal grammar, not relayed" {
			assert.Failf(t, "", "%q was relayed as %q", alien, got)
		}
	}
	rig := fakeBusPath(t)
	p := manifest(t, row("x", "tool", printer(t, "v1.2.3"), "npm:unused", "none"))
	c, out, errs := run(t, rig.with(Environment{}, "NOVA_UPDATE_BUS_MODE=send-alien"), "report", "--file", p, "--send", "--snapshot",
		filepath.Join(t.TempDir(), "s.json"), "--as", "fixture", "--to", "integrator")
	if c != 1 {
		require.EqualValues(t, 1, c, c)
	}
	if strings.Contains(out+errs, "tell-nobody-this") {
		require.NotContainsf(t, out+errs, "tell-nobody-this", "an alien binary's words reached the event line:\n%s%s", out, errs)
	}
	need(t, errs, "a line outside the bus's refusal grammar, not relayed")
}

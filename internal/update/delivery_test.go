package update

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/testbin"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// This fake checks the caller's persisted state machine only. Actual bare-Git
// publication, content equality and crash recovery are separate integration gates.
var joinMain func()
var joinCleanup func()
var fakeBusHang func()

func TestMain(m *testing.M) {
	if os.Getenv("NOVA_UPDATE_FAKE_BUS") == "1" && strings.HasPrefix(filepath.Base(os.Args[0]), "nova-bus") {
		fakeBus()
		return
	}
	if os.Getenv("NOVA_UPDATE_JOIN_REAL") != "" && strings.HasPrefix(filepath.Base(os.Args[0]), "nova-bus") {
		if joinMain != nil {
			joinMain()
		}
		return
	}
	code := m.Run()
	if joinCleanup != nil {
		joinCleanup()
	}
	os.Exit(code)
}
func fakeBus() {
	input, _ := io.ReadAll(os.Stdin)
	verb := os.Args[1]
	log, _ := os.OpenFile(os.Getenv("NOVA_UPDATE_BUS_CALLS"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	fmt.Fprintln(log, verb)
	// The whole argv too, on its own line, so a test can assert what bounds the
	// reporter handed the bus rather than trusting that it handed any.
	fmt.Fprintln(log, "argv "+strings.Join(os.Args[1:], " "))
	log.Close()
	mode := os.Getenv("NOVA_UPDATE_BUS_MODE")
	if verb == "prepare" {
		if mode == "prepare-fail" {
			fmt.Fprintln(os.Stderr, "PREPARE FAIL synthetic refusal")
			os.Exit(1)
		}
		if mode == "prepare-alien" {
			// A binary that answers in no grammar this tool knows. It must not
			// get to put its words on the caller's event line.
			fmt.Fprintln(os.Stderr, "gobbledegook tell-nobody-this")
			os.Exit(1)
		}
		if mode == "prepare-shouty" {
			// A binary on PATH that answers with many lines and a very long one.
			// The caller's grammar must survive it.
			fmt.Fprintf(os.Stderr, "PREPARE FAIL %s\nand a second line\nand a third\n", strings.Repeat("y", 4000))
			os.Exit(1)
		}
		note := string(input)
		if !strings.HasSuffix(note, "\n") {
			note += "\n"
		}
		id := "fixture-" + shaText(note)[:12]
		json.NewEncoder(os.Stdout).Encode(map[string]string{"schema": "nova.bus.prepared/1", "id": id, "path": "from-fixture/fixture.md", "note": note, "sha256": shaText(note)})
		os.Exit(0)
	}
	var a map[string]string
	json.Unmarshal(input, &a)
	if mode == "hang" {
		if fakeBusHang == nil {
			os.Exit(20)
		}
		fakeBusHang()
	}
	if mode == "uncertain" {
		os.Exit(1)
	}
	fmt.Printf("SEND OK id=%s path=from-fixture/fixture.md commit=synthetic pushed=true attempts=0 state=already-published\n", a["id"])
	os.Exit(0)
}
func fakeBusPath(t *testing.T) string {
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
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("NOVA_UPDATE_FAKE_BUS", "1")
	t.Setenv("GORACE", "atexit_sleep_ms=0")
	log := filepath.Join(dir, "calls")
	t.Setenv("NOVA_UPDATE_BUS_CALLS", log)
	return log
}
func calls(t *testing.T, p string) (int, int) {
	t.Helper()
	b, e := os.ReadFile(p)
	if e != nil {
		require.NoError(t, e, e)
	}
	s := string(b)
	return strings.Count(s, "prepare\n"), strings.Count(s, "send\n")
}
func TestDeliveryScopeAndPreparedArtifactChecks(t *testing.T) {
	t.Parallel()

	o := options{as: "a", to: "c,b", bus: ".", remote: "origin", branch: "main", host: "studio"}
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
	good := map[string]string{"schema": "nova.bus.prepared/1", "id": "fixture-123", "path": "from-fixture/note.md", "note": "synthetic\n", "sha256": shaText("synthetic\n")}
	b, _ := json.Marshal(good)
	if _, e := validatePrepared(b); e != nil {
		require.NoError(t, e, e)
	}
	if _, e := validatePrepared(append(b, []byte("{}")...)); e == nil {
		require.Error(t, e, "trailing artifact accepted")
	}
	bad := bytes.Replace(b, []byte("synthetic\\n"), []byte("changed\\n"), 1)
	if _, e := validatePrepared(bad); e == nil {
		require.Error(t, e, "wrong digest accepted")
	}
}

// A second value for one field of a prepared artifact or a snapshot is an
// ambiguous identity. Both readers must refuse it, and must do so without
// quoting the offending key or any of the note back into the diagnostic.
func TestStrictDecodingRefusesAmbiguousAndWrongInput(t *testing.T) {
	t.Parallel()

	note := "a note\n"
	sum := shaText(note)
	good := fmt.Sprintf(`{"schema":"nova.bus.prepared/1","id":"fixture-1","path":"from-fixture/f.md","note":%q,"sha256":%q}`, note, sum)
	if id, err := validatePrepared([]byte(good)); err != nil || id != "fixture-1" {
		require.Failf(t, "", "valid artifact refused: %v %q", err, id)
	}
	secret := "tell-nobody"
	bad := map[string]string{
		"duplicate id":         fmt.Sprintf(`{"schema":"nova.bus.prepared/1","id":"fixture-1","id":"fixture-2","path":"from-fixture/f.md","note":%q,"sha256":%q}`, note, sum),
		"duplicate note":       fmt.Sprintf(`{"schema":"nova.bus.prepared/1","id":"fixture-1","path":"from-fixture/f.md","note":%q,"note":%q,"sha256":%q}`, note, secret+"\n", sum),
		"unknown field":        fmt.Sprintf(`{"schema":"nova.bus.prepared/1","id":"fixture-1","path":"from-fixture/f.md","note":%q,"sha256":%q,"extra":%q}`, note, sum, secret),
		"wrong type":           fmt.Sprintf(`{"schema":"nova.bus.prepared/1","id":7,"path":"from-fixture/f.md","note":%q,"sha256":%q}`, note, sum),
		"trailing data":        good + `{"schema":"nova.bus.prepared/1"}`,
		"digest mismatch":      fmt.Sprintf(`{"schema":"nova.bus.prepared/1","id":"fixture-1","path":"from-fixture/f.md","note":%q,"sha256":%q}`, note, shaText(secret)),
		"note without newline": fmt.Sprintf(`{"schema":"nova.bus.prepared/1","id":"fixture-1","path":"from-fixture/f.md","note":"no lf","sha256":%q}`, shaText("no lf")),
		"too deeply nested":    `{"schema":` + strings.Repeat("[", 40) + strings.Repeat("]", 40) + "}",
	}
	for name, raw := range bad {
		id, err := validatePrepared([]byte(raw))
		if err == nil {
			require.Errorf(t, err, "%s accepted, id=%q", name, id)
		}
		if strings.Contains(err.Error(), secret) || strings.Contains(err.Error(), "a note") {
			require.Failf(t, "", "%s diagnostic echoed content: %v", name, err)
		}
	}
	dir := t.TempDir()
	dup := filepath.Join(dir, "dup.json")
	if e := os.WriteFile(dup, []byte(`{"observed":{},"observed":{"x":{"raw":"`+secret+`","status":"tool","at":"t"}},"delivered":{},"pending":{}}`), 0600); e != nil {
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
			p := manifest(t, row("x", "tool", printer(t, "v1.2.3"), "npm:unused", "none"))
			line := "SEND FAIL synthetic refusal"
			if mode == "send-shouty" {
				// A binary on PATH that answers with many lines and a very long one.
				// The caller's grammar must survive it.
				line = "SEND FAIL " + strings.Repeat("y", 4000)
			}
			env := Environment{Process: func(ctx context.Context, args []string, input io.Reader, cap int) ProcessResult {
				if len(args) == 0 || args[0] != "nova-bus" {
					return process(ctx, args, input, cap)
				}
				return ProcessResult{Reason: "exit 1", Stderr: line + "\nand a second line\nand a third\n"}
			}}
			c, _, errout := run(t, env, "report", "--file", p, "--send", "--snapshot",
				filepath.Join(t.TempDir(), "s.json"), "--as", "fixture", "--to", "integrator",
				"--bus", t.TempDir(), "--remote", "origin", "--branch", "main")
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

	note := "a note\n"
	sum := shaText(note)
	artifact := func(pairs string) string {
		return `{"schema":"nova.bus.prepared/1",` + pairs + `,"path":"from-fixture/f.md","note":` +
			fmt.Sprintf("%q", note) + `,"sha256":` + fmt.Sprintf("%q", sum) + `}`
	}
	for name, raw := range map[string]string{
		"id and ID":         artifact(`"id":"fixture-1","ID":"fixture-2"`),
		"id and Id":         artifact(`"id":"fixture-1","Id":"fixture-2"`),
		"ID alone":          artifact(`"ID":"fixture-2"`),
		"long s in a key":   `{"schema":"nova.bus.prepared/1","id":"fixture-1","path":"from-fixture/f.md","note":` + fmt.Sprintf("%q", note) + `,"ſha256":` + fmt.Sprintf("%q", sum) + `}`,
		"a key short":       `{"schema":"nova.bus.prepared/1","id":"fixture-1","path":"from-fixture/f.md","note":` + fmt.Sprintf("%q", note) + `}`,
		"kelvin in a key":   `{"schema":"nova.bus.prepared/1","id":"fixture-1","path":"from-fixture/f.md","note":` + fmt.Sprintf("%q", note) + `,"sha256":` + fmt.Sprintf("%q", sum) + `,"K":"x"}`,
		"schema and Schema": `{"schema":"nova.bus.prepared/1","Schema":"nova.bus.prepared/1","id":"fixture-1","path":"from-fixture/f.md","note":` + fmt.Sprintf("%q", note) + `,"sha256":` + fmt.Sprintf("%q", sum) + `}`,
	} {
		if id, err := validatePrepared([]byte(raw)); err == nil {
			assert.Errorf(t, err, "%s accepted, id=%q", name, id)
		}
	}
	if id, err := validatePrepared([]byte(artifact(`"id":"fixture-1"`))); err != nil || id != "fixture-1" {
		require.Failf(t, "", "the exact artifact was refused: %v %q", err, id)
	}
	dir := t.TempDir()
	for name, body := range map[string]string{
		"observed and Observed":   `{"observed":{"x":{"raw":"one","status":"tool","at":"t"}},"Observed":{"x":{"raw":"tell-nobody","status":"tool","at":"t"}},"delivered":{},"pending":{}}`,
		"nested raw and Raw":      `{"observed":{"x":{"raw":"one","Raw":"tell-nobody","status":"tool","at":"t"}},"delivered":{},"pending":{}}`,
		"delivered and DELIVERED": `{"observed":{},"delivered":{},"DELIVERED":{},"pending":{}}`,
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

// A binary on PATH called nova-bus that answers in no grammar this tool knows
// must not get to write on the caller's event line.
func TestALineOutsideTheBusGrammarIsNotRelayed(t *testing.T) {
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
	fake := Environment{Process: func(ctx context.Context, args []string, input io.Reader, cap int) ProcessResult {
		if len(args) == 0 || args[0] != "nova-bus" {
			return process(ctx, args, input, cap)
		}
		// A binary that answers in no grammar this tool knows. It must not
		// get to put its words on the caller's event line.
		return ProcessResult{Reason: "exit 1", Stderr: "gobbledegook tell-nobody-this\n"}
	}}
	p := manifest(t, row("x", "tool", printer(t, "v1.2.3"), "npm:unused", "none"))
	c, out, errs := run(t, fake, "report", "--file", p, "--send", "--snapshot",
		filepath.Join(t.TempDir(), "s.json"), "--as", "fixture", "--to", "integrator",
		"--bus", t.TempDir(), "--remote", "origin", "--branch", "main")
	if c != 1 {
		require.EqualValues(t, 1, c, c)
	}
	if strings.Contains(out+errs, "tell-nobody-this") {
		require.NotContainsf(t, out+errs, "tell-nobody-this", "an alien binary's words reached the event line:\n%s%s", out, errs)
	}
	need(t, errs, "a line outside the bus's refusal grammar, not relayed")
}

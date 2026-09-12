package update

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

// This fake checks the caller's persisted state machine only. Actual bare-Git
// publication, content equality and crash recovery are separate integration gates.
func TestMain(m *testing.M) {
	if os.Getenv("NOVA_UPDATE_FAKE_BUS") == "1" && strings.HasPrefix(filepath.Base(os.Args[0]), "nova-bus") {
		fakeBus()
		return
	}
	if os.Getenv("NOVA_UPDATE_JOIN_REAL") != "" && strings.HasPrefix(filepath.Base(os.Args[0]), "nova-bus") {
		joinWrapper()
		return
	}
	code := m.Run()
	removeJoinBinaries()
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
		time.Sleep(30 * time.Second)
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
	raw, e := os.ReadFile(os.Args[0])
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(filepath.Join(dir, name), raw, 0700); e != nil {
		t.Fatal(e)
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
		t.Fatal(e)
	}
	s := string(b)
	return strings.Count(s, "prepare\n"), strings.Count(s, "send\n")
}
func TestPendingBeforeDispatchAndQuietRetry(t *testing.T) {
	for _, mode := range []string{"uncertain", "hang"} {
		t.Run(mode, func(t *testing.T) {
			log := fakeBusPath(t)
			p := manifest(t, row("x", "tool", printer(t, "v1.2.3"), "npm:unused", "none"))
			statePath := filepath.Join(t.TempDir(), "s.json")
			args := []string{"report", "--file", p, "--send", "--snapshot", statePath, "--as", "fixture", "--to", "integrator", "--bus", t.TempDir(), "--remote", "origin", "--branch", "main", "--timeout", "5s"}
			if mode == "hang" {
				// A bus that never answers is now bounded by the BUDGET, not by
				// the version probe's --timeout: that was the repair. So the
				// fixture names a budget, and the hang is cut off by the same
				// allowance a real delivery gets. Ten seconds: enough that a real
				// prepare and the start of a send fit, far less than the fixture's
				// thirty-second sleep.
				args = append(args, "--budget", "10s")
			}
			t.Setenv("NOVA_UPDATE_BUS_MODE", mode)
			code, _, errout := run(t, Environment{}, args...)
			if code != 1 {
				t.Fatal(code)
			}
			need(t, errout, "sent=uncertain")
			s, e := readSnapshot(statePath)
			if e != nil || len(s.Pending) != 1 || len(s.Delivered) != 0 {
				t.Fatal(s, e)
			}
			var id string
			for _, pending := range s.Pending {
				id = pending.ID
				if id == "" {
					t.Fatal("no ID retained")
				}
			}
			t.Setenv("NOVA_UPDATE_BUS_MODE", "ok")
			code, out, errout := run(t, Environment{}, args...)
			if code != 0 {
				t.Fatalf("%d %s %s", code, out, errout)
			}
			need(t, out, "REPORT SENT", "id\\x3d"+id)
			nprep, nsend := calls(t, log)
			if nprep != 1 || nsend != 2 {
				t.Fatal(nprep, nsend)
			}
			code, out, errout = run(t, Environment{}, args...)
			if code != 0 {
				t.Fatalf("%d %s %s", code, out, errout)
			}
			need(t, out, "nothing sent", "sent=no")
			np, ns := calls(t, log)
			if np != nprep || ns != nsend {
				t.Fatal("unchanged confirmed run invoked bus")
			}
		})
	}
}
func TestNewObservationCannotReplaceUnresolvedPending(t *testing.T) {
	log := fakeBusPath(t)
	p := manifest(t, row("x", "tool", printer(t, "v1.0.0"), "npm:unused", "none"))
	sp := filepath.Join(t.TempDir(), "s.json")
	args := []string{"report", "--file", p, "--send", "--snapshot", sp, "--as", "fixture", "--to", "integrator", "--bus", t.TempDir(), "--remote", "origin", "--branch", "main"}
	t.Setenv("NOVA_UPDATE_BUS_MODE", "uncertain")
	run(t, Environment{}, args...)
	s, _ := readSnapshot(sp)
	var old string
	for _, v := range s.Pending {
		old = v.ID
	}
	os.WriteFile(p, []byte(Header+"\n"+row("x", "tool", printer(t, "v2.0.0"), "npm:unused", "none")+"\n"), 0600)
	if c, _, _ := run(t, Environment{}, args...); c != 1 {
		t.Fatal(c)
	}
	if np, ns := calls(t, log); np != 1 || ns != 2 {
		t.Fatal(np, ns)
	}
	s, _ = readSnapshot(sp)
	for _, v := range s.Pending {
		if v.ID != old || v.Observed["x"].Raw != "v1.0.0" {
			t.Fatal("pending was replaced")
		}
	}
	t.Setenv("NOVA_UPDATE_BUS_MODE", "ok")
	if c, o, e := run(t, Environment{}, args...); c != 0 {
		t.Fatalf("%d %s %s", c, o, e)
	}
	if np, ns := calls(t, log); np != 2 || ns != 4 {
		t.Fatal(np, ns)
	}
	s, _ = readSnapshot(sp)
	if len(s.Pending) != 0 {
		t.Fatal("pending not cleared")
	}
	for _, v := range s.Delivered {
		if v.Observed["x"].Raw != "v2.0.0" || v.ID == old {
			t.Fatal(v)
		}
	}
}
func TestDeliveryScopeAndPreparedArtifactChecks(t *testing.T) {
	o := options{as: "a", to: "c,b", bus: ".", remote: "origin", branch: "main", host: "studio"}
	same := o
	same.to = "b,c"
	if snapshotScope(o) != snapshotScope(same) {
		t.Fatal("recipient order changed scope")
	}
	other := o
	other.host = "air"
	if snapshotScope(o) == snapshotScope(other) {
		t.Fatal("bench silently shared delivery state")
	}
	good := map[string]string{"schema": "nova.bus.prepared/1", "id": "fixture-123", "path": "from-fixture/note.md", "note": "synthetic\n", "sha256": shaText("synthetic\n")}
	b, _ := json.Marshal(good)
	if _, e := validatePrepared(b); e != nil {
		t.Fatal(e)
	}
	if _, e := validatePrepared(append(b, []byte("{}")...)); e == nil {
		t.Fatal("trailing artifact accepted")
	}
	bad := bytes.Replace(b, []byte("synthetic\\n"), []byte("changed\\n"), 1)
	if _, e := validatePrepared(bad); e == nil {
		t.Fatal("wrong digest accepted")
	}
}

// A second value for one field of a prepared artifact or a snapshot is an
// ambiguous identity. Both readers must refuse it, and must do so without
// quoting the offending key or any of the note back into the diagnostic.
func TestStrictDecodingRefusesAmbiguousAndWrongInput(t *testing.T) {
	note := "a note\n"
	sum := shaText(note)
	good := fmt.Sprintf(`{"schema":"nova.bus.prepared/1","id":"fixture-1","path":"from-fixture/f.md","note":%q,"sha256":%q}`, note, sum)
	if id, err := validatePrepared([]byte(good)); err != nil || id != "fixture-1" {
		t.Fatalf("valid artifact refused: %v %q", err, id)
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
			t.Fatalf("%s accepted, id=%q", name, id)
		}
		if strings.Contains(err.Error(), secret) || strings.Contains(err.Error(), "a note") {
			t.Fatalf("%s diagnostic echoed content: %v", name, err)
		}
	}
	dir := t.TempDir()
	dup := filepath.Join(dir, "dup.json")
	if e := os.WriteFile(dup, []byte(`{"observed":{},"observed":{"x":{"raw":"`+secret+`","status":"tool","at":"t"}},"delivered":{},"pending":{}}`), 0600); e != nil {
		t.Fatal(e)
	}
	s, err := readSnapshot(dup)
	if err == nil {
		t.Fatalf("ambiguous snapshot accepted: %v", s)
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("snapshot diagnostic echoed content: %v", err)
	}
	if b, _ := os.ReadFile(dup); !strings.Contains(string(b), secret) {
		t.Fatal("refusal did not preserve the snapshot byte for byte")
	}
}

// A failed delivery used to say "exit 1" and nothing else. The bus's own first
// line now reaches the caller's diagnostic -- one line, clipped, and no binary
// on PATH can turn one event into two.
func TestTheBusOwnWordsReachTheCallerBoundedToOneLine(t *testing.T) {
	if got := busSaid(ProcessResult{Stderr: "SEND FAIL one\nSEND FAIL two\n"}); got != "SEND FAIL one" {
		t.Fatalf("%q", got)
	}
	// Stdout is the fallback when the bus said nothing on stderr: a SEND OK for
	// some other id is the bus's grammar and worth relaying.
	if got := busSaid(ProcessResult{Stdout: "SEND OK id=other\nmore"}); got != "SEND OK id=other" {
		t.Fatalf("%q", got)
	}
	if got := busSaid(ProcessResult{}); got != "nothing" {
		t.Fatalf("%q", got)
	}
	if got := busSaid(ProcessResult{Stderr: "SEND FAIL " + strings.Repeat("x", 500)}); len(got) != 203 || !strings.HasSuffix(got, "...") {
		t.Fatalf("unbounded: %d bytes: %q", len(got), got)
	}
	for _, mode := range []string{"prepare-fail", "prepare-shouty"} {
		t.Run(mode, func(t *testing.T) {
			fakeBusPath(t)
			p := manifest(t, row("x", "tool", printer(t, "v1.2.3"), "npm:unused", "none"))
			t.Setenv("NOVA_UPDATE_BUS_MODE", mode)
			c, _, errout := run(t, Environment{}, "report", "--file", p, "--send", "--snapshot",
				filepath.Join(t.TempDir(), "s.json"), "--as", "fixture", "--to", "integrator",
				"--bus", t.TempDir(), "--remote", "origin", "--branch", "main")
			if c != 1 {
				t.Fatal(c)
			}
			need(t, errout, "the bus said: PREPARE FAIL")
			note := ""
			for _, line := range strings.Split(errout, "\n") {
				if strings.HasPrefix(line, "REPORT NOTE") {
					if note != "" {
						t.Fatalf("one refusal became two lines:\n%s", errout)
					}
					note = line
				}
			}
			if note == "" {
				t.Fatalf("no REPORT NOTE line:\n%s", errout)
			}
			if len(note) > 600 {
				t.Fatalf("the refusal line is %d bytes, which is not bounded: %s", len(note), note)
			}
		})
	}
}

// Go's decoder matches a struct field case-insensitively, so refusing only
// BYTE-identical duplicate keys left the ambiguity it was written to close: two
// keys that fold to one field still carried two values, and the LAST won. The
// witness the cold read measured is the first case below.
func TestStrictDecodingRefusesKeysThatFoldTogether(t *testing.T) {
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
			t.Errorf("%s accepted, id=%q", name, id)
		}
	}
	if id, err := validatePrepared([]byte(artifact(`"id":"fixture-1"`))); err != nil || id != "fixture-1" {
		t.Fatalf("the exact artifact was refused: %v %q", err, id)
	}
	dir := t.TempDir()
	for name, body := range map[string]string{
		"observed and Observed":   `{"observed":{"x":{"raw":"one","status":"tool","at":"t"}},"Observed":{"x":{"raw":"tell-nobody","status":"tool","at":"t"}},"delivered":{},"pending":{}}`,
		"nested raw and Raw":      `{"observed":{"x":{"raw":"one","Raw":"tell-nobody","status":"tool","at":"t"}},"delivered":{},"pending":{}}`,
		"delivered and DELIVERED": `{"observed":{},"delivered":{},"DELIVERED":{},"pending":{}}`,
	} {
		p := filepath.Join(dir, strings.ReplaceAll(name, " ", "-")+".json")
		if err := os.WriteFile(p, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
		s, err := readSnapshot(p)
		if err == nil {
			t.Errorf("%s accepted: %+v", name, s)
		}
		if err != nil && strings.Contains(err.Error(), "tell-nobody") {
			t.Errorf("%s echoed content: %v", name, err)
		}
	}
}

// Rule 23's --timeout bounds one version probe. It was also bounding the bus
// child, so a default --send killed its own delivery at five seconds. The
// delivery allowance is what is left of the budget, and the bus is handed finite
// retry controls that fit inside it.
func TestTheBusIsHandedFiniteBoundsOutOfTheRemainingBudget(t *testing.T) {
	for _, c := range []struct {
		remaining            time.Duration
		attempts, gitSeconds int
	}{
		{60 * time.Second, 3, 20},
		{10 * time.Minute, 10, 60},
		{2 * time.Second, 2, 1},
		{500 * time.Millisecond, 1, 1},
		{0, 1, 1},
	} {
		// Below a second there is no whole number of seconds to name, so one is
		// named and the caller's own deadline stays the tighter of the two.
		a, g := busBounds(c.remaining)
		if a != c.attempts || g != c.gitSeconds {
			t.Errorf("%s left: attempts=%d git-timeout=%d, want %d and %d", c.remaining, a, g, c.attempts, c.gitSeconds)
		}
		if a < 1 || g < 1 {
			t.Errorf("%s left: a bound below one is not a bound", c.remaining)
		}
		if want := c.remaining; want >= time.Second && time.Duration(g)*time.Second > want {
			t.Errorf("%s left: a git timeout of %ds promises more than remains", want, g)
		}
	}
	log := fakeBusPath(t)
	p := manifest(t, row("x", "tool", printer(t, "v1.2.3"), "npm:unused", "none"))
	c, out, errs := run(t, Environment{}, "report", "--file", p, "--send", "--snapshot",
		filepath.Join(t.TempDir(), "s.json"), "--as", "fixture", "--to", "integrator",
		"--bus", t.TempDir(), "--remote", "origin", "--branch", "main", "--budget", "60s")
	if c != 0 {
		t.Fatalf("%d %s %s", c, out, errs)
	}
	b, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	sendArgv := ""
	for _, line := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(line, "argv send ") {
			sendArgv = line
		}
	}
	if sendArgv == "" {
		t.Fatalf("no send argv was logged:\n%s", b)
	}
	for _, want := range []string{"--attempts ", "--git-timeout ", "--prepared-stdin"} {
		if !strings.Contains(sendArgv, want) {
			t.Errorf("the send argv does not carry %s: %s", want, sendArgv)
		}
	}
	fields := strings.Fields(sendArgv)
	for i, f := range fields {
		if f != "--attempts" && f != "--git-timeout" {
			continue
		}
		if i+1 >= len(fields) {
			t.Fatalf("%s names no value: %s", f, sendArgv)
		}
		n, err := strconv.Atoi(fields[i+1])
		if err != nil || n < 1 {
			t.Errorf("%s is %q, which is not a finite bound", f, fields[i+1])
		}
		if f == "--git-timeout" && n > 60 {
			t.Errorf("--git-timeout %d exceeds the 60s budget", n)
		}
	}
	// prepare takes neither: it runs no Git and touches no network.
	for _, line := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(line, "argv prepare ") && (strings.Contains(line, "--attempts") || strings.Contains(line, "--git-timeout")) {
			t.Errorf("prepare was handed a Git bound it has no use for: %s", line)
		}
	}
}

// A binary on PATH called nova-bus that answers in no grammar this tool knows
// must not get to write on the caller's event line.
func TestALineOutsideTheBusGrammarIsNotRelayed(t *testing.T) {
	if got := busSaid(ProcessResult{Stderr: "SEND FAIL from-x/n.md: a named reason"}); got != "SEND FAIL from-x/n.md: a named reason" {
		t.Fatalf("the bus's own grammar was not relayed: %q", got)
	}
	// Leading whitespace is normalised away before the grammar is checked, so the
	// cases below are lines whose CONTENT is in no grammar this tool knows.
	for _, alien := range []string{"gobbledegook tell-nobody-this", "bash: nova-bus: command not found", "Traceback (most recent call last):", "sendfail", "the bus says SEND FAIL later on"} {
		got := busSaid(ProcessResult{Stderr: alien})
		if strings.Contains(got, "tell-nobody-this") || got != "a line outside the bus's refusal grammar, not relayed" {
			t.Errorf("%q was relayed as %q", alien, got)
		}
	}
	fakeBusPath(t)
	p := manifest(t, row("x", "tool", printer(t, "v1.2.3"), "npm:unused", "none"))
	t.Setenv("NOVA_UPDATE_BUS_MODE", "prepare-alien")
	c, out, errs := run(t, Environment{}, "report", "--file", p, "--send", "--snapshot",
		filepath.Join(t.TempDir(), "s.json"), "--as", "fixture", "--to", "integrator",
		"--bus", t.TempDir(), "--remote", "origin", "--branch", "main")
	if c != 1 {
		t.Fatal(c)
	}
	if strings.Contains(out+errs, "tell-nobody-this") {
		t.Fatalf("an alien binary's words reached the event line:\n%s%s", out, errs)
	}
	need(t, errs, "a line outside the bus's refusal grammar, not relayed")
}

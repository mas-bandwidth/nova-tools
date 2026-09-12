package update

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
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
	os.Exit(m.Run())
}
func fakeBus() {
	input, _ := io.ReadAll(os.Stdin)
	verb := os.Args[1]
	log, _ := os.OpenFile(os.Getenv("NOVA_UPDATE_BUS_CALLS"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	fmt.Fprintln(log, verb)
	log.Close()
	mode := os.Getenv("NOVA_UPDATE_BUS_MODE")
	if verb == "prepare" {
		if mode == "prepare-fail" {
			fmt.Fprintln(os.Stderr, "PREPARE FAIL synthetic refusal")
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

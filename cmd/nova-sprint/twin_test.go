package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
)

// twinProcess is one command of a twin as a shell runs it: a new process, so a
// new app, over the twin file alone. Its environment is what nova-sprint help
// tells the reader to export.
func twinProcess(t *testing.T, file, line string) (int, string, string) {
	t.Helper()
	env := map[string]string{"NOVA_SPRINT_REDIS": "mem:" + file, "NOVA_SPRINT_ACTOR": "boss"}
	a := newApp(func(k string) string { return env[k] })
	defer a.close()
	var out, errb bytes.Buffer
	code := a.run(split(strings.TrimPrefix(line, prog+" ")), &out, &errb)
	return code, out.String(), errb.String()
}

// The help's own flow (twinSteps) runs, one process a line, over a twin file
// alone: every verb succeeds, and the card is landed at the end. It holds the
// help to what a twin does.
func TestTheTwinRunsTheHelpsCardFlowOneProcessAtATime(t *testing.T) {
	t.Parallel()
	file := filepath.Join(t.TempDir(), "sprint.twin")
	var last string
	for _, line := range twinSteps {
		code, out, errs := twinProcess(t, file, line)
		if code != 0 {
			t.Fatalf("%s: exit %d\n%s%s", line, code, out, errs)
		}
		last = out
	}
	if !strings.Contains(last, "\nDONE\n") {
		t.Errorf("the flow ends with the card landed; where printed:\n%s", last)
	}
	dir, _ := os.ReadDir(filepath.Dir(file))
	if len(dir) != 1 {
		t.Errorf("a twin leaves its one file and no temporary file behind; the directory holds %d entries", len(dir))
	}
	// the help prints the flow the test runs, line for line
	for _, line := range twinSteps {
		if !strings.Contains(banner(), "  "+line+"\n") {
			t.Errorf("nova-sprint help does not show %q", line)
		}
	}
}

// A twin asked for with no file is refused, and a file that is not a twin is
// refused and left as it is: a typo in the path never overwrites a document.
func TestTheTwinRefusesWhatIsNotATwinFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	for _, addr := range []string{"mem", "mem:"} {
		env := map[string]string{"NOVA_SPRINT_REDIS": addr, "NOVA_SPRINT_ACTOR": "boss"}
		a := newApp(func(k string) string { return env[k] })
		var out, errb bytes.Buffer
		if code := a.run([]string{"where"}, &out, &errb); code != 2 || !strings.Contains(errb.String(), "--redis mem:<file>") {
			t.Errorf("--redis %s: exit %d, stderr %q; want a refusal that names mem:<file>", addr, code, errb.String())
		}
	}
	notes := filepath.Join(dir, "notes.txt")
	if err := os.WriteFile(notes, []byte("my notes\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, _, errs := twinProcess(t, notes, "nova-sprint init --members m1")
	if code == 0 || !strings.Contains(errs, "not a twin snapshot") {
		t.Errorf("init over a file that is not a twin: exit %d, stderr %q", code, errs)
	}
	if got, _ := os.ReadFile(notes); string(got) != "my notes\n" {
		t.Errorf("the refused file changed: %q", got)
	}
}

// What needs a machine between commands is refused in a twin, and says what to
// do: tick by hand.
func TestTheTwinRefusesWhatWaitsForAMachine(t *testing.T) {
	t.Parallel()
	file := filepath.Join(t.TempDir(), "sprint.twin")
	if code, out, errs := twinProcess(t, file, "nova-sprint init --members m1"); code != 0 {
		t.Fatalf("init: exit %d\n%s%s", code, out, errs)
	}
	for _, line := range []string{"nova-sprint run", "nova-sprint inbox --wait", "nova-sprint where --watch"} {
		code, out, errs := twinProcess(t, file, line)
		if code != 2 || !strings.Contains(errs, "nova-sprint tick") || out != "" {
			t.Errorf("%s: exit %d, stdout %q, stderr %q; want a refusal that names tick", line, code, out, errs)
		}
	}
	code, out, _ := twinProcess(t, file, "nova-sprint start")
	if code != 0 || !strings.Contains(out, "tick by hand: nova-sprint tick") {
		t.Errorf("start in a twin tells the reader to tick by hand, not to run: exit %d\n%s", code, out)
	}
}

// A store taken to a snapshot and restored answers as the store it was: the
// flow goes on over a fresh Mem restored from the last one at every step, and
// a restored store's snapshot is the snapshot it was restored from.
func TestASnapshotRestoredIsTheStoreItWas(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.live = []string{"m1"}
	for _, line := range twinSteps {
		ta.ok(strings.TrimPrefix(line, prog+" "))
		doc, err := ta.m.Snapshot()
		if err != nil {
			t.Fatalf("%s: %v", line, err)
		}
		fresh := store.NewMem()
		if err := fresh.Restore(doc); err != nil {
			t.Fatalf("%s: %v", line, err)
		}
		// a receipt's empty field maps come back from a read as empty, as from
		// a Redis: the first restore settles them, and from there the document
		// is what it was
		settled, err := fresh.Snapshot()
		if err != nil {
			t.Fatalf("%s: %v", line, err)
		}
		again := store.NewMem()
		if err := again.Restore(settled); err != nil {
			t.Fatalf("%s: %v", line, err)
		}
		if doc2, err := again.Snapshot(); err != nil || !bytes.Equal(settled, doc2) {
			t.Fatalf("%s: a restored store's snapshot is not the one it came from (%v)", line, err)
		}
		ta.m = fresh
	}
	out := ta.ok("where")
	if !strings.Contains(out, "\nDONE\n") {
		t.Errorf("the flow over restored stores ends with the card landed:\n%s", out)
	}
	ta.clean()
}

// A snapshot of another version, or with a field this build does not know, is
// refused and the store is left as it was.
func TestARestoreRefusesADocumentItDoesNotKnow(t *testing.T) {
	t.Parallel()
	m := store.NewMem()
	for _, doc := range []string{`{"version":99}`, `{"version":1,"surprise":true}`, `[]`, ``} {
		if err := m.Restore([]byte(doc)); err == nil {
			t.Errorf("Restore(%q) was accepted", doc)
		}
	}
	if _, err := m.Epoch(context.Background()); err != nil {
		t.Fatal(err)
	}
}

// Every verb's -h shows its example, and the example is a line the verb's own
// flags take: run against a sprint, it is never a usage refusal (exit 2).
func TestEveryVerbHelpShowsAnExampleItsFlagsTake(t *testing.T) {
	t.Parallel()
	for _, v := range verbs {
		if v.example == "" {
			t.Errorf("verb %q has no example", v.name)
			continue
		}
		var out, errb bytes.Buffer
		a := newApp(func(string) string { return "" })
		if code := a.run(append(strings.Fields(v.name), "-h"), &out, &errb); code != 0 {
			t.Errorf("%s -h: exit %d, stderr %q", v.name, code, errb.String())
		}
		if want := "example:\n  " + prog + " " + v.example + "\n"; !strings.Contains(out.String(), want) {
			t.Errorf("%s -h lacks its example %q:\n%s", v.name, v.example, out.String())
		}
		if strings.Index(out.String(), "example:") > strings.Index(out.String(), "flags:") {
			t.Errorf("%s -h shows the example after the flags", v.name)
		}
	}
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b,reader-c --members m1,m2")
	ta.ok("add --stream s1 --count 9")
	for _, v := range verbs {
		switch v.name {
		case "run", "play", "teardown", "fleet sync", "goal set", "goal show":
			continue // run and play tick for ever; teardown drops the sprint; fleet sync reads a config store; a goal is set from a file
		}
		code, out, errs := ta.do(v.example)
		if code == 2 {
			t.Errorf("%s: exit 2, the usage refusal\n%s%s", v.example, out, errs)
		}
	}
}

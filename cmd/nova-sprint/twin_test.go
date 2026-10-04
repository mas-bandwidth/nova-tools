package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

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
		require.Equal(t, 0, code, "%s: exit %d\n%s%s", line, code, out, errs)
		last = out
	}
	assert.Contains(t, last, "\nDONE\n", "the flow ends with the card landed; where printed")
	dir, _ := os.ReadDir(filepath.Dir(file))
	assert.Len(t, dir, 1, "a twin leaves its one file and no temporary file behind; the directory holds %d entries", len(dir))
	// the help prints the flow the test runs, line for line
	for _, line := range twinSteps {
		assert.Contains(t, banner(), "  "+line+"\n", "nova-sprint help does not show %q", line)
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
		code := a.run([]string{"where"}, &out, &errb)
		assert.Equal(t, 2, code, "--redis %s: exit %d, stderr %q; want a refusal that names mem:<file>", addr, code, errb.String())
		assert.Contains(t, errb.String(), "--redis mem:<file>", "--redis %s: exit %d, stderr %q; want a refusal that names mem:<file>", addr, code, errb.String())
	}
	notes := filepath.Join(dir, "notes.txt")
	require.NoError(t, os.WriteFile(notes, []byte("my notes\n"), 0o644))
	code, _, errs := twinProcess(t, notes, "nova-sprint init --members m1")
	assert.NotEqual(t, 0, code, "init over a file that is not a twin: exit %d, stderr %q", code, errs)
	assert.Contains(t, errs, "not a twin snapshot", "init over a file that is not a twin: exit %d, stderr %q", code, errs)
	got, _ := os.ReadFile(notes)
	assert.Equal(t, "my notes\n", string(got), "the refused file changed")
}

// What needs a machine between commands is refused in a twin, and says what to
// do: tick by hand.
func TestTheTwinRefusesWhatWaitsForAMachine(t *testing.T) {
	t.Parallel()
	file := filepath.Join(t.TempDir(), "sprint.twin")
	code, out, errs := twinProcess(t, file, "nova-sprint init --members m1")
	require.Equal(t, 0, code, "init: exit %d\n%s%s", code, out, errs)
	for _, line := range []string{"nova-sprint run", "nova-sprint inbox --wait", "nova-sprint where --watch"} {
		code, out, errs := twinProcess(t, file, line)
		assert.Equal(t, 2, code, "%s: exit %d, stdout %q, stderr %q; want a refusal that names tick", line, code, out, errs)
		assert.Contains(t, errs, "nova-sprint tick", "%s: exit %d, stdout %q, stderr %q; want a refusal that names tick", line, code, out, errs)
		assert.Empty(t, out, "%s: exit %d, stdout %q, stderr %q; want a refusal that names tick", line, code, out, errs)
	}
	code, out, _ = twinProcess(t, file, "nova-sprint start")
	assert.Equal(t, 0, code, "start in a twin tells the reader to tick by hand, not to run: exit %d\n%s", code, out)
	assert.Contains(t, out, "tick by hand: nova-sprint tick", "start in a twin tells the reader to tick by hand, not to run: exit %d\n%s", code, out)
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
		require.NoError(t, err, "%s", line)
		fresh := store.NewMem()
		require.NoError(t, fresh.Restore(doc), "%s", line)
		// a receipt's empty field maps come back from a read as empty, as from
		// a Redis: the first restore settles them, and from there the document
		// is what it was
		settled, err := fresh.Snapshot()
		require.NoError(t, err, "%s", line)
		again := store.NewMem()
		require.NoError(t, again.Restore(settled), "%s", line)
		doc2, err := again.Snapshot()
		require.NoError(t, err, "%s: a restored store's snapshot is not the one it came from (%v)", line, err)
		require.Equal(t, settled, doc2, "%s: a restored store's snapshot is not the one it came from (%v)", line, err)
		ta.m = fresh
	}
	out := ta.ok("where")
	assert.Contains(t, out, "\nDONE\n", "the flow over restored stores ends with the card landed")
	ta.clean()
}

// A snapshot of another version, or with a field this build does not know, is
// refused and the store is left as it was.
func TestARestoreRefusesADocumentItDoesNotKnow(t *testing.T) {
	t.Parallel()
	m := store.NewMem()
	for _, doc := range []string{`{"version":99}`, `{"version":1,"surprise":true}`, `[]`, ``} {
		assert.Error(t, m.Restore([]byte(doc)), "Restore(%q) was accepted", doc)
	}
	_, err := m.Epoch(context.Background())
	require.NoError(t, err)
}

// Every verb's -h shows its example, and the example is a line the verb's own
// flags take: run against a sprint, it is never a usage refusal (exit 2).
func TestEveryVerbHelpShowsAnExampleItsFlagsTake(t *testing.T) {
	t.Parallel()
	for _, v := range verbs {
		if !assert.NotEmpty(t, v.example, "verb %q has no example", v.name) {
			continue
		}
		var out, errb bytes.Buffer
		a := newApp(func(string) string { return "" })
		code := a.run(append(strings.Fields(v.name), "-h"), &out, &errb)
		assert.Equal(t, 0, code, "%s -h: exit %d, stderr %q", v.name, code, errb.String())
		assert.Contains(t, out.String(), "example:\n  "+prog+" "+v.example+"\n", "%s -h lacks its example %q", v.name, v.example)
		assert.LessOrEqual(t, strings.Index(out.String(), "example:"), strings.Index(out.String(), "flags:"), "%s -h shows the example after the flags", v.name)
	}
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b,reader-c --members m1,m2")
	ta.ok("add --stream s1 --count 9")
	for _, v := range verbs {
		switch v.name {
		case "run", "play", "dashboard", "teardown", "fleet sync", "goal set", "goal show", "brief":
			continue // run and play tick for ever; dashboard serves until interrupted (its own tests); teardown drops the sprint; fleet sync reads a config store; a goal is set from a file; a brief is read from a file and linted
		}
		code, out, errs := ta.do(v.example)
		assert.NotEqual(t, 2, code, "%s: exit 2, the usage refusal\n%s%s", v.example, out, errs)
	}
}

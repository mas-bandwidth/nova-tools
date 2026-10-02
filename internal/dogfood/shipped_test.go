package dogfood

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The shipped set is every cmd/nova-* directory, the list `release build`
// compiles: a directory with only tests or none at all is still a tool the
// builder would try to ship, and a name outside the family is not a tool.
func TestReadShippedIsEveryNovaDirectoryUnderCmd(t *testing.T) {
	t.Parallel()

	cmd := t.TempDir()
	write := func(rel string) {
		p := filepath.Join(cmd, rel)
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
		require.NoError(t, os.WriteFile(p, []byte("package main\n"), 0o644))
	}
	write("nova-table/main.go")
	write("nova-bus/main.go")
	write("nova-left/main_test.go")
	write("helper/main.go")
	require.NoError(t, os.Mkdir(filepath.Join(cmd, "nova-empty"), 0o755))
	write("AGENTS.md")

	s, err := ReadShipped(cmd)
	require.NoError(t, err)
	want := []string{"nova-bus", "nova-empty", "nova-left", "nova-table"}
	require.Equal(t, want, s.Tools(), "shipped = %v, want %v", s.Tools(), want)
	got, err := CmdTools(cmd)
	require.True(t, err == nil && reflect.DeepEqual(got, want), "CmdTools = %v, %v, want %v: the gate and the builder read one list", got, err, want)
}

// AN I/O ERROR IS NOT A PARKED TOOL. A second tool directory that cannot be
// read refuses the whole read, naming the tool's path, rather than leaving the
// tool out while the set stays non-empty -- which set its open edge aside and
// passed the gate (Stella, #4531: Shipped:1 Outside:2 Findings:[] on a
// permission denied).
func TestReadShippedRefusesAToolDirectoryItCannotRead(t *testing.T) {
	t.Parallel()
	if os.Geteuid() == 0 {
		t.Skip("root reads a mode-000 directory; the refusal cannot be provoked")
	}

	cmd := t.TempDir()
	for _, tool := range []string{"nova-bus", "nova-example"} {
		dir := filepath.Join(cmd, tool)
		require.NoError(t, os.MkdirAll(dir, 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n"), 0o644))
	}
	locked := filepath.Join(cmd, "nova-example")
	require.NoError(t, os.Chmod(locked, 0o000))
	t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })
	if _, err := os.ReadDir(locked); err == nil {
		t.Skip("this filesystem reads a mode-000 directory; the refusal cannot be provoked")
	}

	s, err := ReadShipped(cmd)
	require.Error(t, err, "an unreadable tool directory was read as a shipped set %v", s.Tools())
	require.Contains(t, err.Error(), locked, "the refusal does not name the tool's path %s: %v", locked, err)
	_, err = CmdTools(cmd)
	require.Error(t, err, "CmdTools listed a tool directory it could not read")
}

// A scope that came back empty would set aside every receipt and pass on
// nothing, so a cmd/ with no tool, or none at all, is refused.
func TestReadShippedRefusesADirectoryWithNoTool(t *testing.T) {
	t.Parallel()

	_, err := ReadShipped(t.TempDir())
	assert.Error(t, err, "an empty cmd/ was read as a shipped set")
	_, err = ReadShipped(filepath.Join(t.TempDir(), "nowhere"))
	assert.Error(t, err, "a missing cmd/ was read as a shipped set")
}

// THE GATE JUDGES WHAT SHIPS. A parked tool's open edge and its not-ok receipt
// on an undeclared verb do not block; a shipped tool's open edge does, and so
// does a shipped tool's not-ok receipt on an undeclared verb.
func TestAParkedToolsOpenItemDoesNotBlockAndAShippedToolsDoes(t *testing.T) {
	t.Parallel()

	list := verbs("nova-table show", "nova-pulse fill")
	got := []Receipt{
		// Parked: an open edge on a verb the reference still declares, and a
		// not-ok run of a verb it does not.
		receipt("nova-pulse fill", "Stella", "2026-09-18T09:00:00Z", false, 0),
		stranded("nova-merge land", "Stella", "2026-09-18T09:01:00Z", false, "r/a.json:1"),
		// Shipped: an open edge.
		receipt("nova-table show", "Stella", "2026-09-18T09:02:00Z", false, 0),
	}
	shipped := NewShipped("nova-table", "nova-bus")

	scopedVerbs, scoped, outside := shipped.Scope(list, got)
	require.Len(t, outside, 2, "outside = %d, want the two parked receipts", len(outside))
	findings, _ := Gate(scopedVerbs, scoped, nil, false)
	require.True(t, len(findings) == 1 && findings[0].Tool == "nova-table" && findings[0].Kind == "open-edge", "findings = %+v, want exactly the shipped tool's open edge", findings)

	// The shipped tool's edge answered: nothing blocks, the parked ones are
	// still set aside rather than counted.
	answer := receipt("nova-table show", "Rowan", "2026-09-18T10:00:00Z", true, 0)
	answer.Closes = got[2].ID()
	scopedVerbs, scoped, _ = shipped.Scope(list, append(got, answer))
	findings, _ = Gate(scopedVerbs, scoped, nil, false)
	require.Empty(t, findings, "findings = %+v, want none once the shipped edge is answered", findings)

	// A shipped tool's not-ok receipt on a verb nobody declares still blocks:
	// the scope is by tool, and a misspelling inside a shipped tool is exactly
	// what the unmatched rule exists for.
	misspelt := stranded("nova-table shwo", "Stella", "2026-09-18T11:00:00Z", false, "r/b.json:1")
	scopedVerbs, scoped, _ = shipped.Scope(list, append(got, answer, misspelt))
	findings, _ = Gate(scopedVerbs, scoped, nil, false)
	require.True(t, len(findings) == 1 && findings[0].Kind == "unmatched" && findings[0].Tool == "nova-table", "findings = %+v, want the shipped tool's unmatched not-ok receipt", findings)
}

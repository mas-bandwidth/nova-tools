package scaffold

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestScaffoldWritesConfinedFiles(t *testing.T) {
	t.Parallel()

	tree := t.TempDir()
	outs := []Planned{
		{Rel: "pkg/a.txt", Data: []byte("hello a")},
		{Rel: "pkg/sub/b.txt", Data: []byte("hello b")},
	}

	written, err := Write(tree, outs)
	require.NoError(t, err, "Write: %v", err)
	require.Len(t, written, 2, "Write returned %d paths, want 2", len(written))

	for _, o := range outs {
		data, err := os.ReadFile(filepath.Join(tree, filepath.FromSlash(o.Rel)))
		assert.NoError(t, err, "read %s: %v", o.Rel, err)
		assert.Equal(t, string(o.Data), string(data), "%s = %q, want %q", o.Rel, data, o.Data)
	}

	// Overwrite refused
	_, err = Write(tree, outs)
	assert.ErrorContains(t, err, "already exists", "expected already exists error, got %v", err)
}

func TestScaffoldRefusesSymlink(t *testing.T) {
	t.Parallel()

	tree := t.TempDir()
	outside := t.TempDir()
	target := filepath.Join(outside, "secret.txt")
	_ = os.WriteFile(target, []byte("secret"), 0o644)

	link := filepath.Join(tree, "evil_link")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("cannot create symlink: %v", err)
	}

	outs := []Planned{
		{Rel: "evil_link", Data: []byte("overwrite")},
	}
	_, err := Write(tree, outs)
	assert.ErrorContains(t, err, "symlink", "expected symlink refusal, got %v", err)

	// Verify outside file was untouched
	data, _ := os.ReadFile(target)
	assert.Equal(t, "secret", string(data), "outside file was modified: %s", data)
}

func TestScaffoldRefusesADanglingFinalSymlink(t *testing.T) {
	t.Parallel()

	tree := t.TempDir()
	link := filepath.Join(tree, "dangling")
	if err := os.Symlink("nonexistent", link); err != nil {
		t.Skipf("cannot create symlink: %v", err)
	}

	outs := []Planned{
		{Rel: "dangling", Data: []byte("hello")},
	}
	_, err := Write(tree, outs)
	require.ErrorContains(t, err, "symlink", "expected symlink refusal for dangling symlink, got %v", err)
}

func TestScaffoldRefusesASymlinkedParentDirectory(t *testing.T) {
	t.Parallel()

	tree := t.TempDir()
	realDir := t.TempDir()
	link := filepath.Join(tree, "linked_dir")
	if err := os.Symlink(realDir, link); err != nil {
		t.Skipf("cannot create symlink: %v", err)
	}

	outs := []Planned{
		{Rel: "linked_dir/file.txt", Data: []byte("hello")},
	}
	_, err := Write(tree, outs)
	require.ErrorContains(t, err, "symlink", "expected symlink refusal for symlinked parent dir, got %v", err)
}

func TestScaffoldNeverOverwritesACollisionRacedInAfterThePreflight(t *testing.T) {
	tree := t.TempDir()
	outs := []Planned{
		{Rel: "pkg/raced.txt", Data: []byte("new content")},
	}
	BeforeCreate = func(rel string) {
		p := filepath.Join(tree, filepath.FromSlash(rel))
		_ = os.WriteFile(p, []byte("pre-existing content"), 0o644)
	}
	defer func() { BeforeCreate = nil }()

	_, err := Write(tree, outs)
	require.ErrorContains(t, err, "appeared while scaffold was writing", "expected raced collision refusal, got %v", err)
	data, _ := os.ReadFile(filepath.Join(tree, "pkg", "raced.txt"))
	require.Equal(t, "pre-existing content", string(data), "file overwritten despite race: %s", data)
}

func TestScaffoldRuleAndVerbBasic(t *testing.T) {
	t.Parallel()

	tree := t.TempDir()
	_ = os.WriteFile(filepath.Join(tree, "go.mod"), []byte("module test"), 0o644)
	writeTool(t, tree, "nova-ci")

	// Rule
	writtenRule, err := Rule(tree, "sample")
	require.NoError(t, err, "Rule failed: %v", err)
	require.Len(t, writtenRule, 3, "Rule wrote %d files, want 3", len(writtenRule))

	// Verb
	writtenVerb, err := Verb(tree, "nova-ci", "sample")
	require.NoError(t, err, "Verb failed: %v", err)
	require.Len(t, writtenVerb, 4, "Verb wrote %d files, want 4", len(writtenVerb))
}

// writeTool lays down cmd/<tool>/main.go with a func main, the least a tool
// needs before new-verb may add a verb to it.
func writeTool(t *testing.T, tree, tool string) {
	t.Helper()
	dir := filepath.Join(tree, "cmd", tool)
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n\nfunc main() {}\n"), 0o644))
}

// A verb scaffolded into a directory with no func main is a package main with
// no entry point, so the tree stops building (rowan hold 6 on #3616, item 1):
// Verb refuses, and writes nothing, for a missing tool, a tool whose only
// main is in a _test.go file, a library package, and a method named main.
func TestVerbRefusesAToolWithNoMain(t *testing.T) {
	t.Parallel()

	for name, files := range map[string]map[string]string{
		"missing":   nil,
		"test-only": {"x_test.go": "package main\n\nfunc main() {}\n"},
		"library":   {"lib.go": "package lib\n\nfunc main() {}\n"},
		"method":    {"m.go": "package main\n\ntype T struct{}\n\nfunc (T) main() {}\n"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			tree := t.TempDir()
			_ = os.WriteFile(filepath.Join(tree, "go.mod"), []byte("module test"), 0o644)
			for f, src := range files {
				_ = os.MkdirAll(filepath.Join(tree, "cmd", "tool"), 0o755)
				_ = os.WriteFile(filepath.Join(tree, "cmd", "tool", f), []byte(src), 0o644)
			}
			written, err := Verb(tree, "tool", "probe")
			require.ErrorContains(t, err, "no func main", "Verb into a tool with no func main: err %v, want a no-func-main refusal", err)
			require.Empty(t, written, "Verb wrote %v despite refusing", written)
			_, err = os.Stat(filepath.Join(tree, "cmd", "tool", "probe.go"))
			require.Error(t, err, "cmd/tool/probe.go was written despite the refusal")
		})
	}
}

// new-verb never edits the tool's dispatch switch; it prints the exact case to
// add (rowan hold 6 on #3616, item 4). The case calls the function the verb
// template declares.
func TestDispatchIsTheCaseTheScaffoldedVerbNeeds(t *testing.T) {
	t.Parallel()

	want := "case \"my-verb\":\n\treturn cmdMyVerb(args[1:], stdout, stderr)"
	require.Equal(t, want, Dispatch("my-verb"), "Dispatch(my-verb)")
	src, err := Render(verbTemplates, "templates/verb/verb.go.tmpl", verbData{Tool: "t", Verb: "my-verb", CamelVerb: toCamel("my-verb")})
	require.NoError(t, err)
	require.Contains(t, string(src), "func cmdMyVerb(args []string, stdout, stderr io.Writer) int", "the verb template no longer declares the function the dispatch case calls")
}

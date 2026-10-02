package testkit_test

import (
	"io"
	"path/filepath"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/testkit"
	"github.com/stretchr/testify/assert"
)

func TestStreamsRunsAVerbThatReadsNoStdin(t *testing.T) {
	t.Parallel()
	verb := testkit.Streams(func(args []string, stdout, stderr io.Writer) int {
		_, _ = io.WriteString(stdout, args[0])
		_, _ = io.WriteString(stderr, "e")
		return 3
	})
	assert.Equal(t, testkit.Result{Code: 3, Stdout: "a", Stderr: "e"}, verb.RunIn("unread", "a"))
}

func TestMkdirAndWriteFileReturnThePathTheyMade(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "a", "b")
	assert.Equal(t, dir, testkit.Mkdir(t, dir))
	assert.DirExists(t, dir)
	path := filepath.Join(dir, "c", "d.txt")
	assert.Equal(t, path, testkit.WriteFile(t, path, "x"))
	rec := &recorder{TB: t}
	runs(rec, func() { testkit.Mkdir(rec, filepath.Join(path, "under-a-file")) })
	assert.True(t, rec.failed, "Mkdir passed a path whose parent is a file")
}

func TestVersionIsTheOneParsedLineNamingTheTool(t *testing.T) {
	t.Parallel()
	f := demo.Version(t, "nova-demo")
	assert.Empty(t, f.Extras)
	for name, m := range map[string]testkit.Main{
		"another tool":   demo,
		"two lines":      testkit.Streams(func(_ []string, o, _ io.Writer) int { _, _ = io.WriteString(o, "nova-x v1 a/b go1\nmore\n"); return 0 }),
		"not four words": testkit.Streams(func(_ []string, o, _ io.Writer) int { _, _ = io.WriteString(o, "nova-x v1\n"); return 0 }),
	} {
		tool := map[bool]string{true: "nova-other", false: "nova-x"}[name == "another tool"]
		rec := &recorder{TB: t}
		runs(rec, func() { m.Version(rec, tool) })
		assert.True(t, rec.failed, "Version passed %s", name)
	}
}

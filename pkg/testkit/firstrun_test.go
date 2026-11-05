package testkit_test

import (
	"path/filepath"
	"runtime"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/update"
	"github.com/mas-bandwidth/nova-tools/pkg/testkit"
	"github.com/stretchr/testify/assert"
)

func TestRepoRootNamesTheCheckout(t *testing.T) {
	t.Parallel()

	root := testkit.RepoRoot(t)
	assert.FileExists(t, filepath.Join(root, "docs", "TESTS.md"))
	assert.FileExists(t, filepath.Join(root, "go.mod"))
}

func TestFirstRunArgsResolvesTheManifestName(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	assert.Equal(t,
		[]string{"report", "--file", filepath.Join(dir, "versions.tsv")},
		testkit.FirstRunArgs([]string{"report", "--file", "versions.tsv"}, dir))
	assert.Equal(t,
		[]string{"report", "--file", "other.tsv"},
		testkit.FirstRunArgs([]string{"report", "--file", "other.tsv"}, dir))
}

func TestVersionLineShapeAcceptsTheOneLine(t *testing.T) {
	t.Parallel()

	line := "faketool build-id " + runtime.GOOS + "/" + runtime.GOARCH + " " + runtime.Version() + "\n"
	testkit.VersionLineShape(t, "faketool", func() (int, string, string) {
		return 0, line, ""
	})
}

func TestExecutableFirstRunRunsTheDocumentedFirstRun(t *testing.T) {
	t.Parallel()

	testkit.ExecutableFirstRun(t, "nova-update", update.Main)
}

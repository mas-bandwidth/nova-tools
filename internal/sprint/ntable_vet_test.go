package sprint

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/goenv"
)

// TestNtableTestFilesVetClean is the missed-22 gate on internal/ntable's test
// files. docs/SPEC-CI.md (`functional`, Its narrowings) makes `go vet ./...` the
// check an untagged file carries and `make vet-functional` the one that compiles
// the redis-backed `<name>_functional_test.go` on every change; this test runs
// both over ./internal/ntable/ and requires a zero exit with no finding printed,
// so a test file that only vet can refuse goes red at the sprint gate rather
// than later in CI's lint job. The amendment the card names is "trust but
// VERIFY": the ok% a friend reports is believed only against the gate that ran.
func TestNtableTestFilesVetClean(t *testing.T) {
	t.Parallel()
	root := sprintRepoRoot(t)
	for _, tags := range []string{"", "functional"} {
		out := ntableVet(t, root, tags)
		require.Empty(t, out,
			"go vet ./internal/ntable/ with -tags %q printed findings:\n%s", tags, out)
	}
}

// ntableVet runs the CI lint job's vet of ./internal/ntable/ (Makefile's `vet`
// and `vet-functional` recipes, docs/SPEC-CI.md `functional`) and returns its
// combined output; a non-zero exit fails the test with that output named.
func ntableVet(t *testing.T, root, tags string) string {
	t.Helper()
	args := []string{"vet", "-trimpath"}
	if tags != "" {
		args = append(args, "-tags", tags)
	}
	args = append(args, "./internal/ntable/")
	cmd := exec.Command("go", args...)
	cmd.Dir = root
	cmd.Env = append(goenv.Clean(os.Environ()), "GOFLAGS=-mod=readonly", "NOVA_TEST_NO_HOST=1")
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "go %s failed:\n%s", strings.Join(args, " "), string(out))
	return string(out)
}

// sprintRepoRoot is the repository root two levels up from internal/sprint,
// the way internal/ci's repoRoot reads it (docs/SPEC-CI.md, `make`: the Makefile
// is the one entry, and a gate walks the same tree).
func sprintRepoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	require.NoError(t, err)
	_, err = os.Stat(filepath.Join(root, "go.mod"))
	require.NoError(t, err, "%s is not the repository root", root)
	return root
}

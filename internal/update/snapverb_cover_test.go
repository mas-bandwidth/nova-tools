package update

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/pkg/buildinfo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// snapverbCoverLine is the version line a stub binary answers with, carrying the
// four source keys the mixed-source gate reads. The stamp is the same for every
// row, so the stamp gate never fires and the source gate is what the run tests.
func snapverbCoverLine(tool, repo, host string, dirty bool) string {
	return tool + " 20260909112233-0123456789ab linux/amd64 go1.26.0 " +
		"repo=" + repo + " revision=0123456789ab dirty=" +
		map[bool]string{true: "true", false: "false"}[dirty] + " build_host=" + host
}

// snapverbCoverStub is the fake child: it answers the `version` exec of any
// binary in the map and refuses to answer a name the map does not hold, like
// the real transport refuses a binary that is not there. No process starts.
func snapverbCoverStub(t *testing.T, lines map[string]string) processFunc {
	t.Helper()
	return func(_ context.Context, args []string, _ io.Reader, _ int) ProcessResult {
		name := filepath.Base(args[0])
		line, ok := lines[name]
		require.True(t, ok, "stub asked for a binary it does not hold: %s", name)
		return ProcessResult{Stdout: line + "\n"}
	}
}

// snapverbCoverRun runs nova-version snapshot with the fake transport and
// returns the exit code with both streams.
func snapverbCoverRun(t *testing.T, env Environment, args ...string) (int, string, string) {
	t.Helper()
	var out, errs strings.Builder
	code := Run("nova-version", args, "test", &out, &errs, env)
	return code, out.String(), errs.String()
}

// sourceString is the one line a Source reads as on a refusal: every field
// named, in the order pkg/buildinfo writes them. A row that dropped a
// field, reordered them, or printed dirty as anything but true/false would
// fail the exact match.
func TestSnapverbCoverSourceStringNamesEveryField(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		src  buildinfo.Source
		want string
	}{
		{
			name: "clean build",
			src:  buildinfo.Source{Repository: "example.com/repo-a", Revision: "0123456789ab", BuildHost: "host-one"},
			want: "repo=example.com/repo-a revision=0123456789ab dirty=false build_host=host-one",
		},
		{
			name: "dirty build",
			src:  buildinfo.Source{Repository: "example.com/repo-b", Revision: "fedcba987654", Dirty: true, BuildHost: "host-two"},
			want: "repo=example.com/repo-b revision=fedcba987654 dirty=true build_host=host-two",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, sourceString(tc.src))
		})
	}
}

// A source with nothing in it still reads as the four-field line, empty values
// and dirty=false, so a refusal that carries it is readable field by field
// rather than a shorter line the reader must re-count.
func TestSnapverbCoverSourceStringZeroValueKeepsTheLineShape(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "repo= revision= dirty=false build_host=", sourceString(buildinfo.Source{}))
}

// The mixed-source gate renders each disagreeing row with sourceString: the
// refusal names both binaries and both sources field by field, and writes no
// --out. The child is faked, so no binary is executed.
func TestSnapverbCoverMixedSourceRefusalRendersBothSources(t *testing.T) {
	t.Parallel()

	bin := t.TempDir()
	for name := range map[string]string{
		"nova-a": snapverbCoverLine("nova-a", "example.com/repo-a", "host-one", false),
		"nova-b": snapverbCoverLine("nova-b", "example.com/repo-b", "host-two", true),
	} {
		require.NoError(t, os.WriteFile(filepath.Join(bin, name), []byte("# placeholder: the fake transport reads the version\n"), 0o644))
	}
	out := filepath.Join(t.TempDir(), "snapshot.tsv")
	env := Environment{Process: snapverbCoverStub(t, map[string]string{
		"nova-a": snapverbCoverLine("nova-a", "example.com/repo-a", "host-one", false),
		"nova-b": snapverbCoverLine("nova-b", "example.com/repo-b", "host-two", true),
	})}

	code, _, stderr := snapverbCoverRun(t, env, "snapshot", "--bin", bin, "--out", out)
	require.Equal(t, 2, code, "stderr: %s", stderr)
	assert.Contains(t, stderr, "mixed source: nova-a=repo=example.com/repo-a revision=0123456789ab dirty=false build_host=host-one nova-b=repo=example.com/repo-b revision=0123456789ab dirty=true build_host=host-two")
	assert.Contains(t, stderr, "rebuild the set under one source")
	_, err := os.Stat(out)
	assert.ErrorIs(t, err, os.ErrNotExist, "a refused set was written to --out")
}

// Rows that name the same source, and a row that names none, pass the gate: a
// sourceless row contributes no opinion, so an old binary beside new ones is
// still a snapshot, and the TSV records every row's revision.
func TestSnapverbCoverSameSourceAndSourcelessRowsPassTheGate(t *testing.T) {
	t.Parallel()

	bin := t.TempDir()
	stubs := map[string]string{
		"nova-a":   snapverbCoverLine("nova-a", "example.com/repo-a", "host-one", false),
		"nova-b":   snapverbCoverLine("nova-b", "example.com/repo-a", "host-one", false),
		"nova-old": "nova-old 20260909112233-0123456789ab linux/amd64 go1.26.0",
	}
	for name := range stubs {
		require.NoError(t, os.WriteFile(filepath.Join(bin, name), []byte("# placeholder: the fake transport reads the version\n"), 0o644))
	}
	out := filepath.Join(t.TempDir(), "snapshot.tsv")
	env := Environment{Process: snapverbCoverStub(t, stubs)}

	code, stdout, stderr := snapverbCoverRun(t, env, "snapshot", "--bin", bin, "--out", out)
	require.Equal(t, 0, code, "stderr: %s", stderr)
	assert.Contains(t, stdout, "SNAPSHOT OK bin=")
	assert.Contains(t, stdout, "tools=3 stamp=20260909112233-0123456789ab")
	assert.NotContains(t, stdout+stderr, "mixed source")
	b, err := os.ReadFile(out)
	require.NoError(t, err)
	assert.Contains(t, string(b), "nova-a\t20260909112233-0123456789ab\t0123456789ab\tlinux/amd64")
	assert.Contains(t, string(b), "nova-old\t20260909112233-0123456789ab\t0123456789ab\tlinux/amd64")
}

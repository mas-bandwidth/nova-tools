package release

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/testbin"
)

// fakeSource is the checkout an incremental build asks: a head, a tree diff
// per recorded commit, and each tool's package directories.
type fakeSource struct {
	commit  string
	dirty   bool
	changed map[string][]string // keyed "<base>..<head>"
	dirs    map[string][]string // keyed "./cmd/<tool>"
	asked   []string
}

func (s *fakeSource) Head(context.Context, string) (string, bool, error) {
	if s.commit == "" {
		return "", false, os.ErrNotExist
	}
	return s.commit, !s.dirty, nil
}

func (s *fakeSource) Changed(_ context.Context, _, base, head string) ([]string, error) {
	s.asked = append(s.asked, base+".."+head)
	return s.changed[base+".."+head], nil
}

func (s *fakeSource) Packages(_ context.Context, _, _, _ string, pkgs []string) (map[string][]string, error) {
	got := map[string][]string{}
	for _, p := range pkgs {
		if d, ok := s.dirs[p]; ok {
			got[p] = d
		}
	}
	return got, nil
}

func (s *fakeSource) GoVersion(context.Context) (string, error) { return "go1.test", nil }

func TestRebuildSetChoosesTheToolsWhoseImportsChanged(t *testing.T) {
	t.Parallel()
	tools := []string{"nova-a", "nova-b", "nova-c", "nova-new", "nova-unlisted"}
	dirs := map[string][]string{
		"nova-a":   {"cmd/nova-a", "internal/x"},
		"nova-b":   {"cmd/nova-b", "internal/xy"},
		"nova-c":   {"cmd/nova-c"},
		"nova-new": {"cmd/nova-new"},
	}
	inBase := func(t string) bool { return t != "nova-new" }
	for _, tc := range []struct {
		name            string
		changed         []string
		rebuild, reused []string
	}{
		{"a dependency's file, a test elsewhere, a doc", []string{"internal/x/a.go", "internal/xy/a_test.go", "docs/CLI.md"},
			[]string{"nova-a", "nova-new", "nova-unlisted"}, []string{"nova-b", "nova-c"}},
		{"an embedded file below a dependency", []string{"internal/xy/sql/one.sql"},
			[]string{"nova-b", "nova-new", "nova-unlisted"}, []string{"nova-a", "nova-c"}},
		{"nothing that is in a binary", []string{"fleet/tools.yml", "cmd/nova-c/main_test.go"},
			[]string{"nova-new", "nova-unlisted"}, []string{"nova-a", "nova-b", "nova-c"}},
		{"the module", []string{"go.sum"},
			[]string{"nova-a", "nova-b", "nova-c", "nova-new", "nova-unlisted"}, nil},
	} {
		rebuild, reused, why := rebuildSet(tools, dirs, tc.changed, inBase)
		assert.Equal(t, tc.rebuild, rebuild, tc.name)
		assert.Equal(t, tc.reused, reused, tc.name)
		assert.Equal(t, "new", why["nova-new"], tc.name)
	}
}

func TestParsePackagesAnswersEachToolsDirectoriesInsideTheCheckout(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	real, err := filepath.EvalSymlinks(root)
	require.NoError(t, err)
	listed := strings.Join([]string{
		"fmt\t/goroot/src/fmt\ttrue\t",
		"example.com/m/internal/x\t" + filepath.Join(real, "internal", "x") + "\tfalse\tfmt",
		"github.com/other/y\t/home/u/go/pkg/mod/github.com/other/y\tfalse\t",
		"example.com/m/cmd/nova-a\t" + filepath.Join(real, "cmd", "nova-a") + "\tfalse\tfmt example.com/m/internal/x github.com/other/y",
		"example.com/m/cmd/nova-b\t" + filepath.Join(real, "cmd", "nova-b") + "\tfalse\tfmt",
	}, "\n")
	got, err := parsePackages(listed, root, []string{"./cmd/nova-a", "./cmd/nova-b"})
	require.NoError(t, err)
	assert.Equal(t, map[string][]string{
		"./cmd/nova-a": {"cmd/nova-a", "internal/x"},
		"./cmd/nova-b": {"cmd/nova-b"},
	}, got)
}

// buildAt runs one build of the fake source tree at version.
func buildAt(t *testing.T, source, out, version string, src Source, tc *fakeToolchain, extra ...string) (string, string) {
	t.Helper()
	var o, e bytes.Buffer
	args := append([]string{"build", "--version", version, "--out", out, "--source", source, "--platform", "linux-amd64"}, extra...)
	code := Run("nova-update", args, &o, &e, Deps{Toolchain: tc, Source: src})
	require.Equal(t, 0, code, "stdout:%s\nstderr:%s", o.String(), e.String())
	return o.String(), e.String()
}

func TestIncrementalBuildRebuildsOnlyWhatChangedSinceTheRecordedCommit(t *testing.T) {
	t.Parallel()
	source, out := sourceTree(t), t.TempDir()
	src := &fakeSource{commit: "c1", dirs: map[string][]string{
		"./cmd/nova-bus":    {"cmd/nova-bus", "internal/bus"},
		"./cmd/nova-worker":  {"cmd/nova-worker", "internal/member"},
		"./cmd/nova-update": {"cmd/nova-update", "internal/release"},
	}}
	first, _ := buildAt(t, source, out, "v0.1.0-dev.c1", src, &fakeToolchain{}, "--incremental")
	assert.Contains(t, first, "RELEASE BUILD WHOLE version=v0.1.0-dev.c1 platform=linux-amd64 rebuilt=3 ")

	src.commit = "c2"
	src.changed = map[string][]string{"c1..c2": {"internal/member/member.go", "internal/release/release_test.go", "docs/FLEET.md"}}
	tc := &fakeToolchain{}
	second, _ := buildAt(t, source, out, "v0.1.0-dev.c2", src, tc, "--incremental")

	assert.Equal(t, []string{"c1..c2"}, src.asked, "the diff is from the recorded commit to the head")
	require.Len(t, tc.calls, 1, "only the changed tool compiles: %v", tc.calls)
	assert.Contains(t, tc.calls[0], "./cmd/nova-worker")
	assert.Contains(t, second, "RELEASE BUILD INCREMENTAL version=v0.1.0-dev.c2 platform=linux-amd64 base=v0.1.0-dev.c1 changed=3 rebuilt=nova-worker reused=2")
	assert.Contains(t, second, "RELEASE BUILT version=v0.1.0-dev.c2 platform=linux-amd64 tools=3 verified=3 ")
	for _, tool := range []string{"nova-bus", "nova-update"} {
		was, err := os.ReadFile(filepath.Join(out, "v0.1.0-dev.c1", "linux-amd64", tool))
		require.NoError(t, err)
		now, err := os.ReadFile(filepath.Join(out, "v0.1.0-dev.c2", "linux-amd64", tool))
		require.NoError(t, err)
		assert.Equal(t, was, now, "%s is the base's bytes", tool)
	}
	rec, err := readRecord(recordPath(out, "v0.1.0-dev.c2", "linux-amd64"))
	require.NoError(t, err)
	assert.Equal(t, buildRecord{Commit: "c2", Go: "go1.test", Ldflags: ldflagsShape(), Base: "v0.1.0-dev.c1"}, rec)
	_, err = os.Stat(filepath.Join(out, "v0.1.0-dev.c2", "linux-amd64", "linux-amd64"+RecordSuffix))
	assert.True(t, os.IsNotExist(err), "the record is never inside the artifact directory")
	sums, err := os.ReadFile(filepath.Join(out, "v0.1.0-dev.c2", "linux-amd64", SumsFile))
	require.NoError(t, err)
	assert.NotContains(t, string(sums), RecordSuffix)
}

func TestIncrementalBuildIsWholeWhenItCannotTrustTheBase(t *testing.T) {
	t.Parallel()
	source, out := sourceTree(t), t.TempDir()
	src := &fakeSource{commit: "c1", dirty: true}
	first, _ := buildAt(t, source, out, "v0.1.0-dev.c1", src, &fakeToolchain{}, "--incremental")
	assert.Contains(t, first, "RELEASE BUILD WHOLE version=v0.1.0-dev.c1 platform=linux-amd64 rebuilt=3 reason=the\\x20checkout\\x20has\\x20uncommitted\\x20changes")

	// The dirty build recorded no commit, so it is no base for the next one.
	src.commit, src.dirty = "c2", false
	tc := &fakeToolchain{}
	second, _ := buildAt(t, source, out, "v0.1.0-dev.c2", src, tc, "--incremental")
	assert.Contains(t, second, "RELEASE BUILD WHOLE version=v0.1.0-dev.c2 platform=linux-amd64 rebuilt=3 reason=no\\x20earlier\\x20build")
	assert.Len(t, tc.calls, 3)
	assert.Empty(t, src.asked)

	// A base whose bytes changed after it was built is no base either.
	require.NoError(t, os.WriteFile(filepath.Join(out, "v0.1.0-dev.c2", "linux-amd64", "nova-bus"), []byte("tampered"), 0o755))
	src.commit = "c3"
	third, _ := buildAt(t, source, out, "v0.1.0-dev.c3", src, &fakeToolchain{}, "--incremental")
	assert.Contains(t, third, "RELEASE BUILD WHOLE version=v0.1.0-dev.c3 platform=linux-amd64 rebuilt=3 reason=the\\x20base\\x20v0.1.0-dev.c2\\x20does\\x20not\\x20verify")
}

func TestABuildWithoutIncrementalBuildsEverythingAndStillRecords(t *testing.T) {
	t.Parallel()
	source, out := sourceTree(t), t.TempDir()
	src := &fakeSource{commit: "c1"}
	buildAt(t, source, out, "v0.1.0-dev.c1", src, &fakeToolchain{})
	src.commit = "c2"
	tc := &fakeToolchain{}
	o, _ := buildAt(t, source, out, "v0.1.0-dev.c2", src, tc)
	assert.Len(t, tc.calls, 3)
	assert.NotContains(t, o, "RELEASE BUILD INCREMENTAL")
	assert.NotContains(t, o, "RELEASE BUILD WHOLE")
	rec, err := readRecord(recordPath(out, "v0.1.0-dev.c2", "linux-amd64"))
	require.NoError(t, err)
	assert.Equal(t, "c2", rec.Commit)
}

func TestBuildGateReportPrintsTheOpenEdgesAndBuilds(t *testing.T) {
	t.Parallel()
	cli, receipts := noOpenEdge(t)
	verdict := func(cli, receipts, cmd string) (DogfoodVerdict, error) {
		return DogfoodVerdict{Verbs: 9, Open: 2, Findings: []string{"DOGFOOD GATE FAIL tool=a verb=b: one", "DOGFOOD GATE FAIL tool=c verb=d: two"}}, nil
	}
	run := func(extra ...string) (int, string, string, *fakeToolchain) {
		tc := &fakeToolchain{}
		var o, e bytes.Buffer
		args := append([]string{"build", "--version", "v0.1.0", "--out", t.TempDir(), "--source", sourceTree(t), "--cli", cli, "--receipts", receipts}, extra...)
		code := Run("nova-update", args, &o, &e, Deps{Toolchain: tc, Dogfood: verdict, Source: &fakeSource{commit: "c1"}})
		return code, o.String(), e.String(), tc
	}

	code, o, e, tc := run("--gate", "report", "--reason", "machinery install of the member fix")
	require.Equal(t, 0, code, e)
	assert.Contains(t, o, "RELEASE BUILD DOGFOOD REPORTED open=2 reason=machinery\\x20install\\x20of\\x20the\\x20member\\x20fix")
	assert.Contains(t, o, "RELEASE BUILD OK version=v0.1.0 ")
	assert.Contains(t, o, " dogfood=report ")
	assert.Contains(t, e, "DOGFOOD GATE FAIL tool=a verb=b: one")
	assert.Len(t, tc.calls, 3)

	for _, refused := range [][]string{
		{"--gate", "report"},
		{"--gate", "report", "--reason", "x", "--no-dogfood-gate"},
		{"--gate", "lenient", "--reason", "x"},
		{}, // the default refuses on the open edges
	} {
		code, _, e, tc := run(refused...)
		assert.Equal(t, 2, code, "%v: %s", refused, e)
		assert.Empty(t, tc.calls, "%v compiled before refusing", refused)
	}
}

func TestCutHasNoReportGate(t *testing.T) {
	t.Parallel()
	var o, e bytes.Buffer
	code := Run("nova-update", cutArgs(changelogIn(t, t.TempDir()), "--gate", "report", "--reason", "x"), &o, &e, cutDeps(t, cutForge()))
	assert.Equal(t, 2, code)
	assert.Contains(t, e.String(), "unknown flag --gate")
}

func TestInstallSkipsAToolThatAlreadyHoldsTheBytes(t *testing.T) {
	t.Parallel()
	source, out, bin := sourceTree(t), t.TempDir(), t.TempDir()
	buildAt(t, source, out, "v0.1.0", &fakeSource{commit: "c1"}, &fakeToolchain{})
	dir := ArtifactDir(out, "v0.1.0", "linux", "amd64")
	require.NoError(t, testbin.Place(filepath.Join(dir, "nova-bus"), filepath.Join(bin, "nova-bus")))
	before, err := os.Stat(filepath.Join(bin, "nova-bus"))
	require.NoError(t, err)

	var o, e bytes.Buffer
	code := Run("nova-update", []string{"install", "--from", out, "--version", "v0.1.0", "--bin", bin, "--platform", "linux-amd64"}, &o, &e,
		Deps{VersionOf: func(context.Context, string) (string, error) { return "nova-bus v0.0.9", nil }})
	require.Equal(t, 0, code, e.String())
	assert.Contains(t, o.String(), "RELEASE INSTALLED version=v0.1.0 tools=2 skipped=1 ")
	after, err := os.Stat(filepath.Join(bin, "nova-bus"))
	require.NoError(t, err)
	assert.True(t, os.SameFile(before, after), "the same bytes were renamed over the running binary")
}

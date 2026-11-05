package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func sum(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }

// fakeRelease is a release held in memory: asset name to bytes, and a SHA256SUMS over them
// unless sums is set.
type fakeRelease struct {
	tag    string
	assets map[string][]byte
	sums   []byte
	opened []string
}

func (f *fakeRelease) Tag() string { return f.tag }
func (f *fakeRelease) Sums(context.Context) ([]byte, error) {
	if f.sums != nil {
		return f.sums, nil
	}
	var lines []string
	for name, b := range f.assets {
		lines = append(lines, sum(b)+"  "+name)
	}
	sort.Strings(lines)
	return []byte(strings.Join(lines, "\n") + "\n"), nil
}
func (f *fakeRelease) Open(_ context.Context, asset string) (io.ReadCloser, error) {
	b, ok := f.assets[asset]
	if !ok {
		return nil, errors.New("no asset " + asset)
	}
	f.opened = append(f.opened, asset)
	return io.NopCloser(bytes.NewReader(b)), nil
}

func newRelease(tag string, tools ...string) *fakeRelease {
	r := &fakeRelease{tag: tag, assets: map[string][]byte{}}
	for _, t := range tools {
		for _, p := range []string{"linux_amd64", "darwin_arm64", "windows_amd64.exe"} {
			r.assets[fmt.Sprintf("%s_%s_%s", t, tag, p)] = []byte(t + " " + tag + " " + p)
		}
	}
	return r
}

func testEnv(rel *fakeRelease, out, errs *bytes.Buffer) env {
	return env{stdout: out, stderr: errs, goos: "linux", goarch: "amd64",
		forge: func(_ context.Context, repo, tag string) (Release, error) {
			if tag != "" && tag != rel.tag {
				return nil, errors.New("no release " + tag)
			}
			return rel, nil
		},
		exe: func() (string, error) { return "", errors.New("no executable in a test") }}
}

func write(t *testing.T, path string, b []byte) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, b, 0o755))
}

func TestReportComparesBytesWithTheRelease(t *testing.T) {
	t.Parallel()
	bin := t.TempDir()
	rel := newRelease("v1.7.0", "nova-bus", "nova-config", "nova-update")
	write(t, filepath.Join(bin, "nova-bus"), rel.assets["nova-bus_v1.7.0_linux_amd64"])
	write(t, filepath.Join(bin, "nova-config"), []byte("older"))
	write(t, filepath.Join(bin, "nova-swarm"), []byte("retired"))
	write(t, filepath.Join(bin, "nova-sprint.prev"), []byte("not a tool"))
	var out, errs bytes.Buffer
	code := run(context.Background(), []string{"--bin", bin}, testEnv(rel, &out, &errs))
	assert.Equal(t, 1, code, "a stale tool is a NO; errs=%s", errs.String())
	got := out.String()
	assert.Contains(t, got, "TOOL name=nova-bus state=current")
	assert.Contains(t, got, "TOOL name=nova-config state=stale")
	assert.Contains(t, got, "TOOL name=nova-update state=missing installed=none")
	assert.Contains(t, got, "TOOL name=nova-swarm state=unreleased")
	assert.NotContains(t, got, "nova-sprint.prev")
	assert.Contains(t, got, "REPORT version=v1.7.0 platform=linux-amd64 bin="+bin+" current=1 stale=1 missing=1 unreleased=1\n")
	assert.Empty(t, rel.opened, "report downloaded a binary")

	out.Reset()
	write(t, filepath.Join(bin, "nova-config"), rel.assets["nova-config_v1.7.0_linux_amd64"])
	assert.Equal(t, 0, run(context.Background(), []string{"status", "--bin", bin}, testEnv(rel, &out, &errs)))
}

func TestInstallOneOrAll(t *testing.T) {
	t.Parallel()
	bin := t.TempDir()
	rel := newRelease("v1.7.0", "nova-bus", "nova-config")
	write(t, filepath.Join(bin, "nova-bus"), []byte("old"))
	var out, errs bytes.Buffer
	e := testEnv(rel, &out, &errs)

	require.Equal(t, 0, run(context.Background(), []string{"install", "nova-bus", "--bin", bin}, e), errs.String())
	assert.Equal(t, rel.assets["nova-bus_v1.7.0_linux_amd64"], read(t, filepath.Join(bin, "nova-bus")))
	assert.NoFileExists(t, filepath.Join(bin, "nova-config"), "install of one tool installed another")

	out.Reset()
	require.Equal(t, 0, run(context.Background(), []string{"apply", "--bin", bin}, e), errs.String())
	assert.Contains(t, out.String(), "INSTALLED version=v1.7.0 tools=1 skipped=1 ")
	assert.Equal(t, rel.assets["nova-config_v1.7.0_linux_amd64"], read(t, filepath.Join(bin, "nova-config")))
	leftovers, err := filepath.Glob(filepath.Join(bin, ".*"))
	require.NoError(t, err)
	assert.Empty(t, leftovers, "staged or aside files were left behind")

	out.Reset()
	assert.Equal(t, 2, run(context.Background(), []string{"install", "nova-nothing", "--bin", bin}, e))
	assert.Equal(t, 2, run(context.Background(), []string{"install", "--version", "v9.9.9", "--bin", bin}, e))
}

func TestInstallRefusesBytesThatAreNotTheReleasesAndReplacesNothing(t *testing.T) {
	t.Parallel()
	bin := t.TempDir()
	rel := newRelease("v1.7.0", "nova-bus", "nova-config")
	rel.sums, _ = (&fakeRelease{assets: rel.assets}).Sums(context.Background())
	rel.assets["nova-config_v1.7.0_linux_amd64"] = []byte("tampered") // after the sums
	write(t, filepath.Join(bin, "nova-bus"), []byte("old"))
	var out, errs bytes.Buffer
	code := run(context.Background(), []string{"install", "--bin", bin}, testEnv(rel, &out, &errs))
	assert.Equal(t, 1, code)
	assert.Contains(t, errs.String(), "INSTALL FAILED")
	assert.Contains(t, errs.String(), "not the release's")
	assert.Equal(t, []byte("old"), read(t, filepath.Join(bin, "nova-bus")), "a tool was replaced before the whole set verified")
	leftovers, _ := filepath.Glob(filepath.Join(bin, ".*")) // ignored: an empty glob is the assertion
	assert.Empty(t, leftovers)
}

func TestInstallDryRunChangesNothing(t *testing.T) {
	t.Parallel()
	bin := t.TempDir()
	rel := newRelease("v1.7.0", "nova-bus")
	var out, errs bytes.Buffer
	require.Equal(t, 0, run(context.Background(), []string{"install", "--dry-run", "--bin", bin}, testEnv(rel, &out, &errs)))
	assert.Equal(t, "WOULD INSTALL nova-bus\n", out.String())
	assert.NoFileExists(t, filepath.Join(bin, "nova-bus"))
}

func TestWindowsAssetsInstallWithTheirSuffix(t *testing.T) {
	t.Parallel()
	bin := t.TempDir()
	rel := newRelease("v1.7.0", "nova-bus")
	var out, errs bytes.Buffer
	e := testEnv(rel, &out, &errs)
	e.goos = "windows"
	require.Equal(t, 0, run(context.Background(), []string{"install", "--bin", bin}, e), errs.String())
	assert.Equal(t, rel.assets["nova-bus_v1.7.0_windows_amd64.exe"], read(t, filepath.Join(bin, "nova-bus.exe")))
}

// builtRelease writes a release as a build leaves it on disk: <root>/<tag>/<platform>/ and SHA256SUMS.
func builtRelease(t *testing.T, root, tag, platform string, files map[string]string) {
	t.Helper()
	dir := filepath.Join(root, tag, platform)
	var lines []string
	for name, body := range files {
		write(t, filepath.Join(dir, name), []byte(body))
		lines = append(lines, sum([]byte(body))+"  "+name)
	}
	sort.Strings(lines)
	write(t, filepath.Join(dir, sumsFile), []byte(strings.Join(lines, "\n")+"\n"))
}

func TestReleaseInstallFromABuildOnDisk(t *testing.T) {
	t.Parallel()
	root, bin, old := t.TempDir(), t.TempDir(), t.TempDir()
	builtRelease(t, root, "v1.7.0", "linux-amd64", map[string]string{"nova-bus": "bus 1.7", "nova-update": "update 1.7"})
	for _, v := range []string{"v1.6.1", "v1.6.2", "v1.6.3", "v1.6.10", "v1.6.9", "notes"} {
		require.NoError(t, os.MkdirAll(filepath.Join(root, v), 0o755))
	}
	write(t, filepath.Join(bin, "nova-update"), []byte("update 1.7"))
	write(t, filepath.Join(old, "nova-bus"), []byte("stale copy"))
	var out, errs bytes.Buffer
	e := testEnv(nil, &out, &errs)
	args := []string{"release", "install", "--from", root, "--version", "v1.7.0", "--bin", bin, "--platform", "linux-amd64", "--retire", old}
	require.Equal(t, 0, run(context.Background(), args, e), errs.String())
	assert.Equal(t, "RELEASE INSTALLED version=v1.7.0 tools=1 skipped=1 retired=1 bin="+bin+" platform=linux-amd64 pruned=1 prune-failed=0\n", out.String())
	assert.Equal(t, []byte("bus 1.7"), read(t, filepath.Join(bin, "nova-bus")))
	assert.NoFileExists(t, filepath.Join(old, "nova-bus"))
	assert.NoDirExists(t, filepath.Join(root, "v1.6.1"), "the oldest release was kept past the newest four")
	for _, kept := range []string{"v1.7.0", "v1.6.10", "v1.6.9", "v1.6.3", "v1.6.2", "notes"} {
		assert.DirExists(t, filepath.Join(root, kept))
	}

	out.Reset()
	require.Equal(t, 0, run(context.Background(), args[:len(args)-2], e))
	assert.Contains(t, out.String(), "tools=0 skipped=2", "an install over itself must replace nothing")
}

func TestReleaseInstallRefusals(t *testing.T) {
	t.Parallel()
	root, bin := t.TempDir(), t.TempDir()
	builtRelease(t, root, "v1.7.0", "linux-amd64", map[string]string{"nova-bus": "bus"})
	write(t, filepath.Join(root, "v1.7.0", "linux-amd64", "nova-bus"), []byte("swapped after the sums"))
	var out, errs bytes.Buffer
	e := testEnv(nil, &out, &errs)
	base := []string{"release", "install", "--from", root, "--version", "v1.7.0", "--bin", bin}
	assert.Equal(t, 1, run(context.Background(), base, e), "a swapped binary installed")
	assert.NoFileExists(t, filepath.Join(bin, "nova-bus"))
	assert.Equal(t, 2, run(context.Background(), base[:len(base)-2], e), "no --bin")
	assert.Equal(t, 2, run(context.Background(), append(base, "--retire", bin), e), "--retire is --bin")
	assert.Equal(t, 2, run(context.Background(), append(base, "--platform", "linux"), e))
	assert.Equal(t, 2, run(context.Background(), []string{"release", "build"}, e), "cutting is not this tool's")
}

func TestParseSumsRefusesPaths(t *testing.T) {
	t.Parallel()
	good := strings.Repeat("a", 64)
	_, err := parseSums([]byte(good + "  nova-bus\n"))
	require.NoError(t, err)
	for _, bad := range []string{good + "  ../nova-bus", good + "  dir/nova-bus", "abc  nova-bus", good + " nova-bus", "", strings.Repeat("z", 64) + "  nova-bus"} {
		_, err := parseSums([]byte(bad))
		assert.Error(t, err, "%q", bad)
	}
}

func TestVersionAndHelp(t *testing.T) {
	t.Parallel()
	var out, errs bytes.Buffer
	require.Equal(t, 0, run(context.Background(), []string{"version"}, testEnv(nil, &out, &errs)))
	assert.Regexp(t, `^nova-update (devel|v\S+) \S+/\S+ go`, out.String(), "callers read the second token")
	out.Reset()
	require.Equal(t, 0, run(context.Background(), []string{"help"}, testEnv(nil, &out, &errs)))
	assert.Contains(t, out.String(), "nova-update release install --from")
	assert.Equal(t, 2, run(context.Background(), []string{"check"}, testEnv(nil, &out, &errs)))
}

func read(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	require.NoError(t, err)
	return b
}

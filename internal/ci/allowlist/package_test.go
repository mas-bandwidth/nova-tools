package allowlist

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var packageLedgerOptions = Options{Ceiling: true, Counted: true}

func writePackageShard(t *testing.T, dir, rel, text string) string {
	t.Helper()
	file := filepath.Join(dir, filepath.FromSlash(rel))
	require.NoError(t, os.MkdirAll(filepath.Dir(file), 0o755))
	require.NoError(t, os.WriteFile(file, []byte(text), 0o644))
	return file
}

func loadPackageFixture(t *testing.T, dir string) *Packages {
	t.Helper()
	p, err := LoadPackages(dir, packageLedgerOptions)
	require.NoError(t, err)
	return p
}

// Counted package shards retain full source keys, including non-Go scripts.
// The reserved root name cannot alias a real @root directory.
func TestPackagesLoadAndEncodeSourceDirectories(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writePackageShard(t, dir, "cmd/nova-bus.txt", "# ceiling: 1\ncmd/nova-bus/main.go:run:blank 2 known sites\n")
	writePackageShard(t, dir, "scripts.txt", "# ceiling: 1\nscripts/bench.sh:or-true 1 known script\n")
	writePackageShard(t, dir, "cmd/worker.v2.txt", "# ceiling: 1\ncmd/worker.v2/main.go:run:blank 1 dotted directory\n")
	writePackageShard(t, dir, "@root.txt", "# ceiling: 1\nroot.go:run:blank 1 root site\n")
	writePackageShard(t, dir, "@@root.txt", "# ceiling: 1\n@root/fixture.go:run:blank 1 escaped directory\n")
	writePackageShard(t, dir, "@@@root.txt", "# ceiling: 1\n@@root/fixture.go:run:blank 1 twice escaped directory\n")
	p := loadPackageFixture(t, dir)
	require.Len(t, p.Rows(), 6)
	n, ok := p.Ceiling()
	require.True(t, ok)
	assert.Equal(t, 6, n)
	for key, want := range map[string]int{
		"cmd/nova-bus/main.go:run:blank":  2,
		"scripts/bench.sh:or-true":        1,
		"cmd/worker.v2/main.go:run:blank": 1,
		"root.go:run:blank":               1,
		"@root/fixture.go:run:blank":      1,
		"@@root/fixture.go:run:blank":     1,
	} {
		assert.True(t, p.Has(key), "key %q", key)
		assert.Equal(t, want, p.Count(key), "key %q", key)
	}
	assert.Len(t, p.Lists(), 6)
}

func TestPackagesExplicitPackageKeysKeepTheirOwner(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writePackageShard(t, dir, "cmd/nova-ci.txt", "# ceiling: 1\ncmd/nova-ci:assert 2 direct package\n")
	writePackageShard(t, dir, "internal/sprint/store.txt", "# ceiling: 1\ninternal/sprint/store:env 1 nested package\n")
	writePackageShard(t, dir, "@root.txt", "# ceiling: 1\n.:assert 1 root package\n")
	writePackageShard(t, dir, "@@root.txt", "# ceiling: 1\n@root:env 1 escaped package\n")
	p, err := LoadPackages(dir, Options{Ceiling: true, Counted: true, PackageKeys: true})
	require.NoError(t, err)
	assert.Len(t, p.Rows(), 4)
	assert.Equal(t, 2, p.Count("cmd/nova-ci:assert"))
	assert.Equal(t, 1, p.Count("internal/sprint/store:env"))
	assert.Equal(t, 1, p.Count(".:assert"))
	assert.Equal(t, 1, p.Count("@root:env"))
	_, err = LoadPackages(dir, packageLedgerOptions)
	require.Error(t, err, "file-key mode must not conflate a package with its parent")
}

func TestPackagesShardPathNamesExistingAndProspectiveShard(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	existing := writePackageShard(t, dir, "internal/ci.txt", "# ceiling: 1\ninternal/ci/check.go:f:blank 1 existing\n")
	p := loadPackageFixture(t, dir)

	got, err := p.ShardPath("internal/ci/check.go:f:blank")
	require.NoError(t, err)
	assert.Equal(t, existing, got)

	got, err = p.ShardPath("cmd/nova-ci/main.go:f:blank")
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(dir, "cmd", "nova-ci.txt"), got)
	_, err = os.Stat(got)
	assert.ErrorIs(t, err, os.ErrNotExist)

	_, err = p.ShardPath("/outside.go:f:blank")
	require.Error(t, err)
}

// Growth in B must not let UPDATE shrink A before it discovers the refusal.
func TestPackagesUpdatePreflightsEveryShardBeforeWriting(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	a := writePackageShard(t, dir, "a.txt", "# ceiling: 1\na/a.go:f:blank 3 existing reason\n")
	b := writePackageShard(t, dir, "b.txt", "# ceiling: 1\nb/b.go:g:blank 1 existing reason\n")
	beforeA, beforeB := readBack(t, a), readBack(t, b)
	p := loadPackageFixture(t, dir)
	r := &recorder{}
	res := CheckPackagesCountedMode(r, p, map[string]int{
		"a/a.go:f:blank": 2,
		"b/b.go:g:blank": 2,
	}, true)
	assert.False(t, res.Updated)
	assert.Len(t, res.Over, 1)
	assert.Contains(t, strings.Join(r.lines, "\n"), filepath.Join(dir, "b.txt"))
	assert.Contains(t, strings.Join(r.lines, "\n"), "refuses to raise a count")
	assert.Equal(t, beforeA, readBack(t, a))
	assert.Equal(t, beforeB, readBack(t, b))
}

// An unchanged shard keeps its inode, bytes and timestamp while a neighboring
// debt count falls. A second UPDATE has nothing to rewrite or report.
func TestPackagesUpdateTouchesOnlyChangedShard(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	a := writePackageShard(t, dir, "a.txt", "# ceiling: 1\na/a.go:f:blank 3 existing reason\n")
	b := writePackageShard(t, dir, "b.txt", "# ceiling: 1\nb/b.go:g:blank 1 existing reason\n")
	oldB, err := os.Stat(b)
	require.NoError(t, err)
	beforeB := readBack(t, b)
	r := &recorder{}
	measured := map[string]int{"a/a.go:f:blank": 2, "b/b.go:g:blank": 1}
	res := CheckPackagesCountedMode(r, loadPackageFixture(t, dir), measured, true)
	assert.True(t, res.Updated)
	assert.Equal(t, 1, r.count(UpdatedRerun))
	assert.Equal(t, "# ceiling: 1\na/a.go:f:blank 2 existing reason\n", readBack(t, a))
	newB, err := os.Stat(b)
	require.NoError(t, err)
	assert.Equal(t, beforeB, readBack(t, b))
	assert.True(t, os.SameFile(oldB, newB))
	assert.True(t, oldB.ModTime().Equal(newB.ModTime()))
	r = &recorder{}
	res = CheckPackagesCountedMode(r, loadPackageFixture(t, dir), measured, true)
	assert.False(t, res.Updated)
	assert.Empty(t, r.lines)
}

func TestPackagesUpdateMayRemoveRowsToFitCeiling(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	file := writePackageShard(t, dir, "a.txt", "# ceiling: 1\na/a.go:f:blank 1 keep\na/b.go:f:blank 1 stale\n")
	measured := map[string]int{"a/a.go:f:blank": 1}
	outside := &recorder{}
	res := CheckPackagesCountedMode(outside, loadPackageFixture(t, dir), measured, false)
	assert.False(t, res.Updated)
	assert.Contains(t, strings.Join(outside.lines, "\n"), "over its ceiling")
	updated := &recorder{}
	res = CheckPackagesCountedMode(updated, loadPackageFixture(t, dir), measured, true)
	assert.True(t, res.Updated)
	assert.Equal(t, 1, updated.count(UpdatedRerun))
	assert.NotContains(t, strings.Join(updated.lines, "\n"), "over its ceiling")
	assert.Equal(t, "# ceiling: 1\na/a.go:f:blank 1 keep\n", readBack(t, file))
}

func TestPackagesUpdateRefusesStillOverfullWithoutAnyShardWrite(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	a := writePackageShard(t, dir, "a.txt", "# ceiling: 1\na/a.go:f:blank 1 keep\na/b.go:f:blank 1 keep\n")
	b := writePackageShard(t, dir, "b.txt", "# ceiling: 1\nb/a.go:f:blank 2 lower\n")
	beforeA, beforeB := readBack(t, a), readBack(t, b)
	r := &recorder{}
	res := CheckPackagesCountedMode(r, loadPackageFixture(t, dir), map[string]int{
		"a/a.go:f:blank": 1,
		"a/b.go:f:blank": 1,
		"b/a.go:f:blank": 1,
	}, true)
	assert.False(t, res.Updated)
	assert.Contains(t, strings.Join(r.lines, "\n"), "would hold 2 rows, over its ceiling of 1")
	assert.Equal(t, beforeA, readBack(t, a))
	assert.Equal(t, beforeB, readBack(t, b))
}

func TestPackagesRejectWrongShardDuplicateAndSymlink(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, file, text string }{
		{"wrong shard", "a.txt", "# ceiling: 1\nb/b.go:f:blank 1 wrong owner\n"},
		{"duplicate", "a.txt", "# ceiling: 2\na/a.go:f:blank 1 one\na/a.go:f:blank 1 two\n"},
		{"no ceiling", "a.txt", "a/a.go:f:blank 1 no ceiling\n"},
		{"uncanonical root", "a/@root.txt", "# ceiling: 0\n"},
		{"unsorted", "a.txt", "# ceiling: 2\na/z.go:f:blank 1 z\na/a.go:f:blank 1 a\n"},
		{"empty kind", "@root.txt", "# ceiling: 1\nroot.go: 1 invalid\n"},
		{"drive path", "@root.txt", "# ceiling: 1\nC:/outside.go:kind 1 invalid\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			writePackageShard(t, dir, tc.file, tc.text)
			_, err := LoadPackages(dir, packageLedgerOptions)
			require.Error(t, err)
		})
	}
	dir := t.TempDir()
	require.NoError(t, os.Symlink(t.TempDir(), filepath.Join(dir, "linked")))
	_, err := LoadPackages(dir, packageLedgerOptions)
	require.Error(t, err)
}

func TestPackagesMalformedShardRejectedAtLoad(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	good := writePackageShard(t, dir, "a.txt", "# ceiling: 1\na/a.go:f:blank 2 lower\n")
	bad := writePackageShard(t, dir, "b.txt", "# ceiling: 1\nb/b.go: 1 malformed\n")
	beforeGood, beforeBad := readBack(t, good), readBack(t, bad)
	_, err := LoadPackages(dir, packageLedgerOptions)
	require.Error(t, err)
	assert.Equal(t, beforeGood, readBack(t, good))
	assert.Equal(t, beforeBad, readBack(t, bad))
}

func TestPackagesMalformedMeasuredKeyRefusesEveryShardWrite(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	a := writePackageShard(t, dir, "a.txt", "# ceiling: 1\na/a.go:f:blank 2 lower\n")
	before := readBack(t, a)
	r := &recorder{}
	res := CheckPackagesCountedMode(r, loadPackageFixture(t, dir), map[string]int{
		"a/a.go:f:blank":     1,
		"C:/outside.go:kind": 1,
	}, true)
	assert.False(t, res.Updated)
	assert.Contains(t, strings.Join(r.lines, "\n"), "invalid package ledger key")
	assert.Equal(t, before, readBack(t, a))
}

func TestPackagesRejectMalformedExplicitPackageKeys(t *testing.T) {
	t.Parallel()
	for _, key := range []string{"cmd/nova-ci:", "C:/outside:assert", "../outside:env", "/absolute:assert", "cmd/nova-ci:assert:other"} {
		t.Run(key, func(t *testing.T) {
			t.Parallel()
			_, err := packageFromPackageKey(key)
			require.Error(t, err, "the key grammar must fail before shard ownership is checked")
			dir := t.TempDir()
			writePackageShard(t, dir, "cmd/nova-ci.txt", "# ceiling: 1\n"+key+" 1 invalid\n")
			_, err = LoadPackages(dir, Options{Ceiling: true, Counted: true, PackageKeys: true})
			require.Error(t, err)
		})
	}
}

func TestPackagesMissingDirectoryIsEmptyAndCannotGrow(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "absent")
	p := loadPackageFixture(t, dir)
	n, ok := p.Ceiling()
	require.True(t, ok)
	assert.Zero(t, n)
	r := &recorder{}
	res := CheckPackagesCountedMode(r, p, map[string]int{"cmd/new/new.go:f:blank": 1}, true)
	assert.False(t, res.Updated)
	assert.Len(t, res.Unlisted, 1)
	assert.Contains(t, strings.Join(r.lines, "\n"), filepath.Join(dir, "cmd", "new.txt"))
	assert.Equal(t, 1, r.count("refuses to grow"))
	_, err := os.Stat(dir)
	assert.ErrorIs(t, err, os.ErrNotExist)
}

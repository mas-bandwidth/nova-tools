package release

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestBuildRefusesWithoutSprintReleaseWithoutOptOut verifies that a build without
// --sprint-release refuses unless an explicit opt-out flag is given.
func TestBuildRefusesWithoutSprintReleaseWithoutOptOut(t *testing.T) {
	t.Parallel()
	source, out := sourceTree(t), t.TempDir()
	var o, e bytes.Buffer
	chain := &fakeToolchain{}
	code := Run("nova-update", []string{
		"build",
		"--version", "v1.2.3",
		"--out", out,
		"--source", source,
		"--platform", "linux-amd64",
	}, &o, &e, Deps{Toolchain: chain, Source: &fakeSource{commit: "c1"}})

	assert.Equal(t, 2, code, "stdout:%s\nstderr:%s", o.String(), e.String())
	assert.Contains(t, e.String(), "BUILD REFUSED")
	assert.Contains(t, e.String(), "--sprint-release")
	assert.Empty(t, chain.calls, "a tool was compiled before the refusal")
	_, err := os.Stat(filepath.Join(out, "v1.2.3"))
	assert.True(t, os.IsNotExist(err), "the refusal left a directory under --out")
}

// TestBuildWithExplicitOptOutSucceedsWithoutSprintRelease verifies that the
// explicit opt-out flag allows building without --sprint-release.
func TestBuildWithExplicitOptOutSucceedsWithoutSprintRelease(t *testing.T) {
	t.Parallel()
	source, out := sourceTree(t), t.TempDir()
	var o, e bytes.Buffer
	chain := &fakeToolchain{}
	code := Run("nova-update", []string{
		"build",
		"--version", "v1.2.3",
		"--out", out,
		"--source", source,
		"--platform", "linux-amd64",
		"--without-sprint-release",
	}, &o, &e, Deps{Toolchain: chain, Source: &fakeSource{commit: "c1"}})

	assert.Equal(t, 0, code, "stdout:%s\nstderr:%s", o.String(), e.String())
	assert.Contains(t, o.String(), "RELEASE BUILT version=v1.2.3")
	assert.Contains(t, o.String(), " sprint=none\n")
	assert.Len(t, chain.calls, 3, "all tools were compiled")
	_, err := os.Stat(filepath.Join(ArtifactDir(out, "v1.2.3", "linux", "amd64"), "nova-sprint"))
	assert.True(t, os.IsNotExist(err))
}

// TestBuildWithSprintReleaseSucceeds verifies that a build with --sprint-release
// succeeds as before.
func TestBuildWithSprintReleaseSucceeds(t *testing.T) {
	t.Parallel()
	source, out := sourceTree(t), t.TempDir()
	rel := sprintReleaseDir(t, "v1.2.3", "linux-amd64")
	var o, e bytes.Buffer
	chain := &fakeToolchain{}
	code := Run("nova-update", []string{
		"build",
		"--version", "v1.2.3",
		"--out", out,
		"--source", source,
		"--platform", "linux-amd64",
		"--sprint-release", rel,
	}, &o, &e, Deps{Toolchain: chain, Source: &fakeSource{commit: "c1"}})

	assert.Equal(t, 0, code, "stdout:%s\nstderr:%s", o.String(), e.String())
	assert.Contains(t, o.String(), "RELEASE BUILT version=v1.2.3")
	assert.Contains(t, o.String(), " sprint=v1.2.3\n")
}

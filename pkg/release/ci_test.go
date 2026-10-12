package release

import (
	"bytes"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
)

// cancelledReleaseJob is one release.yml job on a tag push, cancelled on
// purpose by the publish recipe when the hand cut publishes its own assets.
func cancelledReleaseJob(name string, id int64, headBranch string) CheckRun {
	return CheckRun{ID: id, Name: name, Status: "completed", Conclusion: "cancelled",
		WorkflowPath: ".github/workflows/release.yml", HeadBranch: headBranch, Event: "push", TagPush: true}
}

// A cut ignores a cancelled release.yml run of an older tag on the same commit:
// it says nothing about this commit's tests, so source is green with it set
// aside, and the RELEASE CUT line names what was set aside. A cancelled run of
// this cut's own CI is still a stop.
func TestACutSetsAsideThePreviousTagsCancelledReleaseRun(t *testing.T) {
	t.Parallel()
	runs := []CheckRun{
		{Name: "ci", Status: "completed", Conclusion: "success"},
		{Name: "lint", Status: "completed", Conclusion: "skipped"},
		cancelledReleaseJob("build", 38141786157, "v1.2.10"),
		cancelledReleaseJob("certified", 38141786158, "v1.2.10"),
		cancelledReleaseJob("release", 38141786159, "v1.2.10"),
	}
	const sha = "abc123abc123def"
	f := &fakeForge{head: map[string]string{"main": sha}, checks: map[string][]CheckRun{sha: runs},
		tags: []string{"v1.2.10", "v1.2.11"}}
	var out, errs bytes.Buffer
	code := Run("nova-update", []string{"cut", "--repo", "o/n", "--from", "main",
		"--version", "v1.2.12", "--changelog", filepath.Join(t.TempDir(), "CHANGELOG.md")},
		&out, &errs, cutDeps(t, f))
	assert.Equal(t, 0, code, "out=%s errs=%s", out.String(), errs.String())
	assert.Equal(t, []string{"v1.2.12 " + sha}, f.tagged)
	assert.Contains(t, out.String(), "ci-set-aside=38141786157@v1.2.10,38141786158@v1.2.10,38141786159@v1.2.10")
}

// The same cancelled runs are still red when their run's head branch is this
// cut's own branch or the version being cut.
func TestACutStillRefusesItsOwnCancelledReleaseRun(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		head string
	}{
		{"the release branch", "main"},
		{"the version being cut", "v1.2.12"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			runs := []CheckRun{
				{Name: "ci", Status: "completed", Conclusion: "success"},
				cancelledReleaseJob("release", 38141786159, tc.head),
			}
			const sha = "abc123abc123def"
			f := &fakeForge{head: map[string]string{"main": sha}, checks: map[string][]CheckRun{sha: runs},
				tags: []string{"v1.2.10", "v1.2.11"}}
			var out, errs bytes.Buffer
			code := Run("nova-update", []string{"cut", "--repo", "o/n", "--from", "main",
				"--version", "v1.2.12", "--changelog", filepath.Join(t.TempDir(), "CHANGELOG.md")},
				&out, &errs, cutDeps(t, f))
			assert.Equal(t, 2, code, "out=%s errs=%s", out.String(), errs.String())
			assert.Contains(t, errs.String(), "release=cancelled")
			assert.Empty(t, f.tagged)
		})
	}
}

// Only a PROVEN tag push is set aside: a cancelled release.yml run of an older
// tag whose provenance is not a tag-only push (TagPush false) is still red,
// because nothing proves it is the previous tag's own publication.
func TestACutStillRefusesAnUnprovenCancelledReleaseRun(t *testing.T) {
	t.Parallel()
	runs := []CheckRun{
		{Name: "ci", Status: "completed", Conclusion: "success"},
		{ID: 38141786159, Name: "release", Status: "completed", Conclusion: "cancelled",
			WorkflowPath: ".github/workflows/release.yml", HeadBranch: "v1.2.10", Event: "push", TagPush: false},
	}
	const sha = "abc123abc123def"
	f := &fakeForge{head: map[string]string{"main": sha}, checks: map[string][]CheckRun{sha: runs},
		tags: []string{"v1.2.10", "v1.2.11"}}
	var out, errs bytes.Buffer
	code := Run("nova-update", []string{"cut", "--repo", "o/n", "--from", "main",
		"--version", "v1.2.12", "--changelog", filepath.Join(t.TempDir(), "CHANGELOG.md")},
		&out, &errs, cutDeps(t, f))
	assert.Equal(t, 2, code, "out=%s errs=%s", out.String(), errs.String())
	assert.Contains(t, errs.String(), "release=cancelled")
	assert.NotContains(t, out.String(), "ci-set-aside")
	assert.Empty(t, f.tagged)
}

// A waiver aimed only at the set-aside runs is refused: they are not red, so
// there is nothing to waive. A waiver of every remaining green check stays
// refused too, the existing rule.
func TestACutWaiverDoesNotCoverSetAsideRuns(t *testing.T) {
	t.Parallel()
	t.Run("a waiver for the set-aside runs alone", func(t *testing.T) {
		t.Parallel()
		runs := []CheckRun{
			{Name: "ci", Status: "completed", Conclusion: "success"},
			cancelledReleaseJob("release", 38141786159, "v1.2.10"),
		}
		const sha = "abc123abc123def"
		f := &fakeForge{head: map[string]string{"main": sha}, checks: map[string][]CheckRun{sha: runs},
			tags: []string{"v1.2.10", "v1.2.11"}}
		var out, errs bytes.Buffer
		code := Run("nova-update", []string{"cut", "--repo", "o/n", "--from", "main",
			"--version", "v1.2.12", "--changelog", filepath.Join(t.TempDir(), "CHANGELOG.md"),
			"--waive-ci", "the owner, 2026-10-11"},
			&out, &errs, cutDeps(t, f))
		assert.Equal(t, 2, code, "out=%s errs=%s", out.String(), errs.String())
		assert.Contains(t, errs.String(), "the previous tag's release run is set aside; no waiver is needed")
		assert.Empty(t, f.tagged)
	})
	t.Run("a waiver of every remaining green check", func(t *testing.T) {
		t.Parallel()
		runs := []CheckRun{{Name: "ci", Status: "completed", Conclusion: "success"}}
		const sha = "abc123abc123def"
		f := &fakeForge{head: map[string]string{"main": sha}, checks: map[string][]CheckRun{sha: runs},
			tags: []string{"v1.2.10", "v1.2.11"}}
		var out, errs bytes.Buffer
		code := Run("nova-update", []string{"cut", "--repo", "o/n", "--from", "main",
			"--version", "v1.2.12", "--changelog", filepath.Join(t.TempDir(), "CHANGELOG.md"),
			"--waive-ci", "the owner, 2026-10-11"},
			&out, &errs, cutDeps(t, f))
		assert.Equal(t, 2, code, "out=%s errs=%s", out.String(), errs.String())
		assert.Contains(t, errs.String(), "every check on "+sha+" is green")
		assert.Empty(t, f.tagged)
	})
}

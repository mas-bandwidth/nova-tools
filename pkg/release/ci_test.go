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

// A cut sets aside a cancelled release.yml run of an older tag on the same
// commit: it says nothing about this commit's tests, so source is green with
// it set aside, and the RELEASE CUT line names what was set aside. The same
// cancelled runs are still red when their head branch is this cut's own branch
// or the version being cut, or when nothing proves the run is a tag-only push
// (TagPush false). A waiver aimed only at the set-aside runs is refused (they
// are not red, so there is nothing to waive), and a waiver of every remaining
// green check stays refused too, the existing rule.
func TestACutAndThePreviousTagsCancelledReleaseRun(t *testing.T) {
	t.Parallel()
	const sha = "abc123abc123def"
	green := CheckRun{Name: "ci", Status: "completed", Conclusion: "success"}
	for _, tc := range []struct {
		name              string
		runs              []CheckRun
		waive             string
		code              int
		out, errs, notOut string
	}{
		{name: "TestACutSetsAsideThePreviousTagsCancelledReleaseRun", runs: []CheckRun{green,
			{Name: "lint", Status: "completed", Conclusion: "skipped"},
			cancelledReleaseJob("build", 38141786157, "v1.2.10"),
			cancelledReleaseJob("certified", 38141786158, "v1.2.10"),
			cancelledReleaseJob("release", 38141786159, "v1.2.10")},
			out: "ci-set-aside=38141786157@v1.2.10,38141786158@v1.2.10,38141786159@v1.2.10"},
		{name: "TestACutStillRefusesItsOwnCancelledReleaseRun: the release branch", code: 2, errs: "release=cancelled",
			runs: []CheckRun{green, cancelledReleaseJob("release", 38141786159, "main")}},
		{name: "TestACutStillRefusesItsOwnCancelledReleaseRun: the version being cut", code: 2, errs: "release=cancelled",
			runs: []CheckRun{green, cancelledReleaseJob("release", 38141786159, "v1.2.12")}},
		{name: "TestACutStillRefusesAnUnprovenCancelledReleaseRun", code: 2, errs: "release=cancelled", notOut: "ci-set-aside",
			runs: []CheckRun{green, {ID: 38141786159, Name: "release", Status: "completed", Conclusion: "cancelled",
				WorkflowPath: ".github/workflows/release.yml", HeadBranch: "v1.2.10", Event: "push", TagPush: false}}},
		{name: "TestACutWaiverDoesNotCoverSetAsideRuns: a waiver for the set-aside runs alone", code: 2,
			runs: []CheckRun{green, cancelledReleaseJob("release", 38141786159, "v1.2.10")}, waive: "the owner, 2026-10-11",
			errs: "the previous tag's release run is set aside; no waiver is needed"},
		{name: "TestACutWaiverDoesNotCoverSetAsideRuns: a waiver of every remaining green check", code: 2,
			runs: []CheckRun{green}, waive: "the owner, 2026-10-11", errs: "every check on " + sha + " is green"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := &fakeForge{head: map[string]string{"main": sha}, checks: map[string][]CheckRun{sha: tc.runs},
				tags: []string{"v1.2.10", "v1.2.11"}}
			args := []string{"cut", "--repo", "o/n", "--from", "main",
				"--version", "v1.2.12", "--changelog", filepath.Join(t.TempDir(), "CHANGELOG.md")}
			if tc.waive != "" {
				args = append(args, "--waive-ci", tc.waive)
			}
			var out, errs bytes.Buffer
			code := Run("nova-update", args, &out, &errs, cutDeps(t, f))
			assert.Equal(t, tc.code, code, "out=%s errs=%s", out.String(), errs.String())
			assert.Contains(t, out.String(), tc.out)
			assert.Contains(t, errs.String(), tc.errs)
			if tc.notOut != "" {
				assert.NotContains(t, out.String(), tc.notOut)
			}
			if tc.code == 0 {
				assert.Equal(t, []string{"v1.2.12 " + sha}, f.tagged)
			} else {
				assert.Empty(t, f.tagged)
			}
		})
	}
}

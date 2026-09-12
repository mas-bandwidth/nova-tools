package ci

import (
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// release-evidence.py is the decision behind release.yml's release-gate job: it reads
// the GitHub check-runs API response on stdin and the expected commit SHA (plus the
// required check names) on argv, and prints ALLOW only when every required check run
// exists for the exact SHA, is completed, and concluded success. These tests run that
// same script -- with python3, since it is stdlib-only and has no Go dependency -- over
// fixtures for every refusal the gate must produce and the one path it must allow.

const shaHappy = "1111111111111111111111111111111111111111"
const shaOther = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

func evidencePath(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 is not on PATH; release.yml runs release-evidence.py with python3 and these tests exercise that same script, so without python3 they have nothing to run")
	}
	return filepath.Join(repoRoot(t), ".github", "scripts", "release-evidence.py")
}

func runEvidence(t *testing.T, expectedSHA, fixture string) (int, string) {
	t.Helper()
	cmd := exec.Command("python3", evidencePath(t), expectedSHA, "ci-ok")
	cmd.Stdin = strings.NewReader(fixture)
	out, err := cmd.CombinedOutput()
	code := 0
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		} else {
			t.Fatalf("running release-evidence.py: %v\n%s", err, out)
		}
	}
	return code, string(out)
}

func TestReleaseEvidenceRefusesEveryBadCase(t *testing.T) {
	cases := []struct {
		name         string
		expectedSHA  string
		fixture      string
		wantContains string
	}{
		{
			name:         "no run exists for the SHA",
			expectedSHA:  shaHappy,
			fixture:      `{"total_count": 0, "check_runs": []}`,
			wantContains: "no check runs",
		},
		{
			name:        "run reports a different SHA",
			expectedSHA: shaHappy,
			fixture: `{"total_count": 1, "check_runs": [
				{"id": 1, "name": "ci-ok", "head_sha": "` + shaOther + `", "status": "completed", "conclusion": "success", "completed_at": "2026-09-12T00:00:00Z"}]}`,
			wantContains: "reports SHA " + shaOther,
		},
		{
			name:        "run exists but is not completed",
			expectedSHA: shaHappy,
			fixture: `{"total_count": 1, "check_runs": [
				{"id": 1, "name": "ci-ok", "head_sha": "` + shaHappy + `", "status": "in_progress", "conclusion": null, "completed_at": null}]}`,
			wantContains: "not completed",
		},
		{
			name:        "completed run concluded failure",
			expectedSHA: shaHappy,
			fixture: `{"total_count": 1, "check_runs": [
				{"id": 1, "name": "ci-ok", "head_sha": "` + shaHappy + `", "status": "completed", "conclusion": "failure", "completed_at": "2026-09-12T00:00:00Z"}]}`,
			wantContains: "concluded failure",
		},
		{
			name:        "completed run concluded timed_out",
			expectedSHA: shaHappy,
			fixture: `{"total_count": 1, "check_runs": [
				{"id": 1, "name": "ci-ok", "head_sha": "` + shaHappy + `", "status": "completed", "conclusion": "timed_out", "completed_at": "2026-09-12T00:00:00Z"}]}`,
			wantContains: "concluded timed_out",
		},
		{
			name:        "no check run named ci-ok",
			expectedSHA: shaHappy,
			fixture: `{"total_count": 1, "check_runs": [
				{"id": 1, "name": "smoke (ubuntu-latest)", "head_sha": "` + shaHappy + `", "status": "completed", "conclusion": "success", "completed_at": "2026-09-12T00:00:00Z"}]}`,
			wantContains: "no check run named 'ci-ok'",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, out := runEvidence(t, tc.expectedSHA, tc.fixture)
			if code == 0 {
				t.Fatalf("want a non-zero exit (REFUSE), got 0\noutput: %s", out)
			}
			if !strings.HasPrefix(strings.TrimSpace(out), "REFUSE") {
				t.Fatalf("want output to begin with REFUSE, got: %q", out)
			}
			if !strings.Contains(out, tc.wantContains) {
				t.Fatalf("want refusal to name %q, got: %q", tc.wantContains, out)
			}
		})
	}
}

func TestReleaseEvidenceAllowsTheHappyPath(t *testing.T) {
	fixture := `{"total_count": 1, "check_runs": [
		{"id": 1, "name": "ci-ok", "head_sha": "` + shaHappy + `", "status": "completed", "conclusion": "success", "completed_at": "2026-09-12T00:00:00Z"}]}`
	code, out := runEvidence(t, shaHappy, fixture)
	if code != 0 {
		t.Fatalf("want exit 0 (ALLOW), got %d\noutput: %s", code, out)
	}
	if strings.TrimSpace(out) != "ALLOW" {
		t.Fatalf("want output ALLOW, got: %q", out)
	}
}

// TestWorkflowActionsArePinnedToSHAs asserts that every `uses:` line in both workflow
// files names a 40-hex commit SHA, so no mutable tag (actions/checkout@v4) can move the
// action out from under the release or CI that trusted it. A line that names a tag is a
// failure; a local `./path` action would also fail here, and none of these files has one.
func TestWorkflowActionsArePinnedToSHAs(t *testing.T) {
	root := repoRoot(t)
	shaRe := regexp.MustCompile(`^[0-9a-f]{40}$`)
	useRe := regexp.MustCompile(`^\s*(?:-\s*)?uses:\s*(\S+)`)
	for _, name := range []string{"release.yml", "ci.yml"} {
		path := filepath.Join(root, ".github", "workflows", name)
		raw := readFile(t, path)
		for i, line := range strings.Split(raw, "\n") {
			// Anchored at the start of the line: an actual YAML `uses:` key, never the word
			// "refuses:" inside a run block or a comment that happens to mention `uses:`.
			m := useRe.FindStringSubmatch(line)
			if m == nil {
				continue
			}
			ref := m[1]
			at := strings.LastIndex(ref, "@")
			if at < 0 {
				t.Errorf("%s:%d uses %q with no @; want owner/action@<40-hex-sha>", name, i+1, ref)
				continue
			}
			sha := ref[at+1:]
			if !shaRe.MatchString(sha) {
				t.Errorf("%s:%d uses %q; ref %q is not a 40-hex commit SHA", name, i+1, ref, sha)
			}
		}
	}
}

package main

import (
	"fmt"
	"strings"
	"testing"
)

// The certified verb, pinned by the three outcomes the release-checks job drove
// through a fake gh (newer failure after older success, a run in flight, one
// green) and by every other branch of the gate. The fixtures are whole API
// bodies: the verb reads total_count and a workflow_runs list, and a list
// hand-typed in a test drifts from the shape the API returns.

const certAPI = "repos/example/repo/actions/workflows/certification.yml/runs?head_sha=dr-y-run-sha&per_page=100"

func certHarness(t *testing.T, answer string, rc int) *harness {
	t.Helper()
	h := newHarness(t)
	h.vars = map[string]string{
		"GITHUB_REPOSITORY": "example/repo",
		"SHA":               "dr-y-run-sha",
		"REF":               "v9.9.9-dry-run",
		"GITHUB_RUN_ID":     "0000000000",
		"GH_TOKEN":          "a-token-is-set-and-never-read-by-the-fake",
	}
	h.gh.api = func(args []string) (string, int) { return answer, rc }
	return h
}

func TestCertifiedNewerFailureAfterOlderSuccessRefusesByNamingTheRed(t *testing.T) {
	t.Parallel()
	// The fixture a gate that counts any green would pass.
	h := certHarness(t, httpAnswer("200", "OK", fixture(t, "testdata/certified/old-green-new-red.json")), 0)
	h.wantRC(h.do("certified"), 1)
	h.mustContain("not uniformly green")
	h.mustContain("1 of 1 in the latest-stamp group not success")
	h.mustContain("receipt run 1002 attempt 1, concluded failure")
	h.mustContain("  run 1001 attempt 1 completed success updated 2026-09-14T10:00:00Z ")
	h.mustContain("  run 1002 attempt 1 completed failure updated 2026-09-14T11:00:00Z ")
	h.mustContain("gh workflow run certification.yml -R example/repo --ref v9.9.9-dry-run")
	h.mustContain("gh run rerun 0000000000 -R example/repo")
}

func TestCertifiedARunInFlightRefusesWithTheWaitLine(t *testing.T) {
	t.Parallel()
	h := certHarness(t, httpAnswer("200", "OK", fixture(t, "testdata/certified/in-flight.json")), 0)
	h.wantRC(h.do("certified"), 1)
	h.mustContain("not yet completed")
	h.mustContain("1 certification run(s) on dr-y-run-sha not yet completed; wait for certification-ok, then re-run this workflow: gh run rerun 0000000000 -R example/repo")
	h.mustContain("in_progress -")
}

func TestCertifiedOneGreenVouchesAndPrintsTheReceipt(t *testing.T) {
	t.Parallel()
	h := certHarness(t, httpAnswer("200", "OK", fixture(t, "testdata/certified/single-green.json")), 0)
	h.wantRC(h.do("certified"), 0)
	h.mustContain("receipt run 3001 attempt 1, concluded success")
	h.mustContain("certification runs on dr-y-run-sha: 1 completed; latest update 2026-09-14T13:00:00Z shared by 1 run(s), 0 not green;")
	h.mustNotContain("refusing")
}

func TestCertifiedAsksOnceWithTheStatusLineAndNoStatusFilter(t *testing.T) {
	t.Parallel()
	h := certHarness(t, httpAnswer("200", "OK", fixture(t, "testdata/certified/single-green.json")), 0)
	h.wantRC(h.do("certified"), 0)
	if len(h.gh.apis) != 1 || strings.Join(h.gh.apis[0], " ") != "-i "+certAPI {
		t.Fatalf("asked %v, want one `gh api -i %s`", h.gh.apis, certAPI)
	}
	if len(h.gh.releases) != 0 {
		t.Fatalf("the gate ran gh release: %v", h.gh.releases)
	}
}

func TestCertifiedAnAnswerThatIsNot200IsRefusedWithTheWholeResponse(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name, answer string
		rc           int
	}{
		{"403", httpAnswer("403", "Forbidden", `{"message":"Resource not accessible by integration"}`), 1},
		{"404", httpAnswer("404", "Not Found", ""), 1},
		{"no answer", "error connecting to api.github.com\n", 1},
		{"empty", "", 1},
	} {
		h := certHarness(t, c.answer, c.rc)
		h.wantRC(h.do("certified"), 1)
		h.mustContain(fmt.Sprintf("asking GitHub for certification runs on dr-y-run-sha did not answer 200 (gh exit %d):", c.rc))
		if c.answer != "" {
			h.mustContain(strings.SplitN(c.answer, "\n", 2)[0])
		}
		h.mustNotContain("vouch")
	}
}

func TestCertifiedAListShorterThanItsTotalIsAmbiguousAndRefused(t *testing.T) {
	t.Parallel()
	body := `{"total_count": 3, "workflow_runs": [{"id": 1, "status": "completed", "conclusion": "success", "run_attempt": 1, "updated_at": "2026-09-14T10:00:00Z"}]}`
	h := certHarness(t, httpAnswer("200", "OK", body), 0)
	h.wantRC(h.do("certified"), 1)
	h.mustContain("refusing: 3 certification runs on dr-y-run-sha but only 1 listed; the selection would be ambiguous")
}

func TestCertifiedNoRunAtAllVouchesForNothing(t *testing.T) {
	t.Parallel()
	h := certHarness(t, httpAnswer("200", "OK", `{"total_count": 0, "workflow_runs": []}`), 0)
	h.wantRC(h.do("certified"), 1)
	h.mustContain("refusing: no completed certification run on dr-y-run-sha, so nothing vouches for this tree")
	h.mustContain("receipt run none attempt 0, concluded none")
	h.mustContain("certify it first")
}

func TestCertifiedTheRunCarryingTheLatestUpdateDecidesNotTheHighestId(t *testing.T) {
	t.Parallel()
	// A rerun of an older run id is newer evidence than a later run never rerun:
	// id 1 was rerun last and is green, id 2 is an older red. The group of the
	// maximal updated_at is {1}, so the commit is vouched for.
	body := `{"total_count": 2, "workflow_runs": [
	  {"id": 2, "status": "completed", "conclusion": "failure", "run_attempt": 1, "updated_at": "2026-09-14T10:00:00Z", "html_url": "u2"},
	  {"id": 1, "status": "completed", "conclusion": "success", "run_attempt": 2, "updated_at": "2026-09-14T12:00:00Z", "html_url": "u1"}]}`
	h := certHarness(t, httpAnswer("200", "OK", body), 0)
	h.wantRC(h.do("certified"), 0)
	h.mustContain("receipt run 1 attempt 2, concluded success")

	// And the reverse: the rerun is red, the later-id run is green and older.
	body = strings.NewReplacer(`"conclusion": "failure"`, `"conclusion": "TMP"`, `"conclusion": "success"`, `"conclusion": "failure"`, `"conclusion": "TMP"`, `"conclusion": "success"`).Replace(body)
	h = certHarness(t, httpAnswer("200", "OK", body), 0)
	h.wantRC(h.do("certified"), 1)
	h.mustContain("not uniformly green")
}

func TestCertifiedTwoRunsSharingTheLatestStampWithOppositeConclusionsRefuseNamingTheRed(t *testing.T) {
	t.Parallel()
	// The stamp supplies no order between them and a run id must not invent one.
	for _, green := range []string{"first", "second"} {
		a, b := `"success"`, `"failure"`
		if green == "second" {
			a, b = b, a
		}
		body := fmt.Sprintf(`{"total_count": 2, "workflow_runs": [
		  {"id": 10, "status": "completed", "conclusion": %s, "run_attempt": 1, "updated_at": "2026-09-14T12:00:00Z"},
		  {"id": 11, "status": "completed", "conclusion": %s, "run_attempt": 1, "updated_at": "2026-09-14T12:00:00Z"}]}`, a, b)
		h := certHarness(t, httpAnswer("200", "OK", body), 0)
		h.wantRC(h.do("certified"), 1)
		h.mustContain("shared by 2 run(s), 1 not green")
		red := "11"
		if green == "second" {
			red = "10"
		}
		h.mustContain("receipt run " + red + " attempt 1, concluded failure")
		h.mustContain("1 of 2 in the latest-stamp group not success")
	}
}

func TestCertifiedACancelledOrSkippedLatestRunIsNotSuccess(t *testing.T) {
	t.Parallel()
	for _, conclusion := range []string{`"cancelled"`, `"skipped"`, `"timed_out"`, `null`} {
		body := `{"total_count": 1, "workflow_runs": [{"id": 5, "status": "completed", "conclusion": ` + conclusion + `, "run_attempt": 1, "updated_at": "2026-09-14T12:00:00Z"}]}`
		h := certHarness(t, httpAnswer("200", "OK", body), 0)
		h.wantRC(h.do("certified"), 1)
		h.mustContain("not uniformly green")
	}
}

func TestCertifiedAnAnswerThatIsNotTheDocumentedListIsRefused(t *testing.T) {
	t.Parallel()
	for _, body := range []string{`not json`, `{"total_count": 1, "workflow_runs": ["x"]}`, `{"total_count": "?", "workflow_runs": []}`, ``} {
		h := certHarness(t, httpAnswer("200", "OK", body), 0)
		h.wantRC(h.do("certified"), 1)
		h.mustContain("refusing:")
		h.mustNotContain("vouch")
	}
}

func TestCertifiedNeedsTheWholeEnvironment(t *testing.T) {
	t.Parallel()
	for _, missing := range []string{"GITHUB_REPOSITORY", "SHA", "REF", "GITHUB_RUN_ID", "GH_TOKEN"} {
		h := certHarness(t, httpAnswer("200", "OK", fixture(t, "testdata/certified/single-green.json")), 0)
		delete(h.vars, missing)
		h.wantRC(h.do("certified"), 2)
		h.mustContain(missing + " is not set")
		if len(h.gh.apis) != 0 {
			t.Errorf("%s unset: GitHub was asked anyway", missing)
		}
	}
}

func TestCertifiedTakesNoArguments(t *testing.T) {
	t.Parallel()
	h := certHarness(t, "", 0)
	h.wantRC(h.do("certified", "extra"), 2)
	h.mustContain("usage:")
}

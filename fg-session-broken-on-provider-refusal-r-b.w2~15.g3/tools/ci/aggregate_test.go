package main

import (
	"strings"
	"testing"
)

func runAggregate(args ...string) (int, string, string) {
	e, out, errb := testEnv()
	code := run(append([]string{"aggregate"}, args...), e)
	return code, out.String(), errb.String()
}

// The pull-request aggregate of ci-ok: a skipped lisp and a skipped
// test-hosted-pr are good on any event, a skipped functional only on a pull
// request, and nothing else skipped is.
func ciOKPullRequestArgs(event string, results map[string]string) []string {
	args := []string{"--event", event, "--skip-ok", "lisp,test-hosted-pr", "--skip-ok-on", "pull_request=functional"}
	for _, j := range []string{"lint", "test", "functional", "lisp", "e2e", "test-hosted-pr"} {
		args = append(args, j+"="+results[j])
	}
	return args
}

func allResults(r string) map[string]string {
	m := map[string]string{}
	for _, j := range []string{"lint", "test", "functional", "lisp", "e2e", "test-hosted-pr"} {
		m[j] = r
	}
	return m
}

func TestAggregateAllSuccessPassesAndPrintsEveryPair(t *testing.T) {
	t.Parallel()
	code, out, _ := runAggregate(ciOKPullRequestArgs("merge_group", allResults("success"))...)
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, out)
	}
	if !strings.HasPrefix(out, "success\tlint\nsuccess\ttest\nsuccess\tfunctional\n") || strings.Contains(out, "did not succeed") {
		t.Fatalf("stdout %q", out)
	}
}

func TestAggregateSkipRulesOfTheCIOKPullRequestStep(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		event  string
		job    string
		result string
		want   int
	}{
		{"lisp skipped on a pull request", "pull_request", "lisp", "skipped", 0},
		{"lisp skipped on a merge group", "merge_group", "lisp", "skipped", 0},
		{"test-hosted-pr skipped on a manual run", "workflow_dispatch", "test-hosted-pr", "skipped", 0},
		{"functional skipped on a pull request", "pull_request", "functional", "skipped", 0},
		{"functional skipped on a merge group", "merge_group", "functional", "skipped", 1},
		{"functional skipped on a manual run", "workflow_dispatch", "functional", "skipped", 1},
		{"lint skipped", "pull_request", "lint", "skipped", 1},
		{"test skipped", "merge_group", "test", "skipped", 1},
		{"e2e skipped", "pull_request", "e2e", "skipped", 1},
		{"lisp red", "pull_request", "lisp", "failure", 1},
		{"lisp cancelled", "merge_group", "lisp", "cancelled", 1},
		{"test-hosted-pr red", "pull_request", "test-hosted-pr", "failure", 1},
		{"a job with no result at all", "pull_request", "test", "", 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			res := allResults("success")
			res[tc.job] = tc.result
			code, out, _ := runAggregate(ciOKPullRequestArgs(tc.event, res)...)
			if code != tc.want {
				t.Fatalf("exit %d, want %d\n%s", code, tc.want, out)
			}
			if tc.want == 1 && !strings.HasSuffix(out, "1 job(s) did not succeed\n") {
				t.Fatalf("no verdict line:\n%s", out)
			}
		})
	}
}

func TestAggregateCountsEveryBadPairNotOnlyTheFirst(t *testing.T) {
	t.Parallel()
	res := allResults("success")
	res["lint"], res["test"], res["e2e"] = "failure", "cancelled", "skipped"
	code, out, _ := runAggregate(ciOKPullRequestArgs("pull_request", res)...)
	if code != 1 || !strings.HasSuffix(out, "3 job(s) did not succeed\n") {
		t.Fatalf("exit %d\n%s", code, out)
	}
}

// The push step of ci-ok: only lisp may be skipped.
func TestAggregateSkipRulesOfTheCIOKPushStep(t *testing.T) {
	t.Parallel()
	push := func(r map[string]string) []string {
		return []string{"--skip-ok", "lisp", "lint=" + r["lint"], "test=" + r["test"], "e2e=" + r["e2e"], "lisp=" + r["lisp"], "test-hosted=" + r["test-hosted"]}
	}
	good := map[string]string{"lint": "success", "test": "success", "e2e": "success", "lisp": "skipped", "test-hosted": "success"}
	if code, out, _ := runAggregate(push(good)...); code != 0 {
		t.Fatalf("push with lisp skipped: exit %d\n%s", code, out)
	}
	bad := map[string]string{"lint": "success", "test": "success", "e2e": "success", "lisp": "success", "test-hosted": "skipped"}
	if code, out, _ := runAggregate(push(bad)...); code != 1 {
		t.Fatalf("push with test-hosted skipped: exit %d, want 1 (the hosted suite must have run on main)\n%s", code, out)
	}
}

// certification-ok lists every job and lets none be skipped.
func TestAggregateWithNoSkipRulesRefusesEverySkip(t *testing.T) {
	t.Parallel()
	if code, _, _ := runAggregate("race-cache=success", "perf=success"); code != 0 {
		t.Fatalf("all success: exit %d", code)
	}
	if code, _, _ := runAggregate("race-cache=success", "perf=skipped"); code != 1 {
		t.Fatalf("a skipped job passed certification: exit %d", code)
	}
}

// The smoke gate: steps that did not apply are skipped, and that is fine; the
// verdict names every outcome and uses its own words.
func TestAggregateSmokeGateAcceptsSkipsAndNamesItsOwnFailures(t *testing.T) {
	t.Parallel()
	args := []string{"--skip-ok", "*", "--what", "smoke assertion(s) failed",
		"download the shipped binaries=success", "take this runner's binary=success", "a symlinked --dir (nocode)=skipped", "an unreadable deny-list refuses=success"}
	code, out, _ := runAggregate(args...)
	if code != 0 || !strings.Contains(out, "skipped\ta symlinked --dir (nocode)\n") {
		t.Fatalf("exit %d\n%s", code, out)
	}
	args = append(args, "a clean prose tree passes=failure", "the fail-opens stay closed=cancelled")
	code, out, _ = runAggregate(args...)
	if code != 1 || !strings.HasSuffix(out, "2 smoke assertion(s) failed\n") {
		t.Fatalf("exit %d\n%s", code, out)
	}
}

func TestAggregateRefusesAVerdictOverNothingAndMalformedInput(t *testing.T) {
	t.Parallel()
	for name, args := range map[string][]string{
		"no pairs":             {"--event", "push"},
		"a pair with no =":     {"lint"},
		"a pair with no name":  {"=success"},
		"an unknown flag":      {"--nope", "lint=success"},
		"a flag with no value": {"lint=success", "--event"},
		"bad skip-ok-on":       {"--skip-ok-on", "functional", "lint=success"},
	} {
		code, _, errb := runAggregate(args...)
		if code != 2 || !strings.Contains(errb, "aggregate:") {
			t.Errorf("%s: exit %d stderr %q, want a usage refusal (2)", name, code, errb)
		}
	}
}

func TestAggregateSplitsAtTheLastEquals(t *testing.T) {
	t.Parallel()
	code, out, _ := runAggregate("a=b name=success")
	if code != 0 || out != "success\ta=b name\n" {
		t.Fatalf("exit %d stdout %q", code, out)
	}
}

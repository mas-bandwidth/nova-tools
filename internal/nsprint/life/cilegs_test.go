package life

import "testing"

// TestCountCILegsCountsRunnerWorkers (nova-tools#4293): one leg per
// Runner.Worker process, by the program's base name; the idle listener, a
// grep for the name, and a job's own children are not legs.
func TestCountCILegsCountsRunnerWorkers(t *testing.T) {
	t.Parallel()
	ps := "/usr/sbin/launchd\n" +
		"/Users/ci/actions-runner/bin/Runner.Listener run --startuptype service\n" +
		"/Users/ci/actions-runner/bin/Runner.Worker spawnclient 105 108\n" +
		"/Users/ci/actions-runner/bin/Runner.Worker spawnclient 111 114\n" +
		"  /Users/ci/actions-runner/bin/Runner.Worker spawnclient 120 123\n" +
		"grep Runner.Worker\n" +
		"/usr/bin/nice -n 15 go test ./internal/... Runner.Worker\n" +
		"nova-card copy 4293~1\n" +
		"\n"
	if n := CountCILegs(ps); n != 3 {
		t.Fatalf("legs = %d, want 3", n)
	}
	if n := CountCILegs(""); n != 0 {
		t.Fatalf("legs of nothing = %d, want 0", n)
	}
	if n := CountCILegs("/usr/sbin/launchd\n/Users/ci/actions-runner/bin/Runner.Listener run\n"); n != 0 {
		t.Fatalf("an idle listener counted %d legs, want 0", n)
	}
}

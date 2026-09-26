package ci_test

// nova-tools #2958: a check whose only failure text is `signal: terminated`
// was killed by the bench (Studio SIGTERMed two test binaries at the same
// instant, no `--- FAIL`), so it is infra: the request goes back to the pool
// for a rerun, the attempt is given back, and nothing red reaches the PR
// record. The give-back is bounded (at most cfg:ci max_attempts per head),
// so a head that kills itself still ends red, with the infra why.

import (
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/ci"
)

// killedOutput is what `go test` prints when the bench SIGTERMs a test
// binary mid-run: no `--- FAIL`, the signal, then the package's FAIL line.
const killedOutput = "=== RUN   TestConfigLaunchFilesDefaultsAndIsPrintedOnce\nsignal: terminated\nFAIL\tgithub.com/mas-bandwidth/nova-tools/internal/pulse\t41.207s\n"

func TestBenchKilled(t *testing.T) {
	t.Parallel()

	cases := []struct {
		log  string
		want bool
	}{
		{killedOutput, true},
		{"ok  \tgithub.com/x/a\t1.0s\n" + killedOutput + "signal: terminated\nFAIL\tgithub.com/x/b\t2.0s\n", true},
		{"--- FAIL: TestX (0.1s)\n" + killedOutput, false},
		{"panic: boom\n" + killedOutput, false},
		{"    x_test.go:9: child: signal: terminated\n--- FAIL: TestY (0.1s)\nFAIL\n", false},
		{"    x_test.go:9: child: signal: terminated\nFAIL\tgithub.com/x/y\t1.0s\n", false},
		{"FAIL\tgithub.com/x/y [build failed]\n", false},
		{"", false},
	}
	for _, c := range cases {
		if got := ci.BenchKilled([]byte(c.log)); got != c.want {
			t.Fatalf("BenchKilled(%q) = %v, want %v", c.log, got, c.want)
		}
	}
}

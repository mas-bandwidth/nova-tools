//go:build functional

package gitrun_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/gitrun"
	"github.com/mas-bandwidth/nova-tools/internal/subproc"
)

// A git whose child holds the pipe after git is killed: the deadline (Options.Timeout,
// the seam the test injects a short one through) ends the call and the pipe does not hang
// it. A regression that let the pipe hold the call would run to the functional tier's own
// deadline and fail there.
func TestASlowGitIsKilledAtItsDeadlineAndItsPipeDoesNotHang(t *testing.T) {
	t.Parallel()

	bin := fakeGit(t, "sleep 30 &\nsleep 30\n")
	for _, run := range []struct {
		name string
		fn   func(o gitrun.Options) error
	}{
		{"Run", func(o gitrun.Options) error { _, err := gitrun.Run(t.Context(), o, "fetch"); return err }},
		{"Combined", func(o gitrun.Options) error { _, err := gitrun.Combined(t.Context(), o, "fetch"); return err }},
	} {
		t.Run(run.name, func(t *testing.T) {
			t.Parallel()

			err := run.fn(gitrun.Options{Bin: bin, Timeout: 150 * time.Millisecond, WaitDelay: 150 * time.Millisecond})
			var te *subproc.TimeoutError
			if !errors.As(err, &te) {
				t.Fatalf("a killed git was reported as %v", err)
			}
			if te.Budget != 150*time.Millisecond || !strings.Contains(te.Error(), "git fetch did not finish within 150ms") {
				t.Fatalf("the timeout said %q (budget %s)", te.Error(), te.Budget)
			}
		})
	}
}

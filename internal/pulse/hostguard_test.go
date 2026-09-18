package pulse

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/testguard"
)

// arm turns the guard on for one test and puts it back afterwards. t.Setenv
// restores the variable itself, but the guard caches it, so the cached value is
// restored here first -- a cleanup registered now runs BEFORE t.Setenv's.
func arm(t *testing.T) {
	t.Helper()
	t.Setenv(testguard.EnvNoHost, "1")
	testguard.Reload()
	t.Cleanup(func() {
		os.Unsetenv(testguard.EnvNoHost)
		testguard.Reload()
	})
}

// TestTheRealSSHRunnerPanicsUnderTheGuard is the hurt of 2026-09-18 written as
// a test: a unit test that constructs production code and injects NO fake gets
// production's own default, and production's default is a child `ssh`. Before
// the guard this test ran that ssh -- against a name that cannot resolve here,
// which is the only reason it was cheap; the certify verb's first cut ran the
// real workloads on hulk and reached redis on space the same way.
//
// Under NOVA_TEST_NO_HOST the seam panics and names the command line, so the
// defect reads as what it is -- a test holding the real thing -- instead of a
// bench that was busy or a key that was missing.
func TestTheRealSSHRunnerPanicsUnderTheGuard(t *testing.T) {
	arm(t)
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("the real SSHRunner ran a child under the guard; an unfaked seam must refuse before it reaches a host")
		}
		msg, _ := r.(string)
		for _, want := range []string{testguard.EnvNoHost, "ssh", "bench.invalid", "testguard.AllowHosts"} {
			if !strings.Contains(msg, want) {
				t.Errorf("the panic must name %q so the reader sees the command and the remedy; got %q", want, msg)
			}
		}
	}()
	_, _ = SSHRunner{}.Run(context.Background(), "bench.invalid", "uptime")
}

// TestAFakedRunnerIsUntouchedByTheGuard is the other half: the guard costs a
// test that injects the seam nothing at all. A rule that made the honest test
// harder would be edited around within a week.
func TestAFakedRunnerIsUntouchedByTheGuard(t *testing.T) {
	arm(t)
	var got string
	fake := fakeFleetRunner{run: func(_ context.Context, target, script string) (string, error) {
		got = target + " " + script
		return "READY 4", nil
	}}
	out, err := fake.Run(context.Background(), "hulk", "uptime")
	if err != nil || out != "READY 4" {
		t.Fatalf("the injected runner must answer normally under the guard, got %q %v", out, err)
	}
	if got != "hulk uptime" {
		t.Fatalf("the fake saw %q", got)
	}
}

// fakeFleetRunner is the seam a test is supposed to hold: the interface the
// verbs take, with the child replaced by a function.
type fakeFleetRunner struct {
	run func(ctx context.Context, target, script string) (string, error)
}

func (f fakeFleetRunner) Run(ctx context.Context, target, script string) (string, error) {
	return f.run(ctx, target, script)
}

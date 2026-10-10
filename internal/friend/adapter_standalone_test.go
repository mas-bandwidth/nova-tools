package friend

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// opencode 2.0.25 runs `run` through a shared background server that never
// receives the daemon's environment, so a sealed provider key reaches no
// provider. When the installed run lists --standalone, CheckRun turns it on and
// every run the adapter makes carries it; an older run that lacks it is never
// handed the flag.
func TestTheRunTakesStandaloneWhenTheInstalledOpencodeListsIt(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name, help string
		want       []string
	}{
		{"2.0.25 lists it", "  --session  continue a session\n  --model  the model\n  --standalone  run in this process\n", []string{"run", "--standalone", "--session", "ses_1", "hello"}},
		{"older lacks it", "  --session  continue a session\n  --model  the model\n", []string{"run", "--session", "ses_1", "hello"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var last []string
			run := func(_ context.Context, _, _ string, args []string, _ string) (string, int, error) {
				last = append([]string(nil), args...)
				switch {
				case len(args) == 1 && args[0] == "--version":
					return "2.0.25\n", 0, nil
				case len(args) == 2 && args[0] == "run" && args[1] == "--help":
					return tc.help, 0, nil
				}
				return "ok\n", 0, nil
			}
			oc := &OpenCode{Dir: t.TempDir(), Session: "ses_1", Run: run}
			require.NoError(t, oc.CheckRun(context.Background()))
			_, err := oc.Deliver(context.Background(), "hello")
			require.NoError(t, err)
			assert.Equal(t, tc.want, last)
		})
	}
}

// TestListVerbCarriesStandalone: the lane's session listing carries --standalone
// exactly when CheckRun found it, as the run verb does; without it opencode
// 2.0.25 under a lane's wall exits 1 on its managed-service port.
func TestListVerbCarriesStandalone(t *testing.T) {
	o := &OpenCode{}
	if got := strings.Join(o.listVerb(), " "); got != "session list --format json" {
		t.Fatalf("plain listVerb = %q", got)
	}
	o.Standalone = true
	if got := strings.Join(o.listVerb(), " "); got != "session --standalone list --format json" {
		t.Fatalf("standalone listVerb = %q", got)
	}
	if got := strings.Join(o.runVerb("--session", "s1", "hi"), " "); got != "run --standalone --session s1 hi" {
		t.Fatalf("standalone runVerb = %q", got)
	}
}

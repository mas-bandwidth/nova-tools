//go:build functional

package main

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/pkg/harness"
	"github.com/mas-bandwidth/nova-tools/pkg/swarm"
)

// TestHeadlessHarnessesRunAOneLineCard runs one one-line card through each headless
// harness on this machine (`claude -p`, `codex exec`, `grok --single`) and reads the
// usage the harness printed, as a launch does (swarm.HeadlessArgv, swarm.HeadlessUsage).
// It spends one cheap turn per harness under the machine's own subscription login, so it
// runs only with NOVA_SWARM_HEADLESS=1; a harness not on PATH (grok: also ~/.grok/bin) is
// skipped, and one that is logged out is reported and skipped.
func TestHeadlessHarnessesRunAOneLineCard(t *testing.T) {
	t.Parallel()
	if os.Getenv("NOVA_SWARM_HEADLESS") != "1" {
		t.Skip("NOVA_SWARM_HEADLESS=1 runs one cheap turn per headless harness on this machine")
	}
	for _, kind := range harness.Headless {
		t.Run(kind, func(t *testing.T) {
			bin, err := exec.LookPath(kind)
			if err != nil && kind == harness.Grok {
				if home, herr := os.UserHomeDir(); herr == nil {
					bin, err = exec.LookPath(filepath.Join(home, ".grok", "bin", "grok"))
				}
			}
			if err != nil {
				t.Skipf("%s is on no PATH entry of this machine", kind)
			}
			model := os.Getenv("NOVA_SWARM_HEADLESS_MODEL_" + strings.ToUpper(kind)) // "" takes the CLI's default
			argv, err := swarm.HeadlessArgv(kind, bin, model, "Reply with the single word pong and nothing else.")
			require.NoError(t, err)
			if model == "" {
				argv = withoutFlag(argv, "--model")
			}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
			defer cancel()
			cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
			cmd.Dir = t.TempDir()
			cmd.Stdin = nil // /dev/null: a card has no terminal
			var out bytes.Buffer
			cmd.Stdout, cmd.Stderr = &out, &out
			runErr := cmd.Run()
			if cause, failed := swarm.HeadlessFailure(kind, out.Bytes()); failed {
				t.Skipf("%s reported a failure of its own: %s", kind, cause.Reason())
			}
			require.NoError(t, runErr, out.String())
			u, err := swarm.HeadlessUsage(kind, out.Bytes())
			require.NoError(t, err, out.String())
			require.True(t, u.Observed, "no usage in the harness's output:\n%s", out.String())
			sum, _, partial := u.Budget()
			t.Logf("HEADLESS kind=%s model=%s tokens_in=%s tokens_out=%s cache_read=%s cache_write=%s reasoning=%s cost=%s budget_sum=%d partial=%v turns=%d",
				kind, u.Values["model"], u.Values["tokens_in"], u.Values["tokens_out"], u.Values["cache_read"], u.Values["cache_write"], u.Values["reasoning"], u.Values["cost"], sum, partial, u.Turns)
		})
	}
}

// withoutFlag is argv with one `--flag value` pair left out.
func withoutFlag(argv []string, flag string) []string {
	var out []string
	for i := 0; i < len(argv); i++ {
		if argv[i] == flag && i+1 < len(argv) {
			i++
			continue
		}
		out = append(out, argv[i])
	}
	return out
}

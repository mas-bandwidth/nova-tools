package main

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/pkg/bus"
	"github.com/stretchr/testify/assert"
)

// SPEC-FRIEND.md, Codex: restart, explicit session, notification mode and
// environment changes cannot turn implicit delivery back into queued input.
func TestCodexImplicitDeliveryRefusesBeforeAnySideEffect(t *testing.T) {
	t.Parallel()
	for _, verb := range []string{"install", "run", "check"} {
		for _, mode := range []string{"default", "named", "notifications", "folder notifications"} {
			if verb == "check" && strings.Contains(mode, "notifications") {
				continue // check has no notification flag
			}
			for _, dry := range []bool{false, true} {
				t.Run(verb+"/"+mode+map[bool]string{true: "/dry", false: "/real"}[dry], func(t *testing.T) {
					t.Parallel()
					r := newRig(t, "ada", "bob")
					r.env["CODEX_HOME"] = t.TempDir()
					r.env["CODEX_APP_SERVER_USE_LOCAL_DAEMON"] = "1"
					w := r.world()
					w.open = func(context.Context, string) (bus.Store, func(), error) {
						t.Fatal("refused route opened the bus")
						return nil, nil, nil
					}
					w.exec = func(context.Context, string, string, []string, string) (string, int, error) {
						t.Fatal("refused route ran a harness command")
						return "", 0, nil
					}
					w.binary = func() (string, error) {
						t.Fatal("refused route planned an installation")
						return "", nil
					}
					args := []string{verb, "--as", "bob", "--harness", "codex", "--dir", "/w/bob"}
					if mode != "default" {
						args = append(args, "--session", "real-thread")
					}
					if strings.Contains(mode, "notifications") {
						args = append(args, "--notifications-only")
					}
					if mode == "folder notifications" {
						args = append(args, "--adapter", "folder", "--delivery-dir", t.TempDir())
					}
					if dry {
						args = append(args, "--dry-run")
					}
					var out, errs strings.Builder
					assert.Equal(t, 2, run(args, strings.NewReader(""), &out, &errs, w), errs.String())
					assert.Contains(t, errs.String(), "Codex native push is unavailable")
					assert.Contains(t, errs.String(), "nova-bus wait --as <name> --after <cursor>")
					assert.Contains(t, errs.String(), "nova-bus recv --as <name>")
					assert.Empty(t, r.launchctl)
					assert.Empty(t, r.beats)
					assert.NoDirExists(t, filepath.Join(r.home, "Library", "LaunchAgents"))
				})
			}
		}
	}
}

// The install/run preflight refuses even without the CLI flag check; an explicit
// folder remains a supported route (SPEC-FRIEND.md, Codex).
func TestCodexUndrivenPreflightRefusesImplicitRoute(t *testing.T) {
	t.Parallel()
	assert.NotNil(t, undriven("codex", "/work", "real-thread", "", "", "nothing was written"))
	assert.Nil(t, undriven("codex", "/work", "real-thread", "folder", t.TempDir(), "nothing was written"))
}

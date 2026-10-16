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
		for _, mode := range []string{"default", "named"} {
			if mode == "default" && verb != "check" {
				continue // a one-shot CardRunner needs no session route
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
					if dry {
						args = append(args, "--dry-run")
					}
					var out, errs strings.Builder
					assert.Equal(t, 2, run(args, strings.NewReader(""), &out, &errs, w), errs.String())
					assert.Contains(t, errs.String(), "codex native push is unavailable")
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

// A Codex one-shot runner is a supported daemon path without a desktop
// session route. The dry run accepts it without opening the bus or starting a
// model; the separate check verb still refuses implicit session delivery.
func TestCodexOneShotDryRunNeedsNoSessionRoute(t *testing.T) {
	t.Parallel()
	for _, verb := range []string{"run", "install"} {
		t.Run(verb, func(t *testing.T) {
			t.Parallel()
			r := newRig(t, "ada", "bob")
			r.env["CODEX_HOME"] = "/w/codex"
			w := r.world()
			w.open = func(context.Context, string) (bus.Store, func(), error) {
				t.Fatal("dry run opened the bus")
				return nil, nil, nil
			}
			var out, errs strings.Builder
			dir := t.TempDir()
			if verb == "install" {
				dir = "/w/bob" // the install rig's settings filesystem
			}
			code := run([]string{verb, "--as", "bob", "--harness", "codex", "--dir", dir, "--dry-run"}, strings.NewReader(""), &out, &errs, w)
			assert.Equal(t, 0, code, errs.String())
			assert.Empty(t, r.launchctl)
		})
	}
}

// Session delivery still refuses implicitly without the CLI flag check; an
// explicit folder remains a supported route (SPEC-FRIEND.md, Codex).
func TestCodexUndrivenPreflightRefusesImplicitRoute(t *testing.T) {
	t.Parallel()
	assert.NotNil(t, undriven("codex", "/work", "real-thread", "", "", "nothing was written"))
	assert.Nil(t, undriven("codex", "/work", "real-thread", "folder", t.TempDir(), "nothing was written"))
	for _, harness := range []string{"tmux", "dsh", "claude"} {
		t.Run(harness, func(t *testing.T) {
			t.Parallel()
			assert.Nil(t, undriven(harness, t.TempDir(), "", "", "", "nothing was written"))
		})
	}
}

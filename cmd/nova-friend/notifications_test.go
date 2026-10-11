package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/pkg/bus"
	"github.com/mas-bandwidth/nova-tools/pkg/friend"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The CLI guard selects the notification-only route before native factories, including
// startup and dry run (SPEC-FRIEND.md, notifications).
func TestNotificationRunGuardsNativeFactoriesBeforeStartup(t *testing.T) {
	t.Parallel()
	for _, dry := range []bool{false, true} {
		t.Run(map[bool]string{false: "run", true: "dry"}[dry], func(t *testing.T) {
			t.Parallel()
			r := newRig(t, "ada", "bob")
			w := r.world()
			w.stage = func(string) *friend.Stager { t.Error("native stager constructed"); return nil }
			w.beat = func(context.Context, string, string, time.Time, friend.BeatWords) (string, error) {
				t.Error("native beat called")
				return "", nil
			}
			var stop context.CancelFunc
			w.signals = func(parent context.Context) (context.Context, context.CancelFunc) {
				ctx, cancel := context.WithCancel(parent)
				stop = cancel
				return ctx, cancel
			}
			w.sleep = func(context.Context, time.Duration) {
				if stop != nil {
					stop()
				}
			}
			args := []string{"run", "--as", "bob", "--harness", "codex", "--dir", t.TempDir(), "--session", "thread-test", "--notifications-only"}
			if dry {
				args = append(args, "--dry-run")
			}
			// Use the existing tool harness so flag validation and early dispatch both run.
			var out, errs strings.Builder
			code := run(args, strings.NewReader(""), &out, &errs, w)
			assert.Zero(t, code, errs.String())
			assert.Empty(t, r.beats)
		})
	}
}

// Notification install is separate even when native settings cannot be parsed
// (SPEC-FRIEND.md, Notifications): dry and real plans skip settings and push proof.
func TestNotificationInstallPreservesNativeSettingsBinaryAndLabel(t *testing.T) {
	t.Parallel()
	for _, dry := range []bool{true, false} {
		t.Run(map[bool]string{true: "dry", false: "install"}[dry], func(t *testing.T) {
			t.Parallel()
			r := newRig(t, "ada", "bob")
			r.env["CODEX_HOME"] = "/w/codex"
			config := "/w/codex/config.toml"
			const settings = "native settings deliberately invalid TOML ["
			require.NoError(t, r.fs.MkdirAll(filepath.Dir(config), 0755))
			require.NoError(t, r.fs.WriteFile(config, []byte(settings), 0600))
			native := friend.InstalledBinary(r.home)
			require.NoError(t, os.MkdirAll(filepath.Dir(native), 0755))
			require.NoError(t, os.WriteFile(native, []byte("fenced native binary"), 0755))
			src := filepath.Join(t.TempDir(), "nova-friend")
			require.NoError(t, os.WriteFile(src, []byte("reviewed notification binary"), 0755))
			w := r.world()
			w.binary = func() (string, error) { return src, nil }
			w.copy = friend.CopyExecutable
			w.open = func(context.Context, string) (bus.Store, func(), error) {
				t.Error("notification install attempted native push proof")
				return nil, func() {}, nil
			}
			args := []string{"install", "--as", "bob", "--harness", "codex", "--dir", "/w/bob", "--session", "thread-test", "--notifications-only"}
			if dry {
				args = append(args, "--dry-run")
			}
			var out, errs strings.Builder
			require.Zero(t, run(args, strings.NewReader(""), &out, &errs, w), errs.String())
			assert.Contains(t, out.String(), "com.nova.friend-notifications-bob")
			got, err := r.fs.ReadFile(config)
			require.NoError(t, err)
			assert.Equal(t, settings, string(got))
			got, err = os.ReadFile(native)
			require.NoError(t, err)
			assert.Equal(t, "fenced native binary", string(got))
			assert.NoFileExists(t, filepath.Join(r.home, "Library", "LaunchAgents", "com.nova.friend-bob.plist"))
			plist := filepath.Join(r.home, "Library", "LaunchAgents", "com.nova.friend-notifications-bob.plist")
			if dry {
				assert.Empty(t, r.launchctl)
				assert.NoFileExists(t, plist)
			} else {
				got, err = os.ReadFile(plist)
				require.NoError(t, err)
				assert.Contains(t, string(got), "nova-friend-notifications-bob.log")
				assert.Contains(t, string(got), "--notifications-only")
				assert.Equal(t, []string{"bootout gui/501/com.nova.friend-notifications-bob", "print gui/501/com.nova.friend-notifications-bob", "bootstrap gui/501 " + plist}, r.launchctl, "install waits for launchd to release the label before the bootstrap")
			}
		})
	}
}

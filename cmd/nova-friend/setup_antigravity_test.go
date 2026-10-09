package main

import (
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/friend"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAntigravityCLISetupRefusesBeforePlannerOrProfileWrites(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"inspect", "apply", "dry-run", "json"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			r := newRig(t, "ada", "bob")
			w := r.world()
			fs := friend.NewMemFS()
			w.settings = fs
			w.setupPlanner = func(friend.SetupOptions, friend.SettingsFS) (friend.SetupPlan, error) {
				require.FailNow(t, "unsupported CLI enrollment must never enter generic setup")
				return friend.SetupPlan{}, nil
			}
			args := []string{"setup", "--as", "bob", "--harness", "antigravity-cli", "--dir", "/work", "--config-dir", "/config", "--home", "/private", "--profile", "account", "--program", "/native/agy", "--deny-self", "/coordinator", "--profile-only"}
			switch mode {
			case "apply":
				args = append(args, "--apply")
			case "dry-run":
				args = append(args, "--apply", "--dry-run")
			case "json":
				args = append(args, "--json")
			}
			result := cliOf(w).Do(t, args...).Exit(2)
			if mode == "json" {
				result.Out("antigravity-cli", "unestablished", "per-target agy SSH OAuth", `"applied":false`)
			} else {
				result.Err("harness=antigravity-cli", "applied=false", "unestablished", "per-target agy SSH OAuth", "nova-friend setup -h")
			}
			for _, path := range []string{"/config", "/private", "/config/.nova-friend-setup/PROFILE"} {
				entry, err := fs.Lstat(path)
				require.NoError(t, err)
				assert.Equal(t, friend.KindMissing, entry.Kind)
			}
			assert.Empty(t, r.launchctl)
		})
	}
}

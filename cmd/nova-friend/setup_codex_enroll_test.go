package main

import (
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/friend"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func codexRemoteOptions() friend.SetupOptions {
	return friend.SetupOptions{Harness: "codex", Friend: "worker", Profile: "account-space", Dir: "/workers/work", Home: "/workers/home", ConfigDir: "/workers/config", Program: "/usr/bin/codex", ProfileOnly: true, DenySelf: []string{"/coordinator"}}
}

func TestCodexRemoteEnrollmentQuotesPublicSelectionAndPinsBuild(t *testing.T) {
	t.Parallel()
	o := codexRemoteOptions()
	o.ConfigDir = "/workers/config's root"
	args, err := codexRemoteEnrollmentArgs(o, "user@host", "/usr/local/bin/nova-friend", "account's label $(ignored)", 600, "reviewed-build")
	require.NoError(t, err)
	assert.Contains(t, args, "StrictHostKeyChecking=yes")
	assert.Contains(t, args, "BatchMode=yes")
	assert.Contains(t, args, "ClearAllForwardings=yes")
	for _, flag := range []string{"ForwardAgent=no", "ForwardX11=no", "PermitLocalCommand=no", "ControlPath=none", "ControlMaster=no"} {
		assert.Contains(t, args, flag)
	}
	assert.Equal(t, "user@host", args[len(args)-2])
	remote := args[len(args)-1]
	assert.Contains(t, remote, "'/workers/config'\\''s root'")
	assert.Contains(t, remote, "'account'\\''s label $(ignored)'")
	assert.Contains(t, remote, "'--enrollment-build' 'reviewed-build'")
	assert.NotContains(t, remote, "API_KEY")
	assert.NotContains(t, remote, "auth.json")
}

func TestCodexRemoteEnrollmentRefusesOptionInjectionAndUnknownBuild(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, target, program, label, build string }{
		{"target option", "-Fconfig", "/native", "account", "build"},
		{"target command", "host;command", "/native", "account", "build"},
		{"remote relative", "host", "nova-friend", "account", "build"},
		{"multiline program", "host", "/native\ncommand", "account", "build"},
		{"multiline label", "host", "/native", "account\nother", "build"},
		{"unknown build", "host", "/native", "account", "devel"},
		{"dirty build", "host", "/native", "account", "build-dirty"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := codexRemoteEnrollmentArgs(codexRemoteOptions(), tc.target, tc.program, tc.label, 600, tc.build)
			require.Error(t, err)
		})
	}
}

func TestCodexEnrollmentDryRunDoesNotNeedTerminalAndJSONApplyRefuses(t *testing.T) {
	t.Parallel()
	w := world{settings: friend.NewMemFS()}
	args := []string{"setup", "--as", "worker", "--harness", "codex", "--dir", "/workers/work", "--home", "/workers/home", "--config-dir", "/workers/config", "--profile", "account-space", "--program", "/usr/bin/codex", "--deny-self", "/coordinator", "--profile-only", "--codex-device-enroll", "--enrollment-label", "account label"}
	cliOf(w).Do(t, append(args, "--apply", "--dry-run", "--json")...).Exit(0).Out("codex_device_consent", "native_consent")
	cliOf(w).Do(t, append(args, "--apply", "--json")...).Exit(2).Err("refuses JSON")
	cliOf(w).Do(t, append(args, "--target", "user@host", "--remote-program", "/remote/nova-friend", "--apply", "--json")...).Exit(2).Err("refuses JSON")
	e, err := w.settings.Lstat("/workers/config")
	require.NoError(t, err)
	assert.Equal(t, friend.KindMissing, e.Kind)
	assert.NotContains(t, strings.Join(args, " "), "TOKEN")
}

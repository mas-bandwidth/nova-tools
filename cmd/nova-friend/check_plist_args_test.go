package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/friend"
	"github.com/stretchr/testify/require"
)

func TestFriendCheckPlistArgsHarnessDir(t *testing.T) {
	t.Parallel()

	// Plist with --dir and --harness flags
	plistContent := `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>ProgramArguments</key>
	<array>
		<string>/opt/nova/bin/nova-friend</string>
		<string>run</string>
		<string>--as</string>
		<string>bob</string>
		<string>--dir</string>
		<string>/w/bob</string>
		<string>--harness</string>
		<string>codex</string>
	</array>
</dict>
</plist>
`

	r := newRig(t, "ada", "bob")
	w := r.world()

	// Write the plist
	plistDir := filepath.Join(w.home, "Library", "LaunchAgents")
	require.NoError(t, os.MkdirAll(plistDir, 0o755))
	plistPath := filepath.Join(plistDir, "com.nova.friend-bob.plist")
	require.NoError(t, os.WriteFile(plistPath, []byte(plistContent), 0o644))

	args := friend.PlistArgs(w.readPlist(plistPath))
	require.Equal(t, "/w/bob", argAfter(args, "--dir"))
	require.Equal(t, "codex", argAfter(args, "--harness"))
}

func TestFriendCheckPlistArgsHarnessDirUnknown(t *testing.T) {
	t.Parallel()

	// Plist without --dir and --harness flags
	plistContent := `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>ProgramArguments</key>
	<array>
		<string>/opt/nova/bin/nova-friend</string>
		<string>run</string>
		<string>--as</string>
		<string>bob</string>
	</array>
</dict>
</plist>
`

	r := newRig(t, "ada", "bob")
	w := r.world()

	// Write the plist
	plistDir := filepath.Join(w.home, "Library", "LaunchAgents")
	require.NoError(t, os.MkdirAll(plistDir, 0o755))
	plistPath := filepath.Join(plistDir, "com.nova.friend-bob.plist")
	require.NoError(t, os.WriteFile(plistPath, []byte(plistContent), 0o644))

	args := friend.PlistArgs(w.readPlist(plistPath))
	require.Equal(t, "", argAfter(args, "--dir"))
	require.Equal(t, "", argAfter(args, "--harness"))
}

func TestFriendCheckPlistArgsDirLastElement(t *testing.T) {
	t.Parallel()

	// Plist with --dir as the last element (no value following)
	plistContent := `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>ProgramArguments</key>
	<array>
		<string>/opt/nova/bin/nova-friend</string>
		<string>run</string>
		<string>--as</string>
		<string>bob</string>
		<string>--dir</string>
	</array>
</dict>
</plist>
`

	r := newRig(t, "ada", "bob")
	w := r.world()

	// Write the plist
	plistDir := filepath.Join(w.home, "Library", "LaunchAgents")
	require.NoError(t, os.MkdirAll(plistDir, 0o755))
	plistPath := filepath.Join(plistDir, "com.nova.friend-bob.plist")
	require.NoError(t, os.WriteFile(plistPath, []byte(plistContent), 0o644))

	args := friend.PlistArgs(w.readPlist(plistPath))
	require.Equal(t, "", argAfter(args, "--dir"))
	require.Equal(t, "", argAfter(args, "--harness"))
}

func TestFriendCheckPlistArgsStatusOverridesPlist(t *testing.T) {
	t.Parallel()

	// Plist with --dir and --harness
	plistContent := `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>ProgramArguments</key>
	<array>
		<string>/opt/nova/bin/nova-friend</string>
		<string>run</string>
		<string>--as</string>
		<string>bob</string>
		<string>--dir</string>
		<string>/w/bob</string>
		<string>--harness</string>
		<string>codex</string>
	</array>
</dict>
</plist>
`

	r := newRig(t, "ada", "bob")
	w := r.world()

	// Write the plist
	plistDir := filepath.Join(w.home, "Library", "LaunchAgents")
	require.NoError(t, os.MkdirAll(plistDir, 0o755))
	plistPath := filepath.Join(plistDir, "com.nova.friend-bob.plist")
	require.NoError(t, os.WriteFile(plistPath, []byte(plistContent), 0o644))

	// Write a status file with a different harness
	stateDir := filepath.Join(w.home, ".nova-friend", "bob")
	require.NoError(t, os.MkdirAll(stateDir, 0o755))
	require.NoError(t, friend.WriteStatus(stateDir, friend.Status{
		Friend:  "bob",
		Harness: "opencode",
		At:      start,
	}))

	args := friend.PlistArgs(w.readPlist(plistPath))
	require.Equal(t, "/w/bob", argAfter(args, "--dir"))
	require.Equal(t, "codex", argAfter(args, "--harness"))
}

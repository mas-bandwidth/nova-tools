package friend

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestFriendLaunchdCoverPlistArgs tests the PlistArgs function.
func TestFriendLaunchdCoverPlistArgs(t *testing.T) {
	t.Parallel()

	t.Run("Agent.Plist ProgramArguments in order equal to a.Args()", func(t *testing.T) {
		t.Parallel()
		a := agent()
		plist := a.Plist()
		got := PlistArgs(plist)
		want := a.Args()
		assert.Equal(t, want, got)
	})

	t.Run("XML escapes decoded", func(t *testing.T) {
		t.Parallel()
		plist := `<?xml version="1.0" encoding="UTF-8"?>
<plist version="1.0">
<dict>
  <key>ProgramArguments</key>
  <array>
    <string>test</string>
    <string>value&amp;with&amp;escapes</string>
  </array>
</dict>
</plist>`
		got := PlistArgs(plist)
		want := []string{"test", "value&with&escapes"}
		assert.Equal(t, want, got)
	})

	t.Run("no ProgramArguments key returns nil", func(t *testing.T) {
		t.Parallel()
		plist := `<?xml version="1.0" encoding="UTF-8"?>
<plist version="1.0">
<dict>
  <key>Other</key>
  <string>value</string>
</dict>
</plist>`
		got := PlistArgs(plist)
		assert.Nil(t, got)
	})

	t.Run("text that is not XML returns nil", func(t *testing.T) {
		t.Parallel()
		got := PlistArgs("not xml at all")
		assert.Nil(t, got)
	})

	t.Run("empty ProgramArguments array returns empty slice", func(t *testing.T) {
		t.Parallel()
		plist := `<?xml version="1.0" encoding="UTF-8"?>
<plist version="1.0">
<dict>
  <key>ProgramArguments</key>
  <array>
  </array>
</dict>
</plist>`
		got := PlistArgs(plist)
		assert.Empty(t, got)
	})
}

// TestFriendLaunchdCoverDaemonPart tests the daemonPart function.
func TestFriendLaunchdCoverDaemonPart(t *testing.T) {
	t.Parallel()

	t.Run("with run", func(t *testing.T) {
		t.Parallel()
		args := []string{"binary", "run", "--arg1", "--arg2"}
		got := daemonPart(args)
		want := []string{"run", "--arg1", "--arg2"}
		assert.Equal(t, want, got)
	})

	t.Run("without run", func(t *testing.T) {
		t.Parallel()
		args := []string{"binary", "other", "--arg1"}
		got := daemonPart(args)
		assert.Nil(t, got)
	})
}

// TestFriendLaunchdCoverPlistDriftLine tests the PlistDriftLine function.
func TestFriendLaunchdCoverPlistDriftLine(t *testing.T) {
	t.Parallel()

	t.Run("empty plist", func(t *testing.T) {
		t.Parallel()
		got := PlistDriftLine("", []string{"run", "--arg"})
		assert.Equal(t, "", got)
	})

	t.Run("plist with no run", func(t *testing.T) {
		t.Parallel()
		plist := `<?xml version="1.0" encoding="UTF-8"?>
<plist version="1.0">
<dict>
  <key>ProgramArguments</key>
  <array>
    <string>other</string>
  </array>
</dict>
</plist>`
		got := PlistDriftLine(plist, []string{"run", "--arg"})
		assert.Equal(t, "", got)
	})

	t.Run("running args equal to run on but with different binary path before it", func(t *testing.T) {
		t.Parallel()
		// installed: ["other", "run", "--binary"]
		// running: ["diff", "run", "--binary"]
		// daemonPart starts from "run", so both are ["run", "--binary"]
		plist := `<?xml version="1.0" encoding="UTF-8"?>
<plist version="1.0">
<dict>
  <key>ProgramArguments</key>
  <array>
    <string>other</string>
    <string>run</string>
    <string>--binary</string>
  </array>
</dict>
</plist>`
		got := PlistDriftLine(plist, []string{"diff", "run", "--binary"})
		assert.Equal(t, "", got)
	})

	t.Run("changed --width", func(t *testing.T) {
		t.Parallel()
		// installed has --width 4, running has --width 8
		plist := `<?xml version="1.0" encoding="UTF-8"?>
<plist version="1.0">
<dict>
  <key>ProgramArguments</key>
  <array>
    <string>run</string>
    <string>--width</string>
    <string>4</string>
  </array>
</dict>
</plist>`
		running := []string{"run", "--width", "8"}
		got := PlistDriftLine(plist, running)
		assert.NotEqual(t, "", got)
		assert.Contains(t, got, "plist drift")
		assert.Contains(t, got, "--width")
		assert.Contains(t, got, "nova-friend install again")
	})
}

// TestFriendLaunchdCoverSaid tests the Agent.Said function.
func TestFriendLaunchdCoverSaid(t *testing.T) {
	t.Parallel()

	t.Run("no secrets", func(t *testing.T) {
		t.Parallel()
		a := agent()
		said := a.Said()
		assert.Contains(t, said, "nova-friend run")
		assert.Contains(t, said, "--as bob")
		assert.NotContains(t, said, "nova-secrets exec")
	})

	t.Run("Adapter folder", func(t *testing.T) {
		t.Parallel()
		a := agent()
		a.Adapter = "folder"
		a.Session = "ses_1"
		a.DeliveryDir = "/delivery"
		said := a.Said()
		assert.Contains(t, said, " --session ")
		assert.Contains(t, said, " --adapter folder ")
		assert.Contains(t, said, " --delivery-dir /delivery")
	})

	t.Run("two secrets", func(t *testing.T) {
		t.Parallel()
		a := agent()
		a.Secrets = []string{"KEY1", "KEY2"}
		a.Seat = "seat1"
		a.SecretsTool = "/tool/nova-secrets"
		a.Sops = "/tool/sops"
		said := a.Said()
		assert.Contains(t, said, "nova-secrets exec")
		assert.Contains(t, said, "--as seat1")
		assert.Contains(t, said, "--only KEY1,KEY2")
		assert.Contains(t, said, "--require KEY1")
		assert.Contains(t, said, "--require KEY2")
		assert.NotContains(t, said, "/path")
	})
}

// TestFriendLaunchdCoverArgs tests the Agent.Args function.
func TestFriendLaunchdCoverArgs(t *testing.T) {
	t.Parallel()

	t.Run("Command set", func(t *testing.T) {
		t.Parallel()
		a := agent()
		a.Command = []string{"ping", "--interval=5"}
		args := a.Args()
		assert.Equal(t, []string{a.Binary, "ping", "--interval=5"}, args)
	})

	t.Run("NotificationsOnly plus NotifyKinds and NotifyWindow", func(t *testing.T) {
		t.Parallel()
		a := agent()
		a.NotificationsOnly = true
		a.NotifyKinds = "request,blocker"
		a.NotifyWindow = 30 * time.Second
		args := a.Args()
		assert.Contains(t, args, "--notifications-only")
		assert.Contains(t, args, "--notify-kinds")
		assert.Contains(t, args, "request,blocker")
		assert.Contains(t, args, "--notify-window")
	})

	t.Run("SilentStop and BrokenAfter at defaults", func(t *testing.T) {
		t.Parallel()
		a := agent()
		a.SilentStop = DefaultSilentStop
		a.BrokenAfter = DefaultBrokenAfter
		args := a.Args()
		assert.NotContains(t, args, "--silent-stop")
		assert.NotContains(t, args, "--broken-after")
	})

	t.Run("SilentStop and BrokenAfter with non-default values", func(t *testing.T) {
		t.Parallel()
		a := agent()
		a.SilentStop = 10 * time.Second
		a.BrokenAfter = 100
		args := a.Args()
		assert.Contains(t, args, "--silent-stop")
		assert.Contains(t, args, "--broken-after")
	})
}

// TestFriendLaunchdCoverRemovePartial tests the removePartial function.
func TestFriendLaunchdCoverRemovePartial(t *testing.T) {
	t.Parallel()

	t.Run("file that exists", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		tmp := filepath.Join(dir, "partial")
		err := os.WriteFile(tmp, []byte("test"), 0644)
		require.NoError(t, err)
		origErr := os.ErrPermission
		gotErr := removePartial(tmp, origErr)
		assert.Error(t, gotErr)
		assert.True(t, errors.Is(gotErr, origErr))
		_, statErr := os.Stat(tmp)
		assert.True(t, os.IsNotExist(statErr))
	})

	t.Run("path that does not exist", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		tmp := filepath.Join(dir, "missing")
		origErr := os.ErrPermission
		gotErr := removePartial(tmp, origErr)
		assert.Error(t, gotErr)
		assert.Contains(t, gotErr.Error(), "removing the partial copy")
	})
}

// TestFriendLaunchdCoverCopyExecutable tests the CopyExecutable function.
func TestFriendLaunchdCoverCopyExecutable(t *testing.T) {
	t.Parallel()

	t.Run("missing source", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		src := filepath.Join(dir, "missing")
		dst := filepath.Join(dir, "dst")
		err := CopyExecutable(src, dst)
		assert.Error(t, err)
	})

	t.Run("directory as source", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		src := filepath.Join(dir, "subdir")
		err := os.MkdirAll(src, 0755)
		require.NoError(t, err)
		dst := filepath.Join(dir, "dst")
		err = CopyExecutable(src, dst)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "is not a file")
	})
}

// TestFriendLaunchdCoverBinaryPlan tests the Agent.BinaryPlan function.
func TestFriendLaunchdCoverBinaryPlan(t *testing.T) {
	t.Parallel()

	t.Run("NotificationsOnly with home under /Volumes", func(t *testing.T) {
		t.Parallel()
		a := agent()
		a.NotificationsOnly = true
		a.Home = "/Volumes/home"
		_, _, err := a.BinaryPlan()
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "home off /Volumes")
	})

	t.Run("missing binary", func(t *testing.T) {
		t.Parallel()
		a := agent()
		a.NotificationsOnly = true
		a.Home = t.TempDir()
		a.Binary = "/nonexistent/binary"
		_, _, err := a.BinaryPlan()
		assert.Error(t, err)
	})

	t.Run("directory as binary", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		a := agent()
		a.NotificationsOnly = true
		a.Home = dir
		a.Binary = filepath.Join(dir, "subdir")
		err := os.MkdirAll(a.Binary, 0755)
		require.NoError(t, err)
		_, _, err = a.BinaryPlan()
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "not a regular file")
	})
}

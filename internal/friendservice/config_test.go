package friendservice

import (
	"encoding/xml"
	"github.com/mas-bandwidth/nova-tools/internal/subproc"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func fixture(t *testing.T) Config {
	t.Helper()
	dir := t.TempDir()
	exe := filepath.Join(dir, "deliver ' & executable")
	require.NoError(t, os.WriteFile(exe, []byte("#!/bin/sh\nexit 0\n"), 0700))
	return Config{Friend: "reader", Server: "server:1", Redis: "redis:1", Consumer: "agent", Self: exe, Bus: exe, Sprint: exe, Deliver: exe, ConfigPath: filepath.Join(dir, "config.json"), StdoutPath: filepath.Join(dir, "out"), StderrPath: filepath.Join(dir, "err")}
}
func TestInvalidDeliveryNeverRendersAgent(t *testing.T) {
	t.Parallel()
	c := fixture(t)
	c.Deliver = filepath.Join(t.TempDir(), "missing")
	_, err := c.Plist()
	require.ErrorContains(t, err, "deliver wants")
}
func TestAgentUsesTypedArgumentsAndOwnIdentity(t *testing.T) {
	t.Parallel()
	c := fixture(t)
	raw, err := c.Plist()
	require.NoError(t, err)
	d := xml.NewDecoder(strings.NewReader(string(raw)))
	for {
		_, err := d.Token()
		if err != nil {
			require.Equal(t, "EOF", err.Error())
			break
		}
	}
	require.Contains(t, string(raw), "org.nova.friend.reader")
	require.Contains(t, string(raw), "<key>KeepAlive</key><true/>")
	require.Contains(t, string(raw), "<key>RunAtLoad</key><true/>")
	require.Contains(t, string(raw), "<string>serve</string>")
	require.Contains(t, string(raw), "&amp;")
	args := c.ReceiveArgs()
	require.Equal(t, []string{"recv", "--as", "reader", "--consumer", "agent", "--redis", "redis:1", "--forever", "--exec"}, args[:9])
	require.Contains(t, args[9], "'\\''")
}
func TestConfigRefusesUnknownAndTrailingFields(t *testing.T) {
	t.Parallel()
	for _, raw := range []string{`{"unknown":1}`, `{} {}`} {
		_, err := Parse([]byte(raw))
		require.Error(t, err)
	}
}

func TestQuotedDeliveryPathExecutesAsOnePath(t *testing.T) {
	t.Parallel()
	c := fixture(t)
	require.NoError(t, os.WriteFile(c.Deliver, []byte("#!/bin/sh\nprintf delivered\n"), 0700))
	args := c.ReceiveArgs()
	cmd, cancel := subproc.CommandFor(t.Context(), time.Second, "/bin/sh", "-c", args[len(args)-1])
	defer cancel()
	output, err := cmd.CombinedOutput()
	require.NoError(t, err, string(output))
	require.Equal(t, "delivered", string(output))
}

package main

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// On a Mac the health role does one thing: the Go build cache's trim, from the cache `go env GOCACHE` names for the
// login, hourly from a LaunchDaemon run as the login. None of the Linux tasks run there.
func TestHealthOnDarwinTrimsOnlyTheGoCache(t *testing.T) {
	t.Parallel()
	main, err := os.ReadFile("roles/health/tasks/main.yml")
	require.NoError(t, err)
	m := string(main)
	inc := strings.Index(m, "include_tasks: darwin.yml")
	end := strings.Index(m, "ansible.builtin.meta: end_host")
	require.True(t, inc > 0 && end > inc, "darwin.yml is included before the Linux-only end_host")
	d, err := os.ReadFile("roles/health/tasks/darwin.yml")
	require.NoError(t, err)
	for _, want := range []string{"go env GOCACHE", "/Library/LaunchDaemons/com.nova.go-cache-clean.plist", "<key>UserName</key>",
		"<key>StartInterval</key>", "launchctl bootstrap system", "nova-go-cache-clean",
		"launchctl bootout \"$l\" && echo REMOVED", "when: not health_gocache_on | bool"} {
		assert.Contains(t, string(d), want)
	}
	for _, linux := range []string{"systemd", "/tmp", "DISK_CLEAN_DIRS"} {
		assert.NotContains(t, string(d), linux, "no temp-dir or Linux cleanup on a Mac")
	}
}

package main

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// On a Mac the health role does two things: the Go build cache's trim, from the cache `go env GOCACHE` names for the
// login, hourly from a LaunchDaemon run as the login; and the open-files watch, every 10 s from a LaunchDaemon run as
// root. None of the Linux tasks run there.
func TestHealthOnDarwinTrimsTheGoCacheAndWatchesOpenFiles(t *testing.T) {
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
		"launchctl bootout \"$l\" || { echo \"bootout $l failed\" >&2; exit 1; }", "when: not health_gocache_on | bool", "health_gocache_dir is not match('/Volumes/')",
		"/Library/LaunchDaemons/com.nova.files-watch.plist", "<key>StartInterval</key><integer>{{ nova_health_files_watch_interval }}</integer>",
		"<key>FILES_WATCH_MAX</key><string>{{ nova_health_files_watch_max }}</string>", "/etc/newsyslog.d/nova-files-watch.conf",
		"when: not nova_health_files_watch | bool"} {
		assert.Contains(t, string(d), want)
	}
	watch := string(d)[strings.Index(string(d), "- name: the open-files watch's LaunchDaemon"):]
	watch = watch[:strings.Index(watch, "</plist>")]
	assert.NotContains(t, watch, "<key>UserName</key>", "the watch runs as root: lsof sees every process's descriptors only as root")
	for _, linux := range []string{"systemd", "/tmp", "DISK_CLEAN_DIRS"} {
		assert.NotContains(t, string(d), linux, "no temp-dir or Linux cleanup on a Mac")
	}
	defaults, err := os.ReadFile("roles/health/defaults/main.yml")
	require.NoError(t, err)
	for _, want := range []string{"nova_health_files_watch: true", "nova_health_files_watch_interval: 10", "nova_health_files_watch_max: 50000"} {
		assert.Contains(t, string(defaults), want)
	}
}

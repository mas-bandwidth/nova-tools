// Package testutil starts the throwaway Redis the nova-sprint controls share.
// The server belongs to its test and its sentry reaps it if the test binary
// exits without running cleanup.
package testutil

import (
	"os/exec"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/testredis"
)

// CIEnv names the setting that turns a missing server into a test failure.
const CIEnv = testredis.CIEnv

// Absent reports a missing redis-server under the same policy as Start.
func Absent(t *testing.T, cause error) { testredis.Absent(t, cause) }

// Start runs one disposable Redis server and reaps it on test completion or
// abnormal test-binary exit.
func Start(t *testing.T, extra ...string) string {
	t.Helper()
	for i, option := range extra {
		if option != "--dir" {
			continue
		}
		if i+1 == len(extra) {
			t.Fatal("testutil: --dir needs a test directory")
		}
		other := append(append([]string(nil), extra[:i]...), extra[i+2:]...)
		return testredis.StartInDir(t, extra[i+1], other...)
	}
	return testredis.Start(t, extra...)
}

// Program finds the redis-server used by Start.
func Program(t *testing.T) string { return testredis.Program(t) }

// FreePort returns a currently free loopback port for tests that launch their
// own server through a production path.
func FreePort(t *testing.T) string { return testredis.FreePort(t) }

// Join places cmd into the sentry's process group so that it is reaped if the
// test binary exits abnormally.
func Join(cmd *exec.Cmd) error { return testredis.Join(cmd) }

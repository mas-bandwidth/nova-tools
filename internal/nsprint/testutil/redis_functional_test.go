//go:build functional

package testutil

import (
	"encoding/json"
	"errors"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/testredis"
)

func TestTestutilStartRegistersAndCleansUp(t *testing.T) {
	t.Parallel()

	var addr string
	var pid int
	var portStr string
	var entryPath string

	t.Run("server-lifecycle", func(t *testing.T) {
		addr = Start(t)
		var err error
		_, portStr, err = net.SplitHostPort(addr)
		if err != nil {
			t.Fatalf("failed to split host port from %s: %v", addr, err)
		}

		entries, err := os.ReadDir(testredis.RegistryDir())
		if err != nil {
			t.Fatalf("failed to read registry dir: %v", err)
		}

		found := false
		for _, d := range entries {
			if !d.IsDir() && filepath.Ext(d.Name()) == ".json" {
				p := filepath.Join(testredis.RegistryDir(), d.Name())
				data, err := os.ReadFile(p)
				if err != nil {
					continue
				}
				var entry testredis.Entry
				if err := json.Unmarshal(data, &entry); err == nil {
					if entry.Port == portStr && entry.PPID == os.Getpid() {
						pid = entry.PID
						entryPath = p
						found = true
						break
					}
				}
			}
		}

		if !found {
			t.Fatalf("no registry entry found for port %s and ppid %d", portStr, os.Getpid())
		}

		if pid <= 0 {
			t.Fatalf("invalid registered pid: %d", pid)
		}
	})

	if entryPath != "" {
		if _, err := os.Stat(entryPath); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("expected registry file %s to be removed after test cleanup, got err: %v", entryPath, err)
		}
	}
}

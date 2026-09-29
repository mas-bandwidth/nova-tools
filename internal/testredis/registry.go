package testredis

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Entry is the registry record of one running throwaway redis-server.
type Entry struct {
	PID       int       `json:"pid"`
	Port      string    `json:"port"`
	PPID      int       `json:"ppid"`
	StartedAt time.Time `json:"started_at"`
}

func (e *Entry) UnmarshalJSON(data []byte) error {
	type rawEntry struct {
		PID       int             `json:"pid"`
		Port      json.RawMessage `json:"port"`
		PPID      int             `json:"ppid"`
		StartedAt time.Time       `json:"started_at"`
	}
	var raw rawEntry
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	e.PID = raw.PID
	e.PPID = raw.PPID
	e.StartedAt = raw.StartedAt
	if len(raw.Port) > 0 {
		var pInt int
		if err := json.Unmarshal(raw.Port, &pInt); err == nil {
			e.Port = strconv.Itoa(pInt)
		} else {
			var pStr string
			if err := json.Unmarshal(raw.Port, &pStr); err == nil {
				e.Port = pStr
			}
		}
	}
	return nil
}

// registryDirEnv allows tests to point the registry to a private directory.
const registryDirEnv = "NOVA_TESTREDIS_REGISTRY"

// RegistryDir returns the directory holding active redis-server registrations.
func RegistryDir() string {
	if dir := os.Getenv(registryDirEnv); dir != "" {
		return dir
	}
	tmp := os.Getenv("TMPDIR")
	if tmp == "" {
		tmp = os.TempDir()
	}
	return filepath.Join(tmp, "nova-test-redis")
}

// Register writes an entry for a started redis-server under RegistryDir()
// and returns an unregister function that removes the entry file.
func Register(pid int, port any, ppid int) func() {
	return RegisterDir(RegistryDir(), pid, port, ppid)
}

// RegisterDir writes an entry under the specified directory.
func RegisterDir(dir string, pid int, port any, ppid int) func() {
	if pid <= 0 {
		return func() {}
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return func() {}
	}
	portStr := fmt.Sprintf("%v", port)
	entry := Entry{
		PID:       pid,
		Port:      portStr,
		PPID:      ppid,
		StartedAt: time.Now().UTC(),
	}
	data, err := json.Marshal(entry)
	if err != nil {
		return func() {}
	}
	file := filepath.Join(dir, fmt.Sprintf("%d.json", pid))
	_ = os.WriteFile(file, data, 0644)

	var once sync.Once
	return func() {
		once.Do(func() {
			_ = os.Remove(file)
		})
	}
}

// SweepOrphans checks entries in RegistryDir(), kills (SIGKILL) any redis-server
// whose recorded parent process is no longer alive, removes the entry file, and
// logs "SWEEP REDIS killed=<n>\n" to stderr if orphans were found. It returns
// the count of killed orphans.
func SweepOrphans(stderr io.Writer) int {
	return SweepDir(RegistryDir(), stderr)
}

// SweepDir sweeps orphans in the specified registry directory.
func SweepDir(dir string, stderr io.Writer) int {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}

	killed := 0
	myPID := os.Getpid()

	for _, d := range entries {
		if d.IsDir() || !strings.HasSuffix(d.Name(), ".json") {
			continue
		}
		path := filepath.Join(dir, d.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var entry Entry
		if err := json.Unmarshal(data, &entry); err != nil {
			pidStr := strings.TrimSuffix(d.Name(), ".json")
			if p, err := strconv.Atoi(pidStr); err == nil {
				if !isProcessAlive(p) {
					_ = os.Remove(path)
				}
			}
			continue
		}

		// Don't touch our own active servers.
		if entry.PPID == myPID {
			continue
		}

		// If parent process is still alive, this server is not an orphan.
		if isProcessAlive(entry.PPID) {
			continue
		}

		// Parent process is dead! This is an orphan.
		if isProcessAlive(entry.PID) {
			if err := killProcess(entry.PID); err == nil {
				killed++
			}
		}
		_ = os.Remove(path)
	}

	if killed > 0 && stderr != nil {
		fmt.Fprintf(stderr, "SWEEP REDIS killed=%d\n", killed)
	}
	return killed
}

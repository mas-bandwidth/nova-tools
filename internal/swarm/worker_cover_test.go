package swarm

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestWorkerCoverDefaultDeadline: the worker description's deadline parses to a
// duration, and an absent or malformed one yields no deadline (zero), so a caller
// that asks for one before every task names the absence rather than guessing.
func TestWorkerCoverDefaultDeadline(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name     string
		deadline string
		want     time.Duration
	}{
		{"reads minutes", "20m", 20 * time.Minute},
		{"reads seconds", "2s", 2 * time.Second},
		{"refuses malformed", "not-a-duration", 0},
		{"refuses empty", "", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := Worker{Deadline: tc.deadline}.DefaultDeadline()
			assert.Equal(t, tc.want, got, "DefaultDeadline(%q) = %s, want %s", tc.deadline, got, tc.want)
		})
	}
}

// TestWorkerCoverJobDir: JobDir places a task under the slot's jobs/<id>, the
// directory the harness runs in and writes its RESULT into.
func TestWorkerCoverJobDir(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		w    Worker
		slot int
		id   string
		want string
	}{
		{"slot zero", Worker{WorkerDir: "/root/worker"}, 0, "t1", "/root/worker-0/jobs/t1"},
		{"slot one", Worker{WorkerDir: "/root/worker"}, 1, "abc", "/root/worker-1/jobs/abc"},
		{"empty worker dir", Worker{}, 3, "t2", "-3/jobs/t2"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := tc.w.JobDir(tc.slot, tc.id)
			assert.Equal(t, tc.want, got, "JobDir(%d,%q) = %q, want %q", tc.slot, tc.id, got, tc.want)
		})
	}
}

// TestWorkerCoverDataHome: DataHome is the job's XDG_DATA_HOME, the directory the
// harness keeps its own database in, one per job.
func TestWorkerCoverDataHome(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		w    Worker
		slot int
		id   string
		want string
	}{
		{"slot zero", Worker{WorkerDir: "/root/worker"}, 0, "t1", "/root/worker-0/jobs/t1/data"},
		{"slot one", Worker{WorkerDir: "/root/worker"}, 1, "abc", "/root/worker-1/jobs/abc/data"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := tc.w.DataHome(tc.slot, tc.id)
			assert.Equal(t, tc.want, got, "DataHome(%d,%q) = %q, want %q", tc.slot, tc.id, got, tc.want)
		})
	}
}

// TestWorkerCoverCopyTree: copyTree copies every regular file of the source into
// the destination, making directories as it walks, and refuses a source that does
// not exist.
func TestWorkerCoverCopyTree(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		setup   func(t *testing.T) (src, dst string)
		wantErr bool
		check   func(t *testing.T, dst string)
	}{
		{
			name: "copies files and directories",
			setup: func(t *testing.T) (src, dst string) {
				src = t.TempDir()
				dst = t.TempDir()
				require.NoError(t, os.WriteFile(filepath.Join(src, "a.txt"), []byte("alpha"), 0o644))
				require.NoError(t, os.MkdirAll(filepath.Join(src, "sub"), 0o755))
				require.NoError(t, os.WriteFile(filepath.Join(src, "sub", "b.txt"), []byte("beta"), 0o644))
				return src, dst
			},
			check: func(t *testing.T, dst string) {
				raw, err := os.ReadFile(filepath.Join(dst, "a.txt"))
				require.NoError(t, err)
				assert.Equal(t, "alpha", string(raw))
				raw, err = os.ReadFile(filepath.Join(dst, "sub", "b.txt"))
				require.NoError(t, err)
				assert.Equal(t, "beta", string(raw))
			},
		},
		{
			name: "refuses absent source",
			setup: func(t *testing.T) (src, dst string) {
				return filepath.Join(t.TempDir(), "nope"), filepath.Join(t.TempDir(), "dst")
			},
			wantErr: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			src, dst := tc.setup(t)
			err := copyTree(src, dst)
			if tc.wantErr {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)
			tc.check(t, dst)
		})
	}
}

// TestWorkerCoverCopyFile: copyFile copies one regular file into place, making
// its parent directory, and refuses a source that does not exist.
func TestWorkerCoverCopyFile(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		setup   func(t *testing.T) (src, dst string, mode os.FileMode)
		wantErr bool
		check   func(t *testing.T, dst string)
	}{
		{
			name: "copies a file with its content",
			setup: func(t *testing.T) (src, dst string, mode os.FileMode) {
				dir := t.TempDir()
				src = filepath.Join(dir, "in.txt")
				require.NoError(t, os.WriteFile(src, []byte("hello"), 0o644))
				return src, filepath.Join(dir, "out", "copied.txt"), 0o644
			},
			check: func(t *testing.T, dst string) {
				raw, err := os.ReadFile(dst)
				require.NoError(t, err)
				assert.Equal(t, "hello", string(raw))
			},
		},
		{
			name: "refuses absent source",
			setup: func(t *testing.T) (src, dst string, mode os.FileMode) {
				return filepath.Join(t.TempDir(), "missing"), filepath.Join(t.TempDir(), "out.txt"), 0o644
			},
			wantErr: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			src, dst, mode := tc.setup(t)
			err := copyFile(src, dst, mode)
			if tc.wantErr {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)
			tc.check(t, dst)
		})
	}
}

// TestWorkerCoverRefreshSlot: RefreshSlot creates the slot directory and copies
// the worker's home directory into it, one way, and refuses a worker_dir that does
// not exist.
func TestWorkerCoverRefreshSlot(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		worker  func(t *testing.T) Worker
		slot    int
		wantErr bool
		check   func(t *testing.T, w Worker)
	}{
		{
			name: "copies worker_dir into the slot",
			worker: func(t *testing.T) Worker {
				home := t.TempDir()
				require.NoError(t, os.WriteFile(filepath.Join(home, "run.sh"), []byte("#!/bin/sh\n"), 0o755))
				return Worker{WorkerDir: home}
			},
			slot: 1,
			check: func(t *testing.T, w Worker) {
				slot := w.SlotDir(1)
				assert.DirExists(t, slot)
				raw, err := os.ReadFile(filepath.Join(slot, "run.sh"))
				require.NoError(t, err)
				assert.Equal(t, "#!/bin/sh\n", string(raw))
			},
		},
		{
			name: "refuses absent worker_dir",
			worker: func(t *testing.T) Worker {
				return Worker{WorkerDir: filepath.Join(t.TempDir(), "lost")}
			},
			slot:    2,
			wantErr: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			w := tc.worker(t)
			err := w.RefreshSlot(tc.slot)
			if tc.wantErr {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)
			tc.check(t, w)
		})
	}
}

// TestWorkerCoverWriteHarnessConfig: WriteHarnessConfig writes the harness config
// into the slot directory as opencode.json and refuses when the slot directory
// cannot be created.
func TestWorkerCoverWriteHarnessConfig(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		worker  func(t *testing.T) Worker
		slot    int
		wantErr bool
		check   func(t *testing.T, w Worker, path string)
	}{
		{
			name: "writes the config into the slot",
			worker: func(t *testing.T) Worker {
				w := Worker{WorkerDir: t.TempDir()}
				return w
			},
			slot: 0,
			check: func(t *testing.T, w Worker, path string) {
				assert.FileExists(t, path, "open.json was written into the slot directory")
				var cfg map[string]any
				require.NoError(t, json.Unmarshal(w.HarnessConfig(), &cfg))
				provider, ok := cfg["provider"].(map[string]any)
				require.True(t, ok, "the config has a provider map: %v", cfg)
				assert.Contains(t, provider, w.Provider, "the config names the provider %q", w.Provider)
			},
		},
		{
			name: "refuses absent slot directory",
			worker: func(t *testing.T) Worker {
				return Worker{WorkerDir: filepath.Join(t.TempDir(), "worker")}
			},
			slot:    3,
			wantErr: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			w := tc.worker(t)
			if !tc.wantErr {
				require.NoError(t, os.MkdirAll(w.SlotDir(tc.slot), 0o755), "the main path has a slot directory to write into")
			}
			path, err := w.WriteHarnessConfig(tc.slot)
			if tc.wantErr {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)
			tc.check(t, w, path)
		})
	}
}

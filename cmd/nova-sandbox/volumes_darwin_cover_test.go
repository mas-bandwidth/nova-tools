//go:build darwin

// Unit coverage of the diskutil volume manager's own code paths, the ones
// volumes_darwin_test.go cannot reach because it builds a manager over a fake diskutil and
// `volumeRootUsable` wholesale to test Create's contract: oneLineOf,
// rootOwnedAndWritable and Used. Each test is a table of named cases under
// t.Run, opens with t.Parallel(), writes only inside its own t.TempDir(),
// and runs no process: oneLineOf is pure string work, rootOwnedAndWritable
// asks the operating system, and Used is one statfs.
//
// runDiskutil is not covered here, deliberately: it IS the file's subprocess
// door, and reaching its body means exec'ing a real diskutil, which no unit
// test may do and which it offers no seam to fake — the seam it would fake
// (the PATH lookup) is the process's own, and swapping it is a package-wide
// mutation the serial allowlist refuses. Its callers are all tested through
// the fake diskutil.
package main

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestVolumesDarwinCoverOneLineOfTakesTheLastNonEmptyLine pins the fold of a
// diskutil failure into the one sentence runDiskutil puts in the error: the
// last non-empty line, trimmed, and nothing at all when output said nothing.
func TestVolumesDarwinCoverOneLineOfTakesTheLastNonEmptyLine(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "the last line of a paragraph says why",
			in:   "Started an APFS operation.\nLooking up the disk.\nCould not find disk.\n",
			want: "Could not find disk.",
		},
		{
			name: "stderr and stdout folded with a space",
			in:   "The file “disk18” could not be found.\n no device node\n",
			want: "no device node",
		},
		{
			name: "one line, no newline",
			in:   "Nothing to do.",
			want: "Nothing to do.",
		},
		{
			name: "trailing blank lines carry no sentence",
			in:   "the real reason\n\n   \n\n",
			want: "the real reason",
		},
		{
			name: "surrounding spaces are trimmed",
			in:   "\n\t  padded sentence  \n",
			want: "padded sentence",
		},
		{
			name: "a fold printed with CRLF endings still ends on its line",
			in:   "first line\r\nlast line of the reason\r\n",
			want: "last line of the reason",
		},
		{
			name: "an empty fold says nothing, and that is the answer",
			in:   "",
			want: "",
		},
		{
			name: "a fold of only whitespace says nothing either",
			in:   "\n \n\t\n",
			want: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, oneLineOf(tt.in), "oneLineOf(%q)", tt.in)
		})
	}
}

// TestVolumesDarwinCoverRootOwnedAndWritableAsksOwnerThenWrite pins the two
// questions a new volume's root must answer — this user's, and writable by
// this user — the probe file the write answer leaves nothing behind, and the
// refusals the contention and the half-mounted volume actually produce.
func TestVolumesDarwinCoverRootOwnedAndWritableAsksOwnerThenWrite(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		// stage puts the root in place and names it; "" of says means the
		// root is usable.
		stage func(t *testing.T) string
		says  string
	}{
		{
			name: "the caller's own writable root is usable",
			stage: func(t *testing.T) string {
				dir := t.TempDir()
				require.NoError(t, os.WriteFile(filepath.Join(dir, "survivor"), []byte("card"), 0o600))
				return dir
			},
			says: "",
		},
		{
			name: "a root that is not this user's is refused before anything is written",
			stage: func(t *testing.T) string {
				// A test cannot chown without root, so this asks the question of a
				// path every macOS owns as root's, which the owner check refuses
				// before its write probe could touch it.
				root := "/etc"
				fi, err := os.Stat(root)
				if err != nil {
					t.Skipf("this machine has no %s to ask the owner question of: %v", root, err)
				}
				if st, ok := fi.Sys().(*syscall.Stat_t); !ok || int(st.Uid) == os.Getuid() {
					t.Skipf("this process owns %s, so a uid mismatch cannot be staged without root", root)
				}
				return root
			},
			says: "owned by uid",
		},
		{
			name: "a root this user owns and cannot write is refused",
			stage: func(t *testing.T) string {
				if os.Getuid() == 0 {
					t.Skipf("root writes wherever it likes, so a mode-0500 root cannot refuse on this machine")
				}
				root := filepath.Join(t.TempDir(), "volume-root")
				require.NoError(t, os.Mkdir(root, 0o700))
				require.NoError(t, os.Chmod(root, 0o500))
				return root
			},
			says: "owned by this user and still not writable",
		},
		{
			name: "a mount that is not there refuses with its stat error",
			stage: func(t *testing.T) string {
				return filepath.Join(t.TempDir(), "not-mounted")
			},
			says: "no such file",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := tt.stage(t)
			err := rootOwnedAndWritable(root)
			if tt.says == "" {
				require.NoError(t, err, "the caller's own writable root was refused; every create would die as if the machine were contended")
				entries, err := os.ReadDir(root)
				require.NoError(t, err)
				names := make([]string, 0, len(entries))
				for _, e := range entries {
					names = append(names, e.Name())
				}
				assert.Equal(t, []string{"survivor"}, names, "the write probe was not removed under its mount, or it ate the caller's own files: %v", names)
				return
			}
			require.Error(t, err, "the root was called usable, and a run would mkdir in it only to die with `permission denied` and no cause")
			assert.ErrorContains(t, err, tt.says, "the refusal does not name which of the two questions it failed: %v", err)
		})
	}
}

// TestVolumesDarwinCoverUsedAsksTheFilesystemNotDiskutil pins Used's single
// statfs: a real filesystem answers with the bytes it holds, and a path with
// no filesystem behind it refuses with 0 and the statfs error — and neither
// reaches a process.
func TestVolumesDarwinCoverUsedAsksTheFilesystemNotDiskutil(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		mount   func(t *testing.T) string
		wantErr bool
	}{
		{
			name: "a real filesystem answers the bytes it holds",
			mount: func(t *testing.T) string {
				dir := t.TempDir()
				require.NoError(t, os.WriteFile(filepath.Join(dir, "work"), make([]byte, 128*1024), 0o600))
				return dir
			},
		},
		{
			name: "a path with no filesystem behind it refuses",
			mount: func(t *testing.T) string {
				return filepath.Join(t.TempDir(), "not-a-mount")
			},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			used, err := diskutilVolumes{}.Used(tt.mount(t))
			if tt.wantErr {
				require.Error(t, err, "Used answered for a path that is not a filesystem")
				assert.ErrorIs(t, err, syscall.ENOENT, "the refusal is not the statfs error: %v", err)
				assert.Zero(t, used, "the refusal still carried %d bytes of usage", used)
				return
			}
			require.NoError(t, err, "Used failed on a directory of this machine's own filesystem")
			assert.Positive(t, used, "a volume in use answers 0 bytes: the statfs fields or the block size were read wrong")
		})
	}
}

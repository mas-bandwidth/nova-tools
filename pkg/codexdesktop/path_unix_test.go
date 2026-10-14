//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package codexdesktop

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

type fakeFileInfo struct {
	mode fs.FileMode
	uid  uint32
}

func (info fakeFileInfo) Name() string       { return "fixture" }
func (info fakeFileInfo) Size() int64        { return 0 }
func (info fakeFileInfo) Mode() fs.FileMode  { return info.mode }
func (info fakeFileInfo) ModTime() time.Time { return time.Time{} }
func (info fakeFileInfo) IsDir() bool        { return info.mode.IsDir() }
func (info fakeFileInfo) Sys() any           { return &syscall.Stat_t{Uid: info.uid} }

func TestValidateSocketPathWithRefusesUnsafeMetadata(t *testing.T) {
	t.Parallel()

	const uid = uint32(42)
	socketPath := "/owned/ipc.sock"
	parent := filepath.Dir(socketPath)
	secureParent := fakeFileInfo{mode: os.ModeDir | 0o700, uid: uid}
	secureSocket := fakeFileInfo{mode: os.ModeSocket | 0o600, uid: uid}
	tests := []struct {
		name   string
		parent os.FileInfo
		socket os.FileInfo
	}{
		{name: "secure", parent: secureParent, socket: secureSocket},
		{name: "parent symlink", parent: fakeFileInfo{mode: os.ModeSymlink | 0o700, uid: uid}, socket: secureSocket},
		{name: "foreign socket", parent: secureParent, socket: fakeFileInfo{mode: os.ModeSocket | 0o600, uid: uid + 1}},
		{name: "writable socket", parent: secureParent, socket: fakeFileInfo{mode: os.ModeSocket | 0o622, uid: uid}},
		{name: "regular file", parent: secureParent, socket: fakeFileInfo{mode: 0o600, uid: uid}},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			lstat := func(path string) (os.FileInfo, error) {
				switch path {
				case parent:
					return test.parent, nil
				case socketPath:
					return test.socket, nil
				default:
					return nil, errors.New("unexpected path")
				}
			}
			err := validateSocketPathWith(socketPath, uid, lstat)
			if test.name == "secure" {
				assert.NoError(t, err)
			} else {
				assert.Error(t, err)
			}
		})
	}
}

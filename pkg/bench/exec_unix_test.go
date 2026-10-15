//go:build unix

package bench

import (
	"bytes"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
)

// A FIFO in the tree is refused (mkfifo is unix only).
func TestWriteTreeRefusesSomethingThatIsNotAFileDirOrSymlink(t *testing.T) {
	t.Parallel()
	src := t.TempDir()
	if err := syscall.Mkfifo(filepath.Join(src, "pipe"), 0o644); err != nil {
		t.Skipf("mkfifo: %v", err)
	}
	var b bytes.Buffer
	assert.ErrorContains(t, WriteTree(&b, src, false), "refusing to copy it")
}

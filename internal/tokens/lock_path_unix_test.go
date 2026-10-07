//go:build unix

package tokens

import (
	"testing"
)

func TestFoldLockRefusesFIFO(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	path := r.fifo()
	r.requireRefused(r.take())
	r.requireFIFO(path)
}

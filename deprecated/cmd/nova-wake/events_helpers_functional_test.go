//go:build functional

package main

import (
	"sync"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/bus"
)

// holdLock takes the advisory lock this repo's own tools take on a path, so a
// test can put a lock source's source in the one state it cannot otherwise
// reach: held by somebody else. It is internal/bus's own primitive, because the
// probe under test speaks that protocol and a second lock implementation in a
// test proves nothing about the first.
func holdLock(t *testing.T, path string) func() {
	t.Helper()
	release, err := bus.LockFile(path, 0)
	if err != nil {
		t.Fatalf("holding %s: %v", path, err)
	}
	var once sync.Once
	return func() { once.Do(release) }
}

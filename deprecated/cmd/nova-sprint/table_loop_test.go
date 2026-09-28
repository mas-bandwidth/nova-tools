package main

import (
	"bytes"
	"sync"
	"testing"
	"time"
)

type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

// waitFor polls cond until it holds or NOVA_TEST_WAIT (30 s) passes.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(holdWait())
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("no %s within %s", what, holdWait())
		}
		time.Sleep(5 * time.Millisecond) // wall-ok: polling a condition in a test
	}
}

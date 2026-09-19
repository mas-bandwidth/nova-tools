package main

// react_test.go drives the react verb with miniredis as the bus and a fake forge, so the
// test reaches no network and starts no redis server of its own.

import (
	"bytes"
	"os"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/ci"
)

type reactFakeForge struct{ snap ci.Snapshot }

func (f *reactFakeForge) Snapshot() (ci.Snapshot, error) { return f.snap, nil }

func reactWait() time.Duration {
	if v := os.Getenv("NOVA_TEST_WAIT"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			return d
		}
	}
	return 30 * time.Second
}

// TestReactRefusesMissingRedis: --redis is required and its absence is exit 2.
func TestReactRefusesMissingRedis(t *testing.T) {
	var out, errb bytes.Buffer
	code := run([]string{"react"}, &out, &errb, production())
	if code != 2 {
		t.Fatalf("react without --redis exit = %d, want 2; stderr=%s", code, errb.String())
	}
	if !bytes.Contains(errb.Bytes(), []byte("--redis")) {
		t.Errorf("the refusal does not name --redis: %s", errb.String())
	}
}

// The enqueue path itself is TestReactEnqueuesIntoTheLanesQueueWhereQueueStatusCanSeeIt
// in dogfood_test.go. It used to live here and assert that PR 42 landed in the redis set
// `merge:queue` -- which is exactly what edge 17 turned out to be: a set nothing in this
// tree reads. A test that pins the wrong door pins the bug.

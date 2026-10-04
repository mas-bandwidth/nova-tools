package main

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
	"github.com/stretchr/testify/assert"
)

func TestServerReviewLandingStoreFailureIsVisible(t *testing.T) {
	t.Parallel()
	a := newApp(func(string) string { return "" })
	t.Cleanup(a.close)
	a.backend = func(context.Context, string, sprint.Names) (store.Backend, error) {
		return nil, errors.New("injected store outage")
	}
	var out bytes.Buffer
	code := a.landRound(context.Background(), "mem:unavailable", nil, &out)
	assert.NotEqual(t, 0, code, "unreadable queue is not an empty queue")
	assert.Contains(t, out.String(), "injected store outage", "the loop must surface why it cannot land")
}

func TestServerReviewLandingSetupFailureIsVisible(t *testing.T) {
	t.Parallel()
	r := newLandRig(t)
	r.ok("add --stream s1 --count 1")
	r.queued(map[string]string{"s1-1": r.head("s1-1", "main", "a.txt", "a\n")}, "s1-1")
	var out bytes.Buffer
	code := r.a.landRound(context.Background(), "mem:0", []string{"--repo-dir", filepath.Join(t.TempDir(), "missing"), "--base", "main"}, &out)
	assert.NotEqual(t, 0, code)
	assert.Contains(t, out.String(), "not a directory", "a pre-batch land refusal must not disappear from the loop log")
}

package main

import (
	"bytes"
	"context"
	"errors"
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

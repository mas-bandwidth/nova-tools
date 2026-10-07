package gocache

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestGocacheCoverFail pins Count.fail: each call blips the failure count, and
// when no reason is yet recorded it stamps the path and error; a reason already
// set is never overwritten.
func TestGocacheCoverFail(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name       string
		start      Count
		path       string
		err        error
		wantFailed int
		wantWhy    string
	}{
		{
			name:       "main path records the first failure",
			start:      Count{},
			path:       "/cache/00/abc-d",
			err:        errors.New("permission denied"),
			wantFailed: 1,
			wantWhy:    "/cache/00/abc-d: permission denied",
		},
		{
			name:       "refusal keeps an existing reason",
			start:      Count{Failed: 1, Why: "/cache/00/abc-d: permission denied"},
			path:       "/cache/01/def-a",
			err:        errors.New("read-only"),
			wantFailed: 2,
			wantWhy:    "/cache/00/abc-d: permission denied",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			c := tc.start
			c.fail(tc.path, tc.err)
			assert.Equal(t, tc.wantFailed, c.Failed, "Failed count")
			assert.Equal(t, tc.wantWhy, c.Why, "Why")
		})
	}
}

// TestGocacheCoverNoteFailed pins Trim.noteFailed: a path is recorded once, the
// failed set is allocated on the first failure and left in place thereafter so
// a path is never tried twice.
func TestGocacheCoverNoteFailed(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name       string
		start      Trim
		path       string
		wantFailed map[string]bool
	}{
		{
			name:       "main path allocates and records the first path",
			start:      Trim{},
			path:       "/cache/00/abc-d",
			wantFailed: map[string]bool{"/cache/00/abc-d": true},
		},
		{
			name:       "refusal leaves an existing set and adds the path",
			start:      Trim{failed: map[string]bool{"/cache/00/abc-d": true}},
			path:       "/cache/01/def-a",
			wantFailed: map[string]bool{"/cache/00/abc-d": true, "/cache/01/def-a": true},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tr := tc.start
			tr.noteFailed(tc.path)
			assert.Equal(t, tc.wantFailed, tr.failed, "failed set")
		})
	}
}

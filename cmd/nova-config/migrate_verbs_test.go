//go:build !functional

// The functional build already holds TestMigrateTwiceThenTheSixVerbs against
// Postgres, so this file is the unit-tier run and is not compiled with that tag.

package main

import (
	"bytes"
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// closeCounted counts Close and refuses a later read, so a close that runs
// before the verb uses the store fails the verb, and a close that is named
// and not called leaves the count short.
type closeCounted struct {
	pgStore
	n      *int
	closed bool
}

func (s *closeCounted) Close() error {
	s.closed = true
	*s.n++
	return s.pgStore.Close()
}

func (s *closeCounted) Version(ctx context.Context) (int, error) {
	if s.closed {
		return 0, fmt.Errorf("store closed")
	}
	return s.pgStore.Version(ctx)
}

// TestMigrateTwiceThenTheSixVerbs migrates one file twice, then runs one
// kind's six verbs (docs/nova-config/README.md, "The kinds"). Each open
// closes the store after the verb has chosen its result.
func TestMigrateTwiceThenTheSixVerbs(t *testing.T) {
	t.Parallel()
	h := newHarness()
	h.dir = t.TempDir()
	var closes int
	d := h.deps()
	open := d.openStore
	d.openStore = func(ctx context.Context, dsn string) (pgStore, error) {
		st, err := open(ctx, dsn)
		if err != nil {
			return nil, err
		}
		return &closeCounted{pgStore: st, n: &closes}, nil
	}
	step := func(args ...string) (int, string, string) {
		t.Helper()
		var out, errb bytes.Buffer
		code := run(args, &out, &errb, d)
		return code, out.String(), errb.String()
	}
	file := []string{"--file", "try.json"}
	n := currentSchema()
	code, out, errs := step(append([]string{"migrate"}, file...)...)
	require.Equal(t, 0, code, "migrate: %s", errs)
	assert.Contains(t, out, fmt.Sprintf("from=0 to=%d applied=%d", n, n))
	code, out, errs = step(append([]string{"migrate"}, file...)...)
	require.Equal(t, 0, code, "migrate twice: %s", errs)
	assert.Contains(t, out, fmt.Sprintf("from=%d to=%d applied=0", n, n))

	verbs := []struct {
		args []string
		want string
	}{
		{[]string{"machine", "add", "m1", "--user", "u", "--seat", "s", "--slots", "8", "--width", "4", "--as", "a1"}, "CONFIG ADD kind=machine name=m1"},
		{[]string{"machine", "set", "m1", "--width", "6", "--as", "a1"}, "CONFIG SET kind=machine name=m1"},
		{[]string{"machine", "list"}, "CONFIG LIST kind=machine rows=1"},
		{[]string{"machine", "show", "m1"}, "MACHINE name=m1"},
		{[]string{"machine", "history", "m1"}, "CONFIG HISTORY kind=machine name=m1"},
		{[]string{"machine", "remove", "m1", "--as", "a1"}, "CONFIG REMOVE kind=machine name=m1"},
	}
	for _, v := range verbs {
		code, out, errs = step(append(v.args, file...)...)
		require.Equal(t, 0, code, "%v\nstdout: %s\nstderr: %s", v.args, out, errs)
		assert.Contains(t, out, v.want, "%v\n%s", v.args, out)
	}
	assert.Equal(t, 8, h.opens, "migrate twice and the six verbs each open the store")
	assert.Equal(t, h.opens, closes, "each open closes the store")
}

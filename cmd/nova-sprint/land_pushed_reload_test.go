package main

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
	"github.com/mas-bandwidth/nova-tools/pkg/ntable"
)

// failPushedReload lets the receipt clear apply, then fails the canonical read
// that must precede another landing pass.
type failPushedReload struct {
	store.Backend
	applied bool
}

func (b *failPushedReload) Apply(ctx context.Context, m ntable.BatchManifest) (ntable.Receipt, error) {
	r, err := b.Backend.Apply(ctx, m)
	if err == nil {
		b.applied = true
	}
	return r, err
}

func (b *failPushedReload) Shapes(ctx context.Context, tables []string) ([]ntable.Table, error) {
	if b.applied && slices.Contains(tables, sprint.Fleet) {
		return nil, errors.New("injected receipt reload failure")
	}
	return b.Backend.Shapes(ctx, tables)
}

func TestLandStopsWhenClearedReceiptCannotBeReloaded(t *testing.T) {
	t.Parallel()
	r := newLandRig(t)
	r.ok("add --stream s1 --count 1 --one")
	head := r.head("s1-1", "main", "old.txt", "old\n")
	r.queued(map[string]string{"s1-1": head}, "s1-1")
	st, err := r.a.store(common{redis: "mem:0", actor: "tester"})
	require.NoError(t, err)
	mark := store.Step{Verb: "land", Load: []string{sprint.Work, sprint.Merge}, Actor: "tester",
		Plan: func(s *sprint.Snapshot) sprint.Plan {
			return sprint.MarkPushedUnreported(s, "s1", "old-push", []sprint.PushedPin{{ID: "s1-1", Head: head, Attempt: "1"}})
		}}
	res, err := st.Run(t.Context(), mark)
	require.NoError(t, err)
	require.NotEmpty(t, res.Moved)
	r.ok("return s1-1 --reason 'new attempt'")
	r.ok("rework s1-1 --fix 'new attempt'")
	s, err := st.Load(t.Context(), []string{sprint.Work, sprint.Merge}, nil)
	require.NoError(t, err)
	require.NotEmpty(t, sprint.ClearObsoletePushedUnreported(s, "s1").Units)

	backend := &failPushedReload{Backend: r.m}
	r.a.backend = func(context.Context, string, sprint.Names) (store.Backend, error) { return backend, nil }
	before := r.git(r.remote, "rev-parse", "main")
	code, out, errs := r.do("land --stream s1 --repo-dir " + r.clone + " --base main")
	assert.Equal(t, 2, code, out+errs)
	assert.Contains(t, errs, "obsolete pushed receipt was cleared but its result could not be read")
	assert.Contains(t, errs, "injected receipt reload failure")
	assert.Equal(t, before, r.git(r.remote, "rev-parse", "main"), "no new batch may run from a missing snapshot")
	r.a.backend = func(context.Context, string, sprint.Names) (store.Backend, error) { return r.m, nil }
	r.clean()
}

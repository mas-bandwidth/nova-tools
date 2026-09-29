package store

import "testing"

// S3. A release whose transaction kept failing because the fence moved is
// settled as Acquire settles it: the fence no longer holding the operation
// (another writer finished it) is success; still holding it, or a commit
// whose caller's result is not recorded, is not.
func TestAReleaseTheFenceMovedUnderIsSettledByTheFence(t *testing.T) {
	t.Parallel()
	op := OpRecord{ID: "accept-1-1", CallerOp: "worker-3"}
	bare := OpRecord{ID: "fleet-2-1"}
	for _, c := range []struct {
		name     string
		op       OpRecord
		commit   bool
		held     string
		recorded bool
		want     bool
	}{
		{"finished by another writer, result recorded", op, true, "", true, true},
		{"finished, then another operation took the fence", op, true, "start-9-1", true, true},
		{"still held", op, true, op.ID, true, false},
		{"gone but the caller's result not recorded", op, true, "", false, false},
		{"no caller id: gone is finished", bare, true, "", false, true},
		{"an abandon: gone is done", op, false, "", false, true},
		{"an abandon still held", op, false, op.ID, false, false},
	} {
		if got := released(c.op, c.commit, c.held, c.recorded); got != c.want {
			t.Errorf("%s: released %v, want %v", c.name, got, c.want)
		}
	}
}

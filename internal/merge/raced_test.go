package merge

import "testing"

// Rule 21 tells two refusals apart and gives them different lines: a rejected LEASE is
// MERGE RACED -- the base moved, the next pass rebuilds -- and "a push the remote refuses
// for any reason OTHER than the lease ... is MERGE BLOCKED entry=... missing=
// atomic_publication", which a person must fix by hand.
//
// Widening the lease word turns a protected branch into "the base moved after the gate",
// which sends a reader to wait for a next pass that will be refused the same way forever.
func TestOnlyALeaseRejectionIsRaced(t *testing.T) {
	raced := []string{
		" ! [rejected]        aaaa -> main (stale info)",
		"error: failed to push some refs\n ! [rejected] main (stale info)",
	}
	blocked := []string{
		" ! [remote rejected] aaaa -> main (protected branch hook declined)",
		" ! [rejected]        aaaa -> main (non-fast-forward)",
		"error: cannot lock ref 'refs/heads/main': is at bbbb but expected aaaa",
		"remote: Permission to o/n.git denied to nobody.",
		" ! [remote rejected] main -> main (pre-receive hook declined)",
	}
	for _, out := range raced {
		if !isLeaseRejection(out) {
			t.Errorf("a rejected lease is MERGE RACED; this was not read as one:\n%s", out)
		}
	}
	for _, out := range blocked {
		if isLeaseRejection(out) {
			t.Errorf("a push the remote refused for any reason OTHER than the lease is MERGE BLOCKED missing=atomic_publication, never MERGE RACED:\n%s", out)
		}
	}
}

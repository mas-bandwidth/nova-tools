package main

// canCreateIn has no read-only answer on Windows, where ACLs decide (internal/atomicfile's
// canWrite says the same); the write's own error is the refusal there.
func canCreateIn(string) error { return nil }

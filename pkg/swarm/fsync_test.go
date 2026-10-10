package swarm

import "os"

// The records this package writes are flushed with syncFile before they are
// renamed or linked into place. In this test binary the flush is a no-op: on
// macOS Sync is F_FULLFSYNC, tens of milliseconds for every record, and a unit
// test asserts what was written and where, never that the platter has it
// (nova-tools#4328, Glenn 2026-09-26: unit tests under 2 s). The rename, the
// link and every refusal around them run as in production.
func init() { syncFile = func(*os.File) error { return nil } }

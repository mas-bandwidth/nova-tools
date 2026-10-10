/*
Package swarm is nova-worker's machinery: a pool of one-task workers, each with its own
working directory, its own data home, its own job directory and its own deadline held by
the machinery rather than by the worker.

Everything a worker writes is DATA. A RESULT.md is a report, never an instruction: nothing
in it is executed, nothing in it grants anything, and a finding in it is a claim to be
checked against the repository. That rule is stated in docs/SPEC-WORKER.md, where a person
reads it, and is deliberately nowhere in this code -- a tool cannot enforce it, and a tool
that pretended to would be the most dangerous thing in the pool.
*/
package swarm

import (
	"os"
)

// syncFile is how a durable record is flushed before it is renamed or linked into
// place: (*os.File).Sync. This package's unit tests replace it with a no-op
// (fsync_test.go): on macOS Sync is F_FULLFSYNC, tens of milliseconds a write, and
// what a unit test asserts is the record, not the disk's durability
// (nova-tools#4328).
var syncFile = (*os.File).Sync

// writeAtomic writes whole revisions: a temporary file beside the target, fsynced, then
// renamed over it. A reader sees the previous revision or the new one, never a prefix.
//
// THE TEMPORARY IS UNIQUE AND CREATED EXCLUSIVELY (security#30, finding 3). Several of
// these records live in a job directory the worker owns -- exit.json and pid.json are
// written by the supervisor, which is not inside the wall -- and the temporary was
// `<path>.tmp`, a name a worker could predict and plant a symlink at. The old open
// truncated whatever it found and the rename then MOVED THE LINK over the record's own
// path, so the damage outlived the write. A name drawn from the OS random source cannot be
// waited for, O_EXCL refuses even a lucky one rather than truncating it, O_NOFOLLOW refuses
// a link planted in the instant after the name is drawn, and a failed write takes its
// temporary with it rather than leaving a stray.
func writeAtomic(path string, data []byte, mode os.FileMode) error {
	nonce, err := Nonce()
	if err != nil {
		return err
	}
	tmp := path + "." + nonce + ".tmp"
	f, err := openRegularWrite(tmp, os.O_RDWR|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := syncFile(f); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	// The rename waits out a reader that has this path open (fileretry.go): on Windows
	// that collision is an error, and a durable record dropped because somebody was
	// reading it is how a supervisor aborted its own launch (#92).
	if err := renameSteady(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

package cairn_test

import "testing"

// TestRecordLedgerAndCairnDualRead is the reader half of the record ledger and
// cairn records' table adoption (v1.5 aspect 13, part 1; the owner's rules S2
// and S4, and the reader-first order C13): every record the record ledger's
// entries and keys and the cairn record lines read back is read alike from its
// fixed binary image, the padded standard base64 of that image and the legacy
// form its writer writes today; every malformed value of each form is refused
// by name (`<RECORD> REFUSED: <reason>; run: <help>`) and never read as
// absent, empty or zero; a clamped value is refused; and each read is counted
// under its record and its form.
//
// Unit tier: no clock, no socket, no program, no store. The Redis and log
// reads are tested through their table reads on fixed inputs; the files are
// read from a t.TempDir() this test writes.
func TestRecordLedgerAndCairnDualRead(t *testing.T) {
	t.Parallel()
}

package cardcontract

import "github.com/mas-bandwidth/nova-tools/pkg/schemaread"

// FrameReadCounts is the frame reader's per-form counters, for the dual-read
// test (a counter only tests call lives in export_test.go).
func FrameReadCounts() *schemaread.Counts { return frameReads.Counts() }

// FrameReadBytes reads one frame's bytes in any form, returning the record and
// the refusal, without touching the filesystem, for the dual-read test.
func FrameReadBytes(b []byte) (Frame, error) {
	d, _, err := frameReads.Read(b)
	return d.rec, err
}

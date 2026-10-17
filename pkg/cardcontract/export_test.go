package cardcontract

import "github.com/mas-bandwidth/nova-tools/pkg/schemaread"

// FrameReadCounts is the read counter of ReadFrame, for the dual-read test; a
// counter only the test calls.
func FrameReadCounts() *schemaread.Counts { return frameRead.Counts() }

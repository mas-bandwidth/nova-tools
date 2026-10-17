package fuse

import "github.com/mas-bandwidth/nova-tools/pkg/schemaread"

// NewBoxDualReadReader is a fresh fuse box reader with its own counters, so a
// test counts its own reads while other tests read boxes in parallel. It lives
// in the test build because no production main calls it (the dead code rule,
// internal/ci TestDeadCode).
func NewBoxDualReadReader() *schemaread.Reader[Box] {
	return schemaread.NewReader(boxCodec)
}

package cairn

// The test-facing doors of the log reader: its counters and the table-form writer
// the writer switch (a later card) moves into appendLog. They live in the test
// build because no production main calls them yet (the dead code rule,
// internal/ci TestDeadCode).

import (
	"errors"

	"github.com/mas-bandwidth/nova-tools/pkg/schemaread"
)

// LogReadCounts is the log reader's counters: the reads per form and the
// refusals per reason, the evidence a cutover reads before the writer
// switches (S4).
func LogReadCounts() map[string]uint64 {
	m := logRecords.Counts().Snapshot()
	m["refused_"+schemaread.ReasonMalformed] += openRefused.Load()
	return m
}

// LogLineText is one log record in a table form (the wire, its base64 or the
// schema JSON text), as appendLog will write it after the switch; it refuses
// what the reader refuses, before a byte is written.
func LogLineText(event, session, entry, stamp, publish, source string, form schemaread.Form) ([]byte, error) {
	out, err := logRecords.Write(&logRecord{Event: event, Session: session, Entry: entry, Stamp: stamp, Publish: publish, Source: source}, form)
	if err == nil && len(out) == 0 {
		err = errors.New("the cairn log table is not installed: import internal/record/ledgerread")
	}
	return out, err
}

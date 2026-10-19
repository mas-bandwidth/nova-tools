package sprintwire

import (
	"github.com/mas-bandwidth/nova-tools/pkg/answerwire"
	"github.com/mas-bandwidth/nova-tools/pkg/schemaread"
)

// The test-only doors of the sprint answer's read: a fresh reader (so a test
// counts its own reads), the writers of the table forms the server does not
// write yet (writers stay legacy until the writer switch), and the counters of
// the worker's reads of a Result.

func newAnswerReader() *schemaread.Reader[answerDoc] { return schemaread.NewReader(answerCodec) }

func answerCounts() *schemaread.Counts { return answerReads.Counts() }

// answerWire is a read answer as the binary wire the generated writer writes.
func answerWire(d *answerDoc) []byte {
	n := answerwire.ResponseMeasure(d.v)
	if n < 0 {
		return nil
	}
	buf := make([]byte, n)
	if answerwire.ResponseSave(d.v, buf) != n {
		return nil
	}
	return buf
}

// answerJSON is a read answer as the schema JSON text the generated writer writes.
func answerJSON(d *answerDoc) []byte {
	n := answerwire.ResponseToJsonMeasure(d.v)
	if n < 0 {
		return nil
	}
	buf := make([]byte, n)
	if answerwire.ResponseToJson(d.v, buf) != n {
		return nil
	}
	return buf
}

func resultReadCount() uint64           { return resultReads.read.Load() }
func resultRefusedResultsCount() uint64 { return resultReads.results.Load() }
func resultRefusedOutcomeCount() uint64 { return resultReads.outcome.Load() }

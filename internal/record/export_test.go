package record

// The test-facing doors of the ledger reader: a set of its readers with its own counters, and the table-form writer
// the writer switch (a later card) moves into ReplaceLedgerDays. They live in the test
// build because no production main calls them yet (the dead code rule,
// internal/ci TestDeadCode).

import (
	"errors"

	"github.com/mas-bandwidth/nova-tools/pkg/schemaread"
)

// LedgerReaders is a set of ledger readers with its own counters, so a test
// counts its own reads while other tests read ledger days in parallel.
type LedgerReaders = ledgerReaders

// NewLedgerReaders is a set of ledger readers with empty counters.
func NewLedgerReaders() *LedgerReaders { return newLedgerReaders() }

// ReadDay is ReadLedgerDay through this set of readers.
func (r *ledgerReaders) ReadDay(day string, fields map[string]string) ([]LedgerEntry, error) {
	return r.readDay(day, fields)
}

// Counts is this set's read counters by record (LEDGER_KEY, LEDGER_VALUE):
// the reads per form and the refusals per reason, the evidence a cutover
// reads before the writers switch (S4).
func (r *ledgerReaders) Counts() map[string]map[string]uint64 {
	keys, values := r.keys.Counts().Snapshot(), r.values.Counts().Snapshot()
	keys["refused_"+schemaread.ReasonMalformed] += r.unknownKeys.Load()
	values["refused_"+schemaread.ReasonMalformed] += r.unknownValues.Load()
	return map[string]map[string]uint64{"LEDGER_KEY": keys, "LEDGER_VALUE": values}
}

// FieldText is a row's key and value in a table form (the table wire,
// its base64 or the schema JSON text), as a writer will write them after the
// switch; the writers stay legacy until then. It refuses what the reader
// refuses, before a byte is written.
func (r *ledgerReaders) FieldText(e LedgerEntry, form schemaread.Form) (field, value []byte, err error) {
	k := ledgerKeyText{Day: e.Day, Card: e.Card, Model: e.Model, Repo: e.Repo}
	v := ledgerValueText{Provider: e.Provider, Rough: int64(e.Rough), Sources: e.Sources, Tokens: make([]ledgerTokenText, len(e.Tokens))}
	for i := range e.Tokens {
		if e.Known[i] {
			n := e.Tokens[i]
			v.Tokens[i].Amount = &n
		}
	}
	if field, err = r.keys.Write(&k, form); err != nil {
		return nil, nil, err
	}
	if value, err = r.values.Write(&v, form); err != nil {
		return nil, nil, err
	}
	if len(field) == 0 || len(value) == 0 {
		return nil, nil, errors.New("the ledger tables are not installed: import internal/record/ledgerread")
	}
	return field, value, nil
}

// PrepareLedgerDays builds a batch's table-form fields as ReplaceLedgerDays
// writes them (the switched writer), keyed by day hash: a test writes them to
// a t.TempDir() and reads them back through the dual reader.
func (s *RedisLedger) PrepareLedgerDays(days []LedgerDay) (map[string]map[string]string, error) {
	prepared, err := s.prepareDays(days)
	if err != nil {
		return nil, err
	}
	out := make(map[string]map[string]string, len(prepared))
	for _, p := range prepared {
		fields := make(map[string]string, len(p.fields)/2)
		for i := 0; i < len(p.fields); i += 2 {
			fields[p.fields[i].(string)] = p.fields[i+1].(string)
		}
		out[p.key] = fields
	}
	return out, nil
}

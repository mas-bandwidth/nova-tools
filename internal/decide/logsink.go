// The log has two sinks and one contract (Glenn 2026-09-18): "the decision and
// escalation log lives in Postgres beside card results". A JSONL file is still
// the log on a bench with no database, and it is still what routelog.go writes;
// the table is the same rows in the same database as card_results, so a
// decision and the card result it produced are one join away, and the token
// report and the routing table read the decision rather than a scraped file.
//
// Nothing here rewrites the JSONL log. The sink is a seam BESIDE it: the file
// sink is AppendEntry and ReadEntries, unchanged, behind an interface the
// Postgres sink also satisfies. The summary is a projection of the rows and
// reads the same off either one.
//
// The DSN never reaches the process on argv. It arrives in the environment,
// under a name the caller gives with --dsn-env, put there by
// `nova-secrets exec --only <NAME>`; a verb that is given no name refuses and
// says which variable to set.
package decide

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

// PostgresLog is what --log is given to write the decision to the table rather
// than to a file. Any other value is a path.
const PostgresLog = "postgres"

// LogDSNEnv is the environment variable a decision-log DSN is read from when
// --dsn-env names none. It is the name `nova-secrets exec --only` is given, and
// it is documented in docs/SPEC-DECIDE.md.
const LogDSNEnv = "NOVA_DECIDE_LOG_DSN"

// LogSink is where a decision row lands. Append is the one writer; Entries is
// the read the summary is a projection of; Close releases whatever the sink
// holds. A file sink holds nothing and a table sink holds a pool.
type LogSink interface {
	Append(e Entry) error
	Entries() ([]Entry, error)
	Close() error
}

// OpenLogSink opens the sink the name asks for: PostgresLog is the table, and
// any other value is the path of the JSON lines log. An empty name is a
// refusal, never a guess at a path -- the same refusal AppendEntry has always
// made.
//
// dsnEnv is the environment variable the DSN arrives in; an empty dsnEnv means
// LogDSNEnv. The DSN itself is never a parameter, so it is never on argv and
// never in a process listing.
func OpenLogSink(name, dsnEnv string) (LogSink, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, fmt.Errorf("decide: no log; refusing to guess one. Pass a path, or %s with the DSN in $%s", PostgresLog, dsnEnvOr(dsnEnv))
	}
	if name != PostgresLog {
		return &FileSink{Path: name}, nil
	}
	dsn, err := LogDSN(dsnEnv)
	if err != nil {
		return nil, err
	}
	return OpenPostgresLog(dsn)
}

// LogDSN reads the DSN out of the environment under the name given, or
// LogDSNEnv when none is. An unset or empty variable is a refusal that names
// the variable and the one remedy.
func LogDSN(dsnEnv string) (string, error) {
	name := dsnEnvOr(dsnEnv)
	dsn := strings.TrimSpace(os.Getenv(name))
	if dsn == "" {
		return "", fmt.Errorf("decide: $%s is not set; the decision log's DSN comes in on the environment, never on argv: nova-secrets exec --only %s -- nova-decide ...", name, name)
	}
	return dsn, nil
}

// dsnEnvOr is the name given, or the default.
func dsnEnvOr(dsnEnv string) string {
	if s := strings.TrimSpace(dsnEnv); s != "" {
		return s
	}
	return LogDSNEnv
}

// FileSink is the log as it has always been: append-only JSON lines at a path
// the caller names, written by AppendEntry and read by ReadEntries.
type FileSink struct{ Path string }

// Append writes one row.
func (f *FileSink) Append(e Entry) error { return AppendEntry(f.Path, e) }

// Entries reads the rows back, in the order they were written.
func (f *FileSink) Entries() ([]Entry, error) { return ReadEntries(f.Path) }

// Close is a no-op: the file sink holds nothing open between rows.
func (f *FileSink) Close() error { return nil }

// FakeLogSink is the in-memory sink the unit tests run on. It is the same
// interface the table implements, so the unit suite exercises the contract --
// append, read back in order, an absent counter that stays absent -- without a
// socket, and the tagged integration test is what keeps the fake honest.
type FakeLogSink struct {
	mu   sync.Mutex
	rows []Entry

	// AppendErr, when set, is what Append returns: the seam for a test that
	// wants to see a sink fail.
	AppendErr error
}

// NewFakeLogSink returns an empty sink.
func NewFakeLogSink() *FakeLogSink { return &FakeLogSink{} }

// Append stores the row through the same shape the table writes, so a field the
// table would lose is lost here too.
func (f *FakeLogSink) Append(e Entry) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.AppendErr != nil {
		return f.AppendErr
	}
	row, err := logRowFor(e)
	if err != nil {
		return err
	}
	stored, err := row.entry()
	if err != nil {
		return err
	}
	f.rows = append(f.rows, stored)
	return nil
}

// Entries returns the rows in the order they were appended.
func (f *FakeLogSink) Entries() ([]Entry, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]Entry, len(f.rows))
	copy(out, f.rows)
	return out, nil
}

// Close is a no-op; the fake holds nothing.
func (f *FakeLogSink) Close() error { return nil }

// logRow is one row of decide_log in the table's own columns. It is the shape
// the Postgres sink writes and scans, and it is a pure function of an Entry, so
// the column mapping is tested without a database.
type logRow struct {
	TS                  time.Time
	UnitID              string
	Kind                string
	Files               int
	Packages            int
	Lanes               int
	Lane                string
	RungTried           string
	Height              int
	Confidence          float64
	Floor               float64
	SteppedUp           bool
	Escalated           bool
	Designated          bool
	Source              string
	RowanPick           string
	Reason              string
	Wait                string
	AwaitingTermination bool
	Refusal             string
	Outcome             string
	RungSucceeded       string
	Calls               int
	// TokensIn and TokensOut are NullInt64 because a counter the provider did
	// not report is SQL NULL. A zero is a measurement and is stored as one; an
	// absence is an absence (SPEC-TOKENS rule 14).
	TokensIn    sql.NullInt64
	TokensOut   sql.NullInt64
	UsageFailed bool
	Evidence    []byte
}

// logRowFor renders one Entry as the table's row. A stamp that is not a time is
// a refusal naming it: the durable record never takes a zero stamp for a
// timestamp nobody could read.
func logRowFor(e Entry) (logRow, error) {
	stamp := strings.TrimSpace(e.Time)
	var ts time.Time
	if stamp == "" {
		ts = time.Now().UTC()
	} else {
		parsed, err := time.Parse(time.RFC3339, stamp)
		if err != nil {
			return logRow{}, fmt.Errorf("decide: the log row for unit %s carries %q, which is not an RFC3339 time: %w", e.Unit, e.Time, err)
		}
		ts = parsed.UTC()
	}
	evidence, err := json.Marshal(e.Evidence)
	if err != nil {
		return logRow{}, fmt.Errorf("decide: encode the evidence for unit %s: %w", e.Unit, err)
	}
	kind := strings.TrimSpace(e.Kind)
	if kind == "" {
		kind = strings.TrimSpace(e.Evidence.Kind)
	}
	return logRow{
		TS:                  ts,
		UnitID:              e.Unit,
		Kind:                kind,
		Files:               e.Evidence.Files,
		Packages:            e.Evidence.Packages,
		Lanes:               e.Evidence.Lanes,
		Lane:                e.Evidence.LaneOwner,
		RungTried:           e.RungTried,
		Height:              e.Height,
		Confidence:          e.Confidence,
		Floor:               e.Floor,
		SteppedUp:           e.SteppedUp,
		Escalated:           e.Escalated,
		Designated:          e.Designated,
		Source:              e.Source,
		RowanPick:           e.RowanPick,
		Reason:              e.Reason,
		Wait:                e.Wait,
		AwaitingTermination: e.AwaitingTermination,
		Refusal:             e.Refusal,
		Outcome:             e.Outcome,
		RungSucceeded:       e.RungSucceeded,
		Calls:               e.Calls,
		TokensIn:            nullTokens(e.TokensIn),
		TokensOut:           nullTokens(e.TokensOut),
		UsageFailed:         e.UsageFailed,
		Evidence:            evidence,
	}, nil
}

// entry reads one row back as the Entry it was written from. The stamp comes
// back in the same RFC3339 UTC spelling the JSONL log uses, so a summary over
// the table and a summary over the file are the same text.
func (r logRow) entry() (Entry, error) {
	e := Entry{
		Time:                r.TS.UTC().Format(time.RFC3339),
		Unit:                r.UnitID,
		Kind:                r.Kind,
		RungTried:           r.RungTried,
		Height:              r.Height,
		Confidence:          r.Confidence,
		Floor:               r.Floor,
		SteppedUp:           r.SteppedUp,
		Escalated:           r.Escalated,
		Designated:          r.Designated,
		Source:              r.Source,
		RowanPick:           r.RowanPick,
		Reason:              r.Reason,
		Wait:                r.Wait,
		AwaitingTermination: r.AwaitingTermination,
		Refusal:             r.Refusal,
		Outcome:             r.Outcome,
		RungSucceeded:       r.RungSucceeded,
		Calls:               r.Calls,
		TokensIn:            tokensOrNil(r.TokensIn),
		TokensOut:           tokensOrNil(r.TokensOut),
		UsageFailed:         r.UsageFailed,
	}
	if len(r.Evidence) > 0 {
		if err := json.Unmarshal(r.Evidence, &e.Evidence); err != nil {
			return Entry{}, fmt.Errorf("decide: the log row for unit %s has evidence that is not one JSON object: %w", r.UnitID, err)
		}
	}
	return e, nil
}

// nullTokens writes a reported counter as a number and an unreported one as
// SQL NULL.
func nullTokens(n *int) sql.NullInt64 {
	if n == nil {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: int64(*n), Valid: true}
}

// tokensOrNil reads NULL back as the absence it is, and a number as the
// measurement it is -- including a measured zero.
func tokensOrNil(n sql.NullInt64) *int {
	if !n.Valid {
		return nil
	}
	v := int(n.Int64)
	return &v
}

// logColumns is the column order both the INSERT and the SELECT use, so the two
// can never drift apart.
var logColumns = []string{
	"ts", "unit_id", "kind", "files", "packages", "lanes", "lane",
	"rung_tried", "height", "confidence", "floor",
	"stepped_up", "escalated", "designated", "source", "rowan_pick", "reason",
	"wait", "awaiting_termination", "refusal",
	"outcome", "rung_succeeded",
	"calls", "tokens_in", "tokens_out", "usage_failed", "evidence",
}

// args is the row as the INSERT's parameters, in logColumns order.
func (r logRow) args() []any {
	return []any{
		r.TS, r.UnitID, r.Kind, r.Files, r.Packages, r.Lanes, r.Lane,
		r.RungTried, r.Height, r.Confidence, r.Floor,
		r.SteppedUp, r.Escalated, r.Designated, r.Source, r.RowanPick, r.Reason,
		r.Wait, r.AwaitingTermination, r.Refusal,
		r.Outcome, r.RungSucceeded,
		r.Calls, r.TokensIn, r.TokensOut, r.UsageFailed, r.Evidence,
	}
}

// scanTargets is the row as the SELECT's destinations, in logColumns order.
func (r *logRow) scanTargets() []any {
	return []any{
		&r.TS, &r.UnitID, &r.Kind, &r.Files, &r.Packages, &r.Lanes, &r.Lane,
		&r.RungTried, &r.Height, &r.Confidence, &r.Floor,
		&r.SteppedUp, &r.Escalated, &r.Designated, &r.Source, &r.RowanPick, &r.Reason,
		&r.Wait, &r.AwaitingTermination, &r.Refusal,
		&r.Outcome, &r.RungSucceeded,
		&r.Calls, &r.TokensIn, &r.TokensOut, &r.UsageFailed, &r.Evidence,
	}
}

// insertLog is the one INSERT, built from logColumns so the placeholders and
// the columns are never counted by hand.
func insertLog() string {
	marks := make([]string, len(logColumns))
	for i := range logColumns {
		marks[i] = fmt.Sprintf("$%d", i+1)
	}
	return fmt.Sprintf("INSERT INTO decide_log (%s) VALUES (%s)",
		strings.Join(logColumns, ", "), strings.Join(marks, ", "))
}

// selectLog is the one SELECT, in the order the rows were written: id ascending
// is append order, which is what the JSONL log's own order is.
func selectLog() string {
	return fmt.Sprintf("SELECT %s FROM decide_log ORDER BY id ASC", strings.Join(logColumns, ", "))
}

// sortedNames is the migration files in the order they apply: by name, so 0001
// runs before 0002 and the order is in the repository rather than in a list
// somebody has to remember to edit.
func sortedNames(names []string) []string {
	out := append([]string(nil), names...)
	sort.Strings(out)
	return out
}

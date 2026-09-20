// The decisions table (rule 8): a decision is logged beside the outcome it
// predicted so a floor is re-tuned from data. The row is
// (question_hash, kind, answer, provider_confidence, floor, outcome).
//
// Exactly one writer appends to the table -- the decision client itself, after
// every call -- and every other verb only reads it. The table is a projection
// of the decision journal and never an authority over the machinery.
//
// Postgres is the durable record, reached through a database/sql driver named
// by the DSN, where a build links one. A bench with no Postgres falls back to
// a TSV file, so the files stay the record and the table is an index; the
// fallback is the same contract and never a second one.
package decide

import (
	"crypto/sha256"
	"database/sql"
	"encoding/csv"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"strconv"
	"strings"
	"sync"
)

// DecisionsEnv names the environment variable a decisions table DSN is read
// from when a verb is not given one on the command line.
const DecisionsEnv = "NOVA_DSN"

// PGDriverName is the database/sql driver a postgres:// DSN opens. A build that
// links a Postgres driver (lib/pq, pgx) registers this name; a build with none
// refuses rather than guessing.
const PGDriverName = "postgres"

// decisionsHeader is the TSV fallback's first line, in the column order the
// table is declared in. `source` is LAST because it was added after the other
// six: a file written before it exists is six columns wide and is still read,
// with its source unknown rather than guessed.
var decisionsHeader = []string{"question_hash", "kind", "answer", "provider_confidence", "floor", "outcome", "source"}

// legacyDecisionColumns is the width of a row written before `source` existed.
const legacyDecisionColumns = 6

// noProviderConfidence is the confidence column of a decision no provider
// answered. It is a DASH and never a number: a decision the machinery settled
// cost no call, so there is no calibration evidence about any provider in it,
// and a zero or a display constant written here would become exactly that
// (Stella, 2026-09-19, expanded Go read of #1925).
const noProviderConfidence = "-"

// DecisionRow is one row of the decisions table: the question that was asked,
// the kind of judgment, the answer, the provider's confidence, the floor it
// was gated on, the outcome that followed (empty until known), and which of
// the two decided it.
type DecisionRow struct {
	QuestionHash string
	Kind         string
	Answer       string
	// ProviderConfidence is meaningful only where HasProviderConfidence is
	// true. Where it is false the provider was never asked and the column
	// is written as a dash.
	ProviderConfidence    float64
	HasProviderConfidence bool
	Floor                 float64
	Outcome               string
	// Source is SourceMachinery where the rules settled the decision with
	// no provider asked, SourceProvider where the provider's answer stood
	// (within the constraints), and empty in a row written before the
	// column existed.
	Source string
}

// DecisionDriver is the storage seam for the decisions table. The tool opens
// one driver per process. Append is the one writer; Rows is the read every
// other verb uses; Close releases the handle.
type DecisionDriver interface {
	Append(row DecisionRow) error
	Rows(kind string) ([]DecisionRow, error)
	Close() error
}

// OpenDecisions opens the decisions table a DSN names. A postgres:// or
// postgresql:// DSN opens the linked Postgres driver; any other value is a
// path to the TSV fallback, where the files stay the record. An empty DSN is a
// refusal, never a guess.
func OpenDecisions(dsn string) (DecisionDriver, error) {
	dsn = strings.TrimSpace(dsn)
	if dsn == "" {
		return nil, fmt.Errorf("decide: no decisions table configured; set %s or --dsn", DecisionsEnv)
	}
	if strings.HasPrefix(dsn, "postgres://") || strings.HasPrefix(dsn, "postgresql://") {
		db, err := sql.Open(PGDriverName, dsn)
		if err != nil {
			return nil, fmt.Errorf("decide: open postgres: %w", err)
		}
		return &postgresDriver{db: db}, nil
	}
	return &tsvDriver{path: dsn}, nil
}

// QuestionHash is the stable hash of one question over its state: the kind,
// the name, the instructions and the state text. A replay names the same hash,
// so an answer already journaled is reproduced without calling the provider.
func QuestionHash(state, name string, q Question) string {
	h := sha256.New()
	for _, part := range []string{q.Kind(), name, q.Instructions, state} {
		io.WriteString(h, part)
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

// Kind names which of the three question types q is, or "" for a malformed
// question.
func (q Question) Kind() string {
	switch {
	case q.Choice != nil:
		return "choice"
	case q.Score != nil:
		return "score"
	case q.Noul:
		return "noul"
	default:
		return ""
	}
}

// Value renders one answer as the table's answer column: the chosen option,
// the score, or the noul value.
func (a Answer) Value() string {
	switch a.Type {
	case "score":
		return strconv.FormatFloat(a.Score, 'g', -1, 64)
	case "noul":
		return strconv.FormatFloat(a.Noul, 'g', -1, 64)
	default:
		return a.Choice
	}
}

// UseDecisions attaches the client's decisions table. It is the one writer
// rule 8 names: Decide appends a row per answer after every call. A nil driver
// leaves the client without a table.
func (c *Client) UseDecisions(d DecisionDriver) { c.decisions = d }

// SetFloor sets the confidence floor the client logs beside a decision. The
// zero value keeps DefaultFloor.
func (c *Client) SetFloor(f float64) { c.floor = f }

// DefaultFloor is the confidence floor a decision is gated on when the caller
// names none, matching nova-decide's --floor default.
//
// It was 0.9, and 0.9 was a number nobody had rows behind (rule 8 asks for the
// rows). The rows exist now: 39 real route calls over 13 units on 2026-09-18
// came back between 0.61 and 0.91, and the SAME unit with the SAME evidence
// came back 0.78, 0.80, 0.81 and 0.82 on four calls. A floor inside that band
// does not separate a right answer from a wrong one -- it separates one call
// from the next, and the schema campaign watched three row cards miss 0.90 by
// 0.01 and step a whole rung off pro, which then landed all four green. So the
// floor sits BELOW the band: a step-up now means the provider's confidence
// actually collapsed, not that it returned its ordinary number. 0.65 and not
// 0.7, because 0.7 was still inside it -- one rebase unit came back 0.68, 0.69
// and 0.71 on three calls and routed two ways.
const DefaultFloor = 0.65

// SetRowSource names which of the two decided the answers this call is about,
// and whether a provider confidence stands behind them. Machinery that settled
// a decision writes SourceMachinery with hasProviderConfidence false: the
// provider was not asked, so there is no calibration evidence in the row and
// none is invented.
func (c *Client) SetRowSource(source string, hasProviderConfidence bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.rowSource, c.rowHasConfidence = source, hasProviderConfidence
}

// Recorded is how many rows this client appended, and RecordErr the first
// append that failed. A decision the caller was told succeeded while its
// configured table was never written is a decision with no receipt, so the
// caller reads both and reports rather than assuming (Stella, 2026-09-19,
// expanded Go read of #1925).
func (c *Client) Recorded() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.recorded
}

func (c *Client) RecordErr() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.recordErr
}

// record appends one row per answer to the client's decisions table. The
// write is no longer swallowed: the error is kept for the caller to report.
// One client's Decide is called from several goroutines at once, so the
// bookkeeping is under the client's lock and the Append itself is not -- the
// driver owns its own serialization and holding the lock across a database
// round trip would serialize every decision in the process.
func (c *Client) record(state string, qs map[string]Question, answers map[string]Answer) {
	if c == nil || c.decisions == nil {
		return
	}
	floor := c.floor
	if floor == 0 {
		floor = DefaultFloor
	}
	c.mu.Lock()
	source, hasConfidence := c.rowSource, c.rowHasConfidence
	c.mu.Unlock()
	if source == "" {
		source, hasConfidence = SourceProvider, true
	}
	for name, a := range answers {
		q := qs[name]
		row := DecisionRow{
			QuestionHash:          QuestionHash(state, name, q),
			Kind:                  q.Kind(),
			Answer:                a.Value(),
			HasProviderConfidence: hasConfidence,
			Floor:                 floor,
			Source:                source,
		}
		if hasConfidence {
			row.ProviderConfidence = a.Confidence
		}
		err := c.decisions.Append(row)
		c.mu.Lock()
		if err != nil {
			if c.recordErr == nil {
				c.recordErr = err
			}
		} else {
			c.recorded++
		}
		c.mu.Unlock()
	}
}

// postgresDriver is the decisions table in Postgres, one writer per row.
type postgresDriver struct{ db *sql.DB }

// The durable table carries the same two facts the TSV does: a confidence that
// is NULL where no provider answered, and the source that says which of the
// two decided. A table with no `source` column and a NOT NULL
// `provider_confidence` cannot hold a machinery receipt, and the append fails
// loudly rather than writing a number about a call nobody made.
func (p *postgresDriver) Append(row DecisionRow) error {
	_, err := p.db.Exec(
		`INSERT INTO decisions (question_hash, kind, answer, provider_confidence, floor, outcome, source) VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		row.QuestionHash, row.Kind, row.Answer,
		sql.NullFloat64{Float64: row.ProviderConfidence, Valid: row.HasProviderConfidence},
		row.Floor, row.Outcome, row.Source)
	return err
}

func (p *postgresDriver) Rows(kind string) ([]DecisionRow, error) {
	rows, err := p.db.Query(
		`SELECT question_hash, kind, answer, provider_confidence, floor, outcome, source FROM decisions WHERE kind = $1 ORDER BY question_hash`,
		kind)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]DecisionRow, 0)
	for rows.Next() {
		var row DecisionRow
		var confidence sql.NullFloat64
		var source sql.NullString
		if err := rows.Scan(&row.QuestionHash, &row.Kind, &row.Answer, &confidence, &row.Floor, &row.Outcome, &source); err != nil {
			return nil, err
		}
		row.ProviderConfidence, row.HasProviderConfidence = confidence.Float64, confidence.Valid
		row.Source = source.String
		out = append(out, row)
	}
	return out, rows.Err()
}

func (p *postgresDriver) Close() error { return p.db.Close() }

// tsvDriver is the decisions table as a TSV file, the fallback where no
// Postgres is linked. The file stays the record; the table is its index.
type tsvDriver struct {
	path string
	mu   sync.Mutex
}

func (t *tsvDriver) Append(row DecisionRow) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	needHeader := true
	if fi, err := os.Stat(t.path); err == nil && fi.Size() > 0 {
		needHeader = false
	}
	f, err := os.OpenFile(t.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("decide: append decisions: %w", err)
	}
	defer f.Close()
	w := csv.NewWriter(f)
	w.Comma = '\t'
	if needHeader {
		if err := w.Write(decisionsHeader); err != nil {
			return fmt.Errorf("decide: write decisions header: %w", err)
		}
	}
	confidence := noProviderConfidence
	if row.HasProviderConfidence {
		confidence = strconv.FormatFloat(row.ProviderConfidence, 'g', -1, 64)
	}
	record := []string{
		row.QuestionHash,
		row.Kind,
		row.Answer,
		confidence,
		strconv.FormatFloat(row.Floor, 'g', -1, 64),
		row.Outcome,
		row.Source,
	}
	if err := w.Write(record); err != nil {
		return fmt.Errorf("decide: write decisions row: %w", err)
	}
	w.Flush()
	return w.Error()
}

func (t *tsvDriver) Rows(kind string) ([]DecisionRow, error) {
	f, err := os.Open(t.path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("decide: read decisions: %w", err)
	}
	defer f.Close()
	r := csv.NewReader(f)
	r.Comma = '\t'
	r.FieldsPerRecord = -1
	if _, err := r.Read(); err != nil { // header
		if errors.Is(err, io.EOF) {
			return nil, nil
		}
		return nil, fmt.Errorf("decide: read decisions header: %w", err)
	}
	out := make([]DecisionRow, 0)
	for {
		record, err := r.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("decide: read decisions: %w", err)
		}
		// A row written before `source` existed is six columns wide and is
		// read exactly as it was, with no source rather than a guessed one.
		if len(record) < legacyDecisionColumns {
			continue
		}
		confidence, confErr := strconv.ParseFloat(record[3], 64)
		floor, _ := strconv.ParseFloat(record[4], 64)
		if record[1] != kind {
			continue
		}
		source := ""
		if len(record) > legacyDecisionColumns {
			source = record[6]
		}
		out = append(out, DecisionRow{
			QuestionHash:          record[0],
			Kind:                  record[1],
			Answer:                record[2],
			ProviderConfidence:    confidence,
			HasProviderConfidence: confErr == nil && strings.TrimSpace(record[3]) != noProviderConfidence,
			Floor:                 floor,
			Outcome:               record[5],
			Source:                source,
		})
	}
	return out, nil
}

func (t *tsvDriver) Close() error { return nil }

package events

import (
	"context"
	"database/sql"
	_ "embed"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"time"

	// modernc.org/sqlite is the pure-Go SQLite driver: no cgo, so `go test ./...` and every
	// cross build this repository already makes (linux/amd64, linux/arm64, linux/s390x,
	// darwin/arm64, windows/amd64, windows/arm64) keep working unchanged, and no bench needs
	// an `sqlite3` binary on PATH for the fold's own tests to run.
	_ "modernc.org/sqlite"
)

//go:embed schema.sql
var schemaSQL string

// SchemaVersion is the migration version schema.sql installs.
const SchemaVersion = 1

// DB is the fold's SQLite file: three tables keyed on the event id and the views over them.
type DB struct {
	db   *sql.DB
	path string
}

// OpenDB opens (creating if absent) the file at path and applies the schema. The schema is
// idempotent, so this runs on every start.
func OpenDB(ctx context.Context, path string) (*DB, error) {
	if strings.TrimSpace(path) == "" {
		return nil, fmt.Errorf("the fold wants a database path; refusing to guess")
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("sqlite %s: %w", path, err)
	}
	// One writer, one connection: the fold is a single consumer and a second connection
	// would only buy SQLITE_BUSY.
	db.SetMaxOpenConns(1)
	for _, pragma := range []string{
		"PRAGMA journal_mode = WAL",
		"PRAGMA synchronous = NORMAL",
		"PRAGMA busy_timeout = 5000",
	} {
		if _, err := db.ExecContext(ctx, pragma); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("sqlite %s: %s: %w", path, pragma, err)
		}
	}
	if _, err := db.ExecContext(ctx, schemaSQL); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("sqlite %s: schema: %w", path, err)
	}
	return &DB{db: db, path: path}, nil
}

// Path is the file this fold writes.
func (d *DB) Path() string { return d.path }

// Close closes the file.
func (d *DB) Close() error {
	if d == nil || d.db == nil {
		return nil
	}
	return d.db.Close()
}

// table is where one kind lands. Keeping the three tables of #2563 apart makes the read
// queue's numbers and the landed count separable from the card's own life.
func table(k Kind) string {
	switch k {
	case Read:
		return "reads"
	case Landed:
		return "landings"
	default:
		return "attempts"
	}
}

// Apply folds one batch of entries in ONE transaction and reports how many rows were new.
// The insert is ON CONFLICT DO NOTHING on the event id, so a redelivery adds nothing; a
// batch either lands whole or not at all, and the caller acks only after it lands.
//
// An entry the stream holds that this package cannot read is a skip, not a failure: a
// writer with a bug must not stop the fold for every other writer. It is counted and named.
func (d *DB) Apply(ctx context.Context, entries []Entry) (inserted, skipped int, _ error) {
	if len(entries) == 0 {
		return 0, 0, nil
	}
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, 0, err
	}
	defer func() { _ = tx.Rollback() }()

	stmts := map[string]*sql.Stmt{}
	defer func() {
		for _, s := range stmts {
			_ = s.Close()
		}
	}()
	for _, name := range []string{"attempts", "reads", "landings"} {
		s, err := tx.PrepareContext(ctx, fmt.Sprintf(
			`INSERT INTO %s (event_id, label, attempt, bench, model, route, kind, tokens_in, tokens_out, usd, pr, head, at, day)
			 VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(event_id) DO NOTHING`, name))
		if err != nil {
			return 0, 0, err
		}
		stmts[name] = s
	}

	for _, entry := range entries {
		e, err := FromFields(entry.Fields)
		if err != nil {
			skipped++
			continue
		}
		at := ""
		if !e.At.IsZero() {
			at = e.At.UTC().Format(time.RFC3339)
		}
		res, err := stmts[table(e.Kind)].ExecContext(ctx,
			entry.ID, e.Label, e.Attempt, e.Bench, e.Model, e.Route, string(e.Kind),
			e.TokensIn, e.TokensOut, e.USD, e.PR, e.Head, at, e.Day())
		if err != nil {
			return 0, 0, fmt.Errorf("fold %s: %w", entry.ID, err)
		}
		if n, err := res.RowsAffected(); err == nil && n > 0 {
			inserted++
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, 0, err
	}
	return inserted, skipped, nil
}

// Count is how many rows the three tables hold together: the number Johnny's bar 3 asks for.
func (d *DB) Count(ctx context.Context) (int, error) {
	var n int
	err := d.db.QueryRowContext(ctx,
		`SELECT (SELECT count(*) FROM attempts) + (SELECT count(*) FROM reads) + (SELECT count(*) FROM landings)`).Scan(&n)
	return n, err
}

// CountKind is how many rows of one kind the fold holds.
func (d *DB) CountKind(ctx context.Context, k Kind) (int, error) {
	var n int
	err := d.db.QueryRowContext(ctx, fmt.Sprintf(`SELECT count(*) FROM %s WHERE kind = ?`, table(k)), string(k)).Scan(&n)
	return n, err
}

// dumpColumns is the row shape Dump writes, the same for all three tables.
const dumpColumns = "event_id, label, attempt, bench, model, route, kind, tokens_in, tokens_out, usd, pr, head, at, day"

// Dump writes every row of the three tables as TSV, table by table and ordered inside each
// table by event id. It is deterministic by construction -- no fold timestamp, no insertion
// order -- which is what lets a rebuild be compared with an incremental fold byte for byte.
func (d *DB) Dump(ctx context.Context, w io.Writer) error {
	if _, err := fmt.Fprintf(w, "table\t%s\n", strings.ReplaceAll(dumpColumns, ", ", "\t")); err != nil {
		return err
	}
	for _, name := range []string{"attempts", "landings", "reads"} {
		rows, err := d.db.QueryContext(ctx, fmt.Sprintf(`SELECT %s FROM %s ORDER BY event_id`, dumpColumns, name))
		if err != nil {
			return err
		}
		if err := dumpRows(w, name, rows); err != nil {
			return err
		}
	}
	return nil
}

func dumpRows(w io.Writer, name string, rows *sql.Rows) error {
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var (
			id, label, bench, model, route, kind, pr, head, at, day string
			attempt                                                 int
			tokensIn, tokensOut                                     int64
			usd                                                     float64
		)
		if err := rows.Scan(&id, &label, &attempt, &bench, &model, &route, &kind,
			&tokensIn, &tokensOut, &usd, &pr, &head, &at, &day); err != nil {
			return err
		}
		if _, err := fmt.Fprintf(w, "%s\t%s\t%s\t%d\t%s\t%s\t%s\t%s\t%d\t%d\t%s\t%s\t%s\t%s\t%s\n",
			name, id, label, attempt, bench, model, route, kind, tokensIn, tokensOut,
			strconv.FormatFloat(usd, 'f', -1, 64), pr, head, at, day); err != nil {
			return err
		}
	}
	return rows.Err()
}

// Report writes the views as TSV blocks: the numbers a coordinator reads and the numbers
// `nova-pulse status` will read. max caps the rows of each block; 0 means all.
func (d *DB) Report(ctx context.Context, w io.Writer, max int) error {
	for _, v := range []struct{ name, query string }{
		{"totals", `SELECT cards, "rows", done, ok, fail, reads, landed, usd FROM totals`},
		{"by_model_route", `SELECT model, route, "rows", ok, fail, done, usd, usd_per_ok, landed, usd_per_landed FROM by_model_route ORDER BY model, route`},
		{"by_bench", `SELECT bench, "rows", cards, ok, fail, done, usd FROM by_bench ORDER BY bench`},
		{"by_day", `SELECT day, "rows", cards, ok, fail, done, usd FROM by_day ORDER BY day`},
	} {
		if err := reportBlock(ctx, d.db, w, v.name, v.query, max); err != nil {
			return err
		}
	}
	return nil
}

func reportBlock(ctx context.Context, db *sql.DB, w io.Writer, name, query string, max int) error {
	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	cols, err := rows.Columns()
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "# %s\n%s\n", name, strings.Join(cols, "\t")); err != nil {
		return err
	}
	shown := 0
	for rows.Next() {
		if max > 0 && shown >= max {
			break
		}
		cells := make([]any, len(cols))
		for i := range cells {
			cells[i] = new(any)
		}
		if err := rows.Scan(cells...); err != nil {
			return err
		}
		out := make([]string, len(cols))
		for i, c := range cells {
			out[i] = cell(*(c.(*any)))
		}
		if _, err := fmt.Fprintln(w, strings.Join(out, "\t")); err != nil {
			return err
		}
		shown++
	}
	return rows.Err()
}

// cell renders one view value. A NULL is a dash, never an empty column, so a reader can
// tell "no answer" from "zero" (no evidence is not negative evidence).
func cell(v any) string {
	switch t := v.(type) {
	case nil:
		return "-"
	case []byte:
		return string(t)
	case string:
		return t
	case int64:
		return strconv.FormatInt(t, 10)
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	default:
		return fmt.Sprint(t)
	}
}

// Stats is what one fold pass did.
type Stats struct {
	Read     int // entries taken from the stream
	Inserted int // rows the fold did not already hold
	Acked    int // deliveries committed back to the group
	Skipped  int // entries this package could not read
}

// Add folds one pass's numbers into a running total.
func (s *Stats) Add(o Stats) {
	s.Read += o.Read
	s.Inserted += o.Inserted
	s.Acked += o.Acked
	s.Skipped += o.Skipped
}

// Line is the one-line receipt a fold pass prints.
func (s Stats) Line(db, stream, group string) string {
	return fmt.Sprintf("FOLD %s stream=%s group=%s read=%d new=%d acked=%d skipped=%d",
		db, stream, group, s.Read, s.Inserted, s.Acked, s.Skipped)
}

// Folder is one consumer of the stream into one file.
type Folder struct {
	Reader   Reader
	DB       *DB
	Group    string
	Consumer string
	Count    int           // entries per read
	Block    time.Duration // how long one read may wait for a new entry
}

// Start creates the group, so a fold that starts before any writer still reads entry 0.
func (f *Folder) Start(ctx context.Context) error {
	return f.Reader.EnsureGroup(ctx, f.group())
}

func (f *Folder) group() string {
	if strings.TrimSpace(f.Group) == "" {
		return Group
	}
	return f.Group
}

func (f *Folder) consumer() string {
	if strings.TrimSpace(f.Consumer) == "" {
		return "fold-1"
	}
	return f.Consumer
}

func (f *Folder) count() int {
	if f.Count <= 0 {
		return 100
	}
	return f.Count
}

// Once is one pass: reclaim what a dead predecessor left unacked, then take new entries.
// The order matters -- pending first -- because otherwise a fold that is killed and
// restarted walks past its own unfinished work and the count is short.
func (f *Folder) Once(ctx context.Context) (Stats, error) {
	var total Stats
	pending, err := f.Reader.Pending(ctx, f.group(), f.consumer(), f.count())
	if err != nil {
		return total, err
	}
	s, err := f.apply(ctx, pending)
	total.Add(s)
	if err != nil {
		return total, err
	}
	next, err := f.Reader.Next(ctx, f.group(), f.consumer(), f.count(), f.Block)
	if err != nil {
		return total, err
	}
	s, err = f.apply(ctx, next)
	total.Add(s)
	return total, err
}

// apply is the commit-then-ack order that makes a kill safe in both directions: a kill
// before the commit leaves the entry pending and it is folded again; a kill after the
// commit and before the ack leaves it pending and the re-fold is a no-op on the primary key.
func (f *Folder) apply(ctx context.Context, entries []Entry) (Stats, error) {
	if len(entries) == 0 {
		return Stats{}, nil
	}
	inserted, skipped, err := f.DB.Apply(ctx, entries)
	if err != nil {
		return Stats{Read: len(entries)}, err
	}
	ids := make([]string, 0, len(entries))
	for _, e := range entries {
		ids = append(ids, e.ID)
	}
	if err := f.Reader.Ack(ctx, f.group(), ids...); err != nil {
		return Stats{Read: len(entries), Inserted: inserted, Skipped: skipped}, err
	}
	return Stats{Read: len(entries), Inserted: inserted, Acked: len(ids), Skipped: skipped}, nil
}

// Run holds the loop: one pass per interval until the context is cancelled. A cancelled
// context is the shutdown, not a failure, so it returns the running total and no error --
// which is what makes "kill it half way and restart" an ordinary thing to do.
func (f *Folder) Run(ctx context.Context, interval time.Duration, out io.Writer, stream string) (Stats, error) {
	if interval <= 0 {
		interval = time.Second
	}
	var total Stats
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		s, err := f.Once(ctx)
		total.Add(s)
		if err != nil {
			if ctx.Err() != nil {
				return total, nil
			}
			return total, err
		}
		if s.Read > 0 && out != nil {
			fmt.Fprintln(out, s.Line(f.DB.Path(), stream, f.group()))
		}
		select {
		case <-ctx.Done():
			return total, nil
		case <-ticker.C:
		}
	}
}

// Rebuild replays the stream from its first entry into this file, reading the stream itself
// and never the group: a rebuild must not consume what the running fold has not yet folded.
func Rebuild(ctx context.Context, r Reader, db *DB, batch int) (Stats, error) {
	if batch < 2 {
		batch = 100
	}
	var total Stats
	start := "-"
	last := ""
	for {
		// The range bound is INCLUSIVE, so one extra entry is asked for and the entry the
		// last page ended on is dropped. That costs one row per page and needs no Redis 6.2
		// exclusive range, which the fake would then have to grow too.
		entries, err := r.Range(ctx, start, batch+1)
		if err != nil {
			return total, err
		}
		if last != "" && len(entries) > 0 && entries[0].ID == last {
			entries = entries[1:]
		}
		if len(entries) == 0 {
			return total, nil
		}
		inserted, skipped, err := db.Apply(ctx, entries)
		total.Add(Stats{Read: len(entries), Inserted: inserted, Skipped: skipped})
		if err != nil {
			return total, err
		}
		last = entries[len(entries)-1].ID
		start = last
	}
}

// SortedIDs is the entries' ids in stream order; a helper the tests read.
func SortedIDs(entries []Entry) []string {
	ids := make([]string, 0, len(entries))
	for _, e := range entries {
		ids = append(ids, e.ID)
	}
	sort.Strings(ids)
	return ids
}

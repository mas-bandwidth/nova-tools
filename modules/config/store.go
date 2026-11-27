// Package config reads a fleet's configuration from the PostgreSQL store
// nova-config keeps: its machines, the friends that work on it, the services
// it runs, and the model providers and models it can call, the rows of the
// schema config (nova-config schema). It
// reads them through the same query nova-config's list verbs run, so a
// program that imports it sees exactly what nova-config prints; writes go
// through nova-config, which records each one in the history.
package config

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// SchemaMissing is the error of a store the current schema was never applied to.
const SchemaMissing = `store has no current schema; run: nova-config schema | PGPASSWORD="$NOVA_PG_PASSWORD" psql "$NOVA_PG_DSN"`

// Store is one open connection to the store.
type Store struct {
	c     *pgx.Conn
	user  string
	q     querier                                    // what reads run on: the connection, or a snapshot's transaction
	begin func(context.Context) (transaction, error) // a snapshot's transaction
}

// querier runs a read: a *pgx.Conn or a pgx.Tx.
type querier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

// transaction is a snapshot's read-only transaction.
type transaction interface {
	querier
	Commit(ctx context.Context) error
	Rollback(ctx context.Context) error
}

// snapshotTx is a snapshot's transaction: every read sees the same instant.
var snapshotTx = pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly}

// Row is one row as text: its name, its fields by column name, and its
// stamps (RFC 3339, UTC). An unset value is "".
type Row struct {
	Name             string
	F                map[string]string
	Created, Updated string
}

// cause names why a connection failed in a word or two, never Go's text.
func cause(err error) string {
	var dns *net.DNSError
	var op *net.OpError
	switch {
	case errors.Is(err, syscall.ECONNREFUSED):
		return "connection refused"
	case errors.As(err, &dns):
		return "no such host"
	case os.IsTimeout(err) || errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &op) && op.Timeout()):
		return "timed out"
	}
	return "connect failed"
}

// storeError is a failure of the store as one line, never pgx's text: a store the
// schema was not applied to says how to apply it, a refused login and a denied
// grant say whose and what, anything else is its SQLSTATE and the server's first
// sentence. user is the login and op what was being done.
func storeError(err error, user, op string) error {
	var pe *pgconn.PgError
	switch {
	case err == nil:
		return nil
	case !errors.As(err, &pe):
		return fmt.Errorf("postgres: %s", cause(err))
	case slices.Contains([]string{"42P01", "42703", "3F000"}, pe.Code): // undefined table, undefined column, no such schema
		return errors.New(SchemaMissing)
	case strings.HasPrefix(pe.Code, "28"):
		return fmt.Errorf("postgres refused the login for %s: check NOVA_PG_PASSWORD; run: PGPASSWORD=\"$NOVA_PG_PASSWORD\" psql \"$NOVA_PG_DSN\" -c 'select 1'", user)
	case pe.Code == "42501":
		words := append([]string{"the table"}, strings.Fields(strings.Split(pe.Message, "\n")[0])...) // never empty
		return fmt.Errorf("%s may not %s %s", user, op, cmp.Or(pe.TableName, words[len(words)-1]))
	}
	msg, _, _ := strings.Cut(strings.Split(pe.Message, "\n")[0], ". ")
	return fmt.Errorf("store error (SQLSTATE %s): %s; check the store answers, then try again; run: PGPASSWORD=\"$NOVA_PG_PASSWORD\" psql \"$NOVA_PG_DSN\" -c 'select 1'", pe.Code, strings.TrimSuffix(msg, "."))
}

// DSNProblem says which part of a postgres:// URL is wrong, "" for none; it
// never quotes the URL, which may hold a password.
func DSNProblem(dsn string) string {
	u, err := url.Parse(dsn)
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") {
		return "not a postgres:// URL (want postgres://USER@HOST:PORT/DB?sslmode=disable)"
	}
	if n, err := strconv.Atoi(u.Port()); u.Port() != "" && (err != nil || n < 1 || n > 65535) {
		return "port " + u.Port() + " out of range"
	}
	return ""
}

// connectError is the failure to open the store as one line.
func connectError(cfg *pgx.ConnConfig, err error) error {
	var pe *pgconn.PgError
	switch {
	case errors.As(err, &pe) && pe.Code == "3D000":
		return fmt.Errorf("database %s does not exist on %s; run: createdb -h %s -p %d -U %s %s", cfg.Database, cfg.Host, cfg.Host, cfg.Port, cfg.User, cfg.Database)
	case errors.As(err, &pe):
		return storeError(err, cfg.User, "connect")
	}
	return fmt.Errorf("cannot reach postgres at %s (NOVA_PG_DSN): %s; check NOVA_PG_DSN or run: PGPASSWORD=\"$NOVA_PG_PASSWORD\" psql \"$NOVA_PG_DSN\" -c 'select 1'", net.JoinHostPort(cfg.Host, strconv.Itoa(int(cfg.Port))), cause(err))
}

// Open connects to the store dsn names (postgres://USER@HOST:PORT/DB, no
// password in it), with password when it is not empty. Every error is one
// line that never quotes the dsn.
func Open(ctx context.Context, dsn, password string) (*Store, error) {
	if p := DSNProblem(dsn); p != "" {
		return nil, fmt.Errorf("the postgres dsn is wrong: %s", p)
	}
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		return nil, errors.New("the postgres dsn could not be parsed") // its text may quote a password
	}
	cfg.Password = cmp.Or(password, cfg.Password)
	dctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	c, err := pgx.ConnectConfig(dctx, cfg)
	if err != nil {
		return nil, connectError(cfg, err)
	}
	begin := func(ctx context.Context) (transaction, error) { return c.BeginTx(ctx, snapshotTx) }
	return &Store{c: c, user: cfg.User, q: c, begin: begin}, nil
}

// Close closes the connection.
func (s *Store) Close() { _ = s.c.Close(context.Background()) } // ignored: the caller is done with it

// Conn is the connection, for nova-config's own writes.
func (s *Store) Conn() *pgx.Conn { return s.c }

// Err is a failure of op (read, add, set, remove) on the store as one line.
func (s *Store) Err(err error, op string) error { return storeError(err, s.user, op) }

// Text runs a query whose columns are all text and returns its rows.
func (s *Store) Text(ctx context.Context, query string, args ...any) ([][]string, error) {
	rows, err := s.q.Query(ctx, query, args...)
	if err != nil {
		return nil, s.Err(err, "read")
	}
	defer rows.Close()
	var out [][]string
	for rows.Next() {
		vals, err := rows.Values()
		if err != nil {
			return nil, s.Err(err, "read")
		}
		row := make([]string, len(vals))
		for i, v := range vals {
			row[i], _ = v.(string) // ignored: every column is cast to text
		}
		out = append(out, row)
	}
	return out, s.Err(rows.Err(), "read")
}

func ident(s string) string { return `"` + s + `"` }

// listSQL names every column it reads, never *, so a store that carries
// columns this package does not know is read as it is; the columns are the
// name, the given ones as text (empty for NULL) and the two stamps.
func listSQL(table string, columns []string) string {
	cols := []string{"name"}
	for _, c := range columns {
		cols = append(cols, "coalesce("+ident(c)+"::text, '')")
	}
	stamp := `to_char(%s AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"')`
	cols = append(cols, fmt.Sprintf(stamp, "created_at"), fmt.Sprintf(stamp, "updated_at"))
	return "SELECT " + strings.Join(cols, ", ") + " FROM config." + table + " ORDER BY name"
}

// Rows reads the given columns of every row of config.<table>, in name order:
// the one query behind nova-config's list verbs and the row readers (Machines, Friends, ...).
func (s *Store) Rows(ctx context.Context, table string, columns []string) ([]Row, error) {
	got, err := s.Text(ctx, listSQL(table, columns))
	var out []Row
	for _, v := range got {
		n := len(v)
		r := Row{Name: v[0], F: map[string]string{}, Created: v[n-2], Updated: v[n-1]}
		for i, c := range columns {
			r.F[c] = v[i+1]
		}
		out = append(out, r)
	}
	return out, err
}

// WatchFriends emits the friend directory (each friend's harness, its display name or else its id, enabled and model;
// providers, each harness's display name), read in one query, now and after each committed friend or harness write
// (schema.sql's friend_directory_changed trigger) until emit or the store fails: a caller that reconnects gets it whole.
func (s *Store) WatchFriends(ctx context.Context, emit func(any) error) error {
	_, err := s.c.Exec(ctx, "LISTEN nova_friend_directory")
	for ; err == nil; _, err = s.c.WaitForNotification(ctx) {
		rows, readErr := s.Text(ctx, `SELECT coalesce(f.name, ''), coalesce(h.name, f.harness), coalesce(h.display_name, f.harness), coalesce(f.enabled::text, 'false'), coalesce(f.model, '')
 FROM config.friends f FULL JOIN config.harnesses h ON h.name = f.harness ORDER BY f.name`)
		if readErr != nil {
			return readErr // never an empty directory for a failed read
		}
		friends, providers := map[string]any{}, map[string]string{}
		for _, r := range rows {
			if providers[r[1]] = r[2]; r[0] != "" {
				friends[r[0]] = map[string]any{"harness": r[1], "display_name": r[2], "enabled": r[3] == "true", "model": r[4]}
			}
		}
		if emitErr := emit(map[string]any{"friends": friends, "providers": providers}); emitErr != nil {
			return emitErr
		}
	}
	return storeError(err, s.user, "listen")
}

// The row types. Their json tags are the column names and the field names
// nova-config's --json prints, so json.Marshal of a row is the "fields"
// object of nova-config <kind> show --json. A list (roles, routes, keys) is
// a sorted comma list, as the store keeps it.

// Machine is a machine of the fleet, named by its tailnet host.
type Machine struct {
	Name    string `json:"name"`
	Address string `json:"address"` // the tailnet address Ansible and the fleet use
	Public  string `json:"public"`  // the address the internet reaches it at; "" when unset
	User    string `json:"user"`
	OS      string `json:"os"`
	Arch    string `json:"arch"`
	Cores   *int   `json:"cores"` // nil when unknown
	Roles   string `json:"roles"` // of bench, runner, services
	// Routes names the devices or tools the machine has that a piece of work may need: a comma list of
	// names (lower-case letters, digits and dashes); "" for none
	Routes  string `json:"routes"`
	Runners int    `json:"runners"`
	// RunnerScopes is its Linux and macOS GitHub Actions runners: <url>=<count>[:<label>+<label>], comma-separated
	RunnerScopes string `json:"runner_scopes"`
	// WindowsRunners is a WSL machine's Windows-side runners: <url>=<count>[:<label>+<label>], comma-separated
	WindowsRunners string `json:"windows_runners"`
	// WindowsAdminRunners is a WSL machine's Windows-side runners run as a local administrator account, one per entry:
	// <url>=<account>:<label>[+<label>], comma-separated
	WindowsAdminRunners string `json:"windows_admin_runners"`
	// RunnerEnv is the environment its GitHub Actions runners' services start with (the github-runners play)
	RunnerEnv map[string]string `json:"runner_env"`
	Note      string            `json:"note"`
	Created   string            `json:"created"`
	Updated   string            `json:"updated"`
}

// Friend is an AI that works on the fleet, named by its bus name.
type Friend struct {
	Name     string `json:"name"`
	Harness  string `json:"harness"` // claude, codex, antigravity, grok, muse, ...
	Model    string `json:"model"`
	Pronouns string `json:"pronouns"` // "" when unstated
	Note     string `json:"note"`
	Enabled  bool   `json:"enabled"`
	Created  string `json:"created"`
	Updated  string `json:"updated"`
}

// Service is a program one machine keeps running, or runs every Every seconds.
type Service struct {
	Name      string            `json:"name"`
	Machine   string            `json:"machine"`
	Argv      []string          `json:"argv"`
	Seat      string            `json:"seat"`
	Keys      string            `json:"keys"`
	Every     int               `json:"every"`
	Keepalive bool              `json:"keepalive"`
	Enabled   bool              `json:"enabled"`
	Env       map[string]string `json:"env"`
	Log       string            `json:"log"`
	Created   string            `json:"created"`
	Updated   string            `json:"updated"`
}

// Provider is a model API endpoint the fleet can call.
type Provider struct {
	Name        string `json:"name"`
	URL         string `json:"url"`         // the API base URL requests go to
	API         string `json:"api"`         // the wire protocol it speaks: openai or anthropic
	Key         string `json:"key"`         // the name of the secret holding its API key, never the key; "" for none
	ModelsURL   string `json:"models_url"`  // where its model list is read; "" for none
	BalanceURL  string `json:"balance_url"` // where its balance is read; "" for none
	Concurrency int    `json:"concurrency"` // the most runs it takes at once; 0 is no cap
	Note        string `json:"note"`
	Enabled     bool   `json:"enabled"`
	Created     string `json:"created"`
	Updated     string `json:"updated"`
}

// Model is a model the fleet can run, named <provider>/<model id> as a harness names it: the part before the first
// "/" is a Provider's name, and the model id may itself hold "/".
type Model struct {
	Name           string `json:"name"`
	Harness        string `json:"harness"`          // the harness it runs in: opencode, claude or codex
	PriceIn        *int   `json:"price_in"`         // micro-dollars per million input tokens; nil is unpriced, never free
	PriceOut       *int   `json:"price_out"`        // micro-dollars per million output tokens; nil is unpriced, never free
	PriceCacheRead *int   `json:"price_cache_read"` // micro-dollars per million cached input tokens; nil bills them at PriceIn
	Machine        string `json:"machine"`          // the one machine that serves it (a local model); "" for any
	Note           string `json:"note"`
	Enabled        bool   `json:"enabled"`
	Created        string `json:"created"`
	Updated        string `json:"updated"`
}

// Machines, Friends, Services, Providers and Models return the rows of config.machines, friends, services, providers
// and models in name order.
func (s *Store) Machines(ctx context.Context) ([]Machine, error) {
	return read[Machine](ctx, s, "machines")
}
func (s *Store) Friends(ctx context.Context) ([]Friend, error) {
	return read[Friend](ctx, s, "friends")
}
func (s *Store) Services(ctx context.Context) ([]Service, error) {
	return read[Service](ctx, s, "services")
}
func (s *Store) Providers(ctx context.Context) ([]Provider, error) {
	return read[Provider](ctx, s, "providers")
}
func (s *Store) Models(ctx context.Context) ([]Model, error) { return read[Model](ctx, s, "models") }

// Snapshot is every row of the five kinds, read at one instant.
type Snapshot struct {
	Machines  []Machine
	Friends   []Friend
	Services  []Service
	Providers []Provider
	Models    []Model
}

// Snapshot reads Machines, Friends, Services, Providers and Models inside one
// read-only REPEATABLE READ transaction, so all five are from the same instant.
func (s *Store) Snapshot(ctx context.Context) (Snapshot, error) {
	tx, err := s.begin(ctx)
	if err != nil {
		return Snapshot{}, s.Err(err, "read")
	}
	defer func() { _ = tx.Rollback(ctx) }() // ignored: a no-op after the commit; on failure the failure is returned
	view := &Store{user: s.user, q: tx}
	var snap Snapshot
	for _, load := range []func() error{
		func() (e error) { snap.Machines, e = view.Machines(ctx); return },
		func() (e error) { snap.Friends, e = view.Friends(ctx); return },
		func() (e error) { snap.Services, e = view.Services(ctx); return },
		func() (e error) { snap.Providers, e = view.Providers(ctx); return },
		func() (e error) { snap.Models, e = view.Models(ctx); return },
	} {
		if err = load(); err != nil {
			return Snapshot{}, err
		}
	}
	return snap, s.Err(tx.Commit(ctx), "read")
}

// stamped are the tags a row carries outside its columns.
var stamped = map[string]bool{"name": true, "created": true, "updated": true}

// Columns are a row type's columns, the name and the stamps aside, in order.
func Columns[T any]() []string {
	var out []string
	t := reflect.TypeFor[T]()
	for i := range t.NumField() {
		if tag := t.Field(i).Tag.Get("json"); !stamped[tag] {
			out = append(out, tag)
		}
	}
	return out
}

func read[T any](ctx context.Context, s *Store, table string) ([]T, error) {
	rows, err := s.Rows(ctx, table, Columns[T]())
	if err != nil {
		return nil, err
	}
	out := make([]T, 0, len(rows))
	for _, r := range rows {
		var v T
		if err := fill(reflect.ValueOf(&v).Elem(), r); err != nil {
			return nil, fmt.Errorf("config.%s row %s: %w", table, r.Name, err)
		}
		out = append(out, v)
	}
	return out, nil
}

// fill sets each field of a row struct from the row's text.
func fill(v reflect.Value, r Row) error {
	for i := range v.NumField() {
		tag, f := v.Type().Field(i).Tag.Get("json"), v.Field(i)
		text := map[string]string{"name": r.Name, "created": r.Created, "updated": r.Updated}[tag]
		if !stamped[tag] {
			text = r.F[tag]
		}
		switch f.Kind() {
		case reflect.String:
			f.SetString(text)
		case reflect.Bool:
			f.SetBool(text == "true")
		case reflect.Int, reflect.Pointer:
			if text == "" {
				continue
			}
			n, err := strconv.Atoi(text)
			if err != nil {
				return fmt.Errorf("%s %q: not an integer", tag, text)
			}
			if f.Kind() == reflect.Pointer {
				f.Set(reflect.ValueOf(&n))
			} else {
				f.SetInt(int64(n))
			}
		default: // a JSON array or object
			if err := json.Unmarshal([]byte(text), f.Addr().Interface()); text != "" && err != nil {
				return fmt.Errorf("%s: not JSON of its type", tag)
			}
		}
	}
	return nil
}

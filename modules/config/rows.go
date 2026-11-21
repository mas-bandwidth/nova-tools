package config

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strconv"
)

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

// Machines returns config.machines in name order.
func (s *Store) Machines(ctx context.Context) ([]Machine, error) {
	return read[Machine](ctx, s, "machines")
}

// Friends returns config.friends in name order.
func (s *Store) Friends(ctx context.Context) ([]Friend, error) {
	return read[Friend](ctx, s, "friends")
}

// Services returns config.services in name order.
func (s *Store) Services(ctx context.Context) ([]Service, error) {
	return read[Service](ctx, s, "services")
}

// Providers returns config.providers in name order.
func (s *Store) Providers(ctx context.Context) ([]Provider, error) {
	return read[Provider](ctx, s, "providers")
}

// Models returns config.models in name order.
func (s *Store) Models(ctx context.Context) ([]Model, error) {
	return read[Model](ctx, s, "models")
}

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
	if snap.Machines, err = view.Machines(ctx); err != nil {
		return Snapshot{}, err
	}
	if snap.Friends, err = view.Friends(ctx); err != nil {
		return Snapshot{}, err
	}
	if snap.Services, err = view.Services(ctx); err != nil {
		return Snapshot{}, err
	}
	if snap.Providers, err = view.Providers(ctx); err != nil {
		return Snapshot{}, err
	}
	if snap.Models, err = view.Models(ctx); err != nil {
		return Snapshot{}, err
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

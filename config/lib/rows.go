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
// object of nova-config <kind> show --json. A list (roles, tiers, keys) is
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
	Runners int    `json:"runners"`
	Note    string `json:"note"`
	Created string `json:"created"`
	Updated string `json:"updated"`
}

// Friend is an AI that works on the fleet, named by its bus name.
type Friend struct {
	Name     string `json:"name"`
	Harness  string `json:"harness"` // claude, codex, antigravity, grok, muse, ...
	Model    string `json:"model"`
	Width    int    `json:"width"`    // the cards it works at once
	Tiers    string `json:"tiers"`    // of flash, frontier, heavy, pro
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

// Snapshot is every row of the three kinds, read at one instant.
type Snapshot struct {
	Machines []Machine
	Friends  []Friend
	Services []Service
}

// Snapshot reads Machines, Friends and Services inside one read-only
// REPEATABLE READ transaction, so all three are from the same instant.
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

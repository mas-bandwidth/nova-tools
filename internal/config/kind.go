// Package config is nova-config's library: the permanent, non-ephemeral
// configuration of the fleet, kept in Postgres (schema `config`) and applied
// into Redis so Redis is always a rebuildable copy (Glenn 2026-09-26: "redis
// is good as a hot store of data that can be rebuilt"; docs/SPEC-CONFIG.md).
//
// Every kind of configuration is one Kind descriptor: its name, its table,
// its fields with their types and validators, and the two Redis functions
// that write and remove one row. The CLI's add, remove, set, list, show and
// history verbs, the SQL, the history rows and the apply diff are all
// generated from the descriptor, so a new kind is one descriptor, one
// migration and one Redis writer (docs/SPEC-CONFIG.md, "Adding a kind").
package config

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Type is a field's type. It decides the SQL column, the flag's parsing and
// the validator.
type Type string

const (
	// TypeText is free text, stored as text, escaped on the typed line.
	TypeText Type = "text"
	// TypeInt is a non-negative integer, stored as integer.
	TypeInt Type = "int"
	// TypeEnum is one word from Field.Enum, stored as text.
	TypeEnum Type = "enum"
	// TypeList is a comma list of words from Field.Enum, deduplicated and
	// sorted, stored as text ("" is the empty list).
	TypeList Type = "list"
	// TypeNames is a comma list of names (Field.Pattern each), deduplicated
	// and sorted, stored as text.
	TypeNames Type = "names"
	// TypeRef is the name of a row of another kind (Field.Ref), stored as
	// text with a foreign key.
	TypeRef Type = "ref"
)

// Field is one column of a kind: the flag `--<Name>` on add and set, the
// column of the same name (quoted) in Postgres, and the key on every typed
// line.
type Field struct {
	Name string
	Type Type
	// Required is true when add refuses a row without it. set never requires
	// a field: it updates the ones named.
	Required bool
	// Enum is the word list of a TypeEnum or TypeList field.
	Enum []string
	// Ref is the kind a TypeRef field names.
	Ref string
	// Help is the flag's help line, one sentence.
	Help string
}

// Kind is one kind of configuration. See the package comment.
type Kind struct {
	// Name is the kind's word in the grammar (`nova-config <kind> ...`), the
	// singular; Table is its table under schema config.
	Name  string
	Table string
	// Fields are the columns after the row key `name`, in the order every
	// line prints them and every migration declares them.
	Fields []Field
	// Doc is the one sentence `nova-config kinds` prints about the kind.
	Doc string
	// Singleton is a kind of exactly one row, named as the kind is (the
	// fleet: Glenn 2026-09-27, "in the fleet there is only one coordinator
	// at a time"). Its migration creates the row, so the grammar has no add,
	// remove or list and its set, show and history take no name
	// (docs/SPEC-CONFIG.md, "Singleton kinds").
	Singleton bool
	// Derive, when set, is run by Apply on the kind's rows before they are
	// planned: a value another kind's row decides (the sprint's coordinator
	// as a friend's Redis role) is added here, so Redis holds it and the
	// stored row does not. nil derives nothing.
	Derive func(ctx context.Context, st Store, rows []Row) ([]Row, error)
	// ApplyOrder sorts the rows of this kind for apply: a row with a lower
	// number is written first. nil keeps name order. Friends put the
	// coordinator first so ns_friend_roles' bootstrap has one.
	ApplyOrder func(r Row) int
}

// NamePattern is the shape of a row key: lower-case, digits and dashes, the
// registry key friends and machines already use in Redis.
var NamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

// namesPattern is a word of a TypeNames list: letters, digits and dashes,
// any case.
var namesPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]*$`)

// The five kinds of this cut (docs/SPEC-CONFIG.md lists the planned ones).
const (
	KindMachine = "machine"
	KindFleet   = "fleet"
	KindFriend  = "friend"
	KindSprint  = "sprint"
	KindRoute   = "route"
)

// FriendRoles are the roles someone decides for a friend. The coordinator
// role is not one: who coordinates is the sprint row's one field, and apply
// derives the Redis role from it (Kind.Derive below), so ns_friend_roles
// still sees exactly one coordinator.
var FriendRoles = []string{"builder", "may-hold", "reader"}

// Tiers are the model tiers a friend can do, capacity.lua's filter_ok
// spelling (frontier, pro, flash).
var Tiers = []string{"flash", "frontier", "pro"}

// CoordinatorRole is the Redis role ns_friend_roles and the deal read
// (friend:<f>:roles), derived at apply from the sprint row.
const CoordinatorRole = "coordinator"

// Kinds is the registry, in apply order: machines first, the fleet row next
// (it names machines, and a friend's desired slots are charged to the
// fleet's coordinator machine when her beat names none), friends, the
// sprint row (it names a friend), and routes last.
//
// A machine's record is exactly the declared facts something reads, one
// reader each, and nothing invented (Glenn 2026-09-27: "I only want the
// fleet to have actual defined useful things associated with each machine,
// not invented rando stuff"). Its name is the tailnet host: `ssh <name>`
// reaches it, so there is no address field ("All fleet machines must be on
// the tailnet. This is a hard requirement."). Measured facts (os, arch,
// cores, memory) are never typed: they come live from the machine's own
// beat (Beat, docs/SPEC-CONFIG.md, "Declared and measured").
var Kinds = []*Kind{
	{
		Name:  KindMachine,
		Table: "machines",
		Doc:   "a machine of the fleet, named by its tailnet host: the login, the seat, how many cards and runners it takes, and the model tiers it advertises",
		Fields: []Field{
			{Name: "user", Type: TypeText, Required: true, Help: "the login the plays and seals use on it (ssh <user>@<name>)"},
			{Name: "seat", Type: TypeText, Required: true, Help: "its nova-secrets seat (studio, swarm-hulk, ...)"},
			{Name: "slots", Type: TypeInt, Required: true, Help: "how many cards it may run at once, the machine ceiling (machine:<m>:ceiling); 0 runs none"},
			{Name: "runners", Type: TypeInt, Help: "how many CI runners it hosts; 0 (the default) hosts none"},
			{Name: "tiers", Type: TypeList, Enum: Tiers, Help: "which model tiers it advertises: comma list of " + strings.Join(Tiers, ", ")},
		},
	},
	{
		Name:      KindFleet,
		Table:     "fleet",
		Singleton: true,
		Doc:       "the one row of fleet-wide facts: which machine is the store and which the coordinator",
		Fields: []Field{
			{Name: "store", Type: TypeRef, Ref: KindMachine, Help: "the machine that runs Redis and Postgres (a machine row), or empty"},
			{Name: "coordinator", Type: TypeRef, Ref: KindMachine, Help: "the machine the coordinator's loops run on (a machine row), or empty"},
		},
	},
	{
		// A friend's row is what someone decides for her: how wide, which
		// tiers, which roles. What she would just know (where she runs, her
		// harness, her logins) is runtime data her own presence reports
		// (Glenn 2026-09-27, docs/SPEC-CONFIG.md, "What a friend would just
		// know").
		Name:  KindFriend,
		Table: "friends",
		Doc:   "an AI friend: how wide she runs, which tiers she can do, and her roles",
		Fields: []Field{
			{Name: "slots", Type: TypeInt, Required: true, Help: "her desired slots, under the ceiling of the machine her beat reports"},
			{Name: "tiers", Type: TypeList, Enum: Tiers, Required: true, Help: "which tiers she can do: comma list of " + strings.Join(Tiers, ", ")},
			{Name: "roles", Type: TypeList, Enum: FriendRoles, Help: "comma list of " + strings.Join(FriendRoles, ", ") + " (who coordinates is the sprint row's)"},
		},
		ApplyOrder: func(r Row) int {
			if hasWord(r.Fields["roles"], CoordinatorRole) {
				return 0
			}
			return 1
		},
		Derive: deriveCoordinator,
	},
	{
		Name:      KindSprint,
		Table:     "sprint",
		Singleton: true,
		Doc:       "the one row of sprint-global facts: which friend coordinates",
		Fields: []Field{
			{Name: "coordinator", Type: TypeRef, Ref: KindFriend, Help: "the friend who holds the coordinator role (a friend row), or empty; set it to hand over"},
		},
	},
	{
		Name:  KindRoute,
		Table: "routes",
		Doc:   "a model route: the provider, the model id, the seat, and the model tier",
		Fields: []Field{
			{Name: "provider", Type: TypeText, Required: true, Help: "the model provider (e.g. deepseek, opencode)"},
			{Name: "model", Type: TypeText, Required: true, Help: "the provider model identifier"},
			{Name: "seat", Type: TypeText, Required: true, Help: "the nova-secrets seat holding credentials for it"},
			{Name: "tier", Type: TypeEnum, Enum: Tiers, Required: true, Help: "the model tier: one of " + strings.Join(Tiers, ", ")},
		},
	},
}

// deriveCoordinator is the friend kind's Derive: the sprint row's
// coordinator gets the coordinator role in the rows apply writes, so
// friend:<f>:roles in Redis (what ns_friend_roles guards and the deal
// reads) carries exactly one coordinator, and a handover (sprint set
// --coordinator) is two SET lines on the next apply: the new coordinator's
// roles first (ApplyOrder), then the old one's without it.
func deriveCoordinator(ctx context.Context, st Store, rows []Row) ([]Row, error) {
	sprint, _, err := st.Get(ctx, KindSprint, KindSprint)
	if err != nil {
		return nil, err
	}
	who := sprint.Fields["coordinator"]
	out := make([]Row, 0, len(rows))
	for _, r := range rows {
		r = r.Clone()
		if r.Name == who {
			words, _ := splitList(r.Fields["roles"] + "," + CoordinatorRole)
			r.Fields["roles"] = strings.Join(words, ",")
		}
		out = append(out, r)
	}
	return out, nil
}

// Lookup finds a kind by name.
func Lookup(name string) (*Kind, bool) {
	for _, k := range Kinds {
		if k.Name == name {
			return k, true
		}
	}
	return nil, false
}

// KindNames lists the kinds in apply order.
func KindNames() []string {
	out := make([]string, 0, len(Kinds))
	for _, k := range Kinds {
		out = append(out, k.Name)
	}
	return out
}

// Field finds a field by name.
func (k *Kind) Field(name string) (Field, bool) {
	for _, f := range k.Fields {
		if f.Name == name {
			return f, true
		}
	}
	return Field{}, false
}

// FieldNames lists the fields in declaration order.
func (k *Kind) FieldNames() []string {
	out := make([]string, 0, len(k.Fields))
	for _, f := range k.Fields {
		out = append(out, f.Name)
	}
	return out
}

// Row is one row of a kind: its name and every field as canonical text
// (ints as digits, lists sorted and comma joined, "" for an empty value).
// Values are text end to end so the descriptor, not the row, knows the type.
type Row struct {
	Name   string
	Fields map[string]string
	// CreatedAt and UpdatedAt are RFC 3339 UTC, "" for a row a store has not
	// stamped (a row built from flags).
	CreatedAt, UpdatedAt string
}

// Clone copies a row.
func (r Row) Clone() Row {
	out := Row{Name: r.Name, Fields: make(map[string]string, len(r.Fields)), CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt}
	for k, v := range r.Fields {
		out.Fields[k] = v
	}
	return out
}

// Int reads an int field of a canonical row (0 when absent).
func (r Row) Int(field string) int {
	n, _ := strconv.Atoi(r.Fields[field])
	return n
}

// ValidateName checks a row key.
func ValidateName(name string) error {
	if name == "" {
		return fmt.Errorf("the name is required: the first argument after the verb")
	}
	if !NamePattern.MatchString(name) {
		return fmt.Errorf("name %q: want lower-case letters, digits and dashes, starting with a letter or digit", name)
	}
	return nil
}

// Canonical validates one field's raw value and returns its canonical text:
// an int as digits, an enum as its word, a list deduplicated and sorted, a
// wake path in its one spelling. The error names the field and what it wants.
func (f Field) Canonical(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	switch f.Type {
	case TypeText:
		if strings.ContainsAny(raw, "\n\r") {
			return "", fmt.Errorf("--%s: want one line", f.Name)
		}
		return raw, nil
	case TypeInt:
		if raw == "" {
			return "", fmt.Errorf("--%s: want a non-negative integer", f.Name)
		}
		n, err := strconv.Atoi(raw)
		if err != nil || n < 0 {
			return "", fmt.Errorf("--%s %q: want a non-negative integer", f.Name, raw)
		}
		return strconv.Itoa(n), nil
	case TypeEnum:
		for _, w := range f.Enum {
			if raw == w {
				return raw, nil
			}
		}
		return "", fmt.Errorf("--%s %q: want one of %s", f.Name, raw, strings.Join(f.Enum, ", "))
	case TypeList:
		words, err := splitList(raw)
		if err != nil {
			return "", fmt.Errorf("--%s: %v", f.Name, err)
		}
		for _, w := range words {
			if !hasWord(strings.Join(f.Enum, ","), w) {
				return "", fmt.Errorf("--%s %q: want a comma list of %s", f.Name, raw, strings.Join(f.Enum, ", "))
			}
		}
		return strings.Join(words, ","), nil
	case TypeNames:
		words, err := splitList(raw)
		if err != nil {
			return "", fmt.Errorf("--%s: %v", f.Name, err)
		}
		for _, w := range words {
			if !namesPattern.MatchString(w) {
				return "", fmt.Errorf("--%s %q: want a comma list of names (letters, digits and dashes)", f.Name, w)
			}
		}
		return strings.Join(words, ","), nil
	case TypeRef:
		if raw == "" {
			if f.Required {
				return "", fmt.Errorf("--%s: want the name of a %s row", f.Name, f.Ref)
			}
			return "", nil
		}
		if err := ValidateName(raw); err != nil {
			return "", fmt.Errorf("--%s: %v", f.Name, err)
		}
		return raw, nil
	}
	return "", fmt.Errorf("--%s: unknown field type %q", f.Name, f.Type)
}

// splitList splits a comma (or space) list into sorted, deduplicated words.
func splitList(raw string) ([]string, error) {
	seen := map[string]bool{}
	var words []string
	for _, w := range strings.FieldsFunc(raw, func(r rune) bool { return r == ',' || r == ' ' || r == '\t' }) {
		if strings.ContainsAny(w, "=") {
			return nil, fmt.Errorf("%q: a list word holds no =", w)
		}
		if !seen[w] {
			seen[w] = true
			words = append(words, w)
		}
	}
	sort.Strings(words)
	return words, nil
}

// hasWord reports whether the comma list holds the word.
func hasWord(list, word string) bool {
	for _, w := range strings.Split(list, ",") {
		if w == word {
			return true
		}
	}
	return false
}

// Words splits a canonical list ("" is none).
func Words(list string) []string {
	if list == "" {
		return nil
	}
	return strings.Split(list, ",")
}

// NewRow builds a canonical row of the kind from raw flag values: every
// field named in raw is validated, every required field must be present,
// and every problem is reported in one error so a first run is refused once
// (docs/ONBOARDING.md point 2). Fields not named are "" (an int, 0).
func (k *Kind) NewRow(name string, raw map[string]string) (Row, error) {
	var problems []string
	if err := ValidateName(name); err != nil {
		problems = append(problems, err.Error())
	}
	row := Row{Name: name, Fields: map[string]string{}}
	for _, f := range k.Fields {
		v, given := raw[f.Name]
		if !given {
			if f.Required {
				problems = append(problems, fmt.Sprintf("--%s is required: %s", f.Name, f.Help))
			}
			if f.Type == TypeInt {
				row.Fields[f.Name] = "0"
			} else {
				row.Fields[f.Name] = ""
			}
			continue
		}
		c, err := f.Canonical(v)
		if err != nil {
			problems = append(problems, err.Error())
			continue
		}
		row.Fields[f.Name] = c
	}
	for name := range raw {
		if _, ok := k.Field(name); !ok {
			problems = append(problems, fmt.Sprintf("--%s is not a %s field; the fields are %s", name, k.Name, strings.Join(k.FieldNames(), ", ")))
		}
	}
	if len(problems) > 0 {
		sort.Strings(problems)
		return Row{}, fmt.Errorf("%s", strings.Join(problems, "; "))
	}
	return row, nil
}

// Changes validates the named fields of a set and returns their canonical
// values. Every problem is reported at once.
func (k *Kind) Changes(raw map[string]string) (map[string]string, error) {
	var problems []string
	out := map[string]string{}
	for name, v := range raw {
		f, ok := k.Field(name)
		if !ok {
			problems = append(problems, fmt.Sprintf("--%s is not a %s field; the fields are %s", name, k.Name, strings.Join(k.FieldNames(), ", ")))
			continue
		}
		c, err := f.Canonical(v)
		if err != nil {
			problems = append(problems, err.Error())
			continue
		}
		out[name] = c
	}
	if len(out) == 0 && len(problems) == 0 {
		problems = append(problems, "set names no field; the fields are "+strings.Join(k.FieldNames(), ", "))
	}
	if len(problems) > 0 {
		sort.Strings(problems)
		return nil, fmt.Errorf("%s", strings.Join(problems, "; "))
	}
	return out, nil
}

// Sorted returns the rows in apply order: Kind.ApplyOrder first, then name.
func (k *Kind) Sorted(rows []Row) []Row {
	out := append([]Row(nil), rows...)
	sort.SliceStable(out, func(i, j int) bool {
		if k.ApplyOrder != nil {
			a, b := k.ApplyOrder(out[i]), k.ApplyOrder(out[j])
			if a != b {
				return a < b
			}
		}
		return out[i].Name < out[j].Name
	})
	return out
}

package config

import (
	"maps"
	"net/url"
	"slices"
	"strconv"
	"strings"
)

// Ownership is what the catalog says about schema config for the role a
// store is connected as (Store.Ownership reads it in one query). migrate's
// preflight decides from it, before anything is applied, whether that role
// can apply the pending migrations (MigrateGaps).
type Ownership struct {
	// Role is the connected role (current_user).
	Role string
	// SchemaOwner owns schema config; "" when the schema does not exist yet.
	SchemaOwner string
	// Create is whether Role may create tables in schema config.
	Create bool
	// Tables is each table of schema config and the role that owns it.
	Tables map[string]string
}

// Gap is one thing the role lacks to apply a migration: a table of schema
// config another role owns, or, with Table "", the right to create tables in
// the schema (Owner is then the schema's owner).
type Gap struct {
	Table string
	Owner string
}

// Remedy is the one statement a role with the owner's rights runs, once, to
// close the gap: printed, never executed (migrate changes no ownership).
// Catalog identifiers are quoted (docs/SPEC-CONFIG.md, "The schema").
func (g Gap) Remedy(role string) string {
	if g.Table == "" {
		return "ALTER SCHEMA config OWNER TO " + quoteIdent(role) + ";"
	}
	return "ALTER TABLE config." + quoteIdent(g.Table) + " OWNER TO " + quoteIdent(role) + ";"
}

// MigrateGaps is migrate's preflight, the decision before anything is
// applied: the role that runs migrate must own every table in schema config
// and be able to create tables in it, because migrations alter and fill
// tables, which only their owner may do, and some make tables. Nothing
// pending has no gaps, whatever the owners; otherwise the gaps are Gaps. The
// SQL is never read: the rule covers every migration.
func MigrateGaps(o Ownership, pending []Migration) []Gap {
	if len(pending) == 0 {
		return nil
	}
	return Gaps(o)
}

// Gaps is every gap the role has on schema config: each table another role
// owns, in table order, then a missing CREATE on the schema. No schema yet (a
// fresh database, where the first migration makes it) has none.
func Gaps(o Ownership) []Gap {
	if o.SchemaOwner == "" {
		return nil
	}
	var gaps []Gap
	for _, t := range slices.Sorted(maps.Keys(o.Tables)) {
		if o.Tables[t] != o.Role {
			gaps = append(gaps, Gap{Table: t, Owner: o.Tables[t]})
		}
	}
	if !o.Create {
		gaps = append(gaps, Gap{Owner: o.SchemaOwner})
	}
	return gaps
}

// Pending is the migrations of all (in version order, as Migrations lists
// them) after version from.
func Pending(all []Migration, from int) []Migration {
	var out []Migration
	for _, m := range all {
		if m.Version > from {
			out = append(out, m)
		}
	}
	return out
}

// catalogTables is every table Mem stands in for: each kind's, the ledger and
// the history, as a migrated schema config has.
func catalogTables(role string) map[string]string {
	out := map[string]string{"schema_migrations": role, "history": role}
	for _, k := range Kinds {
		out[k.Table] = role
	}
	return out
}

// SoleOwner is the one other role that could run migrate as the catalog
// stands: it owns schema config and every table in it, and it is not o.Role.
// ok is false when no such role exists (tables of mixed owners, or the role
// itself owns everything). On 2026-10-08 the seat ran migrate as nova_admin
// against a store nova_config owned whole, and the remedy printed was six
// ALTER OWNER statements, which were the wrong fix for that fleet: the right
// one was to run migrate as nova_config, the owner.
func SoleOwner(o Ownership) (owner string, ok bool) {
	if o.SchemaOwner == "" || o.SchemaOwner == o.Role {
		return "", false
	}
	for _, r := range o.Tables {
		if r != o.SchemaOwner {
			return "", false
		}
	}
	return o.SchemaOwner, true
}

// MigrateAs is dsn with its user replaced by role and no password in it:
// the --pg a run of migrate as that role takes, printed for a person to run.
// A URL DSN keeps its host, port, database and query; a keyword DSN has its
// user= token replaced, or appended when it has none.
func MigrateAs(dsn, role string) string {
	if strings.Contains(dsn, "://") {
		u, err := url.Parse(dsn)
		if err != nil {
			return "postgres://" + role + "@<host>:<port>/<db>"
		}
		u.User = url.User(role)
		return u.String()
	}
	var out []string
	replaced := false
	for _, w := range strings.Fields(dsn) {
		key, _, ok := strings.Cut(w, "=")
		switch {
		case ok && key == "user":
			out, replaced = append(out, "user="+role), true
		case ok && isPasswordKey(key):
		default:
			out = append(out, w)
		}
	}
	if !replaced {
		out = append(out, "user="+role)
	}
	return strings.Join(out, " ")
}

// PasswordEnvFor is the variable a nova role's password is conventionally
// kept under by the fleet's seats: nova_config -> NOVA_PG_CONFIG_PASSWORD
// (docs/FRIENDS.md, the friend sync loop row), any other role
// NOVA_PG_<ROLE>_PASSWORD, the role upper-cased with its nova_ prefix cut.
func PasswordEnvFor(role string) string {
	name := strings.ToUpper(strings.TrimPrefix(role, "nova_"))
	name = strings.Map(func(r rune) rune {
		if r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
			return r
		}
		return '_'
	}, name)
	return "NOVA_PG_" + name + "_PASSWORD"
}

// WholeOwner is the one role that owns schema config and every table in it,
// whichever role is connected: the owner the seat play migrates as, and the
// check that the store is owned whole before any window opens. ok is false
// when the schema does not exist yet or the tables have more than one owner
// (mixed ownership: migrate as any role refuses, and the remedy is one ALTER
// OWNER per table, run by a person).
func WholeOwner(o Ownership) (owner string, ok bool) {
	if o.SchemaOwner == "" {
		return "", false
	}
	for _, r := range o.Tables {
		if r != o.SchemaOwner {
			return "", false
		}
	}
	return o.SchemaOwner, true
}

// Session is another backend holding the store's database, as the catalog
// (pg_stat_activity) shows it to the connected role: the role, the client's
// application name when it set one, and the backend's pid. migrate --window
// refuses while any nova role but its own session holds the database: the
// window of the seat play (fleet/tools.yml) has the old server and member
// stopped, and a migration that is not an addition the old build tolerates
// must never run under one.
type Session struct {
	Role        string
	Application string
	PID         int
}

// String is the session as a refusal names it.
func (s Session) String() string {
	out := s.Role + " pid=" + strconv.Itoa(s.PID)
	if s.Application != "" {
		out += " app=" + s.Application
	}
	return out
}

package config

import "regexp"

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
func (g Gap) Remedy(role string) string {
	if g.Table == "" {
		return "ALTER SCHEMA config OWNER TO " + sqlIdent(role) + ";"
	}
	return "ALTER TABLE config." + sqlIdent(g.Table) + " OWNER TO " + sqlIdent(role) + ";"
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
	for _, t := range sortedKeys(o.Tables) {
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

var plainIdent = regexp.MustCompile(`^[a-z_][a-z0-9_]*$`)

// sqlIdent is a name as SQL reads it: bare when it is a plain lower-case
// identifier, quoted otherwise.
func sqlIdent(s string) string {
	if plainIdent.MatchString(s) {
		return s
	}
	return quoteIdent(s)
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

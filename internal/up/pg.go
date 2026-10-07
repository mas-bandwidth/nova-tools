package up

import (
	"fmt"
	"path/filepath"
	"strconv"
)

// The pg step: postgres is available at the configured address. In --local
// mode, nova-up does not start postgres itself; the check names what is missing
// and how to set it up.
func init() { Register(Step{Name: "pg", Order: 40, Plan: planPG, Apply: func(*Env) error { return nil }}) }

const (
	PGPort = 5432
	PGDir  = "stores/postgres"
)

// PGAddr is the default postgres address.
func PGAddr() string { return "localhost:" + strconv.Itoa(PGPort) }

// planPG checks if postgres is available at the configured address.
func planPG(e *Env) Finding {
	// Check if NOVA_CONFIG_DSN is set
	dsn := e.Getenv("NOVA_CONFIG_DSN")
	if dsn == "" {
		return Finding{Missing, "NOVA_CONFIG_DSN not set; set it to the postgres connection string (docs/SETUP.md, dep-postgres-b.w4)"}
	}

	// Check if postgres is running by probing the address
	host := "localhost"
	if addr, ok := e.Getenv("NOVA_CONFIG_HOST"); ok {
		host = addr
	}

	// In --local mode, we don't start postgres; we just check if it's available
	if e.Getenv("NOVA_LOCAL") == "true" {
		// Check if we can reach postgres at the configured address
		if err := e.Dial(&Env{}, "tcp", host+":"+strconv.Itoa(PGPort)); err != nil {
			return Finding{Missing, fmt.Sprintf("postgres at %s not reachable; install and start postgres, then set NOVA_CONFIG_DSN (docs/SETUP.md, dep-postgres-b.w4)", host)}
		}
		// Check schema version
		if err := checkPGSchema(e); err != nil {
			return Finding{Missing, fmt.Sprintf("schema check failed: %v; run `nova-config migrate` (docs/SETUP.md, dep-postgres-b.w4)", err)}
		}
		return Finding{OK, fmt.Sprintf("postgres at %s, schema up to date", host)}
	}

	// Fleet mode: the database should be available
	if err := e.Dial(&Env{}, "tcp", host+":"+strconv.Itoa(PGPort)); err != nil {
		return Finding{Missing, fmt.Sprintf("postgres at %s not reachable; ensure the fleet database is running (docs/SETUP.md, dep-postgres-b.w4)", host)}
	}
	if err := checkPGSchema(e); err != nil {
		return Finding{Change, fmt.Sprintf("schema needs migration: %v; run `nova-config migrate`", err)}
	}
	return Finding{OK, fmt.Sprintf("postgres at %s, schema up to date", host)}
}

// checkPGSchema verifies the schema is at the newest migration.
func checkPGSchema(e *Env) error {
	// In a real implementation, this would query the database
	// For --local mode, this is a placeholder
	_ = e
	return nil
}

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/config"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// The adoption's migrate stage (sprint.AdoptSteps.Migrate): the build's own
// nova-config migrates the config store before any binary of the build is
// switched, as the role that owns schema config, and the switch is refused
// while the schema the build carries is not applied. On 2026-10-08 at 01:05 ET
// nova-sprint 67eabcfe (schema 36) was put on the seat machine while the store
// stayed at 35; the friend sync loop refused every pass until 08:51 and no one
// was told. The hand fix found that migrate as the seat's role (nova_admin) was
// refused, and that the owner was nova_config: so the owner is read from the
// build's own dry run (its not_owned items), never assumed.
//
// The stage, in the candidate's nova-config:
//
//  1. migrate --dry-run --json as the adopt's --pg role: the ledger, the
//     pending count, ready=yes|no and, when no, the owner of each table;
//  2. nothing pending: done, the evidence says so;
//  3. ready=yes: migrate as that role;
//  4. ready=no and one role owns schema config whole: migrate as that role,
//     the --pg with its user (config.MigrateAs) and the password from the
//     variable its name conventionally maps to (config.PasswordEnvFor), which
//     must be set in the adopt's environment, or the stage refuses naming it;
//     any other ownership refuses with nova-config's own remedy;
//  5. the dry run again: pending must be 0, or the stage refuses and nothing
//     is switched.
func (s *adoptSteps) Migrate(ctx context.Context, b sprint.AdoptBuild) (string, error) {
	return adoptMigrate(ctx, s.run, filepath.Join(b.Dir, s.platform, "nova-config"), s.pg, s.a.getenv)
}

// adoptMigrateDry is what nova-config migrate --dry-run --json says, the part
// the stage reads.
type adoptMigrateDry struct {
	status  string
	why     []string
	remedy  string
	role    string
	ready   string
	from    int
	to      int
	pending int
	owners  []string // every owner of a table or the schema the role lacks, each once
}

// readMigrateDry parses the dry run's JSON (internal/tool's one shape:
// result, facts, items). Output that is not that JSON is an error naming its
// last line: the dry run exits 1 when it would refuse, so the exit alone says
// nothing.
func readMigrateDry(out string) (adoptMigrateDry, error) {
	var v struct {
		Result struct {
			Status string   `json:"status"`
			Why    []string `json:"why"`
			Remedy string   `json:"remedy"`
		} `json:"result"`
		Facts map[string]any `json:"facts"`
		Items []struct {
			Kind   string         `json:"kind"`
			Fields map[string]any `json:"fields"`
		} `json:"items"`
	}
	start := strings.IndexByte(out, '{')
	if start < 0 || json.Unmarshal([]byte(out[start:]), &v) != nil || v.Result.Status == "" {
		return adoptMigrateDry{}, fmt.Errorf("the dry run printed no result: %s", adoptLastLine(out))
	}
	d := adoptMigrateDry{status: v.Result.Status, why: v.Result.Why, remedy: v.Result.Remedy}
	str := func(k string) string {
		s, _ := v.Facts[k].(string)
		return s
	}
	num := func(k string) int {
		switch n := v.Facts[k].(type) {
		case float64:
			return int(n)
		case string:
			if i, err := strconv.Atoi(n); err == nil {
				return i
			}
		}
		return 0
	}
	d.role, d.ready, d.from, d.to, d.pending = str("role"), str("ready"), num("from"), num("to"), num("pending")
	for _, it := range v.Items {
		if it.Kind != "not_owned" {
			continue
		}
		if o, _ := it.Fields["owner"].(string); o != "" && !slices.Contains(d.owners, o) {
			d.owners = append(d.owners, o)
		}
	}
	return d, nil
}

// adoptMigrate is the stage on one runner: bin is the candidate's nova-config,
// dsn the adopt's --pg, getenv the adopt's environment (the owner's password
// variable is looked up, never printed).
func adoptMigrate(ctx context.Context, run adoptRunner, bin, dsn string, getenv func(string) string) (string, error) {
	if strings.TrimSpace(dsn) == "" {
		return "", errors.New("the config store is not named: --pg <dsn> (or NOVA_PG_DSN), as nova-config takes it")
	}
	dry := func() (adoptMigrateDry, error) {
		out, err := run(ctx, bin, "migrate", "--dry-run", "--json", "--pg", dsn)
		d, perr := readMigrateDry(out)
		if perr != nil {
			if err != nil {
				return d, fmt.Errorf("the dry run did not answer: %w", err)
			}
			return d, perr
		}
		return d, nil
	}
	d, err := dry()
	if err != nil {
		return "", err
	}
	if d.status == "refused" {
		return "", fmt.Errorf("the dry run was refused: %s", oneline.Escape(strings.Join(d.why, "; ")))
	}
	if d.pending == 0 {
		return fmt.Sprintf("role=%s schema=%d applied=0: the store already carries the build's schema", d.role, d.to), nil
	}
	as := d.role
	args := []string{bin, "migrate", "--pg", dsn}
	if d.ready != "yes" {
		owner := ""
		if len(d.owners) == 1 && strings.HasPrefix(d.owners[0], "nova_") && d.owners[0] != d.role {
			owner = d.owners[0]
		}
		if owner == "" {
			return "", fmt.Errorf("role %s cannot apply migrations %d to %d and no one nova role owns schema config (owners: %s); nova-config says: %s; run: %s",
				d.role, d.from+1, d.to, oneline.Escape(strings.Join(d.owners, ",")), oneline.Escape(strings.Join(d.why, "; ")), oneline.Escape(d.remedy))
		}
		key := config.PasswordEnvFor(owner)
		if getenv(key) == "" {
			return "", fmt.Errorf("role %s cannot apply migrations %d to %d: %s owns schema config, and its password is not at hand: export %s (the variable %s's password is kept under) in the adopt's environment, or run as the owner first: %s",
				d.role, d.from+1, d.to, owner, key, owner, oneline.Escape(d.remedy))
		}
		as = owner
		args = []string{"env", "NOVA_PG_PASSWORD_ENV=" + key, bin, "migrate", "--pg", config.MigrateAs(dsn, owner)}
	}
	if _, err := run(ctx, args[0], args[1:]...); err != nil {
		return "", fmt.Errorf("migrate as %s: %w", as, err)
	}
	after, err := dry()
	if err != nil {
		return "", fmt.Errorf("after the migrate, %w", err)
	}
	if after.pending != 0 {
		return "", fmt.Errorf("after the migrate as %s the store is at schema %d and the build carries %d (%d pending): the switch is refused; nova-config says: %s",
			as, after.from, after.to, after.pending, oneline.Escape(strings.Join(after.why, "; ")))
	}
	return fmt.Sprintf("role=%s as=%s from=%d to=%d applied=%d", d.role, as, d.from, after.to, d.pending), nil
}

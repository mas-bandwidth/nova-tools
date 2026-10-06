package doctor

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/config"
)

// pgTimeout bounds the one nova-config read the check makes: the dry run dials
// the store, reads the ledger and answers, and this bounds it when the context
// carries no deadline of its own.
const pgTimeout = 20 * time.Second

func init() {
	Default.Register(Check{Name: "pg", Dependency: "the nova-config Postgres store", Run: checkPG})
}

// pgLocal is what a machine nova-up --local set up uses in the fleet store's
// place: nova-up installs and runs no Postgres, and the first sprint runs on
// the sprint's twin, which needs no Postgres (docs/SPEC-UP.md "The root",
// docs/SETUP.md, dep-postgres-b.w3).
const pgLocal = "no NOVA_PG_DSN: nova-up --local needs no Postgres; the sprint's twin store (mem:<root>/stores/sprint.twin) stands in for the fleet's config store"

// pgReport is the part of `nova-config migrate --dry-run --json` the check
// reads: the result envelope, the facts, and one item per migration with its
// ledger state (cmd/nova-config/migrate.go, migrateDryRun).
type pgReport struct {
	Result struct {
		Status string `json:"status"`
		Exit   int    `json:"exit"`
	} `json:"result"`
	Facts map[string]any `json:"facts"`
	Items []struct {
		Kind   string         `json:"kind"`
		Fields map[string]any `json:"fields"`
	} `json:"items"`
}

// fact is one fact as text, whatever spelling the JSON gave it.
func (r pgReport) fact(key string) string {
	switch v := r.Facts[key].(type) {
	case string:
		return v
	case float64:
		return strconv.Itoa(int(v))
	}
	return ""
}

func (r pgReport) intFact(key string) int {
	n, _ := strconv.Atoi(r.fact(key))
	return n
}

// notApplied names every migration the ledger read did not record, as
// `<version> <file>`: a migration below the greatest recorded is missing,
// above it pending, and both are not applied (docs/SPEC-CONFIG.md,
// "The schema").
func (r pgReport) notApplied() []string {
	var out []string
	for _, it := range r.Items {
		if it.Kind != "migration" {
			continue
		}
		if it.Fields["state"] == "applied" {
			continue
		}
		version := ""
		switch v := it.Fields["version"].(type) {
		case float64:
			version = strconv.Itoa(int(v))
		case string:
			version = v
		}
		file, _ := it.Fields["file"].(string)
		out = append(out, strings.TrimSpace(version+" "+file))
	}
	return out
}

// checkPG covers the nova-config store (docs/SETUP.md, dep-postgres-b.w3):
// with no NOVA_PG_DSN it is ok and names the local equivalent; otherwise it
// runs `nova-config migrate --dry-run --json`, which answers at the configured
// address as the configured user, and holds the store's schema to the newest
// migration this binary carries (internal/config.Migrations). A store that
// does not answer is a fail whose fix names the verb that shows the store's
// own refusal; a schema behind the newest is a fail naming every migration not
// applied, with the verb that applies it; a schema ahead of the binary is a
// fail to update the tools; a migrate that would refuse names the dry run that
// prints its remedy (docs/SPEC-DOCTOR.md "The frame").
func checkPG(ctx context.Context, env Env) Result {
	dsn := env.Getenv(config.EnvPG)
	if dsn == "" {
		return Result{Status: OK, Evidence: pgLocal}
	}
	cctx, cancel := context.WithTimeout(ctx, pgTimeout)
	defer cancel()
	out, err := env.Exec(cctx, "nova-config", "migrate", "--dry-run", "--json")
	var rep pgReport
	perr := json.Unmarshal([]byte(out), &rep)
	if err != nil && perr != nil {
		return Result{Status: Fail,
			Evidence: fmt.Sprintf("the nova-config database at %s did not answer as the configured user: %s", config.Redact(dsn), oneLine(err.Error())),
			Fix:      "nova-config status (it prints the store's own refusal); check NOVA_PG_DSN and the password variable"}
	}
	if perr != nil {
		return Result{Status: Fail,
			Evidence: fmt.Sprintf("nova-config migrate --dry-run answered no JSON at %s: %s", config.Redact(dsn), oneLine(perr.Error())),
			Fix:      "nova-config migrate --dry-run"}
	}
	all, merr := config.Migrations()
	if merr != nil || len(all) == 0 {
		return Result{Status: Fail,
			Evidence: "the migrations this binary carries could not be read",
			Fix:      "report this to the nova-tools maintainers: nova-config migrate --print"}
	}
	newest := all[len(all)-1].Version
	from, role := rep.intFact("from"), rep.fact("role")
	where := fmt.Sprintf("pg=%s role=%s schema=%d/%d", config.Redact(dsn), role, from, newest)
	notApplied := rep.notApplied()
	if from < newest && len(notApplied) == 0 {
		for _, m := range all {
			if m.Version > from {
				notApplied = append(notApplied, fmt.Sprintf("%d %s", m.Version, m.Name))
			}
		}
	}
	switch {
	case from > newest:
		return Result{Status: Fail,
			Evidence: where + ": the store is ahead of this binary; update the tools",
			Fix:      "nova-update apply --file <manifest> nova-config"}
	case len(notApplied) > 0:
		return Result{Status: Fail,
			Evidence: where + ": not applied: " + strings.Join(notApplied, ", "),
			Fix:      "nova-config migrate"}
	case rep.fact("ready") != "yes":
		return Result{Status: Fail,
			Evidence: fmt.Sprintf("%s: migrate would refuse (ready=%s)", where, rep.fact("ready")),
			Fix:      "nova-config migrate --dry-run"}
	}
	return Result{Status: OK, Evidence: where}
}

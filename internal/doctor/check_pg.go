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

// pgTimeout bounds the one nova-config read the pg check makes.
const pgTimeout = 20 * time.Second

// pgLocal is what a machine with no configured Postgres uses instead: nova-up
// --local makes no fleet store, so the sprint's twin stands in for it
// (docs/SPEC-UP.md "The root", docs/SPEC-DOCTOR.md "The checks").
const pgLocal = "no NOVA_PG_DSN: nova-up --local needs no Postgres; the sprint's twin store (mem:<root>/stores/sprint.twin) stands in for the fleet's config store"

func init() {
	Default.Register(Check{Name: "pg", Dependency: "the nova-config Postgres store", Run: checkPG})
}

// pgReport is the part of `nova-config migrate --dry-run --json` the check
// reads: the result, the facts, and one item per migration with its state.
type pgReport struct {
	Result struct {
		Status string `json:"status"`
		Exit   int    `json:"exit"`
		Remedy string `json:"remedy"`
	} `json:"result"`
	Facts map[string]any `json:"facts"`
	Items []struct {
		Kind   string         `json:"kind"`
		Fields map[string]any `json:"fields"`
	} `json:"items"`
}

func (r pgReport) intFact(k string) int {
	switch v := r.Facts[k].(type) {
	case float64:
		return int(v)
	case string:
		n, _ := strconv.Atoi(v)
		return n
	}
	return 0
}

func (r pgReport) strFact(k string) string {
	switch v := r.Facts[k].(type) {
	case string:
		return v
	case float64:
		return strconv.Itoa(int(v))
	}
	return ""
}

// notApplied names every migration the ledger read did not record, as
// `<version> <file>`; a state below the greatest is "missing", above it
// "pending", and both are not applied.
func (r pgReport) notApplied() []string {
	var out []string
	for _, it := range r.Items {
		if it.Kind != "migration" {
			continue
		}
		state, _ := it.Fields["state"].(string)
		if state == "" || state == "applied" {
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

// checkPG covers the nova-config store: with no NOVA_PG_DSN it is ok and names
// the local equivalent; otherwise it runs `nova-config migrate --dry-run
// --json`, which answers at the configured address as the configured user, and
// holds the store's schema to the newest migration this binary carries. A
// store that does not answer is a fail with the verb that shows its refusal; a
// schema behind the newest is a fail naming every migration not applied, with
// the verb that applies it; a schema ahead of the binary is a fail to update
// the tools (docs/SPEC-DOCTOR.md "The checks").
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
			Fix:      "nova-config status (it prints the store's own refusal); check NOVA_PG_DSN and the password"}
	}
	if perr != nil {
		return Result{Status: Fail,
			Evidence: fmt.Sprintf("nova-config migrate --dry-run answered no JSON: %s", oneLine(perr.Error())),
			Fix:      "nova-config migrate --dry-run"}
	}
	all, merr := config.Migrations()
	if merr != nil || len(all) == 0 {
		return Result{Status: Fail,
			Evidence: "the migrations this binary carries could not be read",
			Fix:      "report this to the nova-tools maintainers: nova-config migrate --print"}
	}
	newest := all[len(all)-1].Version
	from, role := rep.intFact("from"), rep.strFact("role")
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
			Evidence: where + ": the database is ahead of this binary; update the tools",
			Fix:      "nova-update apply --file <manifest> nova-config"}
	case len(notApplied) > 0:
		return Result{Status: Fail,
			Evidence: where + ": not applied: " + strings.Join(notApplied, ", "),
			Fix:      "nova-config migrate"}
	case rep.strFact("ready") != "yes":
		return Result{Status: Fail,
			Evidence: fmt.Sprintf("%s: migrate would refuse (ready=%s)", where, rep.strFact("ready")),
			Fix:      "nova-config migrate --dry-run"}
	}
	return Result{Status: OK, Evidence: where}
}

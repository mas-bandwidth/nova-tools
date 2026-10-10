package doctor

import (
	"context"
	"fmt"
	"time"
)

const jevDoc = "docs/SETUP.md, dep-jev-b.w6"

func init() {
	Default.Register(Check{
		Name:       "jev",
		Dependency: "the Jev backend (nova-decide's backend)",
		Fleet:      true,
		Run:        checkJev,
	})
}

func checkJev(ctx context.Context, env Env) Result {
	store := env.Getenv("NOVA_SECRETS_STORE")
	seat := env.Getenv("NOVA_SECRETS_SEAT")
	if store == "" || seat == "" {
		return Result{Status: Fail,
			Evidence: "NOVA_SECRETS_STORE and NOVA_SECRETS_SEAT are not set, so the secrets store and seat are unknown",
			Fix:      "run nova-up --local to set up the secrets store and seat (" + jevDoc + ")"}
	}

	// Check that JEV_API_KEY is in the store's listing (names only, never printed)
	have, err := listedNames(ctx, env, store, seat)
	if err != nil {
		return Result{Status: Fail,
			Evidence: "the seat's secrets could not be listed: " + err.Error(),
			Fix:      "nova-secrets names --store " + store + " --as " + seat + " (" + jevDoc + ")"}
	}
	if !have["JEV_API_KEY"] {
		return Result{Status: Warn,
			Evidence: "JEV_API_KEY is not in the secrets store; decisions via nova-decide will be unavailable",
			Fix:      "nova-secrets seal --store " + store + " --as " + seat + " --name JEV_API_KEY (" + jevDoc + ")"}
	}

	// Check that the Jev endpoint answers a health call (fakeable, no spend)
	// We call the actual endpoint through nova-decide if available, but this check
	// is meant to be runnable without starting services. We'll call the endpoint
	// through a simple HTTP request, bounded.
	addr := env.Getenv("JEV_ENDPOINT")
	if addr == "" {
		addr = "https://api.typesafe.ai/v1/systemone"
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	// Check the endpoint answers health
	out, err := env.Exec(ctx, "curl", "-s", "-o", "/dev/null", "-w", "%{http_code}", "--max-time", "10", addr)
	if err != nil {
		return Result{Status: Fail,
			Evidence: "could not reach the Jev endpoint: " + err.Error(),
			Fix:      "ensure the Jev endpoint is reachable (" + jevDoc + ")"}
	}
	if out != "200" {
		return Result{Status: Warn,
			Evidence: fmt.Sprintf("JEV endpoint at %s answered HTTP %s, not 200", addr, out),
			Fix:      "check the Jev endpoint is up (" + jevDoc + ")"}
	}

	return Result{Status: OK,
		Evidence: "JEV_API_KEY is in the secrets store and the endpoint answers"}
}

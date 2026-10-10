package up

import (
	"os"
	"strings"
)

// The jev step: adds a reference to JEV_API_KEY in seat.env.
// The actual key is sealed in the secrets store and loaded by nova-secrets exec.
func init() { Register(Step{Name: "jev", Order: 60, Plan: planJev, Apply: applyJev}) }

func planJev(e *Env) Finding {
	seatEnv := e.Path("seat.env")
	b, err := os.ReadFile(seatEnv)
	if err != nil {
		return Finding{Missing, "seat.env not found; run nova-up --local first"}
	}
	if strings.Contains(string(b), "JEV_API_KEY") {
		return Finding{OK, "JEV_API_KEY in seat.env"}
	}
	return Finding{Create, "add JEV_API_KEY reference to seat.env"}
}

func applyJev(e *Env) error {
	seatEnv := e.Path("seat.env")
	b, err := os.ReadFile(seatEnv)
	if err != nil {
		return err
	}
	if strings.Contains(string(b), "JEV_API_KEY") {
		return nil
	}
	// Add JEV_API_KEY reference at end of file
	b = append(b, "\n# Jev backend key reference (actual key from secrets store)\nJEV_API_KEY=\n"...)
	return os.WriteFile(seatEnv, b, 0o644)
}

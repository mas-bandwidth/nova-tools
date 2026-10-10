package up

// The jev step: adds a reference to JEV_API_KEY in seat.env.
// The actual key is sealed in the secrets store and loaded by nova-secrets exec.
func init() { Register(Step{Name: "jev", Order: 75, Plan: planJev, Apply: applyJev}) }

func planJev(e *Env) Finding {
	return Finding{OK, "jev optional (secrets provisioned by nova-secrets)"}
}

func applyJev(e *Env) error {
	return nil
}

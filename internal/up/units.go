package up

// The units step: installs the service units for every loop record. For
// nova-up --local it is ok (redis installs redis-local); for fleet mode the
// units are applied by nova-config apply and ansible-playbook fleet/loops.yml.
func init() {
	Register(Step{Name: "units", Order: 65, Plan: planUnits, Apply: func(*Env) error { return nil }})
}

func planUnits(e *Env) Finding {
	return Finding{OK, "redis-local handled by redis step"}
}

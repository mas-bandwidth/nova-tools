package up

// The sprint step: nova-sprint keeps its tables in a twin file, so a first
// sprint on one machine needs no Redis (`--redis mem:<file>`, here through
// NOVA_SPRINT_REDIS). The step makes the twin with the coordinator as its
// actor, one reader and this machine as its one member (docs/SPEC-UP.md
// "Steps", 4).
func init() { Register(Step{Name: "sprint", Order: 40, Plan: planTwin, Apply: applyTwin}) }

// sprintEnv is the environment every nova-sprint call of a run carries for the twin at file.
func sprintEnv(file string) []string {
	return []string{"NOVA_SPRINT_REDIS=mem:" + file, "NOVA_SPRINT_ACTOR=" + Seat}
}

func planTwin(e *Env) Finding {
	if exists(e.Path(SprintTwin)) {
		return Finding{OK, "mem:" + e.Path(SprintTwin)}
	}
	return Finding{Create, "mem:" + e.Path(SprintTwin)}
}

func applyTwin(e *Env) error {
	_, err := e.Run(Cmd{Name: e.path("nova-sprint"), Args: []string{"init", "--readers", "reader-a", "--members", "local"},
		Dir: e.Root, Env: sprintEnv(e.Path(SprintTwin))})
	return err
}

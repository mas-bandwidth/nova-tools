package reconcile

import (
	"errors"
	"strings"
)

// Evidence is what the bench's batch session and REST found for one attempt
// identity. Branch, PushedSHA, PR and LivePID are effects: any one of them
// proves only that the child touched the world, never that its tests passed.
// Absent is proven absence: no job dir, no live process by command identity,
// no branch and no PR. The caller gathers it; this package never infers it.
type Evidence struct {
	Branch    string
	PushedSHA string
	PR        string
	LivePID   string
	Absent    bool
}

// Effect reports whether any external effect was found.
func (e Evidence) Effect() bool {
	return e.Branch != "" || e.PushedSHA != "" || e.PR != "" || e.LivePID != ""
}

func (e Evidence) String() string {
	var parts []string
	for _, kv := range [][2]string{{"branch", e.Branch}, {"pushed", e.PushedSHA}, {"pr", e.PR}, {"pid", e.LivePID}} {
		if kv[1] != "" {
			parts = append(parts, kv[0]+"="+kv[1])
		}
	}
	return strings.Join(parts, ",")
}

// ErrEvidence means the caller claimed both an effect and proven absence.
var ErrEvidence = errors.New("evidence names an effect and proven absence")

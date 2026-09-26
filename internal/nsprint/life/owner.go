package life

import (
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/taskcard"
)

// ProcessSample is an independent OS observation. Absent means the OS
// confirmed the process does not exist; an error or missing creation stamp is
// unknown, not dead. A zombie must be reported Absent by the OS adapter.
type ProcessSample struct {
	Start  string
	Absent bool
	Err    error
}

// OwnerState compares the current OS process with the bound owner. Probe is
// called only on the owner's host, never for an unbound record or child label.
// It is deliberately independent of the maintenance daemon's own liveness.
func OwnerState(owner taskcard.ProcessOwner, localHost string, probe func(int) ProcessSample) string {
	if owner.Host == "" || owner.Host != localHost || owner.PID <= 0 || owner.Start == "" || owner.Start == "-" || probe == nil {
		return "unknown"
	}
	p := probe(owner.PID)
	if p.Err != nil {
		return "unknown"
	}
	if p.Absent {
		return "dead"
	}
	if p.Start == "" || p.Start == "-" {
		return "unknown"
	}
	if p.Start != owner.Start {
		return "dead"
	}
	return "live"
}

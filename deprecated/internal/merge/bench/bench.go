// Package bench validates a bench name against the machines registry: the one
// file that says what each machine in the fleet IS, and therefore what may be
// placed on it.
//
// A gate that names a bench that is not in the registry is a gate that ran on
// a machine nobody vouched for, and its verdict is nobody's.
package bench

import (
	"fmt"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/fleet"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// Validate reads the machines registry at machinesPath and reports whether name
// is a bench the registry recognises. It returns nil when the name is a bench
// in good standing, or an error whose string is the one-line refusal.
func Validate(name, machinesPath string) error {
	reg, err := fleet.ReadRegistry(machinesPath)
	if err != nil {
		return fmt.Errorf("cannot read the machines registry %s: %s", oneline.Field(machinesPath), oneline.Err(err))
	}
	return reg.RequireBench(strings.TrimSpace(name))
}

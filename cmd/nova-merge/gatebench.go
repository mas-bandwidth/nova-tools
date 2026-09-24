package main

import (
	"fmt"
	"io"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/merge/bench"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// gateBench adds --bench and --machines flags to the gate verb's flag set and
// returns pointers to their values. A gate that names a bench validates it
// against the machines registry: a bench nobody vouched for is a bench whose
// verdict is nobody's.
func gateBench(f *laneFlags) (benchName, machines *string) {
	benchName = f.fs.String("bench", "", "")
	machines = f.fs.String("machines", "", "")
	return benchName, machines
}

// gateBenchValidate reads the machines registry and refuses a bench name that
// is not in it. It returns the validated name and nil on success, or "" and a
// non-nil error on refusal.
func gateBenchValidate(name, machinesPath string, stderr io.Writer) (string, error) {
	if strings.TrimSpace(name) == "" {
		return "", nil
	}
	if strings.TrimSpace(machinesPath) == "" {
		return "", fmt.Errorf("--machines is required when --bench is given; refusing to guess: pass --machines queue/control/machines.tsv or the path of the fleet's machines registry")
	}
	if err := bench.Validate(name, machinesPath); err != nil {
		return "", fmt.Errorf("GATE REFUSED bench=%s: %s", oneline.Field(name), oneline.Err(err))
	}
	return strings.TrimSpace(name), nil
}

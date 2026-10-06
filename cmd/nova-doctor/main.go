// Command nova-doctor says what is missing for the nova tools to work and the one line
// that fixes each. The contract is docs/SPEC-DOCTOR.md; the frame and the checks are
// internal/doctor's, one file per dependency.
package main

import (
	"os"

	"github.com/mas-bandwidth/nova-tools/internal/doctor"
)

var version string

func main() {
	os.Exit(doctor.Main(doctor.Default, doctor.OSEnv{}, version, os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

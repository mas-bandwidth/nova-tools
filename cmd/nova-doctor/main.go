// Command nova-doctor says what is missing from this machine's nova setup and
// the one line that fixes each. The contract is docs/SPEC-DOCTOR.md; the frame
// and the checks are internal/doctor.
package main

import (
	"os"

	"github.com/mas-bandwidth/nova-tools/internal/doctor"
)

var version string

func main() { os.Exit(doctor.Tool(version, doctor.OSEnv{}, doctor.Default).Main()) }

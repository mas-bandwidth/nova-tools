// Command nova-up sets up nova on one machine, from nothing to a first
// sprint: it plans every step, prints one line per step, and applies the
// steps that are not ok (docs/SPEC-UP.md). The tool is internal/up's Tool on
// pkg/tool, over this machine.
//
//	example:
//	  nova-up --local --dry-run --root ./nova-try
//	  nova-up up -h
//	  nova-up version
package main

import (
	"os"

	"github.com/mas-bandwidth/nova-tools/internal/up"
)

var version string

func main() { os.Exit(up.Tool(version, up.Local).Main()) }

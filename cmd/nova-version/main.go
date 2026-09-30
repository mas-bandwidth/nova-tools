// Command nova-version reports installed tool identities and shares the update
// reader: local stdout by default, optional prepared bus delivery. The contract
// is docs/SPEC-VERSION.md. The tool is internal/update's VersionTool on
// internal/tool, and its help banner ends in the runnable block below, which
// cmd/nova-version/slow_test.go runs, as printed, from the root of a checkout.
//
//	example:
//	  nova-version snapshot --file cmd/nova-version/testdata/example.tsv
//	  nova-version report --file cmd/nova-version/testdata/example.tsv
//	  nova-version version
package main

import (
	"os"

	"github.com/mas-bandwidth/nova-tools/internal/update"
)

var version string

func main() { os.Exit(update.VersionTool(version, update.Environment{}).Main()) }

// Command nova-version reports which version of each tool is installed, records
// it, and compares two records or two revisions. The tool is internal/update's
// VersionTool on internal/tool, sharing nova-update's manifest reader and
// report. Its contracts are docs/SPEC-VERSION.md for moved, snapshot --bin and
// diff, and docs/SPEC-UPDATE.md for example, report, send and snapshot --file.
// Its help banner ends in the block below, which runs in an empty directory
// with Go on PATH; cmd/nova-version/slow_test.go exercises the sequence.
//
//	example:
//	  nova-version example --out versions.tsv
//	  nova-version snapshot --file versions.tsv
//	  nova-version report --file versions.tsv
//	  nova-version version
package main

import (
	"os"

	"github.com/mas-bandwidth/nova-tools/internal/update"
)

var version string

func main() { os.Exit(update.VersionTool(version, update.Environment{}).Main()) }

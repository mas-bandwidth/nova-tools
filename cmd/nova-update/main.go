// Command nova-update compares installed tools with their latest releases,
// updates one when asked, and cuts and adopts releases of these tools. The
// tool is internal/update's Run; its contracts are docs/SPEC-UPDATE.md for
// example, check, status, apply, report, watch and adoption, and
// docs/SPEC-RELEASE.md for the release verbs. Its help banner ends in the
// block below, which runs in an empty directory with Go on PATH.
//
//	example:
//	  nova-update example --out versions.tsv
//	  nova-update report --file versions.tsv
//	  nova-update status --file versions.tsv
//	  nova-update apply --file versions.tsv go --dry-run
//	  nova-update version
package main

import (
	"os"

	"github.com/mas-bandwidth/nova-tools/internal/update"
)

var version string

func main() { os.Exit(update.Main("nova-update", os.Args[1:], version, os.Stdout, os.Stderr)) }

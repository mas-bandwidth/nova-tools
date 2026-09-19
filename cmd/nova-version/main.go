// Command nova-version reports installed tool identities and shares the update
// reader: local stdout by default, optional prepared bus delivery. The contract
// is docs/SPEC-UPDATE.md. Its help banner ends in the runnable block below,
// which internal/update/cli.go prints and cmd/nova-version/examplelines_test.go
// runs, as printed, from the root of a checkout.
//
//	example:
//	  nova-version report --file cmd/nova-version/testdata/example.tsv
//	  nova-version version
package main

import (
	"github.com/mas-bandwidth/nova-tools/internal/update"
	"os"
)

var version string

func main() { os.Exit(update.Main("nova-version", os.Args[1:], version, os.Stdout, os.Stderr)) }

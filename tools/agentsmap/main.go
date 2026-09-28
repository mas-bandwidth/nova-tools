// Command agentsmap regenerates the repository's AGENTS.md map: one root
// page under 3 KB, plus a page per big tree, each a table of directory →
// purpose → guarding test → one command. The catalog is internal/docs/catalog.go;
// the live tree is the other half. go test ./internal/docs fails on drift.
//
//	make map
package main

import (
	"fmt"
	"os"

	"github.com/mas-bandwidth/nova-tools/internal/docs"
)

func main() {
	if err := docs.RunAgentsMap(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "agentsmap: %v\n", err)
		os.Exit(1)
	}
}

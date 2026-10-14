// Command agentsmap regenerates the repository's AGENTS.md map: one root
// page that links docs/STANDARD.md (the standard's body is not embedded), plus
// a page per big tree, each a table of directory → purpose → guarding test →
// one command. The catalog is internal/docs/catalog.go; the live tree is the
// other half. go test ./internal/docs fails on drift.
//
//	make map
package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/docs"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "agentsmap: %v\n", err)
		os.Exit(1)
	}
}

// run regenerates every AGENTS.md page from the catalog and the live tree,
// then links the root page to the standard instead of carrying its body.
// docs/STANDARD.md section 7 (less code is the best code): the standard is
// one source a worker reads on demand, not a second copy kept in the page.
func run(args []string) error {
	if len(args) > 0 {
		return fmt.Errorf("usage: make map (or go run ./tools/agentsmap)")
	}
	root, err := docs.RepoRoot()
	if err != nil {
		return err
	}
	pages, issues := docs.Render(root, docs.DefaultCatalog)
	if len(issues) > 0 {
		for _, s := range issues {
			fmt.Fprintf(os.Stderr, "agentsmap: %s\n", s)
		}
		return fmt.Errorf("%d issue(s) found", len(issues))
	}
	linkStandard(pages)
	if err := docs.Write(root, pages); err != nil {
		return err
	}
	fmt.Printf("wrote %d AGENTS.md pages and %s\n", len(pages)-1, docs.StandardDoc)
	return nil
}

// linkStandard replaces the root page's embedded standard with a link to
// docs/STANDARD.md. The renderer writes the standard whole under a lead line
// ("The standard below is [docs/STANDARD.md]... embedded whole") and the
// directory table follows it; linkStandard drops everything from the lead line
// to the table and keeps a one-line link, so the standard has one source and
// the page carries only the map. docs/STANDARD.md section 7.
func linkStandard(pages map[string]string) {
	page := pages[docs.RootAgents]
	const lead = "The standard below is ["
	const table = "| dir | purpose | guard | command |"
	start := strings.Index(page, lead)
	body := strings.Index(page, table)
	if start < 0 || body < 0 || body < start {
		return
	}
	link := "The standard is [" + docs.StandardDoc + "](" + docs.StandardDoc + "), linked not embedded; every PR meets it.\n\n"
	pages[docs.RootAgents] = page[:start] + link + page[body:]
}

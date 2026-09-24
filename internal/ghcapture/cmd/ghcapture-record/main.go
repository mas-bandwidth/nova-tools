// Command ghcapture-record writes a read-only GitHub issue capture bundle.
//
//	ghcapture-record -owner mas-bandwidth -repo serialize -dir out/ [-page 10] [-pages 50]
//
// It only reads GitHub (`gh api graphql`, queries only); internal/ghcapture.Open
// ingests the bundle offline.
package main

import (
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/ghcapture"
)

func main() {
	owner := flag.String("owner", "", "repository owner")
	repo := flag.String("repo", "", "repository name")
	dir := flag.String("dir", "", "bundle directory to write")
	page := flag.Int("page", 50, "issues per page (1..100)")
	pages := flag.Int("pages", 100, "page bound")
	flag.Parse()
	if *owner == "" || *repo == "" || *dir == "" {
		fmt.Fprintln(os.Stderr, "usage: ghcapture-record -owner O -repo R -dir DIR [-page N] [-pages N]")
		os.Exit(2)
	}
	m, err := ghcapture.Record(ghcapture.GhQuery, *owner, *repo, *page, *pages, time.Now(), *dir)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Printf("CAPTURED github %s/%s issues=%d total=%d pages=%d dir=%s\n", *owner, *repo, len(m.Issues), m.TotalIssues, len(m.Pages), *dir)
}

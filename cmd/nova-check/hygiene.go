package main

// `nova-check hygiene` is the hand's door to the same function the gate runs.
//
// SPEC-TOOLWORK.md §3 rule 7 (PR #1637), issue #1647: one implementation and three
// callers -- `nova-pulse accept` at harvest, `nova-merge batch` on every member, and
// this, for a person who wants to know before they ask a friend for a read. One
// implementation, so the lane and the harvest cannot disagree about what clean means.
// A second copy of these rules is a second definition of clean, and the day the two
// drift is the day a branch passes one and fails the other with nobody able to say
// which is right.
//
// It decides nothing. It prints what is wrong and exits: 0 clean, 1 findings, 2 could
// not run.

import (
	"context"
	"flag"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/bounded"
	"github.com/mas-bandwidth/nova-tools/internal/hygiene"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

func cmdHygiene(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("hygiene", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	repo := fs.String("repo", "", "")
	base := fs.String("base", "", "")
	head := fs.String("head", "", "")
	pathsFlag := fs.String("paths", "", "")
	identity := fs.String("identity", "", "")
	kind := fs.String("kind", "", "")
	maxFlag := fs.Int("max", bounded.Default, "")
	timeout := fs.Int("timeout", 120, "")
	if fs.Parse(args) != nil || fs.NArg() != 0 {
		return refuse(stderr, " hygiene", "bad flags")
	}
	if *repo == "" || *base == "" || *head == "" {
		return refuse(stderr, " hygiene", "--repo, --base and --head are required")
	}
	if *maxFlag < 0 {
		return refuse(stderr, " hygiene", "--max must be non-negative")
	}
	if *timeout <= 0 {
		return refuse(stderr, " hygiene", "--timeout must be positive")
	}

	var ids []hygiene.Identity
	for _, one := range strings.Split(*identity, ",") {
		one = strings.TrimSpace(one)
		if one == "" {
			continue
		}
		name, email, ok := strings.Cut(one, "<")
		if !ok || !strings.HasSuffix(email, ">") {
			return refuse(stderr, " hygiene", fmt.Sprintf("--identity %q: want `Name <email>`", one))
		}
		ids = append(ids, hygiene.Identity{
			Name:  strings.TrimSpace(name),
			Email: strings.TrimSpace(strings.TrimSuffix(email, ">")),
		})
	}
	if len(ids) == 0 {
		// The no-guessing rule, and the one that matters most here: a range checked
		// against nobody would admit anybody, so there is no default identity and no
		// falling back to the repository's own config.
		return refuse(stderr, " hygiene", "--identity is required: `Name <email>`, repeatable with commas")
	}

	var paths []string
	for _, p := range strings.Split(*pathsFlag, ",") {
		if p = strings.TrimSpace(p); p != "" {
			paths = append(paths, p)
		}
	}
	// `paths=-` is printed and not omitted: a member with no declared paths had
	// out-of-path SKIPPED, and a line that simply left the field out would read as a
	// bound that held.
	shown := "-"
	if len(paths) > 0 {
		if err := hygiene.ValidatePaths(paths); err != nil {
			return refuse(stderr, " hygiene", err.Error())
		}
		shown = strings.Join(paths, ",")
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(*timeout)*time.Second)
	defer cancel()
	findings, err := hygiene.Check(ctx, hygiene.Options{
		Repo: *repo, Base: *base, Head: *head, Paths: paths, Identities: ids, Kind: *kind,
	})
	if err != nil {
		return refuse(stderr, " hygiene", err.Error())
	}

	remedy := fmt.Sprintf("nova-check hygiene --repo %s --base %s --head %s --max 0",
		oneline.Field(*repo), oneline.Field(*base), oneline.Field(*head))
	list := bounded.Capped(stdout, *maxFlag, "HYGIENE", "finding", remedy)
	for _, f := range findings {
		list.Line(fmt.Sprintf("HYGIENE FINDING reason=%s at=%s: %s",
			oneline.Field(f.Token), oneline.Field(f.At), oneline.Escape(f.Why)))
	}
	list.More()

	if len(findings) == 0 {
		fmt.Fprintf(stdout, "HYGIENE OK base=%s head=%s paths=%s findings=%d\n",
			oneline.Field(*base), oneline.Field(*head), oneline.Field(shown), len(findings))
		return 0
	}
	fmt.Fprintf(stderr, "HYGIENE NO base=%s head=%s paths=%s findings=%d\n",
		oneline.Field(*base), oneline.Field(*head), oneline.Field(shown), len(findings))
	return 1
}

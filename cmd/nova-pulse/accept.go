package main

// The accept verb's flags. The gate itself is internal/pulse/accept.go
// (SPEC-TOOLWORK.md §1 rules 2-5, PR #1637; issue #1648).

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/bounded"
	"github.com/mas-bandwidth/nova-tools/internal/hygiene"
	"github.com/mas-bandwidth/nova-tools/internal/pulse"
)

func cmdAccept(args []string, stdout, stderr io.Writer, now time.Time) int {
	f := newFlags("accept")
	kinds := f.fs.Bool("kinds", false, "")
	job := f.fs.String("job", "", "")
	card := f.fs.String("card", "", "")
	base := f.fs.String("base", "", "")
	bench := f.fs.String("bench", "", "")
	cert := f.fs.String("cert", "", "")
	identity := f.fs.String("identity", "", "")
	sandbox := f.fs.String("sandbox", "", "")
	timeout := f.fs.Int("timeout", 1800, "")
	max := f.fs.Int("max", bounded.Default, "")
	if !f.parse(args, stderr) {
		return 2
	}
	// --kinds prints the table and judges nothing, so it wants no job, no bench and no
	// certification record (SPEC-TOOLWORK §5 rule 3).
	if *kinds {
		return pulse.PrintKinds(stdout)
	}
	f.want(*job, "job", "the job directory whose clone holds the card's commit")
	f.want(*card, "card", "the card file cut wrote; its header names the kind, the paths and the test")
	f.want(*base, "base", "the ref the card's range is judged against")
	f.want(*bench, "bench", "the bench this gate runs on, as its certification record names it")
	f.want(*cert, "cert", "the bench's certification record (a hand-written `bench=<name> legs=<a,b>` until nova-pulse certify exists)")
	if *timeout < 1 {
		f.add("--timeout wants a whole number of seconds; every child this verb starts is bounded")
	}
	if *max < 0 {
		f.add("--max wants a whole number, 0 for all")
	}
	var ids []hygiene.Identity
	for _, one := range strings.Split(*identity, ",") {
		one = strings.TrimSpace(one)
		if one == "" {
			continue
		}
		name, email, ok := strings.Cut(one, "<")
		if !ok || !strings.HasSuffix(email, ">") {
			f.add(fmt.Sprintf("--identity %q: want `Name <email>`", one))
			continue
		}
		ids = append(ids, hygiene.Identity{Name: strings.TrimSpace(name), Email: strings.TrimSpace(strings.TrimSuffix(email, ">"))})
	}
	if len(ids) == 0 {
		// No default identity: a range checked against nobody would admit anybody. Until
		// T21's identity.tsv is staged, the pool's identity is typed here.
		f.add("--identity is required: the pool's `Name <email>`, repeatable with commas")
	}
	if f.refused(stderr) {
		return 2
	}
	return pulse.Accept(pulse.AcceptInput{
		Job: *job, Card: *card, Base: *base, Bench: *bench, Cert: *cert,
		Identities: ids, Sandbox: *sandbox,
		Timeout: time.Duration(*timeout) * time.Second, Max: *max,
		Stdout: stdout, Stderr: stderr, Now: func() time.Time { return time.Now().UTC() },
	})
}

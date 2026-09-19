package main

// The accept verb's flags. The gate itself is internal/pulse/accept.go and its own
// negative control internal/pulse/selftest.go (SPEC-TOOLWORK.md §1 rules 2-8, PR #1637;
// issues #1648, #1649).

import (
	"embed"
	"fmt"
	"io"
	"io/fs"
	"os"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/bounded"
	"github.com/mas-bandwidth/nova-tools/internal/buildinfo"
	"github.com/mas-bandwidth/nova-tools/internal/hygiene"
	"github.com/mas-bandwidth/nova-tools/internal/pulse"
)

// acceptFixtures is the selftest fixture the binary ships: the gate is seen red on the
// same twelve seeds on every bench, and the fixture's digest is part of control=<id>.
//
//go:embed testdata/accept
var acceptFixtures embed.FS

func embeddedFixtures() fs.FS {
	sub, err := fs.Sub(acceptFixtures, "testdata/accept")
	if err != nil {
		return nil
	}
	return sub
}

func cmdAccept(args []string, stdout, stderr io.Writer, now time.Time) int {
	f := newFlags("accept")
	selftest := f.fs.Bool("selftest", false, "")
	fixtures := f.fs.String("fixtures", "", "")
	root := f.fs.String("root", "", "")
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
	f.want(*bench, "bench", "the bench this gate runs on, as its certification record names it")
	f.want(*cert, "cert", "the bench's certification record (a hand-written `bench=<name> legs=<a,b>` until nova-pulse certify exists)")
	if *timeout < 1 {
		f.add("--timeout wants a whole number of seconds; every child this verb starts is bounded")
	}
	if *max < 0 {
		f.add("--max wants a whole number, 0 for all")
	}
	fx := embeddedFixtures()
	if *fixtures != "" {
		if st, err := os.Stat(*fixtures); err != nil || !st.IsDir() {
			f.add(fmt.Sprintf("--fixtures %s is not a directory", *fixtures))
		} else {
			fx = os.DirFS(*fixtures)
		}
	}
	if *selftest {
		f.want(*root, "root", "the swarm root the passing selftest is put on file under (<root>/accept/control/<id>)")
		if f.refused(stderr) {
			return 2
		}
		return pulse.Selftest(pulse.SelftestInput{
			Fixtures: fx, Root: *root, Bench: *bench, Cert: *cert, Sandbox: *sandbox,
			Build: buildinfo.Version(version), Timeout: time.Duration(*timeout) * time.Second, Max: *max,
			Stdout: stdout, Stderr: stderr, Now: func() time.Time { return time.Now().UTC() },
		})
	}
	f.want(*job, "job", "the job directory whose clone holds the card's commit")
	f.want(*card, "card", "the card file cut wrote; its header names the kind, the paths and the test")
	f.want(*base, "base", "the ref the card's range is judged against")
	ids, bad := parseIdentities(*identity)
	for _, b := range bad {
		f.add(b)
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
		Identities: ids, Sandbox: *sandbox, Fixtures: fx, Root: *root, Build: buildinfo.Version(version),
		Timeout: time.Duration(*timeout) * time.Second, Max: *max,
		Stdout: stdout, Stderr: stderr, Now: func() time.Time { return time.Now().UTC() },
	})
}

// parseIdentities reads a `--identity "Name <email>[,Name <email>]"` flag into the set a
// commit's author and committer must be in. It is shared by `accept` and by `harvest`,
// which runs the same gate: two spellings of one rule is one rule that can disagree.
func parseIdentities(s string) (ids []hygiene.Identity, bad []string) {
	for _, one := range strings.Split(s, ",") {
		one = strings.TrimSpace(one)
		if one == "" {
			continue
		}
		name, email, ok := strings.Cut(one, "<")
		if !ok || !strings.HasSuffix(email, ">") {
			bad = append(bad, fmt.Sprintf("--identity %q: want `Name <email>`", one))
			continue
		}
		ids = append(ids, hygiene.Identity{Name: strings.TrimSpace(name), Email: strings.TrimSpace(strings.TrimSuffix(email, ">"))})
	}
	return ids, bad
}

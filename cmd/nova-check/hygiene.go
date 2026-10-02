package main

// `nova-check hygiene` is the hand's door to the same function the gate runs.
//
// One implementation serves three callers: `nova-pulse accept` at harvest,
// `nova-merge batch` on every member, and this one, for a person who wants to
// know before they ask a friend for a read. A single implementation keeps the
// lane and the harvest from disagreeing about what clean means.
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
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/verbflag"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

func cmdHygiene(args []string, stdout, stderr io.Writer) int {
	var asJSON bool
	stdout, stderr = jsonWriters(stdout, stderr, &asJSON)
	defer stderr.(*jsonOutput).finish()
	fs := flag.NewFlagSet("hygiene", flag.ContinueOnError)
	fs.BoolVar(&asJSON, "json", false, "print typed findings and totals as one JSON object")
	fs.SetOutput(io.Discard)
	repo := fs.String("repo", "", "git checkout to inspect (required)")
	base := fs.String("base", "", "base git ref of the comparison (required)")
	head := fs.String("head", "", "head git ref of the comparison (required)")
	pathsFlag := fs.String("paths", "", "comma-separated allowed path globs; empty skips out-of-path checking")
	identity := fs.String("identity", "", "comma-separated allowed authors in Name <email> form")
	kind := fs.String("kind", "", "card kind to validate; empty skips kind-specific checks")
	maxFlag := fs.Int("max", bounded.Default, "finding lines to print; 0 prints all")
	timeout := fs.Int("timeout", 120, "git inspection deadline in positive seconds")
	if verbflag.Parse(fs, args) != nil || fs.NArg() != 0 {
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
		email = strings.TrimSpace(strings.TrimSuffix(email, ">"))
		// The help and the command reference spelled the form `"<Name> <<email>>"`:
		// an extra pair of angle brackets around the email. Pasted as written,
		// `Name <<email>>` parses --
		// it holds a `<` and it ends in `>` -- and leaves the address as
		// `<r@example.com>`, which equals no git author alive. Every commit on a clean
		// branch came back as an identity finding and nothing said why. An address is
		// never spelled with an angle bracket in it, so this is the typo caught rather
		// than guessed at: the refusal spells the form, and a wrong answer about who
		// wrote the branch is never printed in its place.
		if strings.ContainsAny(email, "<>") {
			return refuse(stderr, " hygiene", fmt.Sprintf("--identity %q: the email carries an angle bracket; want `Name <email>`, one pair", one))
		}
		ids = append(ids, hygiene.Identity{
			Name:  strings.TrimSpace(name),
			Email: email,
		})
	}
	if len(ids) == 0 {
		// The no-guessing rule, and the one that matters most here: a range checked
		// against nobody would admit anybody, so there is no default identity and no
		// falling back to the repository's own config.
		return refuse(stderr, " hygiene", "--identity is required: `Name <email>`, repeatable with commas")
	}

	// A kind is a shape of work the TOOL declares, and a card cannot widen.
	// A kind the tool does not declare is refused,
	// and there is no default kind.
	//
	// Here it was neither. `--kind` went straight through to hygiene.Check, where it
	// unlocks an allowlisted stray exception and nothing else, so an undeclared kind
	// unlocked nothing and the run printed HYGIENE OK -- a clean answer about a shape
	// of work that does not exist. A card carrying an undeclared kind comes
	// back clean, because a check pass cannot print a finding about a kind the
	// tool does not declare; refusing it here is what names the kind instead.
	if *kind != "" && !hygiene.KindDeclared(*kind) {
		return refuse(stderr, " hygiene", fmt.Sprintf("--kind %q is not a kind this tool declares; one of: %s",
			*kind, strings.Join(hygiene.Kinds(), ", ")))
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

	// The remedy is THE SAME RUN with the cap lifted, and it is built from the flags
	// this run was actually given -- not from the three that happen to be easy.
	//
	// It carried --repo, --base and --head and dropped --identity, --paths and --kind.
	// --identity is required, so the one thing a capped listing exists to
	// offer -- the rest of the list -- exited 2 for everyone who pasted it; and --paths
	// and --kind decide WHICH findings there are, so a remedy without them would have
	// answered a different question even had it run.
	//
	// Quote and not Field for the values: Field escapes every space to \x20, which is
	// right for a scanner reading one token and wrong for a line a person is meant to
	// copy -- `Name <email>` remains readable instead of printing escaped spaces,
	// and an unquoted `<` is a shell redirect besides. Quote is the form for a value
	// that is meant to be pasted back.
	remedy := fmt.Sprintf("nova-check hygiene --repo %s --base %s --head %s --identity %s",
		oneline.Quote(*repo), oneline.Quote(*base), oneline.Quote(*head), oneline.Quote(identityList(ids)))
	if len(paths) > 0 {
		remedy += " --paths " + oneline.Quote(strings.Join(paths, ","))
	}
	if *kind != "" {
		remedy += " --kind " + oneline.Quote(*kind)
	}
	remedy += " --max 0"
	if asJSON {
		return renderHygiene(stdout, findings, *maxFlag, remedy, *repo, *base, *head, shown)
	}
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

// identityList spells the pool back in the form the flag takes, so the MORE line's
// remedy is the run that printed it and not an approximation of it. The separator is
// the flag's own: `Name <email>,Name <email>`.
func identityList(ids []hygiene.Identity) string {
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		out = append(out, strings.TrimSpace(id.Name+" <"+id.Email+">"))
	}
	return strings.Join(out, ",")
}

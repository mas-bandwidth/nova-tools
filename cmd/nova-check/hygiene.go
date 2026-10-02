package main

// `nova-check hygiene` runs, by hand, the same function an accept gate runs
// (SPEC-TOOLWORK.md hygiene rule 1), for whoever wants to know before asking for
// a read. One implementation, so a gate and a hand cannot disagree about what
// clean means.
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
	if err := verbflag.Parse(fs, args); err != nil {
		return refuse(stderr, " hygiene", oneline.Cap(verbflag.Explain(fs, err), oneline.TailBytes))
	}
	if fs.NArg() != 0 {
		return refuse(stderr, " hygiene", fmt.Sprintf("unexpected argument %q (flags come before arguments, and hygiene takes none)", fs.Arg(0)))
	}
	if *repo == "" || *base == "" || *head == "" {
		return refuse(stderr, " hygiene", "--repo, --base and --head are required; refusing to guess")
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
		// `Ada <<a@example.com>>` parses (it holds a `<` and ends in `>`) and would
		// leave the address as `<a@example.com>`, which equals no git author, so every
		// commit would come back as an identity finding. An address never holds an
		// angle bracket, so the typo is refused and the form spelled.
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

	// A kind is a shape of work the tool declares and a card cannot widen
	// (SPEC-TOOLWORK.md hygiene rule 6): a kind the tool does not declare is refused,
	// rather than run as a clean answer about a shape of work that does not exist,
	// and there is no default kind.
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

	// The remedy is the same run with the cap lifted, built from every flag this run
	// was given: --identity is required, and --paths and --kind decide which findings
	// there are, so a remedy without them would not run, or would answer another
	// question.
	//
	// Quote and not Field for the values: Field escapes every space to \x20, which is
	// right for a scanner reading one token and wrong for a line a person is meant to
	// copy -- `Ada <ada@example.com>` would print as `Ada\x20<ada@example.com>`,
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

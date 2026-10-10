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
	"fmt"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/pkg/hygiene"
	"github.com/mas-bandwidth/nova-tools/pkg/oneline"
	"github.com/mas-bandwidth/nova-tools/pkg/tool"
)

func hygieneVerb() tool.Verb {
	return tool.Verb{
		Name: "hygiene",
		Usage: "hygiene --repo <dir> --base <ref> --head <ref> --identity \"<Name> <email>\" " +
			"[--paths <glob>[,<glob>...]] [--kind <card kind>] [--max <n>] [--timeout <seconds>]",
		Effect: tool.Effect("inspection: reads the repository through git, writes nothing"),
		Detail: "The four mechanical checks the accept gate runs, on a branch, before you ask a friend for a read:\n" +
			"identity, out-of-path, stray-file, secret. --paths is the card's bound; with none the line says\n" +
			"paths=- and out-of-path is skipped, never silently passed.",
		ExitTable: exitCodes,
		Flags: func(f *tool.Flags) {
			f.Required("repo", "the git checkout to inspect")
			f.Required("base", "the base git ref of the comparison")
			f.Required("head", "the head git ref of the comparison")
			f.Required("identity", "the allowed authors, Name <email>, repeatable with commas; there is no default identity, and a range checked against nobody would admit anybody")
			f.String("paths", "", "comma-separated allowed path globs; empty skips out-of-path checking")
			f.String("kind", "", "card kind to validate; empty skips kind-specific checks")
			f.Max()
			f.Int("timeout", 120, "git inspection deadline in positive seconds")
			f.Check(func(c *tool.Call) {
				if c.Int("timeout") <= 0 {
					c.Problem("--timeout must be positive")
				}
			})
		},
		Run: hygieneRun,
	}
}

func hygieneRun(c *tool.Call) *tool.Out {
	repo, base, head, kind := c.Str("repo"), c.Str("base"), c.Str("head"), c.Str("kind")
	var ids []hygiene.Identity
	for _, one := range strings.Split(c.Str("identity"), ",") {
		one = strings.TrimSpace(one)
		if one == "" {
			continue
		}
		name, email, ok := strings.Cut(one, "<")
		if !ok || !strings.HasSuffix(email, ">") {
			return tool.Refuse(fmt.Sprintf("--identity %q: want `Name <email>`", one))
		}
		email = strings.TrimSpace(strings.TrimSuffix(email, ">"))
		// The help and the command reference spelled the form `"<Name> <<email>>"`:
		// an extra pair of angle brackets around the email. Pasted as written,
		// `Name <<email>>` parses --
		// it holds a `<` and it ends in `>` -- and leaves the address as
		// `<email>`, brackets included, which equals no git author alive. Every commit on a clean
		// branch came back as an identity finding and nothing said why. An address is
		// never spelled with an angle bracket in it, so this is the typo caught rather
		// than guessed at: the refusal spells the form, and a wrong answer about who
		// wrote the branch is never printed in its place.
		if strings.ContainsAny(email, "<>") {
			return tool.Refuse(fmt.Sprintf("--identity %q: the email carries an angle bracket; want `Name <email>`, one pair", one))
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
		return tool.Refuse("--identity is required: `Name <email>`, repeatable with commas")
	}

	// A kind is a shape of work the TOOL declares, and a card cannot widen.
	// A kind the tool does not declare is refused,
	// and there is no default kind.
	//
	// Here it was neither. `--kind` went straight through to hygiene.Check, where it
	// unlocks an allowlisted stray exception and nothing else, so an undeclared kind
	// unlocked nothing and the run printed HYGIENE OK -- a clean answer about a shape
	// of work that does not exist, and every card carrying an undeclared kind
	// came back clean. Refusing the kind here, by name, is what makes that
	// answer impossible.
	if kind != "" && !hygiene.KindDeclared(kind) {
		return tool.Refuse(fmt.Sprintf("--kind %q is not a kind this tool declares; one of: %s",
			kind, strings.Join(hygiene.Kinds(), ", ")))
	}

	var paths []string
	for _, p := range strings.Split(c.Str("paths"), ",") {
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
			return tool.Refuse(err.Error())
		}
		shown = strings.Join(paths, ",")
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(c.Int("timeout"))*time.Second)
	defer cancel()
	findings, err := hygiene.Check(ctx, hygiene.Options{
		Repo: repo, Base: base, Head: head, Paths: paths, Identities: ids, Kind: kind,
	})
	if err != nil {
		return tool.Refuse(err.Error())
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
	// Quote and not Field for the values: Field escapes every blank to \x20, which is
	// right for a scanner reading one token and wrong for a line a person is meant to
	// copy -- `Name <email>` remains readable instead of printing escaped spaces,
	// and an unquoted `<` is a shell redirect besides. Quote is the form for a value
	// that is meant to be pasted back.
	remedy := fmt.Sprintf("nova-check hygiene --repo %s --base %s --head %s --identity %s",
		oneline.Quote(repo), oneline.Quote(base), oneline.Quote(head), oneline.Quote(identityList(ids)))
	if len(paths) > 0 {
		remedy += " --paths " + oneline.Quote(strings.Join(paths, ","))
	}
	if kind != "" {
		remedy += " --kind " + oneline.Quote(kind)
	}
	remedy += " --max 0"

	o := tool.Done()
	if len(findings) > 0 {
		o = tool.Fail()
	}
	for _, f := range findings {
		o.ItemText("finding", f.Why, "reason", f.Token, "at", f.At)
	}
	o.Fact("base", base).Fact("head", head).Fact("paths", shown).Fact("findings", len(findings))
	// The cap is this verb's own, so that its MORE line carries the rerun above
	// and not the skeleton's general remedy; the skeleton's cap that follows finds
	// the listing already cut.
	o.Cap(c.Int("max"))
	for i := range o.More {
		o.More[i].Remedy = remedy
	}
	return o
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

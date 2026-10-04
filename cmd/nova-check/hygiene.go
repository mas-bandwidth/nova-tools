package main

// `nova-check hygiene` is the hand's door to the same function the gate runs.
//
// One implementation serves three callers: `nova-pulse accept` at harvest,
// `nova-merge batch` on every member, and this one, for a person who wants to
// know before they ask a friend for a read. A single implementation keeps the
// lane and the harvest from disagreeing about what clean means. A second copy
// of these rules is a second definition of clean, and the day the two drift is
// the day a branch passes one and fails the other with nobody able to say which
// is right.
//
// It decides nothing. It prints what is wrong and exits: 0 clean, 1 findings, 2
// could not run.

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/bounded"
	"github.com/mas-bandwidth/nova-tools/internal/hygiene"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/tool"
)

func hygieneRun(c *tool.Call) *tool.Out {
	repo, base, head := c.Str("repo"), c.Str("base"), c.Str("head")
	pathsFlag := c.Str("paths")
	identity := c.Str("identity")
	kind := c.Str("kind")
	maxFlag := c.Int("max")
	timeout := c.Int("timeout")
	if maxFlag < 0 {
		return tool.Refuse("--max must be non-negative")
	}
	if timeout <= 0 {
		return tool.Refuse("--timeout must be positive")
	}

	var ids []hygiene.Identity
	for _, one := range strings.Split(identity, ",") {
		one = strings.TrimSpace(one)
		if one == "" {
			continue
		}
		name, email, ok := strings.Cut(one, "<")
		if !ok || !strings.HasSuffix(email, ">") {
			return tool.Refuse(fmt.Sprintf("--identity %q: want `Name <email>`", one))
		}
		email = strings.TrimSpace(strings.TrimSuffix(email, ">"))
		if strings.ContainsAny(email, "<>") {
			return tool.Refuse(fmt.Sprintf("--identity %q: the email carries an angle bracket; want `Name <email>`, one pair", one))
		}
		ids = append(ids, hygiene.Identity{Name: strings.TrimSpace(name), Email: email})
	}
	if len(ids) == 0 {
		return tool.Refuse("--identity is required: `Name <email>`, repeatable with commas")
	}
	if kind != "" && !hygiene.KindDeclared(kind) {
		return tool.Refuse(fmt.Sprintf("--kind %q is not a kind this tool declares; one of: %s",
			kind, strings.Join(hygiene.Kinds(), ", ")))
	}

	var paths []string
	for _, p := range strings.Split(pathsFlag, ",") {
		if p = strings.TrimSpace(p); p != "" {
			paths = append(paths, p)
		}
	}
	shown := "-"
	if len(paths) > 0 {
		if err := hygiene.ValidatePaths(paths); err != nil {
			return tool.Refuse(err.Error())
		}
		shown = strings.Join(paths, ",")
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeout)*time.Second)
	defer cancel()
	findings, err := hygiene.Check(ctx, hygiene.Options{
		Repo: repo, Base: base, Head: head, Paths: paths, Identities: ids, Kind: kind,
	})
	if err != nil {
		return tool.Refuse(err.Error())
	}

	remedy := "nova-check hygiene --repo " + oneline.Quote(repo) + " --base " + oneline.Quote(base) + " --head " + oneline.Quote(head) + " --identity " + oneline.Quote(identityList(ids))
	if len(paths) > 0 {
		remedy += " --paths " + oneline.Quote(strings.Join(paths, ","))
	}
	if kind != "" {
		remedy += " --kind " + oneline.Quote(kind)
	}
	remedy += " --max 0"

	if c.Bool("json") {
		out := &tool.Out{Verb: "hygiene", Status: tool.OK}
		out.Fact("repo", repo).Fact("base", base).Fact("head", head).Fact("paths", shown).Fact("findings", len(findings))
		shownFindings := len(findings)
		if maxFlag > 0 && shownFindings > maxFlag {
			shownFindings = maxFlag
		}
		for _, f := range findings[:shownFindings] {
			out.Item("finding", "reason", f.Token, "at", f.At, "why", f.Why)
		}
		if shownFindings < len(findings) {
			out.More = []tool.More{{Kind: "finding", Shown: shownFindings, Total: len(findings), Remedy: remedy}}
		}
		if len(findings) > 0 {
			out.Status = tool.Failed
			out.Exit = 1
		}
		out.Render(c.Stdout, true)
		return tool.Exit(out.Exit)
	}

	list := bounded.Capped(c.Stdout, maxFlag, "HYGIENE", "finding", remedy)
	for _, f := range findings {
		list.Line(fmt.Sprintf("HYGIENE FINDING reason=%s at=%s: %s",
			oneline.Field(f.Token), oneline.Field(f.At), oneline.Escape(f.Why)))
	}
	list.More()

	if len(findings) == 0 {
		fmt.Fprintf(c.Stdout, "HYGIENE OK base=%s head=%s paths=%s findings=%d\n",
			oneline.Field(base), oneline.Field(head), oneline.Field(shown), len(findings))
		return tool.Exit(0)
	}
	fmt.Fprintf(c.Stderr, "HYGIENE FAILED base=%s head=%s paths=%s findings=%d\n",
		oneline.Field(base), oneline.Field(head), oneline.Field(shown), len(findings))
	return tool.Exit(1)
}

// identityList spells the pool back in the form the flag takes, so the MORE
// line's remedy is the run that printed it and not an approximation of it. The
// separator is the flag's own: `Name <email>,Name <email>`.
func identityList(ids []hygiene.Identity) string {
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		out = append(out, strings.TrimSpace(id.Name+" <"+id.Email+">"))
	}
	return strings.Join(out, ",")
}

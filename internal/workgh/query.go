// Package workgh reads GitHub issues, read-only, into nova-work's tree model
// (internal/workfile). It is the recut of the GitHub capture that nova-work
// carried before (its GraphQL page query, its mutation refusal, its page
// chain), widened to every field SPEC-WORK-V1 section 1.3 names and to every
// repository of an organization, with no field cut at a bound: a connection
// longer than one page is fetched to its end, never truncated.
//
// Every call goes through one seam, Query, and is counted; the production
// Query runs `gh api graphql` and refuses any document that is not a query,
// so this package cannot write to GitHub.
package workgh

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/mas-bandwidth/nova-tools/pkg/subproc"
	"github.com/mas-bandwidth/nova-tools/pkg/testguard"
)

// Query runs one read-only GraphQL document with its variables and returns
// the raw response body.
type Query func(ctx context.Context, doc string, vars map[string]any) ([]byte, error)

// ghProgram is the GitHub CLI's name on PATH; --gh overrides it.
const ghProgram = "gh"

// DefaultProgram is the GitHub CLI the production Query runs when none is named.
func DefaultProgram() string { return ghProgram }

// GhQuery is the production Query: `<program> api graphql --input -` with
// the document and variables as the JSON body on stdin. It refuses a
// document that is not a plain query before anything runs.
func GhQuery(program string) Query {
	return func(ctx context.Context, doc string, vars map[string]any) ([]byte, error) {
		if err := RefuseMutation(doc); err != nil {
			return nil, err
		}
		body, err := json.Marshal(map[string]any{"query": doc, "variables": vars})
		if err != nil {
			return nil, err
		}
		args := []string{"api", "graphql", "--input", "-"}
		testguard.RefuseHosts(program, args...)
		cmd := subproc.Context(ctx, program, args...)
		cmd.Stdin = bytes.NewReader(body)
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		out, err := cmd.Output()
		if err != nil {
			if len(out) > 0 {
				// GitHub answered with errors; the body says which.
				return out, ghError(program, err, stderr.String())
			}
			return nil, ghError(program, err, stderr.String())
		}
		return out, nil
	}
}

// ghError is a failed gh run in words: the program, how it ended, and what it
// said on stderr, or that it said nothing.
func ghError(program string, err error, stderr string) error {
	msg := strings.TrimSpace(stderr)
	if msg == "" {
		msg = "it printed nothing on stderr"
	}
	return fmt.Errorf("%s api graphql: %v: %s", program, err, msg)
}

// RefuseMutation refuses a GraphQL document that is not a plain query.
func RefuseMutation(doc string) error {
	q := strings.TrimSpace(doc)
	if !strings.HasPrefix(q, "query") && !strings.HasPrefix(q, "{") {
		return fmt.Errorf("workgh: refused a GraphQL document that is not a query")
	}
	if strings.Contains(q, "mutation") || strings.Contains(q, "subscription") {
		return fmt.Errorf("workgh: refused a GraphQL document naming a mutation or subscription")
	}
	return nil
}

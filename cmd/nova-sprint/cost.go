// The cost verb (nova-tools #3159): `nova-sprint cost import --provider
// <anthropic|openrouter|oc> --file <export.csv> --redis <addr>` imports one
// provider usage export into cost:<provider>:<day> hashes and the cost:idx
// zset, reconciled per UTC day. It runs from any seat: no home path, no --as,
// no friend name; the file is the only input. Zero REST calls, zero tokens.
package main

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/cost"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
)

func init() {
	register(Verb{
		Name:    "cost",
		Summary: "cost import --provider <anthropic|openrouter|oc> --file <export.csv> --redis <addr>: a provider export into cost:<p>:<day> (exit 2 usage, 3 bad export, 4 does not reconcile, 6 no Redis/nothing written, 8 outcome unknown, 9 partial)",
		Run:     runCost,
	})
}

// costNow is the import's clock, the hash's `at`; a test pins it.
var costNow = time.Now

func runCost(ctx context.Context, args []string, out, errOut io.Writer) int {
	if len(args) == 0 || args[0] != "import" {
		return refuse(errOut, "cost", "the one subverb is import: cost import --provider <anthropic|openrouter|oc> --file <export.csv> --redis <addr>")
	}
	fs := taskFlags("cost import")
	provider := fs.String("provider", "", "")
	file := fs.String("file", "", "")
	redisAddr := fs.String("redis", "", "")
	if err := fs.Parse(args[1:]); err != nil {
		return refuse(errOut, "cost import", err.Error())
	}
	switch {
	case fs.NArg() > 0:
		return refuse(errOut, "cost import", "takes flags, not positional arguments: --provider <p> --file <export.csv> --redis <addr>")
	case !cost.ValidProvider(*provider):
		return refuse(errOut, "cost import", fmt.Sprintf("--provider %q: want one of %s", *provider, strings.Join(cost.Providers(), ", ")))
	case *file == "":
		return refuse(errOut, "cost import", "no --file: the provider export is the only input")
	case *redisAddr == "":
		return refuse(errOut, "cost import", "no --redis <addr>")
	}

	// The export is read, parsed and reconciled before Redis is touched, so exits 3 and 4
	// write nothing.
	e, err := cost.Load(*provider, *file)
	if err != nil {
		fmt.Fprintf(errOut, "nova-sprint cost import: %v\n", err)
		return cost.Code(err)
	}
	st, err := store.Open(ctx, *redisAddr)
	if err != nil {
		fmt.Fprintf(errOut, "nova-sprint cost import: %v; nothing written\n", err)
		return cost.ExitPreWrite
	}
	defer func() { _ = st.Close() }()
	return cost.Import(ctx, st.Client(), e, costNow(), out, errOut)
}

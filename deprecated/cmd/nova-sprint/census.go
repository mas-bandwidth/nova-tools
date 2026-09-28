// The census verb (nova-tools #3037) registers itself through the S0 registry
// (registry.go), so adding it never edits main.go. It replaces the redis-pipe
// prototype: one pipelined read of every key in a registry set or a key list,
// one line per key, MISSING <key> for a key not in Redis, then a CENSUS count
// line. With --sprint it is refused, whatever the sprint's s:<S>:card family holds
// (its cards are task records in the ws index, #4411), one line, exit 1, no
// Redis read:
// REFUSED census reads a retired key family; remedy="nova-sprint ws counts".
// The rows and counts come from internal/nsprint/store/census.go, the
// refusal from internal/nsprint/store/census_cards.go; this file only parses
// flags.
//
//	nova-sprint census --sprint <S> [--keys queued,dealt,...]   (refused, #4411)
//	nova-sprint census --redis <addr> --set benches|friends|sprint:<name>:<state> --fields f1,f2
//	nova-sprint census --redis <addr> --keys-from <file|-> --fields f1,f2
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/verbflag"
)

func init() {
	register(Verb{
		Name:    "census",
		Summary: "one pipelined read: --set/--keys-from --fields rows, MISSING when absent; --sprint <S> is refused (a retired key family; remedy nova-sprint ws counts)",
		Run:     runCensus,
	})
}

func runCensus(ctx context.Context, args []string, out, errOut io.Writer) int {
	fs := verbflag.New("census")
	redisAddr := fs.String("redis", redisDefault(), "")
	set := fs.String("set", "", "")
	keysFrom := fs.String("keys-from", "", "")
	fields := fs.String("fields", "", "")
	sprint := fs.String("sprint", "", "")
	keys := fs.String("keys", "", "")
	if err := fs.Parse(args); err != nil {
		return refuse(errOut, "census", err.Error())
	}
	if fs.NArg() != 0 {
		return refuse(errOut, "census", "takes no arguments after the flags")
	}
	if *sprint != "" || *keys != "" {
		if *set != "" || *keysFrom != "" || *fields != "" {
			return refuse(errOut, "census", "--sprint reads the card index sets; it takes no --set, --keys-from or --fields")
		}
		return runCardCensus(*sprint, *keys, out, errOut)
	}
	req := store.CensusRequest{Set: *set}
	for _, f := range strings.Split(*fields, ",") {
		if *fields != "" {
			req.Fields = append(req.Fields, strings.TrimSpace(f))
		}
	}
	if *keysFrom != "" {
		if *keysFrom == "-" {
			req.KeysFrom = os.Stdin
		} else {
			f, err := os.Open(*keysFrom)
			if err != nil {
				return refuse(errOut, "census", err.Error())
			}
			defer f.Close()
			req.KeysFrom = f
		}
	}
	if err := req.Check(); err != nil {
		return refuse(errOut, "census", err.Error())
	}
	if *redisAddr == "" {
		return refuse(errOut, "census", "--redis addr is required")
	}
	st, err := store.Open(ctx, *redisAddr)
	if err != nil {
		return refuse(errOut, "census", err.Error())
	}
	defer st.Close()
	if _, err := store.RunCensus(ctx, st, req, out); err != nil {
		return refuse(errOut, "census", err.Error())
	}
	return 0
}

// runCardCensus is `census --sprint <S> [--keys <states>]`: a bad flag
// exits 2; every well-formed request is refused, one line on stdout, exit 1,
// with no Redis read (#4411): the s:<S>:card family is not a count of the
// sprint's work, whatever it holds, and `ws counts` is.
func runCardCensus(sprint, keys string, out, errOut io.Writer) int {
	req := store.CardCensusRequest{Sprint: sprint}
	if keys != "" {
		for _, k := range strings.Split(keys, ",") {
			req.States = append(req.States, strings.TrimSpace(k))
		}
	}
	err := store.RefuseCardCensus(req)
	if errors.Is(err, store.ErrRetiredFamily) {
		fmt.Fprintf(out, "REFUSED %s\n", err.Error())
		return 1
	}
	return refuse(errOut, "census", err.Error())
}

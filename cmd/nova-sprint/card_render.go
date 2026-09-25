package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/taskcard"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// cmdCardRender prints the harness card a bench runs for the task record
// task:<id> (nova-tools#3911): the RESULT line, the typed header, the
// standard lines and DO: with the issue text, rendered from the record alone
// (one HGETALL) and admitted by card push's linter. --brief prints the friend
// brief from the same record instead. A record a swarm run could not start
// from is refused naming the header keys it lacks. Exit 0 rendered, 1
// refused, 2 usage or Redis.
func cmdCardRender(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("card render", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	id := fs.String("id", "", "")
	brief := fs.Bool("brief", false, "")
	addr := fs.String("redis", "", "")
	if err := fs.Parse(args); err != nil || *id == "" || fs.NArg() > 0 {
		return refuse(stderr, "card render", "wants --id <task> [--brief] [--redis <addr>]")
	}
	start := time.Now()
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	st, err := store.Open(ctx, taskAddr(*addr))
	if err != nil {
		return refuse(stderr, "card render", err.Error())
	}
	defer func() { _ = st.Close() }()
	rec, err := taskcard.Record(ctx, st.Client(), *id)
	render := taskcard.RenderHeader
	if *brief {
		render = taskcard.RenderBrief
	}
	var out []byte
	if err == nil {
		out, err = render(*id, rec)
	}
	if why, ok := taskcard.IsRefused(err); ok {
		fmt.Fprintf(stdout, "REFUSED card render id=%s why=%s\n", *id, oneline.Field(why))
		return 1
	}
	if err != nil {
		return refuse(stderr, "card render", err.Error())
	}
	if _, err := stdout.Write(out); err != nil {
		return refuse(stderr, "card render", err.Error())
	}
	// stdout is exactly the card (a deal pipes it to the bench); the one
	// receipt line goes to stderr.
	fmt.Fprintf(stderr, "RENDERED card id=%s brief=%t bytes=%d ms=%d\n", *id, *brief, len(out), time.Since(start).Milliseconds())
	return 0
}

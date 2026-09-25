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
//
// --for-model <family> (nova-tools#3956) prints instead the prompt the MODEL
// reads, rendered by the family's template (cfg:card:template:<family> in
// Redis, else the built-in one): the goal first, the files, the test and the
// DONE-WHEN command, three lines of layout, a literal RESULT.md example, what
// not to do, and the issue text last, with no wrapper key in it. The family
// is one of qwen, deepseek, kimi, glm, mercury, claude or default, or a model
// launch string whose family card.FamilyOf reads.
func cmdCardRender(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("card render", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	id := fs.String("id", "", "")
	brief := fs.Bool("brief", false, "")
	forModel := fs.String("for-model", "", "")
	addr := fs.String("redis", "", "")
	if err := fs.Parse(args); err != nil || *id == "" || fs.NArg() > 0 || (*brief && *forModel != "") {
		return refuse(stderr, "card render", "wants --id <task> [--brief | --for-model <family>] [--redis <addr>]")
	}
	start := time.Now()
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	st, err := store.Open(ctx, taskAddr(*addr))
	if err != nil {
		return refuse(stderr, "card render", err.Error())
	}
	defer func() { _ = st.Close() }()
	if *forModel != "" {
		p, err := taskcard.RenderPrompt(ctx, st.Client(), *id, *forModel)
		if why, ok := taskcard.IsRefused(err); ok {
			fmt.Fprintf(stdout, "REFUSED card render id=%s for_model=%s why=%s\n", *id, oneline.Field(*forModel), oneline.Field(why))
			return 1
		}
		if err != nil {
			return refuse(stderr, "card render", err.Error())
		}
		if _, err := stdout.Write(p.Text); err != nil {
			return refuse(stderr, "card render", err.Error())
		}
		fmt.Fprintf(stderr, "RENDERED card id=%s for_model=%s family=%s template=%s bytes=%d prompt_bytes=%d ms=%d\n",
			*id, oneline.Field(*forModel), p.Family, p.Source, len(p.Text), p.PromptBytes, time.Since(start).Milliseconds())
		return 0
	}
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

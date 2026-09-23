package fold

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// VerbSummary is the verb's line on nova-sprint help.
const VerbSummary = "fold <S> --store <host:port> --work <nova-work checkout> [--path <rel>] [--as <actor>]: landed, done, useful, $ per useful and per landed per route, one nova-work commit"

// Main is `nova-sprint fold <S> --store <host:port> --work <dir>`. Exit 0 the
// sprint is folded (now or before), 1 an outcome is unknown (a card with no end
// record or a PR with no state; nothing committed), 2 refused or could not run.
func Main(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	var sprint string
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		sprint, args = args[0], args[1:]
	}
	fs := flag.NewFlagSet("fold", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	addr := fs.String("store", "", "")
	work := fs.String("work", "", "")
	path := fs.String("path", "", "")
	actor := fs.String("as", "nova-sprint", "")
	if err := fs.Parse(args); err != nil {
		return refuse(stderr, err.Error()+"; it wants fold <S> --store <host:port> --work <nova-work checkout>")
	}
	var problems []string
	if sprint == "" {
		problems = append(problems, "name the sprint first: fold <S>")
	}
	if fs.NArg() > 0 {
		problems = append(problems, "one sprint per fold; extra arguments "+strings.Join(fs.Args(), " "))
	}
	if *addr == "" {
		problems = append(problems, "--store <host:port> is the sprint's Redis")
	}
	if *work == "" {
		problems = append(problems, "--work names the nova-work checkout the fold commits into")
	}
	if len(problems) > 0 {
		return refuse(stderr, strings.Join(problems, "; "))
	}
	st, err := store.Open(ctx, *addr)
	if err != nil {
		return refuse(stderr, err.Error())
	}
	defer st.Close()
	if _, err := Run(ctx, st.Client(), Options{Sprint: sprint, Work: *work, Path: *path, Actor: *actor}, stdout); err != nil {
		var unknown *UnknownError
		if errors.As(err, &unknown) {
			fmt.Fprintf(stderr, "nova-sprint fold: %s\n", oneline.Escape(err.Error()))
			return 1
		}
		return refuse(stderr, err.Error())
	}
	return 0
}

func refuse(stderr io.Writer, what string) int {
	fmt.Fprintf(stderr, "nova-sprint fold: %s; run: nova-sprint help\n", oneline.Escape(what))
	return 2
}

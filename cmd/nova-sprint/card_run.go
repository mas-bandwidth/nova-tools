package main

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/card"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/verbflag"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

func init() {
	register(Verb{
		Name:    "card",
		Summary: "cut, push, release, stop, run, show, launched, beat, and end one card attempt; fsck and ls --unplaced the card model",
		Run:     runCard,
	})
}

func runCard(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 && (args[0] == "cut" || args[0] == "push" || args[0] == "release" || args[0] == "stop" || args[0] == "show" ||
		args[0] == "fsck" || args[0] == "ls") {
		return runCardPool(ctx, args, stdout, stderr)
	}
	if len(args) > 0 && args[0] == "run" {
		return runCardRun(ctx, args[1:], stdout, stderr)
	}
	if len(args) == 0 {
		return cardUsage(stderr, "", "wants cut, push, release, stop, show, run, launched, beat, or end")
	}
	sub := args[0]
	want, ok := cardWant(sub)
	if !ok {
		return cardUsage(stderr, "unknown verb "+sub, "wants cut, push, release, stop, show, run, launched, beat, or end")
	}
	verbflag.HelpIfAsked(args[1:], "card "+sub, cardFlagNames(sub)...)
	flags, pos, err := parseCardArgs(args[1:])
	if err != nil {
		return cardUsage(stderr, err.Error(), want)
	}
	if len(pos) != 1 {
		return cardUsage(stderr, "wants one label", want)
	}
	label := pos[0]
	for _, name := range cardFlagNames(sub) {
		if flags[name] == "" {
			return cardUsage(stderr, "missing --"+name, want)
		}
	}
	for name := range flags {
		if !cardFlagAllowed(sub, name) {
			return cardUsage(stderr, "unknown flag --"+name, want)
		}
	}
	if sub == "end" && !card.AbsResults(flags["results"]) {
		// #3329: results is the card hash field harvest pushes from; it is
		// absolute so no reader needs a root typed on its argv.
		return cardUsage(stderr, "--results "+flags["results"]+" is relative or not a Unix path",
			"results must be a Unix absolute path on the bench (leading /, no //, no backslash, no ..; the card hash field s:<S>:card:<label> results); "+want)
	}
	st, err := store.Open(ctx, flags["redis"])
	if err != nil {
		fmt.Fprintln(stdout, (card.Result{Code: 6, Verb: "card " + sub, ID: label, Reason: "REDIS"}).Line())
		return 6
	}
	defer st.Close()
	var res card.Result
	switch sub {
	case "launched":
		res, err = card.Launched(ctx, st, card.LaunchRequest{
			Sprint: flags["sprint"], Label: label, Token: flags["token"],
			Branch: flags["branch"], JobDir: flags["jobdir"],
		})
	case "beat":
		res, err = card.Beat(ctx, st, card.BeatRequest{
			Sprint: flags["sprint"], Label: label, Token: flags["token"],
		})
	case "end":
		res, err = card.End(ctx, st, card.EndRequest{
			Sprint: flags["sprint"], Label: label, Token: flags["token"],
			Outcome: flags["outcome"], Reason: flags["reason"], ResultsDir: flags["results"],
		})
	default:
		return cardUsage(stderr, "unknown verb "+sub, "wants cut, push, release, stop, show, run, launched, beat, or end")
	}
	if store.Unreachable(err) {
		// Open sends nothing (#3277): this is the first batch, and an
		// unreachable store is the same REDIS line and exit 6 as before.
		fmt.Fprintln(stdout, (card.Result{Code: 6, Verb: "card " + sub, ID: label, Reason: "REDIS"}).Line())
		return 6
	}
	if err != nil {
		fmt.Fprintf(stderr, "nova-sprint card: %s; run: nova-sprint help\n", oneline.Escape(err.Error()))
		return 2
	}
	fmt.Fprintln(stdout, res.Line())
	return res.Code
}

func cardWant(sub string) (string, bool) {
	switch sub {
	case "launched":
		return "launched wants --redis <addr> --sprint <S> <label> --token <t> --branch <b> --jobdir <d>", true
	case "beat":
		return "beat wants --redis <addr> --sprint <S> <label> --token <t>", true
	case "end":
		return "end wants --redis <addr> --sprint <S> <label> --token <t> --outcome DONE,ABSTAIN,BLOCKED,FAILED --reason <code> --results <dir>", true
	default:
		return "", false
	}
}

func cardFlagNames(sub string) []string {
	switch sub {
	case "launched":
		return []string{"redis", "sprint", "token", "branch", "jobdir"}
	case "beat":
		return []string{"redis", "sprint", "token"}
	case "end":
		return []string{"redis", "sprint", "token", "outcome", "reason", "results"}
	default:
		return nil
	}
}

func cardFlagAllowed(sub, name string) bool {
	for _, allowed := range cardFlagNames(sub) {
		if allowed == name {
			return true
		}
	}
	return false
}

func cardUsage(stderr io.Writer, detail, want string) int {
	msg := want
	if detail != "" {
		msg = oneline.Escape(detail) + "; " + want
	}
	fmt.Fprintf(stderr, "nova-sprint card: %s; run: nova-sprint help\n", msg)
	return 1
}

func parseCardArgs(args []string) (map[string]string, []string, error) {
	flags := map[string]string{}
	var pos []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			return nil, nil, fmt.Errorf("unexpected --")
		}
		if strings.HasPrefix(a, "--") {
			name := strings.TrimPrefix(a, "--")
			val := ""
			if eq := strings.IndexByte(name, '='); eq >= 0 {
				val = name[eq+1:]
				name = name[:eq]
			} else {
				if i+1 >= len(args) {
					return nil, nil, fmt.Errorf("missing value for --%s", name)
				}
				i++
				val = args[i]
			}
			if name == "" || val == "" || strings.HasPrefix(val, "--") {
				return nil, nil, fmt.Errorf("missing value for --%s", name)
			}
			switch name {
			case "redis", "sprint", "token", "branch", "jobdir", "outcome", "reason", "results":
			default:
				return nil, nil, fmt.Errorf("unknown flag --%s", name)
			}
			if _, dup := flags[name]; dup {
				return nil, nil, fmt.Errorf("duplicate --%s", name)
			}
			flags[name] = val
			continue
		}
		if strings.HasPrefix(a, "-") {
			return nil, nil, fmt.Errorf("unknown flag %s", a)
		}
		pos = append(pos, a)
	}
	return flags, pos, nil
}

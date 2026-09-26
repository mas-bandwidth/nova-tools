package main

import (
	"context"
	"fmt"
	"io"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/card"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/verbflag"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

func init() {
	register(Verb{
		Name:    "card",
		Summary: "cut, push, release, stop, run, show, launched, beat, and end one card attempt; fsck and ls --unplaced the card model; deal, work, end, beat, land, cancel, expire, fsck, table, consumers and render: the table moves (render --id: a copy's card, or a primary's harness card, --brief its friend brief)",
		Run:     runCard,
	})
}

func runCard(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 && isCardMove(args[0], args[1:]) {
		return runCardMove(ctx, args[0], args[1:], stdout, stderr) // the table moves (#3929): card_moves.go
	}
	if len(args) > 0 && (args[0] == "cut" || args[0] == "push" || args[0] == "release" || args[0] == "stop" || args[0] == "show" ||
		args[0] == "fsck" || args[0] == "ls" || args[0] == "stitch") {
		return runCardPool(ctx, args, stdout, stderr)
	}
	if len(args) > 0 && args[0] == "run" {
		return runCardRun(ctx, args[1:], stdout, stderr)
	}
	if len(args) == 0 {
		return cardUsage(stderr, "", "wants cut, push, release, stop, show, run, launched, beat, or end")
	}
	sub := args[0]
	return runCardAttempt(ctx, sub, args[1:], stdout, stderr)
}

// runCardAttempt is the bench attempt form of card launched, beat and end
// (the s:<S>:card model): `card <verb> --sprint <S> --ids <label> --token <t>
// ...`, one flag set under the one grammar (#4352 A; the label was a
// positional). end and beat with --sprint are this form; without it they are
// the table moves (card_moves.go).
func runCardAttempt(ctx context.Context, sub string, args []string, stdout, stderr io.Writer) int {
	want, ok := cardWant(sub)
	if !ok {
		return cardUsage(stderr, "unknown verb "+sub, "wants cut, push, release, stop, show, run, launched, beat, or end")
	}
	fs := verbflag.New("card " + sub)
	redisAddr := fs.String("redis", redisDefault(), verbflag.HelpRedis)
	sprint := fs.String("sprint", "", verbflag.HelpSprint)
	ids := fs.String("ids", "", verbflag.HelpIDs)
	token := fs.String("token", "", "the attempt's token, from the launch")
	branch, jobdir, outcome, why, results := new(string), new(string), new(string), new(string), new(string)
	switch sub {
	case "launched":
		branch = fs.String("branch", "", "the branch the attempt works on")
		jobdir = fs.String("jobdir", "", "the attempt's job directory on the bench")
	case "end":
		outcome = fs.String("outcome", "", "DONE, ABSTAIN, BLOCKED or FAILED")
		why = fs.String("why", "", verbflag.HelpWhy)
		results = fs.String("results", "", "the results directory, a Unix absolute path on the bench")
	}
	if err := fs.Parse(args); err != nil {
		return cardUsage(stderr, err.Error(), want)
	}
	if fs.NArg() > 0 {
		return cardUsage(stderr, "takes flags, not positional arguments", want)
	}
	label := oneID(*ids)
	if label == "" {
		return cardUsage(stderr, "wants --ids <label>, one label", want)
	}
	for _, f := range []struct{ name, v string }{{"redis", *redisAddr}, {"sprint", *sprint}, {"token", *token},
		{"branch", *branch}, {"jobdir", *jobdir}, {"outcome", *outcome}, {"why", *why}, {"results", *results}} {
		if f.v == "" && cardFlagAllowed(sub, f.name) {
			return cardUsage(stderr, "missing --"+f.name, want)
		}
	}
	if sub == "end" && !card.AbsResults(*results) {
		// #3329: results is the card hash field harvest pushes from; it is
		// absolute so no reader needs a root typed on its argv.
		return cardUsage(stderr, "--results "+*results+" is relative or not a Unix path",
			"results must be a Unix absolute path on the bench (leading /, no //, no backslash, no ..; the card hash field s:<S>:card:<label> results); "+want)
	}
	st, err := store.Open(ctx, *redisAddr)
	if err != nil {
		fmt.Fprintln(stdout, (card.Result{Code: 6, Verb: "card " + sub, ID: label, Reason: "REDIS"}).Line())
		return 6
	}
	defer st.Close()
	var res card.Result
	switch sub {
	case "launched":
		res, err = card.Launched(ctx, st, card.LaunchRequest{
			Sprint: *sprint, Label: label, Token: *token,
			Branch: *branch, JobDir: *jobdir,
		})
	case "beat":
		res, err = card.Beat(ctx, st, card.BeatRequest{
			Sprint: *sprint, Label: label, Token: *token,
		})
	case "end":
		res, err = card.End(ctx, st, card.EndRequest{
			Sprint: *sprint, Label: label, Token: *token,
			Outcome: *outcome, Reason: *why, ResultsDir: *results,
		})
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
		return "launched wants --redis <addr> --sprint <S> --ids <label> --token <t> --branch <b> --jobdir <d>", true
	case "beat":
		return "beat wants --redis <addr> --sprint <S> --ids <label> --token <t>", true
	case "end":
		return "end wants --redis <addr> --sprint <S> --ids <label> --token <t> --outcome DONE,ABSTAIN,BLOCKED,FAILED --why <code> --results <dir>", true
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
		return []string{"redis", "sprint", "token", "outcome", "why", "results"}
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

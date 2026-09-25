// `nova-sprint card launch --stdin` is the bench side of a deal pass (#2931,
// #2756 4.3): one ssh session carries the bench's whole batch on stdin, each
// line starts a card wrapper detached in its own session with command
// identity `nova-card <S>/<label>/<attempt>`, and the verb exits.
//
// The card verb's other subverbs (launched, beat, end) are card_run.go's
// (#2928). This file joins the one card verb from either side of that
// landing: Go runs a package's init functions in file-name order, so
// card_run.go's register has run by the time this init looks. If a card verb
// is registered, launch is routed in front of it; if not, this file
// registers a card verb that has only launch. Neither order registers the
// name twice.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/launch"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

func init() {
	if v, ok := verbs["card"]; ok {
		inner := v.Run
		v.Run = func(ctx context.Context, args []string, out, errOut io.Writer) int {
			if len(args) > 0 && args[0] == "launch" {
				return runCardLaunch(ctx, args[1:], os.Stdin, out, errOut)
			}
			return inner(ctx, args, out, errOut)
		}
		v.Summary += "; launch a bench's batch detached"
		verbs["card"] = v
		return
	}
	register(Verb{
		Name:    "card",
		Summary: "launch a bench's batch of card wrappers detached",
		Run: func(ctx context.Context, args []string, out, errOut io.Writer) int {
			if len(args) == 0 || args[0] != "launch" {
				return refuse(errOut, "card", "wants launch --stdin")
			}
			return runCardLaunch(ctx, args[1:], os.Stdin, out, errOut)
		},
	})
}

// runCardLaunch exits 0 when every line started inside the launch budget, 1
// when a line was refused or the batch's own wall time overran the budget
// (launch.Result.Overran; the others still started), 2 when the batch could
// not run.
func runCardLaunch(_ context.Context, args []string, in io.Reader, out, errOut io.Writer) int {
	fs := flag.NewFlagSet("card launch", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}
	stdin := fs.Bool("stdin", false, "")
	wrapper := fs.String("wrapper", "", "")
	if err := fs.Parse(args); err != nil {
		return refuse(errOut, "card launch", err.Error()+"; wants --stdin [--wrapper <path>]")
	}
	if fs.NArg() != 0 {
		return refuse(errOut, "card launch", "takes no arguments; the batch is on stdin")
	}
	if !*stdin {
		return refuse(errOut, "card launch", "wants --stdin: the batch is one line per card, <sprint> <label> <attempt> <token>")
	}
	path, err := wrapperPath(*wrapper)
	if err != nil {
		return refuse(errOut, "card launch", err.Error())
	}
	// Every REFUSED line goes to stderr too (#3700), so a session log shows it.
	res, err := launch.Launch(in, out, launch.Config{Wrapper: path, Err: errOut})
	if err != nil {
		fmt.Fprintf(errOut, "nova-sprint %s\n", oneline.Err(err))
		return 2
	}
	if res.Refused > 0 || res.Overran {
		return 1
	}
	return 0
}

// wrapperPath is --wrapper made absolute, or else nova-card beside this
// executable: the bench installs both from one release.
func wrapperPath(flagValue string) (string, error) {
	if flagValue != "" {
		return filepath.Abs(flagValue)
	}
	self, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("no --wrapper and cannot find this executable: %w", err)
	}
	return filepath.Join(filepath.Dir(self), launch.WrapperName), nil
}

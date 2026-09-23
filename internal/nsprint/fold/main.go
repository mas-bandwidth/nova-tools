package fold

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// VerbSummary is the verb's line on nova-sprint help.
const VerbSummary = "fold <S> --store <host:port> --work <nova-work checkout> [--path <rel>] [--as <actor>] [--calib <set.jsonl> --prompt-sha <sha> --jev-eval <cmd> [--candidate <sha>]]: landed, done, useful, $ per useful and per landed per route, Jev calibration per work type, one nova-work commit"

// Main is `nova-sprint fold <S> --store <host:port> --work <dir>`. Exit 0 the
// sprint is folded (now or before), 2 refused or could not run, 3 folded but
// the --candidate Jev prompt was refused (the current prompt stays).
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
	calibPath := fs.String("calib", "", "")
	promptSHA := fs.String("prompt-sha", "", "")
	candidate := fs.String("candidate", "", "")
	jevEval := fs.String("jev-eval", "", "")
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
	calibrating := *calibPath != "" || *promptSHA != "" || *candidate != "" || *jevEval != ""
	if calibrating && (*calibPath == "" || *promptSHA == "" || *jevEval == "") {
		problems = append(problems, "the Jev calibration needs --calib <set.jsonl>, --prompt-sha <current> and --jev-eval <cmd> together")
	}
	if len(problems) > 0 {
		return refuse(stderr, strings.Join(problems, "; "))
	}
	var jc *JevCalib
	if calibrating {
		f, err := os.Open(*calibPath)
		if err != nil {
			return refuse(stderr, err.Error())
		}
		set, err := ReadCalibSet(f)
		f.Close()
		if err != nil {
			return refuse(stderr, *calibPath+": "+err.Error())
		}
		jc = &JevCalib{Set: set, PromptSHA: *promptSHA, Candidate: *candidate, Scorer: ExecScorer{Argv: strings.Fields(*jevEval)}}
	}
	st, err := store.Open(ctx, *addr)
	if err != nil {
		return refuse(stderr, err.Error())
	}
	defer st.Close()
	res, err := Run(ctx, st.Client(), Options{Sprint: sprint, Work: *work, Path: *path, Actor: *actor, Jev: jc}, stdout)
	if err != nil {
		return refuse(stderr, err.Error())
	}
	if res.PromptRefused != "" {
		fmt.Fprintf(stderr, "nova-sprint fold: folded; Jev prompt %s refused, %s stays: %s\n", *candidate, *promptSHA, oneline.Escape(res.PromptRefused))
		return 3
	}
	return 0
}

func refuse(stderr io.Writer, what string) int {
	fmt.Fprintf(stderr, "nova-sprint fold: %s; run: nova-sprint help\n", oneline.Escape(what))
	return 2
}
